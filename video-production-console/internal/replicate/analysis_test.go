package replicate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func analysisFixture(t *testing.T) (*Service, *store.ReferenceAnalysisRepository, *store.ReferenceRepository) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	analyses := store.NewReferenceAnalysisRepository(db)
	svc, err := New(store.NewReplicateRepository(db), analyses, testRuntime{settings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: root}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc, analyses, store.NewReferenceRepository(db)
}

func seedCompletedAnalysis(t *testing.T, analyses *store.ReferenceAnalysisRepository, references *store.ReferenceRepository, referenceID, analysisID, report string) {
	t.Helper()
	ctx := context.Background()
	if _, err := references.Upsert(ctx, store.ReferencePost{ID: referenceID, Platform: "douyin", ExternalID: referenceID}); err != nil {
		t.Fatal(err)
	}
	if _, created, err := analyses.CreateQueued(ctx, store.ReferenceAnalysis{ID: analysisID, ReferenceID: referenceID, Model: "vision"}); err != nil || !created {
		t.Fatalf("create analysis: created=%v err=%v", created, err)
	}
	if err := analyses.MarkRunning(ctx, analysisID); err != nil {
		t.Fatal(err)
	}
	if err := analyses.Complete(ctx, analysisID, report); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFromAnalysisRejectsLegacyReportWithoutPrompts(t *testing.T) {
	svc, analyses, references := analysisFixture(t)
	seedCompletedAnalysis(t, analyses, references, "reference-old", "analysis-old", `{"summary":"旧报告","replicate":{"shot_list":[]}}`)

	_, err := svc.Create(t.Context(), CreateRequest{ReferenceID: "reference-old", AnalysisID: "analysis-old", Product: "海鲜礼盒"})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "analysis_has_no_prompts" {
		t.Fatalf("legacy report error=%v", err)
	}
}

func TestCreateFromAnalysisMapsPromptsAndClampsDurationsByProvider(t *testing.T) {
	svc, analyses, references := analysisFixture(t)
	const report = `{"summary":"可复刻","replicate":{"style_lock":"暖色自然光","ai_prompts":[
		{"shot_index":1,"start_s":0,"end_s":0.2,"purpose":"钩子","first_frame_prompt":"礼盒特写","motion_prompt":"推进","negative":"乱码","keep":"蓝色礼盒"},
		{"shot_index":2,"start_s":1,"end_s":8.6,"purpose":"展示","first_frame_prompt":"开盒","motion_prompt":"环绕","negative":"变形","keep":"银色丝带"},
		{"shot_index":3,"start_s":10,"end_s":30,"purpose":"收尾","first_frame_prompt":"全景","motion_prompt":"拉远","negative":"人物","keep":"品牌标识"}
	]}}`
	seedCompletedAnalysis(t, analyses, references, "reference-new", "analysis-new", report)

	h3, err := svc.Create(t.Context(), CreateRequest{ReferenceID: "reference-new", AnalysisID: "analysis-new", Product: "海鲜礼盒", VideoProvider: "runninghub_h3"})
	if err != nil {
		t.Fatal(err)
	}
	if h3.StyleLock != "暖色自然光" || h3.Product != "海鲜礼盒" || len(h3.Shots) != 3 {
		t.Fatalf("mapped project=%+v", h3)
	}
	wantH3 := []int{1, 8, 10}
	for i, shot := range h3.Shots {
		if shot.Position != i+1 || shot.DurationS != wantH3[i] {
			t.Fatalf("H3 shot %d=%+v want duration=%d", i, shot, wantH3[i])
		}
	}
	first := h3.Shots[0]
	if first.Purpose != "钩子" || first.SourceStartS != 0 || first.SourceEndS != .2 || first.FirstFramePrompt != "礼盒特写" || first.MotionPrompt != "推进" || first.Negative != "乱码" || first.Keep != "蓝色礼盒" {
		t.Fatalf("prompt fields not mapped: %+v", first)
	}

	grok, err := svc.Create(t.Context(), CreateRequest{ReferenceID: "reference-new", AnalysisID: "analysis-new", Product: "海鲜礼盒", VideoProvider: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	wantGrok := []int{6, 6, 15}
	for i, shot := range grok.Shots {
		if shot.DurationS != wantGrok[i] {
			t.Fatalf("Grok shot %d duration=%d want=%d", i, shot.DurationS, wantGrok[i])
		}
	}
}
