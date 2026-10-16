package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openReplicateTestRepository(t *testing.T) (*ReplicateRepository, func()) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return NewReplicateRepository(db), func() { _ = db.Close() }
}

func TestReplicateRepositoryLifecycleAndOrdering(t *testing.T) {
	r, closeDB := openReplicateTestRepository(t)
	defer closeDB()
	ctx := context.Background()
	created := time.Date(2026, 10, 16, 1, 2, 3, 4, time.UTC)
	p := ReplicateProject{ID: "p1", Title: "海鲜复刻", Ratio: "9:16", VideoProvider: "grok", Variants: 2, Status: "draft", CreatedAt: created, UpdatedAt: created}
	shots := []ReplicateShot{
		{ID: "s1", ProjectID: "p1", Position: 1, Purpose: "开头", DurationS: 6, FirstFrameStatus: "none", CreatedAt: created, UpdatedAt: created},
		{ID: "s2", ProjectID: "p1", Position: 2, Purpose: "结尾", DurationS: 10, FirstFrameStatus: "ready", CreatedAt: created, UpdatedAt: created},
	}
	if err := r.Create(ctx, p, shots); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "p1")
	if err != nil || got.Title != "海鲜复刻" || len(got.Shots) != 2 || got.Shots[0].ID != "s1" || len(got.Shots[0].Takes) != 0 {
		t.Fatalf("get=%+v err=%v", got, err)
	}

	inserted := ReplicateShot{ID: "s3", ProjectID: "p1", Purpose: "中段", DurationS: 6, FirstFrameStatus: "none", CreatedAt: created, UpdatedAt: created}
	if err := r.AddShot(ctx, inserted, 1); err != nil {
		t.Fatal(err)
	}
	got, err = r.Get(ctx, "p1")
	if err != nil || len(got.Shots) != 3 || got.Shots[0].ID != "s1" || got.Shots[1].ID != "s3" || got.Shots[2].ID != "s2" {
		t.Fatalf("insert order=%+v err=%v", got.Shots, err)
	}
	if err := r.ReorderShots(ctx, "p1", []string{"s2", "s1", "s3"}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteShot(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	got, err = r.Get(ctx, "p1")
	if err != nil || len(got.Shots) != 2 || got.Shots[0].ID != "s2" || got.Shots[0].Position != 1 || got.Shots[1].ID != "s3" || got.Shots[1].Position != 2 {
		t.Fatalf("delete/compact=%+v err=%v", got.Shots, err)
	}

	p.Title, p.Status, p.UpdatedAt = "新版", "ready", created.Add(time.Minute)
	if err := r.SaveProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	items, err := r.List(ctx, 30)
	if err != nil || len(items) != 1 || items[0].Title != "新版" || items[0].Shots != nil {
		t.Fatalf("list=%+v err=%v", items, err)
	}
	if err := r.DeleteProject(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, "p1"); !errors.Is(err, ErrReplicateNotFound) {
		t.Fatalf("deleted project err=%v", err)
	}
}

func TestReplicateRepositoryTakesAutoSelectAndClearAtomically(t *testing.T) {
	r, closeDB := openReplicateTestRepository(t)
	defer closeDB()
	ctx := context.Background()
	now := time.Now().UTC()
	p := ReplicateProject{ID: "p", Title: "项目", Ratio: "9:16", VideoProvider: "runninghub_h3", Variants: 1, Status: "draft", CreatedAt: now, UpdatedAt: now}
	shot := ReplicateShot{ID: "s", ProjectID: "p", Position: 1, DurationS: 6, FirstFrameStatus: "ready", CreatedAt: now, UpdatedAt: now}
	if err := r.Create(ctx, p, []ReplicateShot{shot}); err != nil {
		t.Fatal(err)
	}
	takes := []ReplicateTake{
		{ID: "t1", ShotID: "s", Provider: "runninghub_h3", Prompt: "一", Status: "queued", KeySlot: -1, CreatedAt: now, UpdatedAt: now},
		{ID: "t2", ShotID: "s", Provider: "runninghub_h3", Prompt: "二", Status: "running", ProviderTaskID: "upstream", KeySlot: 0, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
	}
	if err := r.CreateTakes(ctx, takes); err != nil {
		t.Fatal(err)
	}
	active, err := r.ActiveTakes(ctx)
	if err != nil || len(active) != 2 || active[0].ID != "t1" || active[1].ID != "t2" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	takes[0].Status, takes[0].Path, takes[0].UpdatedAt = "succeeded", "takes/t1.mp4", now.Add(2*time.Second)
	if err := r.SaveTake(ctx, takes[0]); err != nil {
		t.Fatal(err)
	}
	gotShot, err := r.GetShot(ctx, "s")
	if err != nil || gotShot.SelectedTakeID != "t1" || len(gotShot.Takes) != 2 {
		t.Fatalf("auto select shot=%+v err=%v", gotShot, err)
	}
	takes[1].Status, takes[1].UpdatedAt = "succeeded", now.Add(3*time.Second)
	if err := r.SaveTake(ctx, takes[1]); err != nil {
		t.Fatal(err)
	}
	gotShot, _ = r.GetShot(ctx, "s")
	if gotShot.SelectedTakeID != "t1" {
		t.Fatalf("later success replaced selection: %q", gotShot.SelectedTakeID)
	}
	if err := r.DeleteTake(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	gotShot, err = r.GetShot(ctx, "s")
	if err != nil || gotShot.SelectedTakeID != "" || len(gotShot.Takes) != 1 {
		t.Fatalf("delete selected take shot=%+v err=%v", gotShot, err)
	}
}

func TestReplicateRepositoryRejectsCrossShotSelectionAndIncompleteReorder(t *testing.T) {
	r, closeDB := openReplicateTestRepository(t)
	defer closeDB()
	ctx := context.Background()
	now := time.Now().UTC()
	p := ReplicateProject{ID: "p", Title: "项目", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "draft", CreatedAt: now, UpdatedAt: now}
	shots := []ReplicateShot{{ID: "s1", ProjectID: "p", Position: 1, DurationS: 6, FirstFrameStatus: "none", CreatedAt: now, UpdatedAt: now}, {ID: "s2", ProjectID: "p", Position: 2, DurationS: 6, FirstFrameStatus: "none", CreatedAt: now, UpdatedAt: now}}
	if err := r.Create(ctx, p, shots); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateTakes(ctx, []ReplicateTake{{ID: "t2", ShotID: "s2", Provider: "grok", Prompt: "x", Status: "succeeded", KeySlot: -1, CreatedAt: now, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	shots[0].SelectedTakeID = "t2"
	if err := r.SaveShot(ctx, shots[0]); !errors.Is(err, ErrInvalidReplicate) {
		t.Fatalf("cross-shot selection err=%v", err)
	}
	if err := r.ReorderShots(ctx, "p", []string{"s1"}); !errors.Is(err, ErrInvalidReplicate) {
		t.Fatalf("partial reorder err=%v", err)
	}
	got, err := r.Get(ctx, "p")
	if err != nil || got.Shots[0].ID != "s1" || got.Shots[1].ID != "s2" {
		t.Fatalf("invalid mutations changed order: %+v err=%v", got.Shots, err)
	}
}

func TestReplicateRepositorySaveTakeIfStatusDoesNotOverwriteCanceledTake(t *testing.T) {
	r, closeDB := openReplicateTestRepository(t)
	defer closeDB()
	ctx := context.Background()
	now := time.Now().UTC()
	p := ReplicateProject{ID: "p", Title: "项目", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "generating", CreatedAt: now, UpdatedAt: now}
	shot := ReplicateShot{ID: "s", ProjectID: "p", Position: 1, DurationS: 6, FirstFrameStatus: "ready", CreatedAt: now, UpdatedAt: now}
	if err := r.Create(ctx, p, []ReplicateShot{shot}); err != nil {
		t.Fatal(err)
	}
	take := ReplicateTake{ID: "t", ShotID: "s", Provider: "grok", Prompt: "运动", Status: "running", ProviderTaskID: "upstream", KeySlot: -1, CreatedAt: now, UpdatedAt: now}
	if err := r.CreateTakes(ctx, []ReplicateTake{take}); err != nil {
		t.Fatal(err)
	}
	take.Status, take.UpdatedAt = "canceled", now.Add(time.Second)
	if err := r.SaveTake(ctx, take); err != nil {
		t.Fatal(err)
	}
	late := take
	late.Status, late.Path, late.UpdatedAt = "succeeded", "takes/t.mp4", now.Add(2*time.Second)
	updated, err := r.SaveTakeIfStatus(ctx, late, "running")
	if err != nil || updated {
		t.Fatalf("late completion updated=%v err=%v", updated, err)
	}
	got, err := r.GetTake(ctx, "t")
	if err != nil || got.Status != "canceled" || got.Path != "" {
		t.Fatalf("canceled take overwritten: %+v err=%v", got, err)
	}
}

func TestReplicateRepositoryInterruptedProjectsReturnsAllRecoverableWork(t *testing.T) {
	r, closeDB := openReplicateTestRepository(t)
	defer closeDB()
	ctx := context.Background()
	now := time.Now().UTC()
	projects := []ReplicateProject{
		{ID: "frames", Title: "首帧中断", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "draft", CreatedAt: now, UpdatedAt: now},
		{ID: "stitch", Title: "拼接中断", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "stitching", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "idle", Title: "普通草稿", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "draft", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	}
	for _, p := range projects {
		var shots []ReplicateShot
		if p.ID == "frames" {
			shots = []ReplicateShot{{ID: "generating-shot", ProjectID: p.ID, Position: 1, DurationS: 6, FirstFrameStatus: "generating", CreatedAt: now, UpdatedAt: now}}
		}
		if err := r.Create(ctx, p, shots); err != nil {
			t.Fatal(err)
		}
	}
	items, err := r.InterruptedProjects(ctx)
	if err != nil || len(items) != 2 || items[0].ID != "stitch" || items[1].ID != "frames" {
		t.Fatalf("interrupted=%+v err=%v", items, err)
	}
	for _, p := range items {
		if p.Shots != nil {
			t.Fatalf("recovery listing unexpectedly loaded shots: %+v", p)
		}
	}
}

func TestReplicateRepositoryOwnsCreateAndSaveTimestamps(t *testing.T) {
	r, closeDB := openReplicateTestRepository(t)
	defer closeDB()
	ctx := context.Background()
	before := time.Now().UTC()
	p := ReplicateProject{ID: "p", Title: "项目", Ratio: "9:16", VideoProvider: "grok", Variants: 1, Status: "draft"}
	shot := ReplicateShot{ID: "s", ProjectID: "p", Position: 1, DurationS: 6, FirstFrameStatus: "ready"}
	if err := r.Create(ctx, p, []ReplicateShot{shot}); err != nil {
		t.Fatal(err)
	}
	created, err := r.Get(ctx, "p")
	if err != nil || created.CreatedAt.Before(before) || created.UpdatedAt.Before(before) || created.Shots[0].CreatedAt.Before(before) || created.Shots[0].UpdatedAt.Before(before) {
		t.Fatalf("create timestamps not server-owned: %+v err=%v", created, err)
	}

	p = created
	p.Title = "更新"
	p.UpdatedAt = time.Time{}
	projectSaveBefore := time.Now().UTC()
	if err := r.SaveProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	created.Shots[0].Purpose = "更新镜头"
	created.Shots[0].UpdatedAt = time.Time{}
	shotSaveBefore := time.Now().UTC()
	if err := r.SaveShot(ctx, created.Shots[0]); err != nil {
		t.Fatal(err)
	}
	take := ReplicateTake{ID: "t", ShotID: "s", Provider: "grok", Prompt: "运动", Status: "queued", KeySlot: -1}
	if err := r.CreateTakes(ctx, []ReplicateTake{take}); err != nil {
		t.Fatal(err)
	}
	take, err = r.GetTake(ctx, "t")
	if err != nil || take.CreatedAt.Before(before) || take.UpdatedAt.Before(before) {
		t.Fatalf("take create timestamps: %+v err=%v", take, err)
	}
	take.Status = "succeeded"
	take.UpdatedAt = time.Time{}
	takeSaveBefore := time.Now().UTC()
	if err := r.SaveTake(ctx, take); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "p")
	if err != nil || got.UpdatedAt.Before(projectSaveBefore) || got.Shots[0].UpdatedAt.Before(takeSaveBefore) {
		t.Fatalf("save timestamps: project=%v shot=%v projectSaveBefore=%v shotSaveBefore=%v takeSaveBefore=%v err=%v", got.UpdatedAt, got.Shots[0].UpdatedAt, projectSaveBefore, shotSaveBefore, takeSaveBefore, err)
	}
}
