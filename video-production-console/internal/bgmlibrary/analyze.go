package bgmlibrary

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	analysisTimeout = 3 * time.Minute
	// silenceDropDB is how far below the track's peak RMS a one-second
	// window may fall before it counts as "quiet tail".
	silenceDropDB = 30.0
	// silenceFloorDB is the absolute floor: anything louder is never
	// treated as silence even on very loud masters.
	silenceFloorDB = -45.0
	// climax window bounds in seconds.
	climaxMaxSeconds = 55.0
	climaxMinSeconds = 20.0
)

type windowAnalysis struct {
	DurationS       float64
	UsableHeadS     float64
	ClimaxStartS    float64
	ClimaxDurationS float64
}

// loudnessProfile decodes the file with ffmpeg and returns one RMS level (dB)
// per second of audio.
func loudnessProfile(ctx context.Context, ffmpegPath, path string) ([]float64, error) {
	binary := strings.TrimSpace(ffmpegPath)
	if binary == "" {
		binary = "ffmpeg"
	}
	runCtx, cancel := context.WithTimeout(ctx, analysisTimeout)
	defer cancel()
	command := exec.CommandContext(runCtx, binary,
		"-hide_banner", "-nostats", "-v", "error",
		"-i", path,
		"-af", "aresample=44100,asetnsamples=n=44100,astats=metadata=1:reset=1:measure_perchannel=none,ametadata=mode=print:key=lavfi.astats.Overall.RMS_level:file=-",
		"-f", "null", "-",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = nil
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	var levels []float64
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		value, ok := strings.CutPrefix(line, "lavfi.astats.Overall.RMS_level=")
		if !ok {
			continue
		}
		level, parseErr := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if parseErr != nil || math.IsNaN(level) {
			level = math.Inf(-1) // "-inf" marks silence
		}
		levels = append(levels, level)
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg analysis failed: %w", err)
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no loudness windows")
	}
	return levels, nil
}

// analyzeWindows derives the montage windows from per-second RMS levels.
//
// Usable head: everything up to the last window that is still within
// silenceDropDB of the track peak — the quiet tail after it is cut.
// Climax: the loudest sustained window inside the first 70% of the usable
// head, with its start nudged onto a loud second so a splice lands on sound.
func analyzeWindows(levels []float64) windowAnalysis {
	duration := float64(len(levels))
	peak := math.Inf(-1)
	for _, level := range levels {
		if level > peak {
			peak = level
		}
	}
	threshold := peak - silenceDropDB
	if threshold < silenceFloorDB {
		threshold = silenceFloorDB
	}
	lastLoud := -1
	for i := len(levels) - 1; i >= 0; i-- {
		if levels[i] >= threshold {
			lastLoud = i
			break
		}
	}
	usable := duration
	if lastLoud >= 0 {
		usable = float64(lastLoud + 1)
	}
	if usable < 1 {
		usable = duration
	}

	window := climaxMaxSeconds
	if limit := usable * 0.4; limit < window {
		window = limit
	}
	if window < climaxMinSeconds {
		window = math.Min(climaxMinSeconds, usable)
	}
	windowLen := int(window)
	if windowLen < 1 {
		windowLen = 1
	}
	// The climax must sit inside the first 70% of the usable head so the
	// splice still leaves room before the quiet tail.
	searchEnd := int(usable*0.7) - windowLen
	if searchEnd < 0 {
		searchEnd = 0
	}
	bestStart, bestMean := 0, math.Inf(-1)
	for start := 0; start <= searchEnd; start++ {
		mean := meanLevel(levels[start : start+windowLen])
		if mean > bestMean {
			bestMean, bestStart = mean, start
		}
	}
	// Nudge the start forward onto the first second that is at least as
	// loud as the window average, so the spliced-in audio begins on sound.
	windowMean := meanLevel(levels[bestStart : bestStart+windowLen])
	start := bestStart
	for offset := 0; offset < 5 && start+offset < bestStart+windowLen; offset++ {
		if levels[start+offset] >= windowMean-3 {
			start += offset
			break
		}
	}
	climaxDuration := float64(windowLen)
	if float64(start)+climaxDuration > usable {
		climaxDuration = usable - float64(start)
	}
	return windowAnalysis{
		DurationS:    duration,
		UsableHeadS:  round1(usable),
		ClimaxStartS: round1(float64(start)), ClimaxDurationS: round1(climaxDuration),
	}
}

func meanLevel(levels []float64) float64 {
	if len(levels) == 0 {
		return math.Inf(-1)
	}
	total := 0.0
	for _, level := range levels {
		if math.IsInf(level, -1) {
			level = -90
		}
		total += level
	}
	return total / float64(len(levels))
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}
