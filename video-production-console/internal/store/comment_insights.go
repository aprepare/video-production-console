package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

var ErrCommentInsightNotFound = errors.New("comment insight not found")

type CommentInsight struct {
	ID           string    `json:"id"`
	ReferenceID  string    `json:"reference_id"`
	Status       string    `json:"status"`
	Model        string    `json:"model"`
	CommentCount int       `json:"comment_count"`
	ReportJSON   string    `json:"-"`
	Error        string    `json:"error"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CommentInsightRepository struct{ db *sql.DB }

func NewCommentInsightRepository(db *sql.DB) *CommentInsightRepository {
	return &CommentInsightRepository{db: db}
}

const commentInsightColumns = `id, reference_id, status, model, comment_count, report_json, error, created_at, updated_at`
const commentInsightTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatCommentInsightTime(value time.Time) string {
	return value.UTC().Format(commentInsightTimeLayout)
}

func scanCommentInsight(row interface{ Scan(...any) error }) (CommentInsight, error) {
	var item CommentInsight
	var createdAt, updatedAt string
	err := row.Scan(&item.ID, &item.ReferenceID, &item.Status, &item.Model, &item.CommentCount, &item.ReportJSON, &item.Error, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CommentInsight{}, ErrCommentInsightNotFound
	}
	if err != nil {
		return CommentInsight{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err == nil {
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	}
	return item, err
}

func (r *CommentInsightRepository) Get(ctx context.Context, id string) (CommentInsight, error) {
	return scanCommentInsight(r.db.QueryRowContext(ctx, `SELECT `+commentInsightColumns+` FROM comment_insights WHERE id=?`, id))
}

func (r *CommentInsightRepository) Active(ctx context.Context, referenceID string) (CommentInsight, error) {
	return scanCommentInsight(r.db.QueryRowContext(ctx, `SELECT `+commentInsightColumns+` FROM comment_insights WHERE reference_id=? AND status IN ('queued','running')`, referenceID))
}

// CreateQueued serializes the active-record check and insert so concurrent
// callers receive one shared job instead of surfacing a uniqueness error.
func (r *CommentInsightRepository) CreateQueued(ctx context.Context, item CommentInsight) (result CommentInsight, created bool, returnErr error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return result, false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := conn.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	existing, err := scanCommentInsight(conn.QueryRowContext(ctx, `SELECT `+commentInsightColumns+` FROM comment_insights WHERE reference_id=? AND status IN ('queued','running')`, item.ReferenceID))
	if err != nil && !errors.Is(err, ErrCommentInsightNotFound) {
		return result, false, err
	}
	if err == nil {
		result = existing
	} else {
		now := time.Now().UTC()
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		item.Status, item.CommentCount, item.ReportJSON, item.Error, item.UpdatedAt = "queued", 0, "", "", now
		_, err = conn.ExecContext(ctx, `INSERT INTO comment_insights(`+commentInsightColumns+`) VALUES(?,?,?,?,?,?,?,?,?)`,
			item.ID, item.ReferenceID, item.Status, item.Model, item.CommentCount, item.ReportJSON, item.Error,
			formatCommentInsightTime(item.CreatedAt), formatCommentInsightTime(item.UpdatedAt))
		if err != nil {
			return result, false, fmt.Errorf("create comment insight: %w", err)
		}
		result, created = item, true
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return CommentInsight{}, false, err
	}
	committed = true
	return result, created, nil
}

func (r *CommentInsightRepository) ListByReference(ctx context.Context, referenceID string) ([]CommentInsight, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, reference_id, status, model, comment_count, '', error, created_at, updated_at FROM comment_insights WHERE reference_id=?`, referenceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentInsight{}
	for rows.Next() {
		item, err := scanCommentInsight(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *CommentInsightRepository) MarkRunning(ctx context.Context, id string, count int) error {
	return r.update(ctx, id, `status='running', comment_count=?`, `status='queued'`, count)
}

func (r *CommentInsightRepository) Complete(ctx context.Context, id, reportJSON string) error {
	return r.update(ctx, id, `status='completed', report_json=?, error=''`, `status='running'`, reportJSON)
}

func (r *CommentInsightRepository) Fail(ctx context.Context, id, message string) error {
	return r.update(ctx, id, `status='failed', error=?`, `status IN ('queued','running')`, message)
}

func (r *CommentInsightRepository) FailInterrupted(ctx context.Context) error {
	now := formatCommentInsightTime(time.Now())
	_, err := r.db.ExecContext(ctx, `UPDATE comment_insights SET status='failed', error=?, updated_at=? WHERE status IN ('queued','running')`, "控制台重启，评论洞察中断，请重新分析", now)
	return err
}

func (r *CommentInsightRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM comment_insights WHERE id=?`, id)
	return commentInsightRowsAffected(result, err)
}

func (r *CommentInsightRepository) update(ctx context.Context, id, set, condition string, args ...any) error {
	args = append(args, formatCommentInsightTime(time.Now()), id)
	result, err := r.db.ExecContext(ctx, `UPDATE comment_insights SET `+set+`, updated_at=? WHERE id=? AND `+condition, args...)
	return commentInsightRowsAffected(result, err)
}

func commentInsightRowsAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCommentInsightNotFound
	}
	return nil
}
