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
	first, _, err := selectTimelineV2(ranked, 20, "seed-stable", movieMixPolicy())
	if err != nil {
		t.Fatal(err)
	}
	shuffled := append([]rankedCandidate(nil), ranked...)
	rand.New(rand.NewSource(99)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	second, _, err := selectTimelineV2(shuffled, 20, "seed-stable", movieMixPolicy())
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

func TestBuildV2UsesCatalogMatchEvidence(t *testing.T) {
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

func TestBuildV2UnavailableCatalog(t *testing.T) {
	manifest, planPath := v2Fixture(t)
	err := BuildV2(Options{
		ManifestPath: manifest,
		PlanPath:     planPath,
		Duration:     func(string) (float64, error) { return 40, nil },
		CatalogPath:  filepath.Join(t.TempDir(), "missing-root"),
	})
	if err == nil || !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
