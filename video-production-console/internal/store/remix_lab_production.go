package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RemixLabProductionRecord 是一次 run 的生产段状态：确认闸门之后由驱动器
// 按步推进（project→spoken→captions→narration→montage→done），任务 ID 逐步
// 回填，重启后靠 status='running' 的行恢复续跑。
type RemixLabProductionRecord struct {
	RunID        string
	ExperimentID string
	AccountID    string
	Auto         bool
	// Status: waiting_confirm / running / completed / failed
	Status string
	// Step: confirm / project / spoken / captions / narration / montage / done
	Step          string
	ProjectID     string
	SpokenTaskID  string
	CaptionTaskID string
	MontageTaskID string
	Error         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UpsertProduction 建立或整行覆盖一条生产状态。
func (r *RemixLabRepository) UpsertProduction(ctx context.Context, rec RemixLabProductionRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO remix_lab_productions(
			run_id, experiment_id, account_id, auto, status, step, project_id,
			spoken_task_id, caption_task_id, montage_task_id, error, created_at, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET
			account_id=excluded.account_id, auto=excluded.auto, status=excluded.status,
			step=excluded.step, project_id=excluded.project_id,
			spoken_task_id=excluded.spoken_task_id, caption_task_id=excluded.caption_task_id,
			montage_task_id=excluded.montage_task_id, error=excluded.error, updated_at=excluded.updated_at`,
		rec.RunID, rec.ExperimentID, rec.AccountID, rec.Auto, rec.Status, rec.Step, rec.ProjectID,
		rec.SpokenTaskID, rec.CaptionTaskID, rec.MontageTaskID, rec.Error, rec.CreatedAt, rec.UpdatedAt,
	)
	return err
}

func (r *RemixLabRepository) GetProduction(ctx context.Context, runID string) (RemixLabProductionRecord, error) {
	rec, err := scanProduction(r.db.QueryRowContext(ctx, `
		SELECT run_id, experiment_id, account_id, auto, status, step, project_id,
			spoken_task_id, caption_task_id, montage_task_id, error, created_at, updated_at
		FROM remix_lab_productions WHERE run_id=?`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabProductionRecord{}, ErrRemixLabNotFound
	}
	return rec, err
}

// GetProductionByProjectID 用混剪项目反查最近一条生产记录（历史项目打开工作流）。
func (r *RemixLabRepository) GetProductionByProjectID(ctx context.Context, projectID string) (RemixLabProductionRecord, error) {
	rec, err := scanProduction(r.db.QueryRowContext(ctx, `
		SELECT run_id, experiment_id, account_id, auto, status, step, project_id,
			spoken_task_id, caption_task_id, montage_task_id, error, created_at, updated_at
		FROM remix_lab_productions
		WHERE project_id=? AND project_id!=''
		ORDER BY updated_at DESC, created_at DESC, run_id DESC
		LIMIT 1`, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabProductionRecord{}, ErrRemixLabNotFound
	}
	return rec, err
}

func (r *RemixLabRepository) ListProductionsByExperiment(ctx context.Context, experimentID string) ([]RemixLabProductionRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, experiment_id, account_id, auto, status, step, project_id,
			spoken_task_id, caption_task_id, montage_task_id, error, created_at, updated_at
		FROM remix_lab_productions WHERE experiment_id=?`, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemixLabProductionRecord, 0)
	for rows.Next() {
		rec, err := scanProduction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ListRunningProductions 返回重启后需要续跑的生产（驱动器 ResumeAll 用）。
func (r *RemixLabRepository) ListRunningProductions(ctx context.Context) ([]RemixLabProductionRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, experiment_id, account_id, auto, status, step, project_id,
			spoken_task_id, caption_task_id, montage_task_id, error, created_at, updated_at
		FROM remix_lab_productions WHERE status='running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemixLabProductionRecord, 0)
	for rows.Next() {
		rec, err := scanProduction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *RemixLabRepository) UpdateProduction(ctx context.Context, rec RemixLabProductionRecord) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE remix_lab_productions SET
			account_id=?, auto=?, status=?, step=?, project_id=?,
			spoken_task_id=?, caption_task_id=?, montage_task_id=?, error=?, updated_at=?
		WHERE run_id=?`,
		rec.AccountID, rec.Auto, rec.Status, rec.Step, rec.ProjectID,
		rec.SpokenTaskID, rec.CaptionTaskID, rec.MontageTaskID, rec.Error, rec.UpdatedAt, rec.RunID,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRemixLabNotFound
	}
	return nil
}

type productionScanner interface {
	Scan(dest ...any) error
}

func scanProduction(scanner productionScanner) (RemixLabProductionRecord, error) {
	var rec RemixLabProductionRecord
	err := scanner.Scan(
		&rec.RunID, &rec.ExperimentID, &rec.AccountID, &rec.Auto, &rec.Status, &rec.Step, &rec.ProjectID,
		&rec.SpokenTaskID, &rec.CaptionTaskID, &rec.MontageTaskID, &rec.Error, &rec.CreatedAt, &rec.UpdatedAt,
	)
	return rec, err
}
