package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrVideoQCNotFound = errors.New("视频审核记录不存在")

type VideoQCCheck struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	SourceID   string    `json:"source_id"`
	Title      string    `json:"title"`
	Product    string    `json:"product"`
	Model      string    `json:"model"`
	Status     string    `json:"status"`
	DurationS  float64   `json:"duration_s"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	FrameCount int       `json:"frame_count"`
	Verdict    string    `json:"verdict"`
	Score      int       `json:"score"`
	ReportJSON string    `json:"-"`
	Error      string    `json:"error"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type VideoQCRepository struct{ db *sql.DB }

func NewVideoQCRepository(db *sql.DB) *VideoQCRepository { return &VideoQCRepository{db: db} }

const qcColumns = `id,source,source_id,title,product,model,status,duration_s,width,height,frame_count,verdict,score,report_json,error,created_at,updated_at`
const qcTimeFormat = "2006-01-02T15:04:05.000000000Z"

func scanQC(row interface{ Scan(...any) error }) (a VideoQCCheck, err error) {
	var created, updated string
	err = row.Scan(&a.ID, &a.Source, &a.SourceID, &a.Title, &a.Product, &a.Model, &a.Status, &a.DurationS, &a.Width, &a.Height, &a.FrameCount, &a.Verdict, &a.Score, &a.ReportJSON, &a.Error, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrVideoQCNotFound
	}
	if err != nil {
		return
	}
	if a.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return
	}
	a.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return
}

func (r *VideoQCRepository) CreateQueued(ctx context.Context, a VideoQCCheck) (VideoQCCheck, error) {
	now := time.Now().UTC()
	a.Status, a.CreatedAt, a.UpdatedAt = "queued", now, now
	a.DurationS, a.Width, a.Height, a.FrameCount, a.Score = 0, 0, 0, 0, 0
	a.Verdict, a.ReportJSON, a.Error = "", "", ""
	_, err := r.db.ExecContext(ctx, `INSERT INTO video_qc_checks (`+qcColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ID, a.Source, a.SourceID, a.Title, a.Product, a.Model, a.Status, a.DurationS, a.Width, a.Height, a.FrameCount, a.Verdict, a.Score, a.ReportJSON, a.Error, now.Format(qcTimeFormat), now.Format(qcTimeFormat))
	return a, err
}

func (r *VideoQCRepository) Get(ctx context.Context, id string) (VideoQCCheck, error) {
	return scanQC(r.db.QueryRowContext(ctx, `SELECT `+qcColumns+` FROM video_qc_checks WHERE id=?`, id))
}

func (r *VideoQCRepository) List(ctx context.Context, limit int) ([]VideoQCCheck, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+qcColumns+` FROM video_qc_checks ORDER BY created_at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []VideoQCCheck{}
	for rows.Next() {
		a, err := scanQC(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (r *VideoQCRepository) update(ctx context.Context, id, set, where string, args ...any) error {
	args = append(args, time.Now().UTC().Format(qcTimeFormat), id)
	result, err := r.db.ExecContext(ctx, `UPDATE video_qc_checks SET `+set+`,updated_at=? WHERE id=? AND `+where, args...)
	return qcAffected(result, err)
}

func qcAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrVideoQCNotFound
	}
	return nil
}

func (r *VideoQCRepository) MarkRunning(ctx context.Context, id string) error {
	return r.update(ctx, id, `status='running'`, `status='queued'`)
}

func (r *VideoQCRepository) Complete(ctx context.Context, id string, a VideoQCCheck) error {
	return r.update(ctx, id, `status='completed',duration_s=?,width=?,height=?,frame_count=?,verdict=?,score=?,report_json=?,error=''`, `status='running'`, a.DurationS, a.Width, a.Height, a.FrameCount, a.Verdict, a.Score, a.ReportJSON)
}

func (r *VideoQCRepository) Fail(ctx context.Context, id, message string) error {
	return r.update(ctx, id, `status='failed',error=?`, `status IN ('queued','running')`, message)
}

func (r *VideoQCRepository) FailInterrupted(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE video_qc_checks SET status='failed',error=?,updated_at=? WHERE status IN ('queued','running')`, "控制台重启中断", time.Now().UTC().Format(qcTimeFormat))
	return err
}

func (r *VideoQCRepository) Delete(ctx context.Context, id string) error {
	return qcAffected(r.db.ExecContext(ctx, `DELETE FROM video_qc_checks WHERE id=?`, id))
}
