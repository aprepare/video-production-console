package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/platformapi"
)

var (
	ErrMonitorNotFound     = errors.New("monitor task not found")
	ErrMonitorItemNotFound = errors.New("monitor item not found")
	ErrMonitorRunNotFound  = errors.New("monitor run not found")
	ErrMonitorRunning      = errors.New("monitor task is running")
)

type MonitorTask struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Platform          string  `json:"platform"`
	Keyword           string  `json:"keyword"`
	IntervalMinutes   int     `json:"interval_minutes"`
	Pages             int     `json:"pages"`
	Enabled           bool    `json:"enabled"`
	Model             string  `json:"model"`
	ScoreEnabled      bool    `json:"score_enabled"`
	ScoreThreshold    int     `json:"score_threshold"`
	ScoreFocus        string  `json:"score_focus"`
	AutoCollect       bool    `json:"auto_collect"`
	AutoTranscribe    bool    `json:"auto_transcribe"`
	AutoAnalyze       bool    `json:"auto_analyze"`
	AutoInsight       bool    `json:"auto_insight"`
	SurgeMode         string  `json:"surge_mode"`
	SurgeLikeDelta    int64   `json:"surge_like_delta"`
	SurgeLikeRatio    float64 `json:"surge_like_ratio"`
	SurgeCommentDelta int64   `json:"surge_comment_delta"`
	SurgeCommentRatio float64 `json:"surge_comment_ratio"`
	SurgeMinBase      int64   `json:"surge_min_base"`
	LastRunAt         string  `json:"last_run_at"`
	NextRunAt         string  `json:"next_run_at"`
	LastStatus        string  `json:"last_status"`
	LastError         string  `json:"last_error"`
	Running           bool    `json:"running"`
	ItemCount         int64   `json:"item_count"`
	UnreadCount       int64   `json:"unread_count"`
	SurgeCount        int64   `json:"surge_count"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

type MonitorRun struct {
	ID             string `json:"id"`
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	Trigger        string `json:"trigger"`
	Found          int64  `json:"found"`
	NewCount       int64  `json:"new_count"`
	SurgeCount     int64  `json:"surge_count"`
	ScoredCount    int64  `json:"scored_count"`
	CollectedCount int64  `json:"collected_count"`
	Error          string `json:"error"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
}

type MonitorSnapshot struct {
	At       string `json:"at"`
	Likes    int64  `json:"likes"`
	Comments int64  `json:"comments"`
	Shares   int64  `json:"-"`
	Collects int64  `json:"-"`
}

type MonitorItem struct {
	ID              string            `json:"id"`
	TaskID          string            `json:"task_id"`
	TaskName        string            `json:"task_name"`
	Platform        string            `json:"platform"`
	ExternalID      string            `json:"external_id"`
	URL             string            `json:"url"`
	Title           string            `json:"title"`
	Caption         string            `json:"caption"`
	Author          string            `json:"author"`
	CoverURL        string            `json:"cover_url"`
	VideoURL        string            `json:"video_url"`
	DurationS       float64           `json:"duration_s"`
	PostedAt        int64             `json:"posted_at"`
	Likes           int64             `json:"likes"`
	Comments        int64             `json:"comments"`
	Shares          int64             `json:"shares"`
	Collects        int64             `json:"collects"`
	FirstSeenAt     string            `json:"first_seen_at"`
	LastSeenAt      string            `json:"last_seen_at"`
	SeenCount       int64             `json:"seen_count"`
	ScoreStatus     string            `json:"score_status"`
	Score           int               `json:"score"`
	ScoreReason     string            `json:"score_reason"`
	ScoreTags       []string          `json:"score_tags"`
	ScoreError      string            `json:"score_error"`
	Surge           bool              `json:"surge"`
	SurgeReason     string            `json:"surge_reason"`
	SurgeAt         string            `json:"surge_at"`
	LikeDelta       int64             `json:"like_delta"`
	CommentDelta    int64             `json:"comment_delta"`
	LikeRatePerHour float64           `json:"like_rate_per_hour"`
	ReferenceID     string            `json:"reference_id"`
	Read            bool              `json:"read"`
	Dismissed       bool              `json:"-"`
	History         []MonitorSnapshot `json:"history"`
}

type MonitorFeedCounts struct {
	All    int64 `json:"all"`
	Surge  int64 `json:"surge"`
	High   int64 `json:"high"`
	Unread int64 `json:"unread"`
}

type MonitorFeed struct {
	Items  []MonitorItem     `json:"items"`
	Counts MonitorFeedCounts `json:"counts"`
}

type MonitorRepository struct{ db *sql.DB }

func NewMonitorRepository(db *sql.DB) *MonitorRepository { return &MonitorRepository{db: db} }

const monitorTaskColumns = `t.id,t.name,t.platform,t.keyword,t.interval_minutes,t.pages,t.enabled,t.model,
	t.score_enabled,t.score_threshold,t.score_focus,t.auto_collect,t.auto_transcribe,t.auto_analyze,t.auto_insight,
	t.surge_mode,t.surge_like_delta,t.surge_like_ratio,t.surge_comment_delta,t.surge_comment_ratio,t.surge_min_base,
	t.last_run_at,t.next_run_at,t.last_status,t.last_error,t.created_at,t.updated_at,
	EXISTS(SELECT 1 FROM monitor_runs r WHERE r.task_id=t.id AND r.status='running'),
	(SELECT COUNT(*) FROM monitor_items i WHERE i.task_id=t.id AND i.dismissed=0),
	(SELECT COUNT(*) FROM monitor_items i WHERE i.task_id=t.id AND i.dismissed=0 AND i.read=0),
	(SELECT COUNT(*) FROM monitor_items i WHERE i.task_id=t.id AND i.dismissed=0 AND i.surge=1)`

func scanMonitorTask(row interface{ Scan(...any) error }) (MonitorTask, error) {
	var task MonitorTask
	err := row.Scan(&task.ID, &task.Name, &task.Platform, &task.Keyword, &task.IntervalMinutes, &task.Pages, &task.Enabled, &task.Model,
		&task.ScoreEnabled, &task.ScoreThreshold, &task.ScoreFocus, &task.AutoCollect, &task.AutoTranscribe, &task.AutoAnalyze, &task.AutoInsight,
		&task.SurgeMode, &task.SurgeLikeDelta, &task.SurgeLikeRatio, &task.SurgeCommentDelta, &task.SurgeCommentRatio, &task.SurgeMinBase,
		&task.LastRunAt, &task.NextRunAt, &task.LastStatus, &task.LastError, &task.CreatedAt, &task.UpdatedAt,
		&task.Running, &task.ItemCount, &task.UnreadCount, &task.SurgeCount)
	return task, err
}

func (r *MonitorRepository) CreateTask(ctx context.Context, task MonitorTask) (MonitorTask, error) {
	_, err := r.db.ExecContext(ctx, `INSERT INTO monitor_tasks(id,name,platform,keyword,interval_minutes,pages,enabled,model,
		score_enabled,score_threshold,score_focus,auto_collect,auto_transcribe,auto_analyze,auto_insight,
		surge_mode,surge_like_delta,surge_like_ratio,surge_comment_delta,surge_comment_ratio,surge_min_base,
		last_run_at,next_run_at,last_status,last_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		task.ID, task.Name, task.Platform, task.Keyword, task.IntervalMinutes, task.Pages, task.Enabled, task.Model,
		task.ScoreEnabled, task.ScoreThreshold, task.ScoreFocus, task.AutoCollect, task.AutoTranscribe, task.AutoAnalyze, task.AutoInsight,
		task.SurgeMode, task.SurgeLikeDelta, task.SurgeLikeRatio, task.SurgeCommentDelta, task.SurgeCommentRatio, task.SurgeMinBase,
		task.LastRunAt, task.NextRunAt, task.LastStatus, task.LastError, task.CreatedAt, task.UpdatedAt)
	if err != nil {
		return MonitorTask{}, fmt.Errorf("create monitor task: %w", err)
	}
	return r.GetTask(ctx, task.ID)
}

func (r *MonitorRepository) UpdateTask(ctx context.Context, task MonitorTask) (MonitorTask, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE monitor_tasks SET name=?,platform=?,keyword=?,interval_minutes=?,pages=?,enabled=?,model=?,
		score_enabled=?,score_threshold=?,score_focus=?,auto_collect=?,auto_transcribe=?,auto_analyze=?,auto_insight=?,
		surge_mode=?,surge_like_delta=?,surge_like_ratio=?,surge_comment_delta=?,surge_comment_ratio=?,surge_min_base=?,
		next_run_at=CASE WHEN interval_minutes<>? THEN ? ELSE next_run_at END,updated_at=? WHERE id=?`,
		task.Name, task.Platform, task.Keyword, task.IntervalMinutes, task.Pages, task.Enabled, task.Model,
		task.ScoreEnabled, task.ScoreThreshold, task.ScoreFocus, task.AutoCollect, task.AutoTranscribe, task.AutoAnalyze, task.AutoInsight,
		task.SurgeMode, task.SurgeLikeDelta, task.SurgeLikeRatio, task.SurgeCommentDelta, task.SurgeCommentRatio, task.SurgeMinBase,
		task.IntervalMinutes, task.NextRunAt, task.UpdatedAt, task.ID)
	if err != nil {
		return MonitorTask{}, fmt.Errorf("update monitor task: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return MonitorTask{}, ErrMonitorNotFound
	}
	return r.GetTask(ctx, task.ID)
}

func (r *MonitorRepository) GetTask(ctx context.Context, id string) (MonitorTask, error) {
	task, err := scanMonitorTask(r.db.QueryRowContext(ctx, `SELECT `+monitorTaskColumns+` FROM monitor_tasks t WHERE t.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MonitorTask{}, ErrMonitorNotFound
	}
	if err != nil {
		return MonitorTask{}, fmt.Errorf("get monitor task: %w", err)
	}
	return task, nil
}

func (r *MonitorRepository) ListTasks(ctx context.Context) ([]MonitorTask, error) {
	return r.listTasks(ctx, `1=1`, nil, `t.created_at DESC,t.id`)
}

func (r *MonitorRepository) DueTasks(ctx context.Context, now time.Time) ([]MonitorTask, error) {
	return r.listTasks(ctx, `t.enabled=1 AND t.next_run_at<>'' AND t.next_run_at<=?`, []any{formatMonitorTime(now)}, `t.next_run_at,t.id`)
}

func (r *MonitorRepository) listTasks(ctx context.Context, where string, args []any, order string) ([]MonitorTask, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+monitorTaskColumns+` FROM monitor_tasks t WHERE `+where+` ORDER BY `+order, args...)
	if err != nil {
		return nil, fmt.Errorf("list monitor tasks: %w", err)
	}
	defer rows.Close()
	out := []MonitorTask{}
	for rows.Next() {
		task, err := scanMonitorTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan monitor task: %w", err)
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (r *MonitorRepository) DeleteTask(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM monitor_tasks WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete monitor task: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrMonitorNotFound
	}
	return nil
}

func (r *MonitorRepository) CreateRun(ctx context.Context, run MonitorRun) (MonitorRun, error) {
	err := runImmediate(ctx, r.db, "create monitor run", nil, func(tx assetDBTX, _ time.Time) error {
		if run.Status == "running" {
			var active int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_runs WHERE task_id=? AND status='running'`, run.TaskID).Scan(&active); err != nil {
				return fmt.Errorf("check active monitor run: %w", err)
			}
			if active != 0 {
				return ErrMonitorRunning
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_runs(id,task_id,status,trigger,found,new_count,surge_count,scored_count,collected_count,error,started_at,finished_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.TaskID, run.Status, run.Trigger, run.Found, run.NewCount, run.SurgeCount, run.ScoredCount, run.CollectedCount, run.Error, run.StartedAt, run.FinishedAt); err != nil {
			return fmt.Errorf("create monitor run: %w", err)
		}
		return nil
	})
	if err != nil {
		return MonitorRun{}, err
	}
	return run, nil
}

func (r *MonitorRepository) FinishRun(ctx context.Context, run MonitorRun, next time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish monitor run: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE monitor_runs SET status=?,found=?,new_count=?,surge_count=?,scored_count=?,collected_count=?,error=?,finished_at=? WHERE id=? AND task_id=?`,
		run.Status, run.Found, run.NewCount, run.SurgeCount, run.ScoredCount, run.CollectedCount, run.Error, run.FinishedAt, run.ID, run.TaskID)
	if err != nil {
		return fmt.Errorf("finish monitor run: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrMonitorRunNotFound
	}
	lastStatus := "failed"
	if run.Status == "completed" {
		lastStatus = "ok"
	}
	result, err = tx.ExecContext(ctx, `UPDATE monitor_tasks SET last_run_at=?,last_status=?,last_error=?,next_run_at=?,updated_at=? WHERE id=?`,
		run.FinishedAt, lastStatus, run.Error, formatMonitorTime(next), run.FinishedAt, run.TaskID)
	if err != nil {
		return fmt.Errorf("update monitor task after run: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrMonitorNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finish monitor run: %w", err)
	}
	return nil
}

func (r *MonitorRepository) FailInterrupted(ctx context.Context, now time.Time) error {
	at := formatMonitorTime(now)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fail interrupted monitors: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE monitor_tasks SET last_run_at=?,next_run_at=?,last_status='failed',last_error='控制台重启中断',updated_at=?
		WHERE EXISTS(SELECT 1 FROM monitor_runs r WHERE r.task_id=monitor_tasks.id AND r.status='running')`, at, at, at); err != nil {
		return fmt.Errorf("update interrupted monitor tasks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE monitor_runs SET status='failed',error='控制台重启中断',finished_at=? WHERE status='running'`, at); err != nil {
		return fmt.Errorf("fail interrupted monitor runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fail interrupted monitors: %w", err)
	}
	return nil
}

func (r *MonitorRepository) ListRuns(ctx context.Context, taskID string, limit int) ([]MonitorRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,task_id,status,trigger,found,new_count,surge_count,scored_count,collected_count,error,started_at,finished_at
		FROM monitor_runs WHERE task_id=? ORDER BY started_at DESC,rowid DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list monitor runs: %w", err)
	}
	defer rows.Close()
	out := []MonitorRun{}
	for rows.Next() {
		var run MonitorRun
		if err := rows.Scan(&run.ID, &run.TaskID, &run.Status, &run.Trigger, &run.Found, &run.NewCount, &run.SurgeCount, &run.ScoredCount, &run.CollectedCount, &run.Error, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan monitor run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

const monitorItemColumns = `i.id,i.task_id,t.name,i.platform,i.external_id,i.url,i.title,i.caption,i.author,i.cover_url,i.video_url,
	i.duration_s,i.posted_at,i.likes,i.comments,i.shares,i.collects,i.first_seen_at,i.last_seen_at,i.seen_count,
	i.score_status,i.score,i.score_reason,i.score_tags,i.score_error,i.surge,i.surge_reason,i.surge_at,
	i.like_delta,i.comment_delta,i.like_rate_per_hour,i.reference_id,i.read,i.dismissed`

func scanMonitorItem(row interface{ Scan(...any) error }) (MonitorItem, error) {
	var item MonitorItem
	var tags string
	err := row.Scan(&item.ID, &item.TaskID, &item.TaskName, &item.Platform, &item.ExternalID, &item.URL, &item.Title, &item.Caption, &item.Author, &item.CoverURL, &item.VideoURL,
		&item.DurationS, &item.PostedAt, &item.Likes, &item.Comments, &item.Shares, &item.Collects, &item.FirstSeenAt, &item.LastSeenAt, &item.SeenCount,
		&item.ScoreStatus, &item.Score, &item.ScoreReason, &tags, &item.ScoreError, &item.Surge, &item.SurgeReason, &item.SurgeAt,
		&item.LikeDelta, &item.CommentDelta, &item.LikeRatePerHour, &item.ReferenceID, &item.Read, &item.Dismissed)
	if err == nil {
		item.ScoreTags = []string{}
		if tags != "" {
			if decodeErr := json.Unmarshal([]byte(tags), &item.ScoreTags); decodeErr != nil {
				return MonitorItem{}, fmt.Errorf("decode monitor score tags: %w", decodeErr)
			}
		}
	}
	return item, err
}

type monitorQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getMonitorItem(ctx context.Context, q monitorQuerier, where string, args ...any) (MonitorItem, error) {
	item, err := scanMonitorItem(q.QueryRowContext(ctx, `SELECT `+monitorItemColumns+` FROM monitor_items i JOIN monitor_tasks t ON t.id=i.task_id WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return MonitorItem{}, ErrMonitorItemNotFound
	}
	if err != nil {
		return MonitorItem{}, fmt.Errorf("get monitor item: %w", err)
	}
	history, err := monitorHistory(ctx, q, item.ID)
	if err != nil {
		return MonitorItem{}, err
	}
	item.History = history
	return item, nil
}

func monitorHistory(ctx context.Context, q monitorQuerier, itemID string) ([]MonitorSnapshot, error) {
	rows, err := q.QueryContext(ctx, `SELECT at,likes,comments,shares,collects FROM monitor_snapshots WHERE item_id=? ORDER BY rowid DESC LIMIT 12`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list monitor snapshots: %w", err)
	}
	defer rows.Close()
	history := []MonitorSnapshot{}
	for rows.Next() {
		var snapshot MonitorSnapshot
		if err := rows.Scan(&snapshot.At, &snapshot.Likes, &snapshot.Comments, &snapshot.Shares, &snapshot.Collects); err != nil {
			return nil, fmt.Errorf("scan monitor snapshot: %w", err)
		}
		history = append(history, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(history)-1; left < right; left, right = left+1, right-1 {
		history[left], history[right] = history[right], history[left]
	}
	return history, nil
}

func (r *MonitorRepository) UpsertItem(ctx context.Context, taskID, runID string, post platformapi.Post, at time.Time) (MonitorItem, *MonitorItem, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return MonitorItem{}, nil, fmt.Errorf("begin monitor item upsert: %w", err)
	}
	defer tx.Rollback()
	platform := string(post.Platform)
	previous, err := getMonitorItem(ctx, tx, `i.task_id=? AND i.platform=? AND i.external_id=?`, taskID, platform, post.ExternalID)
	isNew := errors.Is(err, ErrMonitorItemNotFound)
	if err != nil && !isNew {
		return MonitorItem{}, nil, err
	}
	atText := formatMonitorItemTime(at)
	itemID := previous.ID
	if isNew {
		itemID = uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO monitor_items(id,task_id,platform,external_id,url,title,caption,author,cover_url,video_url,duration_s,posted_at,
			likes,comments,shares,collects,first_seen_at,last_seen_at,seen_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`,
			itemID, taskID, platform, post.ExternalID, post.URL, post.Title, post.Text, post.Author, post.CoverURL, post.VideoURL, post.DurationS, post.CreatedAt,
			post.Likes, post.Comments, post.Shares, post.Collects, atText, atText)
	} else {
		var existingSnapshotAt string
		err = tx.QueryRowContext(ctx, `SELECT at FROM monitor_snapshots WHERE item_id=? AND run_id=?`, itemID, runID).Scan(&existingSnapshotAt)
		sameRun := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return MonitorItem{}, nil, fmt.Errorf("check monitor snapshot: %w", err)
		}
		lastSeen, seenCount := atText, previous.SeenCount+1
		if sameRun {
			lastSeen, seenCount = previous.LastSeenAt, previous.SeenCount
			atText = existingSnapshotAt
		} else if previousAt, parseErr := time.Parse(time.RFC3339Nano, previous.LastSeenAt); parseErr == nil && !at.UTC().After(previousAt) {
			atText = formatMonitorItemTime(previousAt.Add(time.Nanosecond))
			lastSeen = atText
		}
		_, err = tx.ExecContext(ctx, `UPDATE monitor_items SET url=?,title=?,caption=?,author=?,cover_url=?,video_url=?,duration_s=?,posted_at=?,
			likes=?,comments=?,shares=?,collects=?,last_seen_at=?,seen_count=? WHERE id=?`,
			post.URL, post.Title, post.Text, post.Author, post.CoverURL, post.VideoURL, post.DurationS, post.CreatedAt,
			post.Likes, post.Comments, post.Shares, post.Collects, lastSeen, seenCount, itemID)
	}
	if err != nil {
		return MonitorItem{}, nil, fmt.Errorf("upsert monitor item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_snapshots(item_id,run_id,at,likes,comments,shares,collects) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(item_id,run_id) DO UPDATE SET at=excluded.at,likes=excluded.likes,comments=excluded.comments,shares=excluded.shares,collects=excluded.collects`,
		itemID, runID, atText, post.Likes, post.Comments, post.Shares, post.Collects); err != nil {
		return MonitorItem{}, nil, fmt.Errorf("upsert monitor snapshot: %w", err)
	}
	item, err := getMonitorItem(ctx, tx, `i.id=?`, itemID)
	if err != nil {
		return MonitorItem{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return MonitorItem{}, nil, fmt.Errorf("commit monitor item upsert: %w", err)
	}
	if isNew {
		return item, nil, nil
	}
	return item, &previous, nil
}

func (r *MonitorRepository) GetItem(ctx context.Context, id string) (MonitorItem, error) {
	return getMonitorItem(ctx, r.db, `i.id=?`, id)
}

func (r *MonitorRepository) Feed(ctx context.Context, taskID, filter string, limit int) (MonitorFeed, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	scope, scopeArgs := `i.dismissed=0`, []any{}
	if taskID != "" {
		scope += ` AND i.task_id=?`
		scopeArgs = append(scopeArgs, taskID)
	}
	where := scope
	switch filter {
	case "surge":
		where += ` AND i.surge=1`
	case "high":
		where += ` AND i.score>=t.score_threshold`
	case "unread":
		where += ` AND i.read=0`
	}
	args := append(append([]any{}, scopeArgs...), limit)
	rows, err := r.db.QueryContext(ctx, `SELECT `+monitorItemColumns+` FROM monitor_items i JOIN monitor_tasks t ON t.id=i.task_id
		WHERE `+where+` ORDER BY i.surge DESC,i.surge_at DESC,i.score DESC,i.last_seen_at DESC,i.id LIMIT ?`, args...)
	if err != nil {
		return MonitorFeed{}, fmt.Errorf("list monitor feed: %w", err)
	}
	items := []MonitorItem{}
	for rows.Next() {
		item, err := scanMonitorItem(rows)
		if err != nil {
			rows.Close()
			return MonitorFeed{}, fmt.Errorf("scan monitor feed: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return MonitorFeed{}, err
	}
	if err := rows.Err(); err != nil {
		return MonitorFeed{}, err
	}
	for index := range items {
		history, err := monitorHistory(ctx, r.db, items[index].ID)
		if err != nil {
			return MonitorFeed{}, err
		}
		items[index].History = history
	}
	var counts MonitorFeedCounts
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN i.surge=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN i.score>=t.score_threshold THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN i.read=0 THEN 1 ELSE 0 END),0)
		FROM monitor_items i JOIN monitor_tasks t ON t.id=i.task_id WHERE `+scope, scopeArgs...).Scan(&counts.All, &counts.Surge, &counts.High, &counts.Unread); err != nil {
		return MonitorFeed{}, fmt.Errorf("count monitor feed: %w", err)
	}
	return MonitorFeed{Items: items, Counts: counts}, nil
}

func (r *MonitorRepository) PatchItem(ctx context.Context, id string, read, dismissed *bool) (MonitorItem, error) {
	sets, args := []string{}, []any{}
	if read != nil {
		sets = append(sets, `read=?`)
		args = append(args, *read)
	}
	if dismissed != nil {
		sets = append(sets, `dismissed=?`)
		args = append(args, *dismissed)
	}
	if len(sets) > 0 {
		args = append(args, id)
		result, err := r.db.ExecContext(ctx, `UPDATE monitor_items SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
		if err != nil {
			return MonitorItem{}, fmt.Errorf("patch monitor item: %w", err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return MonitorItem{}, ErrMonitorItemNotFound
		}
	}
	return r.GetItem(ctx, id)
}

func (r *MonitorRepository) ReadAll(ctx context.Context, taskID string) error {
	query, args := `UPDATE monitor_items SET read=1`, []any{}
	if taskID != "" {
		query += ` WHERE task_id=?`
		args = append(args, taskID)
	}
	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("read all monitor items: %w", err)
	}
	return nil
}

func (r *MonitorRepository) SetReference(ctx context.Context, id, referenceID string) error {
	return r.updateItem(ctx, id, `reference_id=?`, referenceID)
}

func (r *MonitorRepository) SetScore(ctx context.Context, id, status string, score int, reason string, tags []string, scoreErr string) error {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("encode monitor score tags: %w", err)
	}
	return r.updateItem(ctx, id, `score_status=?,score=?,score_reason=?,score_tags=?,score_error=?`, status, score, reason, string(raw), scoreErr)
}

func (r *MonitorRepository) SetSurge(ctx context.Context, id string, surge bool, reason, surgeAt string, likeDelta, commentDelta int64, rate float64) error {
	return r.updateItem(ctx, id, `surge=?,surge_reason=?,read=CASE WHEN ?=1 AND ?<>surge_at THEN 0 ELSE read END,surge_at=?,like_delta=?,comment_delta=?,like_rate_per_hour=?`,
		surge, reason, surge, surgeAt, surgeAt, likeDelta, commentDelta, rate)
}

func (r *MonitorRepository) updateItem(ctx context.Context, id, set string, args ...any) error {
	args = append(args, id)
	result, err := r.db.ExecContext(ctx, `UPDATE monitor_items SET `+set+` WHERE id=?`, args...)
	if err != nil {
		return fmt.Errorf("update monitor item: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrMonitorItemNotFound
	}
	return nil
}

func (r *MonitorRepository) Prune(ctx context.Context, taskID string, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin prune monitor data: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_snapshots WHERE item_id IN (SELECT id FROM monitor_items WHERE task_id=?) AND julianday(at)<julianday(?)`, taskID, formatMonitorTime(now.Add(-30*24*time.Hour))); err != nil {
		return fmt.Errorf("prune monitor snapshots: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_runs WHERE task_id=? AND id IN (
		SELECT id FROM monitor_runs WHERE task_id=? ORDER BY started_at DESC,rowid DESC LIMIT -1 OFFSET 200)`, taskID, taskID); err != nil {
		return fmt.Errorf("prune monitor runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit prune monitor data: %w", err)
	}
	return nil
}

func formatMonitorTime(value time.Time) string { return value.UTC().Format(time.RFC3339) }

func formatMonitorItemTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
