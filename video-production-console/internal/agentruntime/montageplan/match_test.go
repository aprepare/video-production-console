package montageplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureShots() []matchShot {
	return []matchShot{
		{
			item: mediaItem{ID: "src-bank", Kind: mediaKindMovie, RelativePath: "movies/bank.mp4",
				DurationSeconds: 120, SourceInSeconds: 0, SourceOutSeconds: 12, ShotID: "shot-bank", Category: "bank"},
			tags: []string{"银行柜台", "取款"}, mood: "warning", setting: "bank", motion: "low",
			embedding: []float32{1, 0, 0},
		},
		{
			item: mediaItem{ID: "src-door", Kind: mediaKindMovie, RelativePath: "movies/door.mp4",
				DurationSeconds: 120, SourceInSeconds: 0, SourceOutSeconds: 8, ShotID: "shot-door", Category: "door"},
			tags: []string{"关门"}, mood: "warning", setting: "hallway", motion: "medium",
			embedding: []float32{0, 1, 0},
		},
		{
			item: mediaItem{ID: "src-water", Kind: mediaKindBroll, RelativePath: "broll/water.mp4",
				DurationSeconds: 40, ShotID: "shot-water", Category: "water"},
			tags: []string{"水位下降"}, mood: "warning", setting: "reservoir", motion: "low",
			embedding: []float32{0, 1, 0},
		},
		{
			item: mediaItem{ID: "src-night", Kind: mediaKindBroll, RelativePath: "broll/night.mp4",
				DurationSeconds: 30, ShotID: "shot-night", Category: "night"},
			tags: []string{"深夜独坐"}, mood: "anxious", setting: "apartment", motion: "static",
			embedding: []float32{0, 0, 1},
		},
		{
			item: mediaItem{ID: "src-traffic", Kind: mediaKindBroll, RelativePath: "broll/traffic.mp4",
				DurationSeconds: 30, ShotID: "shot-traffic", Category: "City_Traffic"},
			tags: []string{"城市交通"}, mood: "neutral", setting: "city", motion: "medium",
			embedding: []float32{0.1, 0.1, 0.1},
		},
	}
}

type mapEmbedder map[string][]float32

func (m mapEmbedder) Embed(_ context.Context, input string) ([]float32, error) {
	if vector, ok := m[input]; ok {
		return vector, nil
	}
	switch {
	case strings.Contains(input, "银行"):
		return []float32{1, 0, 0}, nil
	case strings.Contains(input, "流动性") || strings.Contains(input, "水位") || strings.Contains(input, "关门"):
		return []float32{0, 1, 0}, nil
	case strings.Contains(input, "夜里") || strings.Contains(input, "深夜") || strings.Contains(input, "焦虑"):
		return []float32{0, 0, 1}, nil
	default:
		return []float32{0.1, 0.1, 0.1}, nil
	}
}

func TestMatchLevelsHitFiveFixtureShots(t *testing.T) {
	pool := fixtureShots()
	ctx := context.Background()
	embedder := mapEmbedder{}
	scoreCtx := scoreContext{usedShots: map[string]bool{}}

	cases := []struct {
		name   string
		intent NarrativeIntent
		level  string
		shot   string
	}{
		{"direct", NarrativeIntent{SegmentID: "seg-001", Text: "去银行柜台取钱", Entities: []string{"银行"}, Topics: []string{"银行"}, Mood: "warning", Importance: 0.8}, matchLevelDirect, "shot-bank"},
		{"metaphor", NarrativeIntent{SegmentID: "seg-002", Text: "流动性正在收紧", Topics: []string{"流动性"}, Metaphors: []string{"水位下降"}, Mood: "warning", Importance: 0.8}, matchLevelMetaphor, "shot-water"},
		{"emotion", NarrativeIntent{SegmentID: "seg-003", Text: "夜里睡不着", Mood: "anxious", VisualConcepts: []string{"深夜家庭"}, Importance: 0.7}, matchLevelEmotion, "shot-night"},
		{"neutral", NarrativeIntent{SegmentID: "seg-004", Text: "过渡一下", Mood: "curious", Importance: 0.3}, matchLevelNeutral, "shot-traffic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ranked := matchCandidates(ctx, tc.intent, pool, embedder, scoreCtx)
			if len(ranked) == 0 {
				t.Fatal("no candidates")
			}
			found := false
			for _, candidate := range ranked {
				if candidate.Item.ShotID == tc.shot && candidate.Match.Level == tc.level {
					found = true
					if candidate.Match.Reason == "" || candidate.Match.IntentID != tc.intent.SegmentID {
						t.Fatalf("evidence=%#v", candidate.Match)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s/%s in %#v", tc.level, tc.shot, ranked)
			}
		})
	}
}

type splitCatalog struct {
	tagged []matchShot
	ready  []matchShot
}

func (c splitCatalog) RecallByTags(context.Context, []string, int) ([]matchShot, error) {
	return c.tagged, nil
}
func (c splitCatalog) RecallByMoodSetting(context.Context, string, string, int) ([]matchShot, error) {
	return nil, nil
}
func (c splitCatalog) RecallReadyShots(context.Context, int) ([]matchShot, error) {
	return c.ready, nil
}

func TestRankLibraryPullsEmbeddingNeighborsOutsideTagPool(t *testing.T) {
	money := matchShot{
		item: mediaItem{ID: "src-money", Kind: mediaKindBroll, ShotID: "shot-money",
			RelativePath: "money.mp4", DurationSeconds: 20, Tags: []string{"money"}},
		tags: []string{"money"}, embedding: []float32{0, 1, 0},
	}
	city := matchShot{
		item: mediaItem{ID: "src-city", Kind: mediaKindBroll, ShotID: "shot-city",
			RelativePath: "city.mp4", DurationSeconds: 20, Summary: "night city skyline",
			Tags: []string{"cityscape"}},
		tags: []string{"cityscape"}, embedding: []float32{1, 0, 0},
	}
	intent := NarrativeIntent{
		SegmentID: "seg-001", Text: "现在还要不要买房",
		VisualConcepts: []string{"cityscape"}, Mood: "warning", Importance: 0.8,
	}
	ranked, notes, err := rankLibrary(context.Background(), []NarrativeIntent{intent}, splitCatalog{
		tagged: []matchShot{money},
		ready:  []matchShot{money, city},
	}, mapEmbedder{"现在还要不要买房 cityscape": []float32{1, 0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "embedding_pool") {
		t.Fatalf("notes=%v", notes)
	}
	found := false
	for _, candidate := range ranked {
		if candidate.Item.ShotID == "shot-city" {
			found = true
			if candidate.Match.Level != matchLevelDirect {
				t.Fatalf("city match=%#v", candidate.Match)
			}
		}
	}
	if !found {
		t.Fatalf("embedding neighbor missing: %#v", ranked)
	}
}

func TestMatchKeepsEnglishCatalogTagWithoutEmbedder(t *testing.T) {
	shot := matchShot{
		item: mediaItem{ID: "src-city", Kind: mediaKindBroll, RelativePath: "broll/city.mp4",
			DurationSeconds: 20, ShotID: "shot-city"},
		tags: []string{"cityscape", "money"}, mood: "focused", setting: "city",
	}
	intent := NarrativeIntent{
		SegmentID: "seg-001", Text: "现在还要不要买房？",
		Entities: []string{"买房"}, Topics: []string{"买房"},
		VisualConcepts: []string{"买房", "cityscape", "money"},
		Mood:           "warning", Importance: 0.8,
	}
	ranked := matchCandidates(context.Background(), intent, []matchShot{shot}, nil, scoreContext{usedShots: map[string]bool{}})
	if len(ranked) != 1 || ranked[0].Match.Level != matchLevelDirect {
		t.Fatalf("want lexical direct without embedder, got %#v", ranked)
	}
}

func TestMatchPenaltiesLowerScore(t *testing.T) {
	shot := fixtureShots()[0]
	shot.hasText = true
	intent := NarrativeIntent{SegmentID: "seg-001", Text: "银行", Entities: []string{"银行"}, Topics: []string{"银行"}, Mood: "warning", Importance: 0.8}
	clean, _ := scoreShot(intent, fixtureShots()[0], []float32{1, 0, 0}, matchLevelDirect, "银行", scoreContext{usedShots: map[string]bool{}})
	dirty, _ := scoreShot(intent, shot, []float32{1, 0, 0}, matchLevelDirect, "银行", scoreContext{
		usedShots:  map[string]bool{shot.item.shotKey(): true},
		prevSource: shot.item.sourceKey(),
		kindShare:  0.9, kindTargetMid: 0.3,
	})
	if dirty.Score >= clean.Score {
		t.Fatalf("penalties did not lower score: clean=%.3f dirty=%.3f", clean.Score, dirty.Score)
	}
}

func TestMatchOrderShuffleDoesNotChangeTimeline(t *testing.T) {
	pool := fixtureShots()
	// Expand the pool so selectTimelineV2 can fill ~20s.
	extra := make([]matchShot, 0, 12)
	for i := 0; i < 8; i++ {
		item := pool[i%len(pool)].item
		item.ID = item.ID + "-x" + string(rune('a'+i))
		item.ShotID = item.ShotID + "-x" + string(rune('a'+i))
		item.RelativePath = filepath.ToSlash(filepath.Join("dup", item.RelativePath))
		clone := pool[i%len(pool)]
		clone.item = item
		extra = append(extra, clone)
	}
	pool = append(pool, extra...)
	intent := NarrativeIntent{SegmentID: "seg-001", Text: "银行流动性收紧夜里难眠", Entities: []string{"银行"}, Topics: []string{"流动性"}, Metaphors: []string{"水位下降", "关门"}, Mood: "warning", Importance: 0.8}
	ranked := matchCandidates(context.Background(), intent, pool, mapEmbedder{}, scoreContext{usedShots: map[string]bool{}})
	if len(ranked) < 3 {
		t.Fatalf("not enough ranked candidates: %d", len(ranked))
	}
	first, _, err := selectTimelineV2(ranked, 20, "seed-stable", movieMixPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := append([]rankedCandidate(nil), ranked...)
	rand.New(rand.NewSource(99)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	second, _, err := selectTimelineV2(shuffled, 20, "seed-stable", movieMixPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("timeline changed after shuffle\n%s\n%s", a, b)
	}
}

func expandFixtureLibrary(base []matchShot) []matchShot {
	out := append([]matchShot(nil), base...)
	for i := 0; i < 12; i++ {
		src := base[i%len(base)]
		item := src.item
		item.ID = fmt.Sprintf("%s-extra-%02d", item.ID, i)
		item.ShotID = fmt.Sprintf("%s-extra-%02d", item.ShotID, i)
		item.RelativePath = fmt.Sprintf("extra/%02d/%s", i, item.RelativePath)
		src.item = item
		out = append(out, src)
	}
	return out
}

type fakeCatalog struct {
	shots []matchShot
	err   error
}

func (f fakeCatalog) RecallByTags(context.Context, []string, int) ([]matchShot, error) {
	return f.shots, f.err
}
func (f fakeCatalog) RecallByMoodSetting(context.Context, string, string, int) ([]matchShot, error) {
	return f.shots, f.err
}
func (f fakeCatalog) RecallReadyShots(context.Context, int) ([]matchShot, error) {
	return f.shots, f.err
}

type limitCatalog struct {
	shots      []matchShot
	semantic   int
	readyLimit int
	err        error
}

func firstMatchShots(shots []matchShot, limit int) []matchShot {
	if limit <= 0 || limit > len(shots) {
		limit = len(shots)
	}
	return shots[:limit]
}

func (c limitCatalog) RecallByTags(_ context.Context, _ []string, limit int) ([]matchShot, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.semantic > 0 && (limit <= 0 || limit > c.semantic) {
		limit = c.semantic
	}
	return firstMatchShots(c.shots, limit), nil
}

func (c limitCatalog) RecallByMoodSetting(_ context.Context, _, _ string, limit int) ([]matchShot, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.semantic > 0 && (limit <= 0 || limit > c.semantic) {
		limit = c.semantic
	}
	return firstMatchShots(c.shots, limit), nil
}

func (c limitCatalog) RecallReadyShots(_ context.Context, limit int) ([]matchShot, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.readyLimit > 0 && (limit <= 0 || limit > c.readyLimit) {
		limit = c.readyLimit
	}
	return firstMatchShots(c.shots, limit), nil
}

func fillerBrollShots(n int) []matchShot {
	out := make([]matchShot, 0, n)
	for i := 0; i < n; i++ {
		item := mediaItem{
			ID:              fmt.Sprintf("src-fill-%02d", i),
			Kind:            mediaKindBroll,
			RelativePath:    fmt.Sprintf("originals/broll/fill-%02d.mp4", i),
			DurationSeconds: 12,
			ShotID:          fmt.Sprintf("shot-fill-%02d", i),
			Category:        "city",
			Tags:            []string{"城市交通"},
		}
		out = append(out, matchShot{
			item: item, tags: []string{"城市交通"}, mood: "neutral",
			setting: "city", motion: "medium", embedding: []float32{0.1, 0.1, 0.1},
		})
	}
	return out
}

func landscapeCatalogShots() []matchShot {
	out := make([]matchShot, 0, 12)
	kinds := []mediaKind{mediaKindBroll, mediaKindMovie, mediaKindImage}
	for i := 0; i < 12; i++ {
		kind := kinds[i%len(kinds)]
		item := mediaItem{
			ID:              fmt.Sprintf("src-land-%02d", i),
			Kind:            kind,
			RelativePath:    fmt.Sprintf("landscape/%02d.mp4", i),
			DurationSeconds: 40,
			ShotID:          fmt.Sprintf("shot-land-%02d", i),
			Category:        "Nature_Landscape",
			Tags:            []string{"风景", "景观"},
		}
		if kind == mediaKindMovie {
			item.SourceInSeconds = 10
			item.SourceOutSeconds = 22
		}
		if kind == mediaKindImage {
			item.DurationSeconds = 0
			item.RelativePath = fmt.Sprintf("landscape/%02d.png", i)
		}
		out = append(out, matchShot{
			item: item, tags: []string{"风景", "景观"}, mood: "neutral",
			setting: "nature", embedding: []float32{0.1, 0.1, 0.1},
		})
	}
	return out
}

func TestBuildV2UsesCatalogMatchEvidence(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 16, nil },
		Catalog:      fakeCatalog{shots: append(expandFixtureLibrary(fixtureShots()), landscapeCatalogShots()...)},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
		SelectMode:   SelectModeMovieCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	sawReal := false
	for _, shot := range plan.Timeline {
		if shot.Match.Reason != v2NeutralMatchReason && shot.Match.Level != "" {
			sawReal = true
			break
		}
	}
	if !sawReal {
		t.Fatalf("expected real match evidence, notes=%v first=%#v", plan.PlannerNotes, plan.Timeline[0].Match)
	}
}

func TestBuildV2ScenicMixesCatalogBrollWithoutIntentMatch(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 16, nil },
		Catalog:      fakeCatalog{shots: expandFixtureLibrary(fixtureShots())},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.PlannerNotes, "\n")
	if !strings.Contains(joined, "scenic_landscape_pool") {
		t.Fatalf("scenic must mix index + catalog broll, notes=%v", plan.PlannerNotes)
	}
	if !strings.Contains(joined, "scenic_catalog_broll: merged") {
		t.Fatalf("scenic must merge catalog broll, notes=%v", plan.PlannerNotes)
	}
	if strings.Contains(joined, "embedding_pool") {
		t.Fatalf("scenic must not run intent/embedding match, notes=%v", plan.PlannerNotes)
	}
	for _, shot := range plan.Timeline {
		if shot.SourceID == "src-bank" || shot.SourceID == "src-door" {
			t.Fatalf("movie catalog shot leaked onto scenic timeline: %s notes=%v", shot.SourceID, plan.PlannerNotes)
		}
	}
	if len(plan.Timeline) == 0 {
		t.Fatal("expected scenic timeline")
	}
}

func TestBuildV2MovieCatalogKeepsNonLandscapeShots(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 16, nil },
		Catalog:      fakeCatalog{shots: expandFixtureLibrary(fixtureShots())},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
		SelectMode:   SelectModeMovieCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.MediaMixPolicy.Preset != mixPresetMovieCatalog {
		t.Fatalf("preset=%q", plan.MediaMixPolicy.Preset)
	}
	joined := strings.Join(plan.PlannerNotes, "\n")
	if strings.Contains(joined, "match_candidates_not_landscape") {
		t.Fatalf("movie catalog must not apply landscape filter notes: %v", plan.PlannerNotes)
	}
	sawMovie := false
	for _, shot := range plan.Timeline {
		if shot.MediaKind == string(mediaKindMovie) {
			sawMovie = true
		}
		if strings.HasPrefix(shot.SourceID, "src-bank") || strings.HasPrefix(shot.SourceID, "src-door") ||
			strings.HasPrefix(shot.SourceID, "src-night") || strings.HasPrefix(shot.SourceID, "src-traffic") {
			return
		}
	}
	if !sawMovie {
		t.Fatalf("expected movie catalog shots on the timeline, first=%#v notes=%v", plan.Timeline[0], plan.PlannerNotes)
	}
}

func thinLandscapeCatalogFromOneFile() []matchShot {
	out := make([]matchShot, 0, 32)
	for i := 0; i < 32; i++ {
		in := float64(i * 8)
		item := mediaItem{
			ID:               fmt.Sprintf("src-thin-%02d", i),
			Kind:             mediaKindBroll,
			RelativePath:     "landscape/repeat.mp4",
			DurationSeconds:  320,
			SourceInSeconds:  in,
			SourceOutSeconds: in + 8,
			ShotID:           fmt.Sprintf("shot-thin-%02d", i),
			Category:         "Nature_Landscape",
			Tags:             []string{"风景", "景观"},
		}
		out = append(out, matchShot{
			item: item, tags: []string{"风景", "景观"}, mood: "neutral",
			setting: "nature", embedding: []float32{0.1, 0.1, 0.1},
		})
	}
	return out
}

func TestBuildV2CatalogShotsNotSupplementedByIndex(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 120, nil },
		Catalog:      fakeCatalog{shots: thinLandscapeCatalogFromOneFile()},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
		SelectMode:   SelectModeMovieCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Timeline) < 10 {
		t.Fatalf("timeline too short: %d notes=%v", len(plan.Timeline), plan.PlannerNotes)
	}
	joined := strings.Join(plan.PlannerNotes, "\n")
	if strings.Contains(joined, "landscape_pool_supplemented") || strings.Contains(joined, "falling back to media index") {
		t.Fatalf("catalog already had shots; must not mix index: %v", plan.PlannerNotes)
	}
	for _, shot := range plan.Timeline {
		if !strings.HasPrefix(shot.SourceID, "src-thin-") {
			t.Fatalf("expected catalog shot, got %s notes=%v", shot.SourceID, plan.PlannerNotes)
		}
	}
}

func TestBuildV2EmptyCatalogFallsBackToLandscapeIndex(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 16, nil },
		Catalog:      fakeCatalog{},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.PlannerNotes, "\n")
	if !strings.Contains(joined, "scenic_landscape_pool") {
		t.Fatalf("expected mixed scenic pool, got %v", plan.PlannerNotes)
	}
	for _, shot := range plan.Timeline {
		if strings.HasPrefix(shot.SourceID, "src-") {
			t.Fatalf("empty catalog must not invent catalog shots: %s", shot.SourceID)
		}
	}
	if len(plan.Timeline) == 0 {
		t.Fatal("expected landscape index timeline")
	}
}

func TestBuildV2MovieCatalogEmptyFailsWithoutLandscapeFallback(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 16, nil },
		Catalog:      fakeCatalog{},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
		SelectMode:   SelectModeMovieCatalog,
	})
	if err == nil || !strings.Contains(err.Error(), "movie catalog produced no usable shots") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildV2UnavailableCatalog(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 40, nil },
		CatalogPath:  filepath.Join(t.TempDir(), "missing-root"),
		SelectMode:   SelectModeMovieCatalog,
	})
	if err == nil || !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildV2ScenicIgnoresMissingCatalog(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 16, nil },
		CatalogPath:  filepath.Join(t.TempDir(), "missing-root"),
	})
	if err != nil {
		t.Fatalf("scenic must not require catalog.db: %v", err)
	}
}

func TestBuildV2RealFinanceCatalogPrefersBroll(t *testing.T) {
	mediaRoot := `E:\B站素材\AI_Media_Library`
	catalogPath := filepath.Join(mediaRoot, "catalog.db")
	indexPath := filepath.Join(mediaRoot, "00_INDEX", "media_index.json")
	if _, err := os.Stat(catalogPath); err != nil {
		t.Skip("finance catalog is not on this machine")
	}
	root := t.TempDir()
	narration := filepath.Join(root, "narration.mp3")
	background := filepath.Join(root, "bg.png")
	srtPath := filepath.Join(root, "sub.srt")
	for _, path := range []string{narration, background} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(srtPath, []byte(v2FixtureSRT()), 0o644); err != nil {
		t.Fatal(err)
	}
	taskID := "task-finance-catalog"
	manifestPath := filepath.Join(root, "task_manifest.json")
	rawManifest, err := json.Marshal(map[string]any{
		"task_id": taskID, "job_id": taskID, "action": "montage.execute",
		"output_dir": filepath.Join(root, "output"),
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": background},
			{"role": "subtitle_srt", "path": srtPath},
		},
		"non_secret_settings": map[string]string{
			"media_root":         mediaRoot,
			"media_index_path":   indexPath,
			"media_catalog_path": catalogPath,
			"draft_display_name": "财经素材库验收",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "output", "production_plan.json")
	if err := BuildV2(Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		CatalogPath:  catalogPath,
		Analyzer:     LocalIntentAnalyzer{},
		Duration:     func(string) (float64, error) { return 48, nil },
		SelectMode:   SelectModeMovieCatalog,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.PlannerNotes, "\n")
	if strings.Contains(joined, "falling back to media index") || strings.Contains(joined, "match_candidates_not_landscape") {
		t.Fatalf("real finance catalog must not fall back to landscape index: %v", plan.PlannerNotes)
	}
	broll := 0
	for _, shot := range plan.Timeline {
		path := filepath.ToSlash(shot.SourcePath + " " + shot.SourceID)
		if strings.Contains(path, "01_Nature_Landscape") || strings.Contains(path, "12_Pexels_Landscape") {
			t.Fatalf("landscape index path used: %s notes=%v", shot.SourcePath, plan.PlannerNotes)
		}
		if strings.Contains(filepath.ToSlash(shot.SourcePath), "originals/broll/") {
			broll++
		}
	}
	if broll == 0 {
		t.Fatalf("expected originals/broll shots, first=%#v notes=%v", plan.Timeline[0], plan.PlannerNotes)
	}
}

func TestBuildV2PadsCatalogFillersForLongNarration(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	const duration = 400.0
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return duration, nil },
		Catalog:      limitCatalog{shots: fillerBrollShots(80), semantic: 50, readyLimit: 80},
		Analyzer:     LocalIntentAnalyzer{},
		Embedder:     mapEmbedder{},
		SelectMode:   SelectModeMovieCatalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if mathAbs(plan.ProjectDurationS-duration) > 0.01 {
		t.Fatalf("duration=%v", plan.ProjectDurationS)
	}
	joined := strings.Join(plan.PlannerNotes, "\n")
	if !strings.Contains(joined, "catalog_fill") {
		t.Fatalf("expected catalog filler note, notes=%v", plan.PlannerNotes)
	}
	if len(plan.Timeline) <= 50 {
		t.Fatalf("long narration must use more than the 50-shot recall cap, got %d", len(plan.Timeline))
	}
	for i, shot := range plan.Timeline {
		if strings.TrimSpace(shot.Match.IntentID) == "" || strings.TrimSpace(shot.Match.Reason) == "" {
			t.Fatalf("shot %d match evidence incomplete: %#v", i+1, shot.Match)
		}
	}
}

func TestBuildV2RealFinanceCatalogCoversLongNarration(t *testing.T) {
	mediaRoot := `E:\B站素材\AI_Media_Library`
	catalogPath := filepath.Join(mediaRoot, "catalog.db")
	indexPath := filepath.Join(mediaRoot, "00_INDEX", "media_index.json")
	if _, err := os.Stat(catalogPath); err != nil {
		t.Skip("finance catalog is not on this machine")
	}
	root := t.TempDir()
	narration := filepath.Join(root, "narration.mp3")
	background := filepath.Join(root, "bg.png")
	srtPath := filepath.Join(root, "sub.srt")
	for _, path := range []string{narration, background} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(srtPath, []byte(v2FixtureSRT()), 0o644); err != nil {
		t.Fatal(err)
	}
	taskID := "task-finance-long"
	manifestPath := filepath.Join(root, "task_manifest.json")
	rawManifest, err := json.Marshal(map[string]any{
		"task_id": taskID, "job_id": taskID, "action": "montage.execute",
		"output_dir": filepath.Join(root, "output"),
		"inputs": []map[string]string{
			{"role": "narration", "path": narration},
			{"role": "account_background", "path": background},
			{"role": "subtitle_srt", "path": srtPath},
		},
		"non_secret_settings": map[string]string{
			"media_root":         mediaRoot,
			"media_index_path":   indexPath,
			"media_catalog_path": catalogPath,
			"draft_display_name": "长口播财经库验收",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "output", "production_plan.json")
	const duration = 446.85
	if err := BuildV2(Options{
		ManifestPath: manifestPath,
		PlanPath:     planPath,
		CatalogPath:  catalogPath,
		Analyzer:     LocalIntentAnalyzer{},
		Duration:     func(string) (float64, error) { return duration, nil },
		SelectMode:   SelectModeMovieCatalog,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan ProductionPlanV2
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Timeline) <= 50 {
		t.Fatalf("446s finance plan must exceed the 50-shot recall cap, got %d notes=%v", len(plan.Timeline), plan.PlannerNotes)
	}
	for _, shot := range plan.Timeline {
		if strings.Contains(filepath.ToSlash(shot.SourcePath), "01_Nature_Landscape") {
			t.Fatalf("landscape fallback used: %s", shot.SourcePath)
		}
	}
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
