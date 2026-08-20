package mediacatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BundledPaths returns the partner-payload FFmpeg/FFprobe locations.
func BundledPaths(appRoot string) (ffmpegPath, ffprobePath string) {
	return filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffmpeg.exe"),
		filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffprobe.exe")
}

// ErrFFmpegNotConfigured is returned when the caller did not inject validated
// ffmpeg/ffprobe binary paths (they come from settings, never from PATH).
var ErrFFmpegNotConfigured = errors.New("ffmpeg_not_configured: ffmpeg and ffprobe paths must be provided via settings")

const (
	// DefaultSceneThreshold feeds ffmpeg's select='gt(scene,THRESHOLD)'.
	DefaultSceneThreshold = 0.32
	// MinShotDurationMS: shorter boundaries are merged into a neighbour.
	MinShotDurationMS = int64(1500)
	// MaxShotDurationMS: longer boundaries are split into equal parts.
	MaxShotDurationMS = int64(12000)
	// KeyframeMaxEdge caps the longest edge of every extracted keyframe.
	KeyframeMaxEdge = 512

	keyframeJPEGQuality   = "3"
	keyframeScaleFilter   = "scale='min(512,iw)':'min(512,ih)':force_original_aspect_ratio=decrease"
	defaultCommandTimeout = 10 * time.Minute
)

// CommandRunner executes one binary with argv arguments. Implementations must
// never pass anything through a shell and must kill the child process when the
// context is done.
type CommandRunner interface {
	Run(ctx context.Context, binary string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner is the real CommandRunner: direct argv execution, no shell, and
// context cancellation kills the child process.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = fmt.Errorf("%s killed by context: %w", binary, ctxErr)
	} else if err != nil {
		err = fmt.Errorf("run %s: %w", binary, err)
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// Probe is the parsed ffprobe result for one media file.
type Probe struct {
	DurationMS int64
	Width      int
	Height     int
	FPS        float64
	HasAudio   bool
}

// SceneBoundary is a half-open shot window [InMS, OutMS) on the source.
type SceneBoundary struct {
	InMS  int64
	OutMS int64
}

// FFmpegConfig carries validated binary paths injected by the caller
// (they originate from settings; this package never reads PATH implicitly).
type FFmpegConfig struct {
	FFmpegPath     string
	FFprobePath    string
	SceneThreshold float64       // 0 means DefaultSceneThreshold
	Timeout        time.Duration // 0 means defaultCommandTimeout per command
	Runner         CommandRunner // nil means ExecRunner
}

// FFmpeg wraps controlled ffmpeg/ffprobe invocations.
type FFmpeg struct {
	ffmpegPath  string
	ffprobePath string
	threshold   float64
	timeout     time.Duration
	runner      CommandRunner
}

func NewFFmpeg(cfg FFmpegConfig) (*FFmpeg, error) {
	if strings.TrimSpace(cfg.FFmpegPath) == "" || strings.TrimSpace(cfg.FFprobePath) == "" {
		return nil, ErrFFmpegNotConfigured
	}
	threshold := cfg.SceneThreshold
	if threshold <= 0 || threshold >= 1 {
		threshold = DefaultSceneThreshold
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	runner := cfg.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &FFmpeg{
		ffmpegPath:  cfg.FFmpegPath,
		ffprobePath: cfg.FFprobePath,
		threshold:   threshold,
		timeout:     timeout,
		runner:      runner,
	}, nil
}

func (f *FFmpeg) run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	return f.runner.Run(ctx, binary, args...)
}

// Probe runs ffprobe with JSON output and parses duration, dimensions, frame
// rate, and audio presence.
func (f *FFmpeg) Probe(ctx context.Context, path string) (Probe, error) {
	stdout, stderr, err := f.run(ctx, f.ffprobePath,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return Probe{}, fmt.Errorf("ffprobe failed: %w (%s)", err, truncateCommandOutput(stderr))
	}
	var payload struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
			RFrameRate   string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(stdout, &payload); err != nil {
		return Probe{}, fmt.Errorf("parse ffprobe json: %w", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(payload.Format.Duration), 64)
	if err != nil || seconds <= 0 {
		return Probe{}, fmt.Errorf("%w: ffprobe reported no usable duration", ErrInvalidValue)
	}
	probe := Probe{DurationMS: int64(math.Round(seconds * 1000))}
	for _, stream := range payload.Streams {
		switch stream.CodecType {
		case "video":
			if probe.Width == 0 {
				probe.Width = stream.Width
				probe.Height = stream.Height
				rate := stream.AvgFrameRate
				if rate == "" || rate == "0/0" {
					rate = stream.RFrameRate
				}
				probe.FPS = parseFrameRateFraction(rate)
			}
		case "audio":
			probe.HasAudio = true
		}
	}
	if probe.Width <= 0 || probe.Height <= 0 {
		return Probe{}, fmt.Errorf("%w: ffprobe found no video stream", ErrInvalidValue)
	}
	return probe, nil
}

func parseFrameRateFraction(value string) float64 {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) == 1 {
		return numerator
	}
	denominator, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || denominator == 0 {
		return 0
	}
	return numerator / denominator
}

// DetectScenes runs ffmpeg scene detection (select + showinfo) and returns
// normalized shot boundaries covering [0, durationMS).
func (f *FFmpeg) DetectScenes(ctx context.Context, path string, durationMS int64) ([]SceneBoundary, error) {
	if durationMS <= 0 {
		return nil, fmt.Errorf("%w: scene detection needs a positive duration", ErrInvalidValue)
	}
	filter := fmt.Sprintf("select='gt(scene,%s)',showinfo",
		strconv.FormatFloat(f.threshold, 'f', -1, 64))
	_, stderr, err := f.run(ctx, f.ffmpegPath,
		"-hide_banner", "-nostats", "-i", path, "-vf", filter, "-an", "-f", "null", "-")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg scene detection failed: %w (%s)", err, truncateCommandOutput(stderr))
	}
	cuts := parseShowinfoTimestampsMS(stderr)
	return NormalizeSceneBoundaries(cuts, durationMS), nil
}

var showinfoPTSPattern = regexp.MustCompile(`pts_time:([0-9]+(?:\.[0-9]+)?)`)

func parseShowinfoTimestampsMS(stderr []byte) []int64 {
	var cuts []int64
	for _, line := range strings.Split(string(stderr), "\n") {
		if !strings.Contains(line, "Parsed_showinfo") {
			continue
		}
		match := showinfoPTSPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		seconds, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			continue
		}
		cuts = append(cuts, int64(math.Round(seconds*1000)))
	}
	return cuts
}

// NormalizeSceneBoundaries turns interior cut timestamps into shot windows:
// segments shorter than MinShotDurationMS merge into a neighbour, segments
// longer than MaxShotDurationMS split into equal parts, and the result is
// stably sorted by InMS.
func NormalizeSceneBoundaries(cutsMS []int64, durationMS int64) []SceneBoundary {
	if durationMS <= 0 {
		return nil
	}
	interior := make([]int64, 0, len(cutsMS))
	for _, cut := range cutsMS {
		if cut > 0 && cut < durationMS {
			interior = append(interior, cut)
		}
	}
	sort.Slice(interior, func(i, j int) bool { return interior[i] < interior[j] })
	segments := []SceneBoundary{}
	previous := int64(0)
	for _, cut := range interior {
		if cut == previous {
			continue
		}
		segments = append(segments, SceneBoundary{InMS: previous, OutMS: cut})
		previous = cut
	}
	segments = append(segments, SceneBoundary{InMS: previous, OutMS: durationMS})

	// Merge too-short segments into a neighbour until none remain (a single
	// short segment for a very short source is kept as-is).
	for len(segments) > 1 {
		shortIndex := -1
		for i, segment := range segments {
			if segment.OutMS-segment.InMS < MinShotDurationMS {
				shortIndex = i
				break
			}
		}
		if shortIndex < 0 {
			break
		}
		if shortIndex == 0 {
			segments[1].InMS = segments[0].InMS
			segments = segments[1:]
		} else {
			segments[shortIndex-1].OutMS = segments[shortIndex].OutMS
			segments = append(segments[:shortIndex], segments[shortIndex+1:]...)
		}
	}

	// Split too-long segments into equal parts.
	normalized := make([]SceneBoundary, 0, len(segments))
	for _, segment := range segments {
		length := segment.OutMS - segment.InMS
		if length <= MaxShotDurationMS {
			normalized = append(normalized, segment)
			continue
		}
		parts := (length + MaxShotDurationMS - 1) / MaxShotDurationMS
		for part := int64(0); part < parts; part++ {
			in := segment.InMS + length*part/parts
			out := segment.InMS + length*(part+1)/parts
			normalized = append(normalized, SceneBoundary{InMS: in, OutMS: out})
		}
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].InMS != normalized[j].InMS {
			return normalized[i].InMS < normalized[j].InMS
		}
		return normalized[i].OutMS < normalized[j].OutMS
	})
	return normalized
}

// ExtractKeyframe writes one downscaled (longest edge <= 512px), audio-free,
// fixed-quality JPEG frame taken at atMS to destPath.
func (f *FFmpeg) ExtractKeyframe(ctx context.Context, sourcePath string, atMS int64, destPath string) error {
	if atMS < 0 {
		return fmt.Errorf("%w: keyframe timestamp cannot be negative", ErrInvalidValue)
	}
	_, stderr, err := f.run(ctx, f.ffmpegPath,
		"-hide_banner", "-nostats",
		"-ss", formatSecondsArg(atMS),
		"-i", sourcePath,
		"-frames:v", "1", "-an",
		"-vf", keyframeScaleFilter,
		"-c:v", "mjpeg", "-q:v", keyframeJPEGQuality,
		"-f", "image2", "-y", destPath)
	if err != nil {
		return fmt.Errorf("ffmpeg keyframe extraction failed: %w (%s)", err, truncateCommandOutput(stderr))
	}
	return nil
}

func formatSecondsArg(atMS int64) string {
	return strconv.FormatFloat(float64(atMS)/1000, 'f', 3, 64)
}

func truncateCommandOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	const limit = 300
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
