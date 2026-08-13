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
