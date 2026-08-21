package imagevideo

import (
	"fmt"
	"strings"
	"testing"

	"video-production-console/internal/narration"
)

func TestChooseVideoTierUsesSmallestCoveringDuration(t *testing.T) {
	cases := []struct {
		us   int64
		want int
	}{
		{4_300_000, 6},
		{6_000_000, 6},
		{6_000_001, 10},
		{10_000_001, 15},
	}
	for _, test := range cases {
		got, err := ChooseVideoTier(test.us)
		if err != nil || got != test.want {
			t.Fatalf("duration %d: got %d err=%v want %d", test.us, got, err, test.want)
		}
	}
	if _, err := ChooseVideoTier(15_000_001); err == nil {
		t.Fatal("expected error above 15 seconds")
	}
}

func TestBuildScenesFromTimingKeepsFirst30SecondsBetween43And46(t *testing.T) {
	timing := mustTimingFromSentences(t, 8, 4.5)
	scenes, err := BuildScenesFromTiming(timing)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) == 0 {
		t.Fatal("expected scenes")
	}
	var early int
	for _, scene := range scenes {
		if scene.EndUS > firstThirtySecondsUS {
			break
		}
		early++
		if scene.TimelineDurationUS < slideshowEarlyMinUS || scene.TimelineDurationUS > slideshowEarlyMaxUS+500_000 {
			t.Fatalf("early scene %d duration=%d", scene.Ordinal, scene.TimelineDurationUS)
		}
	}
	if early == 0 {
		t.Fatal("expected scenes fully inside the first 30 seconds")
	}
}

func TestBuildScenesFromTimingAfter30SecondsAreAtLeast6Seconds(t *testing.T) {
	timing := mustTimingFromSentences(t, 12, 4.5)
	scenes, err := BuildScenesFromTiming(timing)
	if err != nil {
		t.Fatal(err)
	}
	var later int
	for _, scene := range scenes {
		if scene.StartUS < firstThirtySecondsUS {
			continue
		}
		later++
		if scene.TimelineDurationUS < slideshowLaterMinUS && scene.Ordinal != scenes[len(scenes)-1].Ordinal {
			t.Fatalf("later scene %d duration=%d", scene.Ordinal, scene.TimelineDurationUS)
		}
	}
	if later == 0 {
		t.Fatal("expected scenes after 30 seconds")
	}
}

func TestBuildScenesFromTimingPreservesSpokenText(t *testing.T) {
	timing := mustTimingFromSentences(t, 10, 4.5)
	scenes, err := BuildScenesFromTiming(timing)
	if err != nil {
		t.Fatal(err)
	}
	if alignmentText(joinSceneText(scenes)) != alignmentText(timing.Script) {
		t.Fatalf("text mismatch scenes=%q script=%q", joinSceneText(scenes), timing.Script)
	}
	if scenes[len(scenes)-1].EndUS != secondsToUS(timing.Duration) {
		t.Fatalf("end=%d duration=%d", scenes[len(scenes)-1].EndUS, secondsToUS(timing.Duration))
	}
}

func TestBuildScenesFromTimingAllowsMoreThanEighteenScenes(t *testing.T) {
	timing := mustTimingFromSentences(t, 40, 4.5)
	scenes, err := BuildScenesFromTiming(timing)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) <= 18 {
		t.Fatalf("scenes=%d, want more than 18", len(scenes))
	}
	if len(scenes) > MaxTimedScenes {
		t.Fatalf("scenes=%d, want at most %d", len(scenes), MaxTimedScenes)
	}
}

func TestBuildScenesFromTimingCapsAtSixty(t *testing.T) {
	timing := mustTimingFromSentences(t, 200, 4.5)
	scenes, err := BuildScenesFromTiming(timing)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != MaxTimedScenes {
		t.Fatalf("scenes=%d, want %d", len(scenes), MaxTimedScenes)
	}
	if alignmentText(joinSceneText(scenes)) != alignmentText(timing.Script) {
		t.Fatal("capped scenes dropped spoken text")
	}
}

func TestBuildScenesFromTimingRequiresWords(t *testing.T) {
	if _, err := BuildScenesFromTiming(narration.WordTimingDocument{}); err == nil {
		t.Fatal("expected error")
	}
}

func mustTimingFromSentences(t *testing.T, count int, secondsEach float64) narration.WordTimingDocument {
	t.Helper()
	script, words := timedSentences(count, secondsEach)
	doc, err := narration.NewWordTimingDocument(script, "test", "", words)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func timedSentences(count int, secondsEach float64) (string, []narration.Word) {
	var script strings.Builder
	words := make([]narration.Word, 0, count)
	cursor := 0.0
	for index := 0; index < count; index++ {
		text := fmt.Sprintf("第%03d段旁白。", index+1)
		words = append(words, narration.Word{
			Text:      text,
			StartTime: cursor,
			EndTime:   cursor + secondsEach - 0.05,
		})
		script.WriteString(text)
		cursor += secondsEach
	}
	return script.String(), words
}
