package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrReferenceAnalysisNotFound = errors.New("reference analysis not found")

type ReferenceAnalysis struct {
	ID          string    `json:"id"`
	ReferenceID string    `json:"reference_id"`
	Status      string    `json:"status"`
	Model       string    `json:"model"`
	Focus       string    `json:"focus"`
	FrameCount  int       `json:"frame_count"`
	ReportJSON  string    `json:"-"`
	Error       string    `json:"error"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ReferenceAnalysisRepository struct{ db *sql.DB }

func NewReferenceAnalysisRepository(db *sql.DB) *ReferenceAnalysisRepository {
	return &ReferenceAnalysisRepository{db: db}
}

const referenceAnalysisColumns = `id, reference_id, status, model, focus, frame_count, report_json, error, created_at, updated_at`

func scanReferenceAnalysis(row interface{ Scan(...any) error }) (ReferenceAnalysis, error) {
	var a ReferenceAnalysis
	err := row.Scan(&a.ID, &a.ReferenceID, &a.Status, &a.Model, &a.Focus, &a.FrameCount, &a.ReportJSON, &a.Error, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReferenceAnalysis{}, ErrReferenceAnalysisNotFound
	}
	return a, err
}

func (r *ReferenceAnalysisRepository) Get(ctx context.Context, id string) (ReferenceAnalysis, error) {
	return scanReferenceAnalysis(r.db.QueryRowContext(ctx, `SELECT `+referenceAnalysisColumns+` FROM reference_analyses WHERE id=?`, id))
}

func (r *ReferenceAnalysisRepository) Active(ctx context.Context, referenceID string) (ReferenceAnalysis, error) {
	return scanReferenceAnalysis(r.db.QueryRowContext(ctx, `SELECT `+referenceAnalysisColumns+` FROM reference_analyses WHERE reference_id=? AND status IN ('queued','running')`, referenceID))
}

// CreateQueued 在同一写事务内去重，覆盖 queued 窗口及多个并发调用者。
func (r *ReferenceAnalysisRepository) CreateQueued(ctx context.Context, a ReferenceAnalysis) (result ReferenceAnalysis, created bool, returnErr error) {
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
			_, err := conn.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, err)
		}
	}()
	existing, err := scanReferenceAnalysis(conn.QueryRowContext(ctx, `SELECT `+referenceAnalysisColumns+` FROM reference_analyses WHERE reference_id=? AND status IN ('queued','running')`, a.ReferenceID))
	if err != nil && !errors.Is(err, ErrReferenceAnalysisNotFound) {
		return result, false, err
	}
	if err == nil {
		result = existing
	} else {
		now := time.Now().UTC()
		if a.CreatedAt.IsZero() {
			a.CreatedAt = now
		}
		a.Status, a.UpdatedAt = "queued", now
		a.FrameCount, a.ReportJSON, a.Error = 0, "", ""
		_, err = conn.ExecContext(ctx, `INSERT INTO reference_analyses(`+referenceAnalysisColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?)`, a.ID, a.ReferenceID, a.Status, a.Model, a.Focus, a.FrameCount, a.ReportJSON, a.Error, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return result, false, fmt.Errorf("create reference analysis: %w", err)
		}
		result, created = a, true
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return ReferenceAnalysis{}, false, err
	}
	committed = true
	return result, created, nil
}

func (r *ReferenceAnalysisRepository) ListByReference(ctx context.Context, referenceID string) ([]ReferenceAnalysis, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+referenceAnalysisColumns+` FROM reference_analyses WHERE reference_id=? ORDER BY created_at DESC, id DESC LIMIT 20`, referenceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReferenceAnalysis{}
	for rows.Next() {
		a, err := scanReferenceAnalysis(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ReferenceAnalysisRepository) MarkRunning(ctx context.Context, id string) error {
	return r.update(ctx, id, `status='running'`, `status='queued'`)
}

func (r *ReferenceAnalysisRepository) SetFrames(ctx context.Context, id string, count int) error {
	return r.update(ctx, id, `frame_count=?`, `status='running'`, count)
}

func (r *ReferenceAnalysisRepository) Complete(ctx context.Context, id, reportJSON string) error {
	return r.update(ctx, id, `status='completed', report_json=?, error=''`, `status='running'`, reportJSON)
}

func (r *ReferenceAnalysisRepository) Fail(ctx context.Context, id, message string) error {
	return r.update(ctx, id, `status='failed', error=?`, `status IN ('queued','running')`, message)
}

func (r *ReferenceAnalysisRepository) FailInterrupted(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE reference_analyses SET status='failed', error=?, updated_at=? WHERE status IN ('queued','running')`, "控制台重启，分析中断，请重新分析", time.Now().UTC())
	return err
}

func (r *ReferenceAnalysisRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM reference_analyses WHERE id=?`, id)
	return analysisRowsAffected(result, err)
}

func (r *ReferenceAnalysisRepository) update(ctx context.Context, id, set, condition string, args ...any) error {
	args = append(args, time.Now().UTC(), id)
	result, err := r.db.ExecContext(ctx, `UPDATE reference_analyses SET `+set+`, updated_at=? WHERE id=? AND `+condition, args...)
	return analysisRowsAffected(result, err)
}

func analysisRowsAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrReferenceAnalysisNotFound
	}
	return nil
}
