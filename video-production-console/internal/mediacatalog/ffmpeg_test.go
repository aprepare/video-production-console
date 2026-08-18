package mediacatalog

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedCommand struct {
	Binary string
	Args   []string
}

// fakeCommandRunner records every invocation and dispatches to a swappable
// handler, letting tests pin the exact argv contract without any binary.
type fakeCommandRunner struct {
	mu     sync.Mutex
	calls  []recordedCommand
	handle func(ctx context.Context, binary string, args []string) ([]byte, []byte, error)
}

func (r *fakeCommandRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, recordedCommand{Binary: binary, Args: append([]string(nil), args...)})
	handle := r.handle
	r.mu.Unlock()
	if handle == nil {
		return nil, nil, nil
	}
	return handle(ctx, binary, args)
}

func (r *fakeCommandRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *fakeCommandRunner) lastCall(t *testing.T) recordedCommand {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("no command was executed")
	}
	return r.calls[len(r.calls)-1]
}

func (r *fakeCommandRunner) setHandler(handle func(ctx context.Context, binary string, args []string) ([]byte, []byte, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handle = handle
}

func hasArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func writeTestJPEG(t *testing.T, path string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

const fakeProbeJSON = `{
  "format": {"duration": "20.500000"},
  "streams": [
    {"codec_type": "video", "width": 1920, "height": 1080, "avg_frame_rate": "30000/1001"},
    {"codec_type": "audio"}
  ]
}`

const fakeSceneStderr = `[Parsed_showinfo_1 @ 0x1] n:   0 pts: 107520 pts_time:4.2 pos: 100
[Parsed_showinfo_1 @ 0x1] n:   1 pts: 230400 pts_time:9 pos: 200
[unrelated @ 0x2] frame dropped pts_time:17.5
`

func newFakeFFmpeg(t *testing.T, runner *fakeCommandRunner) *FFmpeg {
	t.Helper()
	ff, err := NewFFmpeg(FFmpegConfig{
		FFmpegPath:  "fake-ffmpeg",
		FFprobePath: "fake-ffprobe",
		Runner:      runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ff
}

func TestNewFFmpegRequiresConfiguredBinaries(t *testing.T) {
	for _, cfg := range []FFmpegConfig{
		{},
		{FFmpegPath: "only-ffmpeg"},
		{FFprobePath: "only-ffprobe"},
	} {
		_, err := NewFFmpeg(cfg)
		if !errors.Is(err, ErrFFmpegNotConfigured) {
			t.Fatalf("config %+v: expected ErrFFmpegNotConfigured, got %v", cfg, err)
		}
		if !strings.Contains(err.Error(), "ffmpeg_not_configured") {
			t.Fatalf("error must carry the ffmpeg_not_configured code, got %q", err.Error())
		}
	}
}

func TestProbeParsesFFprobeJSONViaArgv(t *testing.T) {
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return []byte(fakeProbeJSON), nil, nil
	})
	ff := newFakeFFmpeg(t, runner)

	probe, err := ff.Probe(context.Background(), "C:\\media\\originals\\movies\\clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	call := runner.lastCall(t)
	if call.Binary != "fake-ffprobe" {
		t.Fatalf("probe must use the injected ffprobe path, got %q", call.Binary)
	}
	wantArgs := []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", "C:\\media\\originals\\movies\\clip.mp4"}
	if !reflect.DeepEqual(call.Args, wantArgs) {
		t.Fatalf("ffprobe argv mismatch:\n got %v\nwant %v", call.Args, wantArgs)
	}
	if probe.DurationMS != 20500 || probe.Width != 1920 || probe.Height != 1080 || !probe.HasAudio {
		t.Fatalf("unexpected probe result: %+v", probe)
	}
	if math.Abs(probe.FPS-29.97) > 0.01 {
		t.Fatalf("expected ~29.97 fps, got %f", probe.FPS)
	}
}

func TestProbeRejectsMalformedAndVideoLessOutput(t *testing.T) {
	runner := &fakeCommandRunner{}
	ff := newFakeFFmpeg(t, runner)

	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return []byte("not json"), nil, nil
	})
	if _, err := ff.Probe(context.Background(), "x.mp4"); err == nil {
		t.Fatal("malformed ffprobe JSON must fail")
	}
	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return []byte(`{"format":{"duration":"10.0"},"streams":[{"codec_type":"audio"}]}`), nil, nil
	})
	if _, err := ff.Probe(context.Background(), "x.mp4"); err == nil {
		t.Fatal("ffprobe output without a video stream must fail")
	}
}

func TestDetectScenesUsesConfigurableThresholdAndParsesTimestamps(t *testing.T) {
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return nil, []byte(fakeSceneStderr), nil
	})
	ff, err := NewFFmpeg(FFmpegConfig{
		FFmpegPath: "fake-ffmpeg", FFprobePath: "fake-ffprobe",
		SceneThreshold: 0.4, Runner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}

	boundaries, err := ff.DetectScenes(context.Background(), "movie.mp4", 20000)
	if err != nil {
		t.Fatal(err)
	}
	call := runner.lastCall(t)
	if call.Binary != "fake-ffmpeg" {
		t.Fatalf("scene detection must use the injected ffmpeg path, got %q", call.Binary)
	}
	wantArgs := []string{"-hide_banner", "-nostats", "-i", "movie.mp4", "-vf", "select='gt(scene,0.4)',showinfo", "-an", "-f", "null", "-"}
	if !reflect.DeepEqual(call.Args, wantArgs) {
		t.Fatalf("scene detection argv mismatch:\n got %v\nwant %v", call.Args, wantArgs)
	}
	want := []SceneBoundary{{InMS: 0, OutMS: 4200}, {InMS: 4200, OutMS: 9000}, {InMS: 9000, OutMS: 20000}}
	if !reflect.DeepEqual(boundaries, want) {
		t.Fatalf("boundaries mismatch:\n got %v\nwant %v", boundaries, want)
	}
}

func TestDetectScenesDefaultsToThresholdPoint32(t *testing.T) {
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return nil, nil, nil
	})
	ff := newFakeFFmpeg(t, runner)
	if _, err := ff.DetectScenes(context.Background(), "movie.mp4", 8000); err != nil {
		t.Fatal(err)
	}
	if !hasArg(runner.lastCall(t).Args, "select='gt(scene,0.32)',showinfo") {
		t.Fatalf("expected default 0.32 threshold, got args %v", runner.lastCall(t).Args)
	}
}

func TestNormalizeSceneBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		cuts     []int64
		duration int64
		want     []SceneBoundary
	}{
		{
			name: "plain interior cuts", cuts: []int64{9000, 4200}, duration: 20000,
			want: []SceneBoundary{{0, 4200}, {4200, 9000}, {9000, 20000}},
		},
		{
			name: "short leading segment merges forward then splits", cuts: []int64{1000}, duration: 20000,
			want: []SceneBoundary{{0, 10000}, {10000, 20000}},
		},
		{
			name: "short middle segment merges into previous", cuts: []int64{5000, 5800}, duration: 20000,
			want: []SceneBoundary{{0, 5800}, {5800, 12900}, {12900, 20000}},
		},
		{
			name: "long segment splits evenly", cuts: nil, duration: 30000,
			want: []SceneBoundary{{0, 10000}, {10000, 20000}, {20000, 30000}},
		},
		{
			name: "very short source keeps its single segment", cuts: nil, duration: 800,
			want: []SceneBoundary{{0, 800}},
		},
		{
			name: "cuts outside range are ignored", cuts: []int64{-5, 0, 25000, 9000}, duration: 20000,
			want: []SceneBoundary{{0, 9000}, {9000, 20000}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeSceneBoundaries(tc.cuts, tc.duration)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("boundaries mismatch:\n got %v\nwant %v", got, tc.want)
			}
			for _, boundary := range got {
				length := boundary.OutMS - boundary.InMS
				if length > MaxShotDurationMS {
					t.Fatalf("segment %v exceeds max duration", boundary)
				}
			}
			if len(got) > 1 {
				for _, boundary := range got {
					if boundary.OutMS-boundary.InMS < MinShotDurationMS {
						t.Fatalf("segment %v below min duration in multi-shot result", boundary)
					}
				}
			}
		})
	}
}

func TestExtractKeyframeCommandContract(t *testing.T) {
	runner := &fakeCommandRunner{}
	runner.setHandler(func(_ context.Context, _ string, _ []string) ([]byte, []byte, error) {
		return nil, nil, nil
	})
	ff := newFakeFFmpeg(t, runner)

	if err := ff.ExtractKeyframe(context.Background(), "movie.mp4", 1500, "out.jpg.part"); err != nil {
		t.Fatal(err)
	}
	call := runner.lastCall(t)
	wantArgs := []string{
		"-hide_banner", "-nostats",
		"-ss", "1.500",
		"-i", "movie.mp4",
		"-frames:v", "1", "-an",
		"-vf", "scale='min(512,iw)':'min(512,ih)':force_original_aspect_ratio=decrease",
		"-c:v", "mjpeg", "-q:v", "3",
		"-f", "image2", "-y", "out.jpg.part",
	}
	if !reflect.DeepEqual(call.Args, wantArgs) {
		t.Fatalf("keyframe argv mismatch:\n got %v\nwant %v", call.Args, wantArgs)
	}
}

func TestExecRunnerKillsProcessOnContextTimeout(t *testing.T) {
	var binary string
	var args []string
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("cmd"); err != nil {
			t.Skip("cmd.exe unavailable")
		}
		binary, args = "cmd", []string{"/C", "ping -n 30 127.0.0.1"}
	} else {
		if _, err := exec.LookPath("sleep"); err != nil {
			t.Skip("sleep unavailable")
		}
		binary, args = "sleep", []string{"30"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := ExecRunner{}.Run(ctx, binary, args...)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error after the context deadline killed the process")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("process was not killed promptly, took %s", elapsed)
	}
}

// TestRealFFmpegIntegration exercises the actual binaries when they are
// installed; environments without FFmpeg skip (never download binaries).
func TestRealFFmpegIntegration(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; integration test skipped")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed; integration test skipped")
	}
	dir := t.TempDir()
	sample := filepath.Join(dir, "sample.mp4")
	synth := exec.Command(ffmpegPath,
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=10",
		"-pix_fmt", "yuv420p", "-y", sample)
	if output, err := synth.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize sample video: %v (%s)", err, output)
	}

	ff, err := NewFFmpeg(FFmpegConfig{FFmpegPath: ffmpegPath, FFprobePath: ffprobePath, Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := ff.Probe(context.Background(), sample)
	if err != nil {
		t.Fatal(err)
	}
	if probe.DurationMS < 3500 || probe.DurationMS > 4500 || probe.Width != 320 || probe.Height != 240 {
		t.Fatalf("unexpected probe of synthetic video: %+v", probe)
	}
	boundaries, err := ff.DetectScenes(context.Background(), sample, probe.DurationMS)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundaries) == 0 {
		t.Fatal("scene detection must yield at least one boundary")
	}
	keyframe := filepath.Join(dir, "frame.jpg.part")
	if err := ff.ExtractKeyframe(context.Background(), sample, 1000, keyframe); err != nil {
		t.Fatal(err)
	}
	width, height, digest, err := inspectKeyframeJPEG(keyframe)
	if err != nil {
		t.Fatal(err)
	}
	if width > KeyframeMaxEdge || height > KeyframeMaxEdge || len(digest) != 64 {
		t.Fatalf("keyframe validation failed: %dx%d digest %q", width, height, digest)
	}
}
