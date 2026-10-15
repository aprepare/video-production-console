package store

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func commentInsightDB(t *testing.T) (*CommentInsightRepository, *ReferenceRepository) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	refs := NewReferenceRepository(db)
	if _, err := refs.Upsert(t.Context(), ReferencePost{ID: "ref", Platform: "douyin", ExternalID: "video"}); err != nil {
		t.Fatal(err)
	}
	return NewCommentInsightRepository(db), refs
}

func TestCommentInsightRepositoryLifecycleAndListHidesReport(t *testing.T) {
	repo, _ := commentInsightDB(t)
	created, fresh, err := repo.CreateQueued(t.Context(), CommentInsight{ID: "insight", ReferenceID: "ref", Model: "m", CreatedAt: time.Date(2026, 10, 15, 8, 0, 5, 0, time.UTC)})
	if err != nil || !fresh || created.Status != "queued" {
		t.Fatalf("CreateQueued = %+v, %v, %v", created, fresh, err)
	}
	var storedCreated string
	if err := repo.db.QueryRow(`SELECT created_at FROM comment_insights WHERE id='insight'`).Scan(&storedCreated); err != nil || storedCreated != "2026-10-15T08:00:05.000000000Z" {
		t.Fatalf("stored created_at = %q, %v", storedCreated, err)
	}
	if err := repo.MarkRunning(t.Context(), created.ID, 8); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(t.Context(), created.ID, `{"summary":"ok"}`); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), created.ID)
	if err != nil || got.Status != "completed" || got.CommentCount != 8 || got.ReportJSON == "" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	listed, err := repo.ListByReference(t.Context(), "ref")
	if err != nil || len(listed) != 1 || listed[0].ReportJSON != "" {
		t.Fatalf("List = %+v, %v", listed, err)
	}
}

func TestCommentInsightRepositoryConcurrentCreateDeduplicates(t *testing.T) {
	repo, _ := commentInsightDB(t)
	start := make(chan struct{})
	results := make(chan struct {
		item    CommentInsight
		created bool
		err     error
	}, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			item, created, err := repo.CreateQueued(t.Context(), CommentInsight{ID: id, ReferenceID: "ref", Model: "m"})
			results <- struct {
				item    CommentInsight
				created bool
				err     error
			}{item, created, err}
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	createdCount := 0
	ids := map[string]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
		ids[result.item.ID] = true
	}
	if createdCount != 1 || len(ids) != 1 {
		t.Fatalf("created=%d ids=%v", createdCount, ids)
	}
}

func TestCommentInsightRepositoryFailResumeDeleteAndCascade(t *testing.T) {
	repo, refs := commentInsightDB(t)
	for _, item := range []CommentInsight{{ID: "failed", ReferenceID: "ref"}, {ID: "cascade", ReferenceID: "ref"}} {
		if _, _, err := repo.CreateQueued(t.Context(), item); err != nil {
			t.Fatal(err)
		}
		if item.ID == "failed" {
			if err := repo.FailInterrupted(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := repo.Get(t.Context(), "failed")
	if err != nil || got.Status != "failed" || got.Error != "控制台重启，评论洞察中断，请重新分析" {
		t.Fatalf("interrupted = %+v, %v", got, err)
	}
	if err := repo.Delete(t.Context(), "failed"); err != nil {
		t.Fatal(err)
	}
	if err := refs.Delete(t.Context(), "ref"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(t.Context(), "cascade"); err != ErrCommentInsightNotFound {
		t.Fatalf("cascade err = %v", err)
	}
}

func TestCommentInsightRepositoryListsAllRecordsBeyondTwenty(t *testing.T) {
	repo, _ := commentInsightDB(t)
	base := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		id := "item-" + strconv.Itoa(i)
		item, created, err := repo.CreateQueued(t.Context(), CommentInsight{ID: id, ReferenceID: "ref", CreatedAt: base.Add(time.Duration(i) * time.Second)})
		if err != nil || !created {
			t.Fatalf("create %d: %+v %v", i, item, err)
		}
		if err := repo.Fail(t.Context(), id, "done"); err != nil {
			t.Fatal(err)
		}
	}
	items, err := repo.ListByReference(t.Context(), "ref")
	if err != nil || len(items) != 25 {
		t.Fatalf("ListByReference len=%d err=%v", len(items), err)
	}
	if items[0].ID != "item-24" || items[24].ID != "item-0" {
		t.Fatalf("order: first=%s last=%s", items[0].ID, items[24].ID)
	}
}

func TestCommentInsightRepositoryOrdersWholeSecondBeforeEarlierAndAfterFractional(t *testing.T) {
	repo, _ := commentInsightDB(t)
	rows := []struct{ id, created string }{
		{"whole", "2026-10-15T08:00:05Z"},
		{"later", "2026-10-15T08:00:05.100000000Z"},
		{"earlier", "2026-10-15T08:00:04.999999999Z"},
	}
	for _, row := range rows {
		if _, err := repo.db.Exec(`INSERT INTO comment_insights(id,reference_id,status,created_at,updated_at) VALUES(?,?,'completed',?,?)`, row.id, "ref", row.created, row.created); err != nil {
			t.Fatal(err)
		}
	}
	items, err := repo.ListByReference(t.Context(), "ref")
	if err != nil || len(items) != 3 {
		t.Fatalf("ListByReference = %+v, %v", items, err)
	}
	want := []string{"later", "whole", "earlier"}
	for i := range want {
		if items[i].ID != want[i] {
			t.Fatalf("order[%d]=%s want %s", i, items[i].ID, want[i])
		}
	}
}
