package montageplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

// quotaCandidates builds an in-memory candidate pool: brollN whole clips,
// movieSources files each cut into shotsPerSource shots, and imageN stills.
func quotaCandidates(brollN, movieSources, shotsPerSource, imageN int) []rankedCandidate {
	out := make([]rankedCandidate, 0, brollN+movieSources*shotsPerSource+imageN)
	brollCategories := []string{"Nature_Landscape", "Architecture", "City_Traffic", "Family_Life"}
	for i := 0; i < brollN; i++ {
		out = append(out, rankedCandidate{Item: mediaItem{
			ID:              fmt.Sprintf("broll-%02d", i),
			Kind:            mediaKindBroll,
			Category:        brollCategories[i%len(brollCategories)],
			RelativePath:    fmt.Sprintf("broll/b%02d.mp4", i),
			DurationSeconds: 30,
		}})
	}
	movieCategories := []string{"office", "street", "home"}
	for s := 0; s < movieSources; s++ {
		for k := 0; k < shotsPerSource; k++ {
			in := float64(k * 20)
			out = append(out, rankedCandidate{Item: mediaItem{
				ID:               fmt.Sprintf("movie-%02d-%d", s, k),
				Kind:             mediaKindMovie,
				Category:         movieCategories[s%len(movieCategories)],
				RelativePath:     fmt.Sprintf("movies/m%02d.mp4", s),
				DurationSeconds:  600,
				SourceInSeconds:  in,
				SourceOutSeconds: in + 6,
				ShotID:           fmt.Sprintf("shot-%02d-%d", s, k),
			}})
		}
	}
	imageCategories := []string{"ledger", "chart", "people"}
	for i := 0; i < imageN; i++ {
		out = append(out, rankedCandidate{Item: mediaItem{
			ID:           fmt.Sprintf("image-%02d", i),
			Kind:         mediaKindImage,
			Category:     imageCategories[i%len(imageCategories)],
			RelativePath: fmt.Sprintf("images/i%02d.png", i),
		}})
	}
	return out
}

func assertContiguousTimeline(t *testing.T, segments []plannedMedia, duration float64) {
	t.Helper()
	if len(segments) == 0 {
		t.Fatal("timeline is empty")
	}
	if segments[0].StartS != 0 {
		t.Fatalf("first segment starts at %v", segments[0].StartS)
	}
	for i := 1; i < len(segments); i++ {
		if math.Abs(segments[i].StartS-segments[i-1].EndS) > 1e-6 {
			t.Fatalf("segment %d starts at %v but previous ends at %v", i, segments[i].StartS, segments[i-1].EndS)
		}
		if segments[i].EndS <= segments[i].StartS {
			t.Fatalf("segment %d has non-positive length: %+v", i, segments[i])
		}
	}
	if last := segments[len(segments)-1].EndS; math.Abs(last-duration) > 1e-6 {
		t.Fatalf("timeline ends at %v, want %v", last, duration)
	}
}

func kindSeconds(segments []plannedMedia) map[mediaKind]float64 {
	used := map[mediaKind]float64{}
	for _, seg := range segments {
		used[seg.Item.Kind] += seg.EndS - seg.StartS
	}
	return used
}

func TestSelectTimelineMeetsDurationQuotas(t *testing.T) {
	const duration = 300.0
	candidates := quotaCandidates(24, 20, 2, 20)
	segments, warnings, err := selectTimeline(candidates, duration, "task-quota", movieMixPolicy())
	if err != nil {
		t.Fatalf("selectTimeline: %v", err)
	}
	assertContiguousTimeline(t, segments, duration)
	if len(warnings) != 0 {
		t.Fatalf("full library must produce no warnings: %#v", warnings)
	}
	used := kindSeconds(segments)
	policy := movieMixPolicy()
	for kind, target := range map[mediaKind]quotaRange{
		mediaKindBroll: policy.Targets[mediaKindBroll],
		mediaKindMovie: policy.Targets[mediaKindMovie],
		mediaKindImage: policy.Targets[mediaKindImage],
	} {
		fraction := used[kind] / duration
		if fraction < target.Min-1e-9 || fraction > target.Max+1e-9 {
			t.Fatalf("%s fraction %.4f outside [%v, %v]; used=%v", kind, fraction, target.Min, target.Max, used)
		}
	}
}

func TestSelectTimelineNeverRepeatsShotAndSpacesSourceReuse(t *testing.T) {
	const duration = 300.0
	policy := movieMixPolicy()
	candidates := quotaCandidates(24, 12, 2, 20)
	segments, warnings, err := selectTimeline(candidates, duration, "task-reuse", policy)
	if err != nil {
		t.Fatalf("selectTimeline: %v", err)
	}
	assertContiguousTimeline(t, segments, duration)
	if len(warnings) != 0 {
		t.Fatalf("library is large enough, warnings = %#v", warnings)
	}
	seenShots := map[string]int{}
	sourceUses := map[string][]int{}
	movieReused := false
	for i, seg := range segments {
		shot := seg.Item.shotKey()
		if prev, ok := seenShots[shot]; ok {
			t.Fatalf("shot %q repeated at segments %d and %d", shot, prev, i)
		}
		seenShots[shot] = i
		source := seg.Item.sourceKey()
		sourceUses[source] = append(sourceUses[source], i)
	}
	for source, uses := range sourceUses {
		if len(uses) > policy.MaxSourceUses {
			t.Fatalf("source %q used %d times: %v", source, len(uses), uses)
		}
		for j := 1; j < len(uses); j++ {
			if gap := uses[j] - uses[j-1]; gap <= policy.MinSegmentsBetweenReuse {
				t.Fatalf("source %q reused after %d segments (uses %v)", source, gap, uses)
			}
		}
		if len(uses) == 2 {
			movieReused = true
		}
	}
	if !movieReused {
		t.Fatal("fixture is sized so at least one source must be reused twice")
	}
}

func TestSelectTimelineIsStableForSameSeed(t *testing.T) {
	const duration = 300.0
	candidates := quotaCandidates(24, 20, 2, 20)
	encode := func(segments []plannedMedia, warnings []quotaWarning) []byte {
		raw, err := json.Marshal(map[string]any{"segments": segments, "warnings": warnings})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}
	first, firstWarn, err := selectTimeline(candidates, duration, "task-stable", movieMixPolicy())
	if err != nil {
		t.Fatalf("selectTimeline: %v", err)
	}
	second, secondWarn, err := selectTimeline(candidates, duration, "task-stable", movieMixPolicy())
	if err != nil {
		t.Fatalf("selectTimeline retry: %v", err)
	}
	if !bytes.Equal(encode(first, firstWarn), encode(second, secondWarn)) {
		t.Fatal("same seed must produce byte-identical output")
	}
	other, _, err := selectTimeline(candidates, duration, "task-other-seed", movieMixPolicy())
	if err != nil {
		t.Fatalf("selectTimeline other seed: %v", err)
	}
	assertContiguousTimeline(t, other, duration)
	seen := map[string]bool{}
	for _, seg := range other {
		if seen[seg.Item.shotKey()] {
			t.Fatalf("other seed repeated shot %q", seg.Item.shotKey())
		}
		seen[seg.Item.shotKey()] = true
	}
}

func TestSelectTimelineImageVideoPresetPrefersImages(t *testing.T) {
	const duration = 120.0
	candidates := quotaCandidates(10, 4, 2, 30)
	segments, warnings, err := selectTimeline(candidates, duration, "task-image-video", imageVideoPolicy())
	if err != nil {
		t.Fatalf("selectTimeline: %v", err)
	}
	assertContiguousTimeline(t, segments, duration)
	if len(warnings) != 0 {
		t.Fatalf("image-rich library must satisfy the preset: %#v", warnings)
	}
	used := kindSeconds(segments)
	if fraction := used[mediaKindImage] / duration; fraction < 0.85 {
		t.Fatalf("image fraction %.4f below 0.85; used=%v", fraction, used)
	}
	if used[mediaKindMovie] != 0 {
		t.Fatalf("image_video preset must not select movie footage: %v", used)
	}
	if fraction := used[mediaKindBroll] / duration; fraction > 0.15+1e-9 {
		t.Fatalf("broll fraction %.4f above 0.15; used=%v", fraction, used)
	}
}

func landscapeOnlyCandidates(n int) []rankedCandidate {
	out := make([]rankedCandidate, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, rankedCandidate{Item: mediaItem{
			ID:              fmt.Sprintf("land-%02d", i),
			Kind:            mediaKindBroll,
			Category:        "Nature_Landscape",
			RelativePath:    fmt.Sprintf("landscape/l%02d.mp4", i),
			DurationSeconds: 30,
		}})
	}
	return out
}

func TestSelectTimelineV2KeepsIntentShotsInTheirWindow(t *testing.T) {
	early := rankedCandidate{
		Item: mediaItem{
			ID: "early", Kind: mediaKindBroll, RelativePath: "early.mp4",
			DurationSeconds: 20, ShotID: "shot-early",
		},
		Score: 0.20,
		Match: MatchEvidence{IntentID: "seg-001", Level: matchLevelDirect, Reason: "early housing"},
	}
	late := rankedCandidate{
		Item: mediaItem{
			ID: "late", Kind: mediaKindBroll, RelativePath: "late.mp4",
			DurationSeconds: 20, ShotID: "shot-late",
		},
		Score: 0.95,
		Match: MatchEvidence{IntentID: "seg-002", Level: matchLevelDirect, Reason: "late housing"},
	}
	fillers := make([]rankedCandidate, 0, 6)
	for i := 0; i < 6; i++ {
		fillers = append(fillers, rankedCandidate{
			Item: mediaItem{
				ID: fmt.Sprintf("fill-%02d", i), Kind: mediaKindBroll,
				RelativePath: fmt.Sprintf("fill-%02d.mp4", i), DurationSeconds: 20,
				ShotID: fmt.Sprintf("shot-fill-%02d", i),
			},
			Score: 0,
			Match: MatchEvidence{IntentID: "catalog-fill", Level: matchLevelNeutral, Reason: "fill"},
		})
	}
	intents := []NarrativeIntent{
		{SegmentID: "seg-001", StartMS: 0, EndMS: 8000, Importance: 0.8},
		{SegmentID: "seg-002", StartMS: 8000, EndMS: 20000, Importance: 0.9},
	}
	segments, _, err := selectTimelineV2(append([]rankedCandidate{early, late}, fillers...), 20, "task-align", movieMixPolicy(), intents)
	if err != nil {
		t.Fatalf("selectTimelineV2: %v", err)
	}
	assertContiguousTimeline(t, segments, 20)
	if segments[0].Item.ID != "early" {
		t.Fatalf("first slot used %q, want early even though late scored higher", segments[0].Item.ID)
	}
	sawLate := false
	for _, seg := range segments {
		if seg.Item.ID == "late" {
			sawLate = true
			if seg.StartS < 7.5 {
				t.Fatalf("late shot used at %v, want it reserved for its 8s window", seg.StartS)
			}
		}
	}
	if !sawLate {
		t.Fatal("late intent shot was never used in its window")
	}
}

func TestSelectTimelineV2ReservesNeutralLateShots(t *testing.T) {
	early := rankedCandidate{
		Item: mediaItem{
			ID: "early", Kind: mediaKindBroll, RelativePath: "early.mp4",
			DurationSeconds: 20, ShotID: "shot-early",
		},
		Score: 0.15,
		Match: MatchEvidence{IntentID: "seg-001", Level: matchLevelNeutral, Reason: "early"},
	}
	late := rankedCandidate{
		Item: mediaItem{
			ID: "late", Kind: mediaKindBroll, RelativePath: "late.mp4",
			DurationSeconds: 20, ShotID: "shot-late",
		},
		Score: 0.95,
		Match: MatchEvidence{IntentID: "seg-002", Level: matchLevelNeutral, Reason: "late"},
	}
	intents := []NarrativeIntent{
		{SegmentID: "seg-001", StartMS: 0, EndMS: 8000, Importance: 0.8},
		{SegmentID: "seg-002", StartMS: 8000, EndMS: 20000, Importance: 0.9},
	}
	segments, _, err := selectTimelineV2([]rankedCandidate{early, late}, 12, "task-reserve-neutral", movieMixPolicy(), intents)
	if err != nil {
		t.Fatalf("selectTimelineV2: %v", err)
	}
	if segments[0].Item.ID != "early" {
		t.Fatalf("first slot used %q, want early reserved late even when both are neutral", segments[0].Item.ID)
	}
	if segments[0].Match.IntentID != "seg-001" {
		t.Fatalf("first intent=%q", segments[0].Match.IntentID)
	}
}

func TestSelectTimelineV2LastResortRotatesLeastUsed(t *testing.T) {
	const duration = 180.0
	const pool = 12
	segments, _, err := selectTimelineV2(landscapeOnlyCandidates(pool), duration, "task-last-resort", movieMixPolicy(), nil)
	if err != nil {
		t.Fatalf("selectTimelineV2: %v", err)
	}
	assertContiguousTimeline(t, segments, duration)
	if len(segments) <= pool {
		t.Fatalf("need more slots than the pool to exercise last-resort, got %d", len(segments))
	}

	uses := map[string]int{}
	run := 1
	maxRun := 1
	tailSources := map[string]bool{}
	for i, seg := range segments {
		src := seg.Item.sourceKey()
		uses[src]++
		if i > 0 && src == segments[i-1].Item.sourceKey() {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 1
		}
		if i >= pool {
			tailSources[src] = true
		}
	}
	if maxRun >= 3 {
		t.Fatalf("last-resort stuck on one file for %d consecutive shots; uses=%v", maxRun, uses)
	}
	if len(tailSources) < pool/2 {
		t.Fatalf("tail after %d unique shots used %d sources, want rotation through the pool; uses=%v",
			pool, len(tailSources), uses)
	}
	for src, n := range uses {
		if n > 3 {
			t.Fatalf("source %q used %d times under least-used last-resort", src, n)
		}
	}
}

func TestSelectTimelineReportsQuotaFallback(t *testing.T) {
	const duration = 120.0
	candidates := quotaCandidates(24, 0, 0, 0)
	segments, warnings, err := selectTimeline(candidates, duration, "task-fallback", movieMixPolicy())
	if err != nil {
		t.Fatalf("a broll-only library must degrade, not fail: %v", err)
	}
	assertContiguousTimeline(t, segments, duration)
	for i, seg := range segments {
		if seg.Item.Kind != mediaKindBroll {
			t.Fatalf("segment %d must fall back to broll, got %q", i, seg.Item.Kind)
		}
	}
	warned := map[string]bool{}
	for _, w := range warnings {
		if w.Code == "" {
			t.Fatalf("warning without code: %#v", w)
		}
		warned[w.Kind] = true
	}
	if !warned[string(mediaKindMovie)] || !warned[string(mediaKindImage)] {
		t.Fatalf("missing movie/image quota warnings: %#v", warnings)
	}
}
