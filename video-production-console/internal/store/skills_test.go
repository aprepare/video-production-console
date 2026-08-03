package store

import (
	"path/filepath"
	"testing"
	"time"

	"video-production-console/internal/domain"
)

func TestSkillRepositoryPersistsPerFileAndAggregateSnapshots(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSkillRepository(db)
	now := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	want := domain.SkillSnapshot{
		ID: "snapshot-1", Name: "finance-topic-selector", Path: `C:\skills\finance-topic-selector`,
		SHA256: "aggregate", ModifiedAt: now, CreatedAt: now,
		Files: []domain.SkillFileSnapshot{{Path: "SKILL.md", SHA256: "file-hash", Size: 7}},
	}
	if err := repo.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.SHA256 != want.SHA256 || len(got.Files) != 1 || got.Files[0] != want.Files[0] {
		t.Fatalf("snapshot=%+v", got)
	}
	latest, err := repo.Latest(t.Context(), want.Name)
	if err != nil || latest.ID != want.ID {
		t.Fatalf("Latest()=%+v err=%v", latest, err)
	}
	listed, err := repo.ListLatest(t.Context())
	if err != nil || len(listed) != 1 || listed[0].ID != want.ID {
		t.Fatalf("ListLatest()=%+v err=%v", listed, err)
	}
}

func TestSkillRepositoryLatestUsesInsertionOrderForEqualTimestamps(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSkillRepository(db)
	now := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	old := domain.SkillSnapshot{ID: "z-old", Name: "skill", Path: "old", SHA256: "old", Files: []domain.SkillFileSnapshot{}, ModifiedAt: now, CreatedAt: now}
	newer := domain.SkillSnapshot{ID: "a-new", Name: "skill", Path: "new", SHA256: "new", Files: []domain.SkillFileSnapshot{}, ModifiedAt: now, CreatedAt: now}
	if err := repo.Save(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	latest, err := repo.Latest(t.Context(), "skill")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != newer.ID {
		t.Fatalf("latest=%s, want inserted last %s", latest.ID, newer.ID)
	}
}
