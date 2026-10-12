package montageplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scenicTestOptions(t *testing.T) Options {
	t.Helper()
	manifest, plan := v2Fixture(t)
	opts := v2Options(manifest, plan)
	opts.MixPreset = mixPresetScenicSemantic
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var mf manifestFile
	if err = json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	var items []mediaItem
	for i := 0; i < 90; i++ {
		category, title := "Housing_Residential", "China residential apartment house"
		if i%3 == 1 {
			category, title = "Rural_Life", "China village rice farmland"
		}
		if i%3 == 2 {
			category, title = "Transport_Infrastructure", "China river bridge railway city"
		}
		rel := fmt.Sprintf("china-%03d.mp4", i)
		if err = os.WriteFile(filepath.Join(mf.NonSecretSettings.MediaRoot, rel), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		items = append(items, mediaItem{ID: fmt.Sprintf("china-%03d", i), Kind: mediaKindBroll, Category: category, Summary: title, Geography: "china_metadata", RelativePath: rel, DurationSeconds: 20, Width: 1920, Height: 1080, SHA256: fmt.Sprintf("%064x", i+1)})
	}
	if err = writeScenicJSON(filepath.Join(filepath.Dir(mf.NonSecretSettings.MediaIndexPath), "china_scenic_index.json"), items); err != nil {
		t.Fatal(err)
	}
	return opts
}
func TestScenicSemanticNormalSpeedCutsAndReproducibility(t *testing.T) {
	opts := scenicTestOptions(t)
	if err := BuildV2(opts); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(opts.PlanPath)
	var protocol map[string]any
	if err := json.Unmarshal(first, &protocol); err != nil {
		t.Fatal(err)
	}
	assertSafeExecutionActions(t, protocol["execution_actions"])
	var plan ProductionPlanV2
	if err := json.Unmarshal(first, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.MediaMixPolicy.Preset != mixPresetScenicSemantic {
		t.Fatal("wrong preset")
	}
	seen := map[string]bool{}
	cuts, dissolves := 0, 0
	cursor := 0.0
	for i, shot := range plan.Timeline {
		if shot.StartS != cursor {
			t.Fatal("timeline gap")
		}
		cursor = shot.EndS
		if seen[shot.SourceID] {
			t.Fatal("source repeated")
		}
		seen[shot.SourceID] = true
		if shot.SourceInS == nil || shot.SourceOutS == nil {
			t.Fatal("missing source window")
		}
		delta := (*shot.SourceOutS - *shot.SourceInS) - (shot.EndS - shot.StartS)
		if delta < -0.00001 || delta > .00001 {
			t.Fatalf("not 1x: %#v", shot)
		}
		if *shot.SourceInS < 0 || *shot.SourceOutS > 20.001 {
			t.Fatal("out of bounds")
		}
		if i > 0 {
			if shot.Transition == nil {
				cuts++
			} else {
				dissolves++
				if plan.Timeline[i-1].Transition != nil {
					t.Fatal("adjacent dissolves")
				}
			}
		}
		if shot.Match.Reason == "" {
			t.Fatal("missing editorial evidence")
		}
	}
	if cursor != plan.ProjectDurationS || cuts <= dissolves || dissolves == 0 {
		t.Fatalf("duration/cuts mismatch %v cuts=%d dissolve=%d", cursor, cuts, dissolves)
	}
	if err := BuildV2(opts); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(opts.PlanPath)
	if !bytes.Equal(first, second) {
		t.Fatal("same inputs and task must be stable")
	}
	var qc ScenicSelectionQC
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(opts.PlanPath), "scenic_selection_qc.json"))
	if json.Unmarshal(raw, &qc) != nil || qc.RepeatCount != 0 || qc.PlaybackSpeed != 1 {
		t.Fatal("missing accurate QC")
	}
}
func TestScenicChinaLocationAndOrientationFilters(t *testing.T) {
	if chinaScenicItem(mediaItem{Kind: mediaKindBroll, Title: "德国4K风景 中国剪辑素材"}) {
		t.Fatal("foreign source accepted")
	}
	if chinaScenicItem(mediaItem{Kind: mediaKindBroll, Title: "beautiful lake"}) {
		t.Fatal("unknown location accepted")
	}
	if !chinaScenicItem(mediaItem{Kind: mediaKindBroll, Title: "中国农村住宅"}) {
		t.Fatal("China metadata rejected")
	}
	if chinaScenicItem(mediaItem{Kind: mediaKindMovie, Title: "China drama"}) {
		t.Fatal("movie not allowed in scenic preset")
	}
}
func TestScenicImageryFollowsHousingAndRuralNarration(t *testing.T) {
	house := mediaItem{Summary: "China residential apartment buildings", Category: "Housing_Residential"}
	village := mediaItem{Summary: "China countryside farmland village", Category: "Rural_Life"}
	h, _, _ := scenicFit("买房之后还要还房贷", house)
	v, _, _ := scenicFit("买房之后还要还房贷", village)
	if h <= v {
		t.Fatal("housing intent not respected")
	}
	h, _, _ = scenicFit("农村的土地和种地", house)
	v, _, _ = scenicFit("农村的土地和种地", village)
	if v <= h {
		t.Fatal("rural intent not respected")
	}
	score, level, _ := scenicFit("财富正在转移机会换了轨道", mediaItem{Summary: "China bridge railway river"})
	if score < .5 || level != "metaphor" {
		t.Fatal("metaphoric selection failed")
	}
}
func TestScenicCancellationAndInvalidPolicy(t *testing.T) {
	opts := scenicTestOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opts.Context = ctx
	if err := BuildV2(opts); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
	p := defaultScenicPolicy()
	p.PlaybackSpeed = 1.5
	if p.validate() == nil {
		t.Fatal("normal speed contract bypassed")
	}
}
func TestScenicNeverSilentlyUsesForeignFallback(t *testing.T) {
	opts := scenicTestOptions(t)
	raw, _ := os.ReadFile(opts.ManifestPath)
	var mf manifestFile
	_ = json.Unmarshal(raw, &mf)
	_ = os.Remove(filepath.Join(filepath.Dir(mf.NonSecretSettings.MediaIndexPath), "china_scenic_index.json"))
	err := BuildV2(opts)
	if err == nil || !strings.Contains(err.Error(), "china_scenic_pool_empty") {
		t.Fatalf("must fail explicitly, got %v", err)
	}
}
func TestScenicPerceptualRepeatDetection(t *testing.T) {
	a := mediaItem{FrameHashes: []string{"0123456789abcdef", "0123456789abcdef", "0123456789abcdef"}}
	b := mediaItem{FrameHashes: []string{"0123456789abcdee", "0123456789abcdee", "fedcba9876543210"}}
	if !nearSame(a, b) {
		t.Fatal("near duplicate missed")
	}
	if nearSame(a, mediaItem{}) {
		t.Fatal("missing fingerprints treated as duplicate")
	}
}

func TestScenicPreflightIgnoresUnusedMissingLegacyClip(t *testing.T) {
	opts := scenicTestOptions(t)
	raw, _ := os.ReadFile(opts.ManifestPath)
	var mf manifestFile
	_ = json.Unmarshal(raw, &mf)
	// Keep the curated China index intact while an unrelated legacy item goes missing.
	if err := os.WriteFile(mf.NonSecretSettings.MediaIndexPath, []byte(`[{"id":"missing","category":"Nature_Landscape","relative_path":"missing.mp4","duration_seconds":20}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMediaLibraryForPreset(mixPresetScenicSemantic, mf.NonSecretSettings.MediaIndexPath, mf.NonSecretSettings.MediaRoot, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMediaLibraryForPreset("", mf.NonSecretSettings.MediaIndexPath, mf.NonSecretSettings.MediaRoot, ""); err == nil {
		t.Fatal("legacy strict validation must be preserved")
	}
}

func TestScenicVisualWordsAndDuplicateSceneGroups(t *testing.T) {
	if containsScenicVisual("nanjing presidential palace", "residential") {
		t.Fatal("substring is not a housing label")
	}
	if !containsScenicVisual("china residential apartments", "apartment") {
		t.Fatal("plural term must match")
	}
	a := mediaItem{ID: "a", Creator: "creator", Summary: "evening sky over marina in hong kong"}
	b := a
	b.ID = "b"
	if scenicGroupKey(a) != scenicGroupKey(b) {
		t.Fatal("same creator and description must share a scene group")
	}
	if chinaScenicItem(mediaItem{Kind: mediaKindBroll, Title: "a Chinese temple"}) {
		t.Fatal("generic Chinese label is not geographic evidence")
	}
}
