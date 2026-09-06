package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrRemixLabActive = errors.New("experiment has an active run")

// AppendRemixRun atomically adds an isolated model slot and a new version.
// Taking the write lock before checking activity also serializes double submits.
func (r *RemixLabRepository) AppendRemixRun(ctx context.Context, slot RemixLabSlotRecord, run RemixLabRunRecord, workflow string, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE remix_lab_experiments SET updated_at=updated_at WHERE id=?`, run.ExperimentID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrRemixLabNotFound
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM remix_lab_runs WHERE experiment_id=? AND status IN ('queued','running')`, run.ExperimentID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrRemixLabActive
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remix_lab_slots(id,experiment_id,sort_index,label,base_url,model,reasoning_effort,run_count,api_key_ciphertext,pipeline,service_tier)
 VALUES(?,?,(SELECT COALESCE(MAX(sort_index),-1)+1 FROM remix_lab_slots WHERE experiment_id=?),?,?,?,?,?,?,?,?)`, slot.ID, run.ExperimentID, run.ExperimentID, slot.Label, slot.BaseURL, slot.Model, slot.ReasoningEffort, 1, slot.APIKeyCiphertext, slot.Pipeline, slot.ServiceTier)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remix_lab_runs(id,experiment_id,slot_id,run_index,status,prompt_id,prompt_stamp,prompt_name) VALUES(?,?,?,1,'queued',?,?,?)`, run.ID, run.ExperimentID, slot.ID, run.PromptID, run.PromptStamp, run.PromptName)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO remix_lab_run_configs(run_id,workflow_json) VALUES(?,?)`, run.ID, workflow); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE remix_lab_experiments SET status='running',updated_at=? WHERE id=?`, now, run.ExperimentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *RemixLabRepository) GetRunWorkflowJSON(ctx context.Context, runID string) (string, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT workflow_json FROM remix_lab_run_configs WHERE run_id=?`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return raw, err
}

// UpdateEffectiveWorkflow keeps retries of a new version isolated from siblings.
func (r *RemixLabRepository) UpdateEffectiveWorkflow(ctx context.Context, runID, expID, raw string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE remix_lab_run_configs SET workflow_json=? WHERE run_id=?`, raw, runID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		return nil
	}
	return r.UpdateExperimentWorkflowJSON(ctx, expID, raw)
}
