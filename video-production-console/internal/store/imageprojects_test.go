package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestImageProjectRepositoryPersistsOrderedItemsAndGenerationResult(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "image-projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewImageProjectRepository(db)
	now := time.Now().UTC()
	project := domain.ImageProject{
		ID: uuid.NewString(), Title: "养老现金流", Script: "第一句。第二句。",
		ImageCount: 2, Ratio: "3:4", Style: "ledger_investigation", Concurrency: 2,
		Status: "draft", CreatedAt: now, UpdatedAt: now,
	}
	items := []domain.ImageProjectItem{
		{ID: uuid.NewString(), ProjectID: project.ID, Sequence: 1, SourceText: "第一句。", Title: "第一句", Prompt: "p1", Status: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), ProjectID: project.ID, Sequence: 2, SourceText: "第二句。", Title: "第二句", Prompt: "p2", Status: "pending", CreatedAt: now, UpdatedAt: now},
	}
	if err := repo.Create(context.Background(), project, items); err != nil {
		t.Fatal(err)
	}
	got, gotItems, err := repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != project.Title || len(gotItems) != 2 || gotItems[0].Sequence != 1 || gotItems[1].Sequence != 2 {
		t.Fatalf("project=%+v items=%+v", got, gotItems)
	}
	if err := repo.MarkItemReady(context.Background(), items[0].ID, "C:/images/001.png", "image/png", 1024, 1365, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, gotItems, err = repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotItems[0].Status != "ready" || gotItems[0].ImagePath == nil || gotItems[0].Width != 1024 || gotItems[0].Height != 1365 {
		t.Fatalf("ready item=%+v", gotItems[0])
	}
}

func TestImageProjectRepositoryPersistsQuickRunStateAndAttempts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "image-quick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewImageProjectRepository(db)
	now := time.Now().UTC()
	project := domain.ImageProject{
		ID: uuid.NewString(), Title: "fallback", Script: "第一句。第二句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 2,
		Status: "draft", RunMode: "quick", RunPhase: "planning", RunStatus: "running",
		ImageAttempts: 2, TextModel: "gpt-5.6-sol", ReasoningEffort: "medium",
		ImageModel: "gpt-image-2", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(context.Background(), project, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunMode != "quick" || got.RunPhase != "planning" || got.RunStatus != "running" {
		t.Fatalf("created run state=%+v", got)
	}
	if got.ImageAttempts != 2 || got.TextModel != "gpt-5.6-sol" || got.ImageModel != "gpt-image-2" || got.ReasoningEffort != "medium" {
		t.Fatalf("created run config=%+v", got)
	}

	planItems := []domain.ImageProjectItem{
		{ID: uuid.NewString(), ProjectID: project.ID, Sequence: 1, Role: "cover", SourceText: "第一句。", Title: "第一句", Status: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), ProjectID: project.ID, Sequence: 2, Role: "content", SourceText: "第二句。", Title: "第二句", Status: "pending", CreatedAt: now, UpdatedAt: now},
	}
	candidates := []domain.PublishingCandidate{
		{Position: 1, Title: "存款流向", Description: "描述一 #存款 #财富管理 #思维提升"},
		{Position: 2, Title: "钱去哪了", Description: "描述二 #存款 #理财 #认知"},
		{Position: 3, Title: "现金流", Description: "描述三 #存款 #财富 #干货"},
		{Position: 4, Title: "复利", Description: "描述四 #复利 #理财 #思维"},
		{Position: 5, Title: "风险", Description: "描述五 #风险 #存款 #认知"},
	}
	later := now.Add(time.Minute)
	if err := repo.SaveQuickPlan(context.Background(), project.ID, "存款流向", planItems, candidates, "", later); err != nil {
		t.Fatal(err)
	}
	got, gotItems, err := repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunMode != "quick" || got.RunPhase != "prompting" || got.RunStatus != "running" {
		t.Fatalf("run state=%+v", got)
	}
	if got.ImageAttempts != 2 || got.TextModel != "gpt-5.6-sol" || got.ImageModel != "gpt-image-2" {
		t.Fatalf("run config=%+v", got)
	}
	if got.Title != "存款流向" || got.Script != project.Script || got.ImageCount != 2 {
		t.Fatalf("plan fields=%+v", got)
	}
	if got.SelectedPosition == nil || *got.SelectedPosition != 1 || len(got.PublishingCandidates) != 5 {
		t.Fatalf("publishing=%+v selected=%v", got.PublishingCandidates, got.SelectedPosition)
	}
	if len(gotItems) != 2 || gotItems[0].SourceText != "第一句。" {
		t.Fatalf("items=%+v", gotItems)
	}

	if err := repo.SavePrompts(context.Background(), project.ID, []string{"prompt-a", "prompt-b"}, later.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, gotItems, err = repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunPhase != "imaging" || gotItems[0].Prompt != "prompt-a" || gotItems[1].Prompt != "prompt-b" {
		t.Fatalf("prompts phase=%s items=%+v", got.RunPhase, gotItems)
	}
	if err := repo.MarkItemAttempts(context.Background(), gotItems[0].ID, 2, later.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, gotItems, err = repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotItems) != 2 || gotItems[0].AttemptCount != 2 {
		t.Fatalf("items=%+v", gotItems)
	}
	if err := repo.SetRunState(context.Background(), project.ID, "completed", "completed", "", 1, 1, later.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateProjectTitle(context.Background(), project.ID, " 用户改名 ", later.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _, err = repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "用户改名" || got.RunPhase != "completed" || got.RunStatus != "completed" || got.SuccessCount != 1 || got.FailureCount != 1 {
		t.Fatalf("final project=%+v", got)
	}
	if err := repo.UpdateProjectTitle(context.Background(), project.ID, "   ", later.Add(5*time.Minute)); err == nil {
		t.Fatal("expected blank title rejection")
	}
}

func TestImageProjectRepositoryMarksRunningQuickProjectsInterrupted(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "image-interrupt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewImageProjectRepository(db)
	now := time.Now().UTC()
	quick := domain.ImageProject{
		ID: uuid.NewString(), Title: "quick", Script: "第一句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", RunMode: "quick", RunPhase: "imaging", RunStatus: "running",
		ImageAttempts: 2, CreatedAt: now, UpdatedAt: now,
	}
	manual := domain.ImageProject{
		ID: uuid.NewString(), Title: "manual", Script: "第二句。",
		ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1,
		Status: "draft", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(context.Background(), quick, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), manual, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRunningQuickProjectsInterrupted(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotQuick, _, err := repo.Get(context.Background(), quick.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotManual, _, err := repo.Get(context.Background(), manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuick.RunStatus != "interrupted" || gotQuick.RunPhase != "imaging" {
		t.Fatalf("quick after interrupt=%+v", gotQuick)
	}
	if gotManual.RunMode != "manual" || gotManual.RunStatus != "idle" || gotManual.RunPhase != "idle" {
		t.Fatalf("manual after interrupt=%+v", gotManual)
	}
}

func TestImageProjectRepositoryUpdatePublishingRejectsBlankDescription(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()
	repo := NewImageProjectRepository(db)
	now := time.Now().UTC()
	p := domain.ImageProject{ID: uuid.NewString(), Title: "t", Script: "s", ImageCount: 1, Ratio: "3:4", Style: "red_ink", Concurrency: 1, Status: "draft", CreatedAt: now, UpdatedAt: now, PublishingCandidates: []domain.PublishingCandidate{{Position: 1, Title: "a", Description: "d"}, {Position: 2, Title: "b", Description: "d"}, {Position: 3, Title: "c", Description: "d"}, {Position: 4, Title: "e", Description: "d"}, {Position: 5, Title: "f", Description: "d"}}}
	if err := repo.Create(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdatePublishingCandidate(context.Background(), p.ID, domain.PublishingCandidate{Position: 1, Title: "a", Description: "  "}); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestImageProjectRepositoryDeleteCascadesItems(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "image-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewImageProjectRepository(db)
	now := time.Now().UTC()
	id := uuid.NewString()
	itemID := uuid.NewString()
	if err := repo.Create(context.Background(), domain.ImageProject{ID: id, Title: "x", Script: "文案。", ImageCount: 1, Ratio: "3:4", Style: "finance_documentary", Concurrency: 1, Status: "draft", CreatedAt: now, UpdatedAt: now}, []domain.ImageProjectItem{{ID: itemID, ProjectID: id, Sequence: 1, SourceText: "文案。", Title: "文案", Prompt: "p", Status: "pending", CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM image_project_items WHERE project_id=?`, id); got != "0" {
		t.Fatalf("items after delete=%s", got)
	}
}
