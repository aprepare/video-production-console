package store

import (
	"fmt"
	"testing"
	"time"

	"video-production-console/internal/platformapi"
)

func TestMonitorHistoryUsesSnapshotTimeNotInsertionOrder(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	task, err := repo.CreateTask(t.Context(), monitorTaskFixture("history", now))
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := repo.UpsertItem(t.Context(), task.ID, "seed", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "one"}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Insert newest first, so rowid order is the reverse of observation time.
	for i := 15; i >= 1; i-- {
		_, err := repo.db.ExecContext(t.Context(), `INSERT INTO monitor_snapshots(item_id,run_id,at,likes,comments,shares,collects) VALUES(?,?,?,?,0,0,0)`, item.ID, fmt.Sprint(i), formatMonitorItemTime(now.Add(time.Duration(i)*time.Minute)), i)
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.GetItem(t.Context(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.History) != 12 {
		t.Fatalf("history length=%d", len(got.History))
	}
	for i, snapshot := range got.History {
		if snapshot.Likes != int64(i+4) {
			t.Fatalf("history[%d]=%+v want likes=%d", i, snapshot, i+4)
		}
	}
}

func TestMonitorMissingCountsPreserveValuesWithoutSnapshot(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	now := time.Now().UTC()
	task, err := repo.CreateTask(t.Context(), monitorTaskFixture("missing", now))
	if err != nil {
		t.Fatal(err)
	}
	post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: "one", Likes: 1000, Comments: 80, Shares: 20, Collects: 30}
	item, _, err := repo.UpsertItem(t.Context(), task.ID, "first", post, now)
	if err != nil {
		t.Fatal(err)
	}
	post.CountsMissing, post.Likes, post.Comments, post.Shares, post.Collects, post.Title = true, 0, 0, 0, 0, "new title"
	got, _, err := repo.UpsertItem(t.Context(), task.ID, "missing", post, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Likes != 1000 || got.Comments != 80 || got.Shares != 20 || got.Collects != 30 || len(got.History) != 1 || got.Title != "new title" || got.SeenCount != 2 || got.LastSeenAt == item.LastSeenAt {
		t.Fatalf("missing counts overwrote observation: %+v", got)
	}
	post.CountsMissing = false
	got, _, err = repo.UpsertItem(t.Context(), task.ID, "zero", post, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Likes != 0 || got.Comments != 0 || len(got.History) != 2 {
		t.Fatalf("explicit zero not saved: %+v", got)
	}
	post.ExternalID, post.CountsMissing = "new-missing", true
	got, previous, err := repo.UpsertItem(t.Context(), task.ID, "new-missing", post, now)
	if err != nil || previous != nil || len(got.History) != 0 {
		t.Fatalf("new missing snapshot: %+v previous=%+v err=%v", got, previous, err)
	}
}
