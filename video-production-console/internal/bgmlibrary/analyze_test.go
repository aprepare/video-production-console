package bgmlibrary

import (
	"math"
	"testing"
)

// buildLevels composes a per-second RMS profile from (seconds, level) runs.
func buildLevels(runs ...[2]float64) []float64 {
	var levels []float64
	for _, run := range runs {
		for i := 0; i < int(run[0]); i++ {
			levels = append(levels, run[1])
		}
	}
	return levels
}

func TestAnalyzeWindowsTrimsQuietTail(t *testing.T) {
	// 180s of music at -20dB followed by a 40s fade below the threshold.
	levels := buildLevels([2]float64{180, -20}, [2]float64{40, -60})
	analysis := analyzeWindows(levels)
	if analysis.DurationS != 220 {
		t.Fatalf("duration=%v", analysis.DurationS)
	}
	if analysis.UsableHeadS != 180 {
		t.Fatalf("usable head should stop before the quiet tail, got %v", analysis.UsableHeadS)
	}
	if analysis.ClimaxStartS+analysis.ClimaxDurationS > analysis.UsableHeadS {
		t.Fatalf("climax escapes usable head: %+v", analysis)
	}
}

func TestAnalyzeWindowsPicksLoudestSustainedSegment(t *testing.T) {
	// Quiet intro, loud chorus at 60s..130s, mid outro. Climax should start
	// inside the chorus, on a loud second.
	levels := buildLevels(
		[2]float64{60, -30},
		[2]float64{70, -12},
		[2]float64{80, -25},
	)
	analysis := analyzeWindows(levels)
	if analysis.ClimaxStartS < 55 || analysis.ClimaxStartS > 90 {
		t.Fatalf("climax start %v should land in the loud chorus", analysis.ClimaxStartS)
	}
	if analysis.ClimaxDurationS < climaxMinSeconds {
		t.Fatalf("climax duration too short: %v", analysis.ClimaxDurationS)
	}
	if analysis.UsableHeadS != 210 {
		t.Fatalf("no quiet tail here (outro is above threshold), got %v", analysis.UsableHeadS)
	}
}

func TestAnalyzeWindowsSurvivesFullSilenceAndShortTracks(t *testing.T) {
	silent := analyzeWindows(buildLevels([2]float64{30, math.Inf(-1)}))
	if silent.UsableHeadS <= 0 || silent.DurationS != 30 {
		t.Fatalf("silent track must fall back to full duration: %+v", silent)
	}
	short := analyzeWindows(buildLevels([2]float64{8, -15}))
	if short.UsableHeadS != 8 || short.ClimaxStartS < 0 || short.ClimaxDurationS <= 0 {
		t.Fatalf("short track windows invalid: %+v", short)
	}
	if short.ClimaxStartS+short.ClimaxDurationS > short.DurationS+0.001 {
		t.Fatalf("short track climax out of range: %+v", short)
	}
}
