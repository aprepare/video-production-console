package imagevideo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"video-production-console/internal/security"
)

var (
	ErrFFmpegNotConfigured = errors.New("image_video_ffmpeg_not_configured")
	ErrMediaProbe          = errors.New("image_video_media_probe_failed")
	ErrMediaDecode         = errors.New("image_video_media_decode_failed")
	ErrMediaTooShort       = errors.New("image_video_media_too_short")
	ErrMediaNormalize      = errors.New("image_video_media_normalize_failed")
)

const (
	mediaCommandTimeout = 20 * time.Minute
	maxMediaErrorRunes  = 2000
)

type MediaProbe struct {
	DurationMS         int64
	FrameCount         int64
	Width, Height      int
	FPS                float64
	Codec, PixelFormat string
}

type MediaProcessor interface {
	NormalizeAndVerify(context.Context, string, string, int) (MediaProbe, error)
}

// MediaCommandRunner executes one argv-only command. It is injectable so the
// production state machine can be verified without invoking local binaries.
type MediaCommandRunner interface {
	Run(context.Context, string, ...string) (stdout, stderr []byte, err error)
}

type execMediaCommandRunner struct{}

func (execMediaCommandRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = 5 * time.Second
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type mediaProcessor struct {
	ffmpeg, ffprobe string
	runner          MediaCommandRunner
	redactor        *security.Redactor
}

func NewMediaProcessor(runtime RuntimeSnapshot) (MediaProcessor, error) {
	redactor := security.NewRedactor()
	for _, secret := range []string{
		runtime.GrokAPIKey, runtime.RemixAPIKey, runtime.PexelsAPIKey,
		runtime.VolcSpeechAPIKey, runtime.AuraSTDTTsAPIKey, runtime.ImageAPIKey,
		runtime.ImageTextAPIKey, runtime.VisionAPIKey, runtime.EmbeddingAPIKey,
		runtime.PixabayAPIKey,
	} {
		redactor.Register(secret)
	}
	return NewMediaProcessorWithRunner(runtime, nil, redactor)
}

func NewMediaProcessorWithRunner(runtime RuntimeSnapshot, runner MediaCommandRunner, redactor *security.Redactor) (MediaProcessor, error) {
	ffmpeg := strings.TrimSpace(runtime.FFmpegPath)
	ffprobe := strings.TrimSpace(runtime.FFprobePath)
	if ffmpeg == "" || ffprobe == "" {
		return nil, ErrFFmpegNotConfigured
	}
	if runner == nil {
		runner = execMediaCommandRunner{}
	}
	if redactor == nil {
		redactor = security.NewRedactor()
	}
	return &mediaProcessor{ffmpeg: ffmpeg, ffprobe: ffprobe, runner: runner, redactor: redactor}, nil
}

func (p *mediaProcessor) run(ctx context.Context, kind error, binary string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, mediaCommandTimeout)
	defer cancel()
	stdout, stderr, err := p.runner.Run(commandCtx, binary, args...)
	if err == nil {
		return stdout, nil
	}
	detail := p.safeCommandError(stderr, err)
	if detail == "" {
		return nil, kind
	}
	return nil, fmt.Errorf("%w: %s", kind, detail)
}

func (p *mediaProcessor) safeCommandError(stderr []byte, commandErr error) string {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(string(stderr)); text != "" {
		parts = append(parts, text)
	}
	if commandErr != nil {
		parts = append(parts, commandErr.Error())
	}
	value := p.redactor.Redact(strings.Join(parts, ": "))
	runes := []rune(value)
	if len(runes) > maxMediaErrorRunes {
		value = string(runes[:maxMediaErrorRunes]) + "…"
	}
	return value
}

func (p *mediaProcessor) probe(ctx context.Context, mediaPath string) (MediaProbe, error) {
	output, err := p.run(ctx, ErrMediaProbe, p.ffprobe,
		"-v", "error", "-count_frames", "-print_format", "json",
		"-show_format", "-show_streams", mediaPath,
	)
	if err != nil {
		return MediaProbe{}, err
	}
	var payload struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			PixFmt     string `json:"pix_fmt"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			Avg        string `json:"avg_frame_rate"`
			Rate       string `json:"r_frame_rate"`
			Frames     string `json:"nb_frames"`
			FramesRead string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return MediaProbe{}, fmt.Errorf("%w: invalid ffprobe JSON", ErrMediaProbe)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(payload.Format.Duration), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return MediaProbe{}, fmt.Errorf("%w: invalid duration", ErrMediaProbe)
	}
	result := MediaProbe{DurationMS: int64(math.Round(seconds * 1000))}
	for _, stream := range payload.Streams {
		if stream.CodecType != "video" || result.Width != 0 {
			continue
		}
		result.Width = stream.Width
		result.Height = stream.Height
		result.Codec = strings.TrimSpace(stream.CodecName)
		result.PixelFormat = strings.TrimSpace(stream.PixFmt)
		result.FPS = parseMediaFPS(stream.Avg)
		if result.FPS == 0 {
			result.FPS = parseMediaFPS(stream.Rate)
		}
		result.FrameCount = parsePositiveFrameCount(stream.FramesRead)
		if result.FrameCount == 0 {
			result.FrameCount = parsePositiveFrameCount(stream.Frames)
		}
	}
	if result.Width <= 0 || result.Height <= 0 {
		return MediaProbe{}, fmt.Errorf("%w: no video stream", ErrMediaProbe)
	}
	if result.FrameCount <= 0 {
		return MediaProbe{}, fmt.Errorf("%w: video stream has no decoded frames", ErrMediaProbe)
	}
	return result, nil
}

func parsePositiveFrameCount(value string) int64 {
	frames, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || frames <= 0 {
		return 0
	}
	return frames
}

func parseMediaFPS(value string) float64 {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return 0
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || math.IsNaN(numerator) || math.IsInf(numerator, 0) {
		return 0
	}
	if len(parts) == 1 {
		return numerator
	}
	denominator, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || denominator == 0 || math.IsNaN(denominator) || math.IsInf(denominator, 0) {
		return 0
	}
	return numerator / denominator
}

func durationCovers(probe MediaProbe, requestedSeconds int) bool {
	if requestedSeconds <= 0 {
		return true
	}
	toleranceMS := int64(math.Ceil(1000 / float64(VideoFPS)))
	return probe.DurationMS+toleranceMS >= int64(requestedSeconds)*1000
}

func (p *mediaProcessor) NormalizeAndVerify(ctx context.Context, source, destination string, requestedSeconds int) (MediaProbe, error) {
	if requestedSeconds != 0 && requestedSeconds != 6 && requestedSeconds != 10 && requestedSeconds != 15 {
		return MediaProbe{}, fmt.Errorf("%w: requested duration must be 6, 10, or 15 seconds", ErrMediaNormalize)
	}
	initial, err := p.probe(ctx, source)
	if err != nil {
		return MediaProbe{}, err
	}
	if !durationCovers(initial, requestedSeconds) {
		return MediaProbe{}, fmt.Errorf("%w: duration=%dms requested=%ds", ErrMediaTooShort, initial.DurationMS, requestedSeconds)
	}
	if _, err = p.run(ctx, ErrMediaDecode, p.ffmpeg,
		"-hide_banner", "-nostats", "-v", "error", "-i", source,
		"-map", "0:v:0", "-f", "null", "-",
	); err != nil {
		return MediaProbe{}, err
	}

	destinationDir := filepath.Dir(destination)
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return MediaProbe{}, fmt.Errorf("%w: create output directory", ErrMediaNormalize)
	}
	temporary, err := os.CreateTemp(destinationDir, ".imagevideo-*.mp4")
	if err != nil {
		return MediaProbe{}, fmt.Errorf("%w: create temporary output", ErrMediaNormalize)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return MediaProbe{}, fmt.Errorf("%w: close temporary output", ErrMediaNormalize)
	}
	defer os.Remove(temporaryPath)

	filter := "scale=480:848:force_original_aspect_ratio=decrease,pad=480:848:(ow-iw)/2:(oh-ih)/2:color=black,fps=24"
	if _, err = p.run(ctx, ErrMediaNormalize, p.ffmpeg,
		"-hide_banner", "-nostats", "-v", "error", "-i", source,
		"-map", "0:v:0", "-vf", filter,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart",
		"-an", "-y", temporaryPath,
	); err != nil {
		return MediaProbe{}, err
	}

	result, err := p.probe(ctx, temporaryPath)
	if err != nil {
		return MediaProbe{}, fmt.Errorf("%w: output probe: %v", ErrMediaNormalize, err)
	}
	if result.Width != VideoWidth || result.Height != VideoHeight ||
		math.Abs(result.FPS-float64(VideoFPS)) > 0.01 ||
		!strings.EqualFold(result.Codec, "h264") ||
		!strings.EqualFold(result.PixelFormat, "yuv420p") {
		return MediaProbe{}, fmt.Errorf("%w: output metadata invalid", ErrMediaNormalize)
	}
	if !durationCovers(result, requestedSeconds) {
		return MediaProbe{}, fmt.Errorf("%w: normalized duration=%dms requested=%ds", ErrMediaTooShort, result.DurationMS, requestedSeconds)
	}
	if _, err = p.run(ctx, ErrMediaDecode, p.ffmpeg,
		"-hide_banner", "-nostats", "-v", "error", "-i", temporaryPath,
		"-map", "0:v:0", "-f", "null", "-",
	); err != nil {
		return MediaProbe{}, fmt.Errorf("%w: normalized output", err)
	}

	output, err := os.Open(temporaryPath)
	if err != nil {
		return MediaProbe{}, fmt.Errorf("%w: open temporary output", ErrMediaNormalize)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return MediaProbe{}, fmt.Errorf("%w: sync temporary output", ErrMediaNormalize)
	}
	if err := output.Close(); err != nil {
		return MediaProbe{}, fmt.Errorf("%w: close temporary output", ErrMediaNormalize)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return MediaProbe{}, fmt.Errorf("%w: commit output", ErrMediaNormalize)
	}
	return result, nil
}
