package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"video-production-console/internal/platformapi"
)

func openMonitorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func monitorTaskFixture(id string, created time.Time) MonitorTask {
	at := created.UTC().Format(time.RFC3339)
	return MonitorTask{
		ID: id, Name: "海鲜", Platform: "douyin", Keyword: "岚山海鲜",
		IntervalMinutes: 30, Pages: 1, Enabled: true, Model: "",
		ScoreEnabled: true, ScoreThreshold: 70, AutoCollect: true,
		SurgeMode: "rule", SurgeLikeDelta: 500, SurgeLikeRatio: .5,
		SurgeCommentDelta: 50, SurgeCommentRatio: .5, SurgeMinBase: 20,
		NextRunAt: at, CreatedAt: at, UpdatedAt: at,
	}
}

func TestMonitorMigrationsCreateVersionsAndTables(t *testing.T) {
	db := openMonitorTestDB(t)
	if got := scalar(t, db, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`); got != "46" {
		t.Fatalf("latest migration=%s, want 46", got)
	}
	for _, table := range []string{"monitor_tasks", "monitor_runs", "monitor_items", "monitor_snapshots", "comment_insights"} {
		if !tableExists(t, db, table) {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestMonitorMigrationsUpgradeVersion44(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version-44.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for index, migration := range migrations[:44] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply migration %d: %v", index+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, index+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO reference_posts(id,platform,external_id,created_at,updated_at) VALUES('legacy','douyin','legacy',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version IN (45,46)`); got != "2" {
		t.Fatalf("new migration count=%s", got)
	}
	if got := scalar(t, db, `SELECT platform || ':' || external_id FROM reference_posts WHERE id='legacy'`); got != "douyin:legacy" {
		t.Fatalf("legacy row after upgrade=%s", got)
	}
}

func TestMonitorTaskCRUDDueAndAggregates(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	first, err := repo.CreateTask(ctx, monitorTaskFixture("task-1", now))
	if err != nil {
		t.Fatal(err)
	}
	secondTask := monitorTaskFixture("task-2", now.Add(time.Minute))
	secondTask.Enabled = false
	secondTask.NextRunAt = now.Add(-time.Minute).Format(time.RFC3339)
	if _, err := repo.CreateTask(ctx, secondTask); err != nil {
		t.Fatal(err)
	}
	first.Name = "新名称"
	if _, err := repo.UpdateTask(ctx, first); err != nil {
		t.Fatal(err)
	}
	run := MonitorRun{ID: "run-1", TaskID: first.ID, Status: "running", Trigger: "manual", StartedAt: now.Format(time.RFC3339)}
	if _, err := repo.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: "post-1", Title: "作品", Likes: 10}
	item, _, err := repo.UpsertItem(ctx, first.ID, run.ID, post, now)
	if err != nil {
		t.Fatal(err)
	}
	read := false
	if _, err := repo.PatchItem(ctx, item.ID, &read, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSurge(ctx, item.ID, true, "增长", now.Format(time.RFC3339), 10, 2, 20); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetTask(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "新名称" || !got.Running || got.ItemCount != 1 || got.UnreadCount != 1 || got.SurgeCount != 1 {
		t.Fatalf("task aggregates=%+v", got)
	}
	list, err := repo.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "task-2" {
		t.Fatalf("task order=%+v", list)
	}
	due, err := repo.DueTasks(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != first.ID {
		t.Fatalf("due=%+v", due)
	}
	if err := repo.DeleteTask(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"monitor_runs", "monitor_items", "monitor_snapshots"} {
		if got := scalar(t, repo.db, `SELECT COUNT(*) FROM `+table); got != "0" {
			t.Fatalf("%s rows after cascade=%s", table, got)
		}
	}
}

func TestMonitorCreateRunRejectsConcurrentTaskRun(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	base := MonitorRun{TaskID: "task", Status: "running", Trigger: "manual", StartedAt: now.Format(time.RFC3339)}
	base.ID = "run-1"
	if _, err := repo.CreateRun(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.ID = "run-2"
	if _, err := repo.CreateRun(ctx, base); !errors.Is(err, ErrMonitorRunning) {
		t.Fatalf("second CreateRun error=%v, want ErrMonitorRunning", err)
	}
}

func TestMonitorCreateRunSerializesConcurrentWriters(t *testing.T) {
	db := openMonitorTestDB(t)
	db.SetMaxOpenConns(4)
	repo := NewMonitorRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := repo.CreateRun(ctx, MonitorRun{ID: fmt.Sprintf("run-%d", index), TaskID: "task", Status: "running", Trigger: "manual", StartedAt: now.Format(time.RFC3339)})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded, rejected := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrMonitorRunning):
			rejected++
		default:
			t.Fatalf("unexpected CreateRun error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent outcomes: succeeded=%d rejected=%d", succeeded, rejected)
	}
}

func TestMonitorFinishRunWritesCountersAndTaskState(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	task := monitorTaskFixture("task", now)
	if _, err := repo.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := MonitorRun{ID: "run", TaskID: task.ID, Status: "running", Trigger: "manual", StartedAt: now.Format(time.RFC3339)}
	if _, err := repo.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	run.Status = "completed"
	run.Found, run.NewCount, run.SurgeCount, run.ScoredCount, run.CollectedCount = 8, 5, 3, 4, 2
	run.FinishedAt = now.Add(time.Minute).Format(time.RFC3339)
	next := now.Add(31 * time.Minute)
	if err := repo.FinishRun(ctx, run, next); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRuns(ctx, task.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].Found != 8 || runs[0].CollectedCount != 2 {
		t.Fatalf("runs=%+v", runs)
	}
	got, err := repo.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Running || got.LastRunAt != run.FinishedAt || got.LastStatus != "ok" || got.NextRunAt != next.Format(time.RFC3339) {
		t.Fatalf("task after finish=%+v", got)
	}
}

func TestMonitorUpdateTaskPreservesRunStateAndOnlyReschedulesChangedInterval(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	task, err := repo.CreateTask(ctx, monitorTaskFixture("task", now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE monitor_tasks SET last_run_at='finished',last_status='ok',last_error='kept',next_run_at='current' WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	task.Name = "修改"
	task.LastRunAt, task.LastStatus, task.LastError, task.NextRunAt = "stale", "failed", "stale", "replacement"
	got, err := repo.UpdateTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt != "finished" || got.LastStatus != "ok" || got.LastError != "kept" || got.NextRunAt != "current" {
		t.Fatalf("same interval update overwrote run state: %+v", got)
	}
	task.IntervalMinutes = 60
	task.NextRunAt = "replacement"
	got, err = repo.UpdateTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextRunAt != "replacement" || got.IntervalMinutes != 60 {
		t.Fatalf("changed interval did not reschedule: %+v", got)
	}
}

func TestMonitorUpsertReturnsPreviousAndDoesNotDoubleCountSameRun(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	t0 := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", t0)); err != nil {
		t.Fatal(err)
	}
	post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: "post", URL: "old", Title: "旧", Text: "文案", Author: "作者", CoverURL: "cover", VideoURL: "video", DurationS: 12.5, CreatedAt: 123, Likes: 10, Comments: 2, Shares: 3, Collects: 4}
	first, previous, err := repo.UpsertItem(ctx, "task", "run-1", post, t0)
	if err != nil {
		t.Fatal(err)
	}
	if previous != nil || first.SeenCount != 1 || len(first.History) != 1 {
		t.Fatalf("first=%+v previous=%+v", first, previous)
	}
	post.URL, post.Title, post.Likes, post.Comments = "new", "新", 30, 5
	second, previous, err := repo.UpsertItem(ctx, "task", "run-2", post, t0.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || previous.Likes != 10 || previous.LastSeenAt != "2026-10-15T08:00:00.000000000Z" || len(previous.History) != 1 {
		t.Fatalf("previous=%+v", previous)
	}
	if second.URL != "new" || second.Title != "新" || second.Likes != 30 || second.SeenCount != 2 || len(second.History) != 2 {
		t.Fatalf("second=%+v", second)
	}
	third, previous, err := repo.UpsertItem(ctx, "task", "run-2", post, t0.Add(31*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || third.SeenCount != 2 || len(third.History) != 2 {
		t.Fatalf("same-run upsert=%+v previous=%+v", third, previous)
	}
}

func TestMonitorItemTimestampsIncreaseAndHistoryKeepsLatestTwelve(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: "post"}
	lastSeen := time.Time{}
	var item MonitorItem
	for i := 0; i < 13; i++ {
		post.Likes = int64(i)
		var err error
		item, _, err = repo.UpsertItem(ctx, "task", fmt.Sprintf("run-%02d", i), post, now)
		if err != nil {
			t.Fatal(err)
		}
		seen, err := time.Parse(time.RFC3339Nano, item.LastSeenAt)
		if err != nil {
			t.Fatal(err)
		}
		if !lastSeen.IsZero() && !seen.After(lastSeen) {
			t.Fatalf("last_seen_at did not increase: previous=%s current=%s", lastSeen, seen)
		}
		lastSeen = seen
	}
	if len(item.History) != 12 || item.History[0].Likes != 1 || item.History[11].Likes != 12 {
		t.Fatalf("history=%+v", item.History)
	}
}

func TestMonitorFeedOrdersSameSecondItemsByNanoseconds(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		externalID string
		at         time.Time
	}{
		{externalID: "older", at: now},
		{externalID: "newer", at: now.Add(500 * time.Millisecond)},
	} {
		post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: fixture.externalID}
		if _, _, err := repo.UpsertItem(ctx, "task", "run-"+fixture.externalID, post, fixture.at); err != nil {
			t.Fatal(err)
		}
	}
	feed, err := repo.Feed(ctx, "task", "all", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 2 || feed.Items[0].ExternalID != "newer" || feed.Items[1].ExternalID != "older" {
		t.Fatalf("same-second feed order=%+v", feed.Items)
	}
}

func TestMonitorListRunsUsesInsertionOrderForSameStartedAt(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"z-older", "a-newer"} {
		if _, err := repo.CreateRun(ctx, MonitorRun{ID: id, TaskID: "task", Status: "completed", Trigger: "manual", StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := repo.ListRuns(ctx, "task", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "a-newer" || runs[1].ID != "z-older" {
		t.Fatalf("same-start run order=%+v", runs)
	}
}

func TestMonitorPruneKeepsLatestInsertedRunsWhenStartedAtTies(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	ids := []string{"zz-oldest", "zy-second-oldest"}
	for index := 0; index < 200; index++ {
		ids = append(ids, fmt.Sprintf("aa-new-%03d", index))
	}
	for _, id := range ids {
		if _, err := repo.CreateRun(ctx, MonitorRun{ID: id, TaskID: "task", Status: "completed", Trigger: "schedule", StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Prune(ctx, "task", now); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, repo.db, `SELECT COUNT(*) FROM monitor_runs WHERE task_id='task'`); got != "200" {
		t.Fatalf("runs after prune=%s", got)
	}
	if got := scalar(t, repo.db, `SELECT COUNT(*) FROM monitor_runs WHERE id IN ('zz-oldest','zy-second-oldest')`); got != "0" {
		t.Fatalf("old same-time runs retained=%s", got)
	}
	if got := scalar(t, repo.db, `SELECT COUNT(*) FROM monitor_runs WHERE id='aa-new-199'`); got != "1" {
		t.Fatalf("latest same-time run missing=%s", got)
	}
}

func TestMonitorFeedPatchReadAllAndScoreRoundTrip(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	for i, threshold := range []int{70, 90} {
		task := monitorTaskFixture(fmt.Sprintf("task-%d", i), now.Add(time.Duration(i)*time.Minute))
		task.ScoreThreshold = threshold
		if _, err := repo.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: fmt.Sprintf("post-%d", i), Title: fmt.Sprintf("作品%d", i)}
		item, _, err := repo.UpsertItem(ctx, task.ID, "run", post, now.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SetScore(ctx, item.ID, "scored", 80, "合适", []string{"反差", "价格"}, ""); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := repo.SetSurge(ctx, item.ID, true, "增长", now.Format(time.RFC3339), 12, 3, 24); err != nil {
				t.Fatal(err)
			}
		}
	}
	high, err := repo.Feed(ctx, "", "high", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(high.Items) != 1 || high.Items[0].TaskID != "task-0" || len(high.Items[0].ScoreTags) != 2 {
		t.Fatalf("high feed=%+v", high)
	}
	if high.Counts.All != 2 || high.Counts.High != 1 || high.Counts.Surge != 1 || high.Counts.Unread != 2 {
		t.Fatalf("counts=%+v", high.Counts)
	}
	limited, err := repo.Feed(ctx, "", "all", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Items) != 1 || limited.Counts.All != 2 || limited.Counts.Unread != 2 {
		t.Fatalf("limited feed changed aggregate counts: %+v", limited)
	}
	surging, err := repo.Feed(ctx, "", "surge", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(surging.Items) != 1 || !surging.Items[0].Surge || surging.Counts.All != 2 {
		t.Fatalf("surge feed=%+v", surging)
	}
	item := high.Items[0]
	read, dismissed := true, true
	patched, err := repo.PatchItem(ctx, item.ID, &read, &dismissed)
	if err != nil {
		t.Fatal(err)
	}
	if !patched.Read || !patched.Dismissed {
		t.Fatalf("patched=%+v", patched)
	}
	taskAfterDismiss, err := repo.GetTask(ctx, "task-0")
	if err != nil {
		t.Fatal(err)
	}
	if taskAfterDismiss.ItemCount != 0 || taskAfterDismiss.UnreadCount != 0 || taskAfterDismiss.SurgeCount != 0 {
		t.Fatalf("dismissed item included in task aggregates: %+v", taskAfterDismiss)
	}
	read = false
	if _, err := repo.PatchItem(ctx, item.ID, &read, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReadAll(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetReference(ctx, "missing", "reference"); !errors.Is(err, ErrMonitorItemNotFound) {
		t.Fatalf("missing SetReference error=%v", err)
	}
	if err := repo.SetReference(ctx, high.Items[0].ID, "reference"); err != nil {
		t.Fatal(err)
	}
	collected, err := repo.GetItem(ctx, high.Items[0].ID)
	if err != nil || collected.ReferenceID != "reference" {
		t.Fatalf("set reference item=%+v err=%v", collected, err)
	}
	if !collected.Read {
		t.Fatalf("read-all left dismissed item unread: %+v", collected)
	}
	feed, err := repo.Feed(ctx, "", "all", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 || feed.Counts.All != 1 || feed.Counts.Unread != 0 {
		t.Fatalf("feed after patch=%+v", feed)
	}
}

func TestMonitorSetSurgeOnlyReopensOnNewHit(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	item, _, err := repo.UpsertItem(ctx, "task", "run", platformapi.Post{Platform: platformapi.Douyin, ExternalID: "post"}, now)
	if err != nil {
		t.Fatal(err)
	}
	read := true
	if _, err := repo.PatchItem(ctx, item.ID, &read, nil); err != nil {
		t.Fatal(err)
	}
	firstHit := now.Format(time.RFC3339)
	if err := repo.SetSurge(ctx, item.ID, true, "命中", firstHit, 10, 1, 20); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetItem(ctx, item.ID)
	if err != nil || got.Read {
		t.Fatalf("new hit should reopen item: item=%+v err=%v", got, err)
	}
	read = true
	if _, err := repo.PatchItem(ctx, item.ID, &read, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSurge(ctx, item.ID, true, "迟滞保留", firstHit, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetItem(ctx, item.ID)
	if err != nil || !got.Read {
		t.Fatalf("same surge hit must preserve read: item=%+v err=%v", got, err)
	}
}

func TestMonitorFailInterruptedAndPruneRuns(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		run := MonitorRun{ID: fmt.Sprintf("run-%03d", i), TaskID: "task", Status: "completed", Trigger: "schedule", StartedAt: now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339), FinishedAt: now.Add(time.Duration(i)*time.Minute + time.Second).Format(time.RFC3339)}
		if _, err := repo.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	active := MonitorRun{ID: "active", TaskID: "task", Status: "running", Trigger: "manual", StartedAt: now.Format(time.RFC3339)}
	if _, err := repo.CreateRun(ctx, active); err != nil {
		t.Fatal(err)
	}
	if err := repo.FailInterrupted(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, repo.db, `SELECT status || ':' || error FROM monitor_runs WHERE id='active'`); got != "failed:控制台重启中断" {
		t.Fatalf("interrupted=%s", got)
	}
	if err := repo.Prune(ctx, "task", now); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, repo.db, `SELECT COUNT(*) FROM monitor_runs WHERE task_id='task'`); got != "200" {
		t.Fatalf("runs after prune=%s", got)
	}
	if got := scalar(t, repo.db, `SELECT COUNT(*) FROM monitor_runs WHERE id='run-000'`); got != "0" {
		t.Fatalf("oldest run retained=%s", got)
	}
}

func TestMonitorPruneSnapshotsOlderThanThirtyDays(t *testing.T) {
	repo := NewMonitorRepository(openMonitorTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := repo.CreateTask(ctx, monitorTaskFixture("task", now)); err != nil {
		t.Fatal(err)
	}
	post := platformapi.Post{Platform: platformapi.Douyin, ExternalID: "post"}
	if _, _, err := repo.UpsertItem(ctx, "task", "old", post, now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.UpsertItem(ctx, "task", "boundary", post, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Prune(ctx, "task", now); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, repo.db, `SELECT COUNT(*) FROM monitor_snapshots`); got != "1" {
		t.Fatalf("snapshots after prune=%s, want boundary snapshot retained", got)
	}
	if got := scalar(t, repo.db, `SELECT run_id FROM monitor_snapshots`); got != "boundary" {
		t.Fatalf("retained snapshot=%s", got)
	}
}
