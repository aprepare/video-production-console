package videoqc

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandOutputLimitCannotBeBypassedByCopy(t *testing.T) {
	output := &boundedBuffer{limit: 100}
	_, err := io.Copy(output, io.LimitReader(zerosReader{}, 1000))
	if err == nil || output.Len() > 100 {
		t.Fatalf("unbounded copy: len=%d err=%v", output.Len(), err)
	}
}

func TestLowResolutionFramesAreNotUpscaled(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	video := filepath.Join(t.TempDir(), "low.mp4")
	if out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1", "-c:v", "mpeg4", "-pix_fmt", "yuv420p", video).CombinedOutput(); err != nil {
		t.Fatalf("video %v %s", err, out)
	}
	raw, err := extractFrame(ctx, ffmpeg, video, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil || info.Width != 320 || info.Height != 240 {
		t.Fatalf("frame was upscaled: %+v %v", info, err)
	}
}

func TestProbeVideoMetadata(t *testing.T) {
	info, err := parseProbe([]byte(`{"streams":[{"width":1920,"height":1080,"duration":"3.2","avg_frame_rate":"30000/1001"}],"format":{"duration":"4.0"}}`))
	if err != nil || info.DurationS != 3.2 || info.Width != 1920 || info.Height != 1080 || info.FPS < 29.9 || info.FPS > 30 {
		t.Fatalf("%+v %v", info, err)
	}
	for _, raw := range []string{`{}`, `{"streams":[{"width":0,"height":10}],"format":{"duration":"2"}}`, `{"streams":[{"width":10,"height":10}],"format":{"duration":"NaN"}}`} {
		if _, err := parseProbe([]byte(raw)); err == nil {
			t.Fatalf("invalid %s", raw)
		}
	}
}

func TestFFmpegIntegration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	video := filepath.Join(t.TempDir(), "scene-cuts.mp4")
	output, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=black:size=960x540:rate=10:duration=1", "-f", "lavfi", "-i", "color=white:size=960x540:rate=10:duration=1", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-c:v", "mpeg4", "-q:v", "4", video).CombinedOutput()
	if err != nil {
		t.Fatalf("test video: %v %s", err, output)
	}
	info, cuts, err := inspectMedia(ctx, ffmpeg, ffprobe, video)
	if err != nil || info.DurationS < 1.9 || info.DurationS > 2.1 || info.Width != 960 || info.Height != 540 || len(cuts) != 1 {
		t.Fatalf("media %+v cuts=%v error=%v", info, cuts, err)
	}
	frames := sampleFrames(info.DurationS, info.FPS, cuts)
	if len(frames) != 4 {
		t.Fatalf("expected base and transition frames: %+v", frames)
	}
	for _, frame := range frames {
		raw, err := extractFrame(ctx, ffmpeg, video, frame.TimeS)
		if err != nil || !validJPEG(raw) {
			t.Fatalf("frame %d: bytes=%d error=%v", frame.Index, len(raw), err)
		}
	}
	if _, _, err := inspectMedia(ctx, ffmpeg, ffprobe, filepath.Join(t.TempDir(), "missing.mp4")); err == nil {
		t.Fatal("missing video accepted")
	}
}
