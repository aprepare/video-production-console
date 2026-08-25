package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const RemixLabPresetSettingsKey = "remix_lab_slot_presets"

var ErrRemixLabNotFound = errors.New("remix lab record not found")

type RemixLabExperimentRecord struct {
	ID, Title, SourceText, PromptStamp, Status string
	CreatedAt, UpdatedAt                       time.Time
}

type RemixLabSlotRecord struct {
	ID, ExperimentID, Label, BaseURL, Model, ReasoningEffort, APIKeyCiphertext string
	SortIndex, RunCount                                                         int
}

type RemixLabRunRecord struct {
	ID, ExperimentID, SlotID, Status, ContinuousScript, TitlesJSON string
	ErrorMessage, Comment, OutputDir, AdoptedProjectID             string
	RunIndex                                                       int
	StartedAt, FinishedAt                                          *time.Time
}

type RemixLabRepository struct {
	db *sql.DB
}

func NewRemixLabRepository(db *sql.DB) *RemixLabRepository {
	return &RemixLabRepository{db: db}
}

func (r *RemixLabRepository) CreateExperiment(ctx context.Context, exp RemixLabExperimentRecord, slots []RemixLabSlotRecord, runs []RemixLabRunRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO remix_lab_experiments(id, title, source_text, prompt_stamp, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)`,
		exp.ID, exp.Title, exp.SourceText, exp.PromptStamp, exp.Status, exp.CreatedAt, exp.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert remix lab experiment: %w", err)
	}
	for _, slot := range slots {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO remix_lab_slots(id, experiment_id, sort_index, label, base_url, model, reasoning_effort, run_count, api_key_ciphertext)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			slot.ID, slot.ExperimentID, slot.SortIndex, slot.Label, slot.BaseURL, slot.Model, slot.ReasoningEffort, slot.RunCount, slot.APIKeyCiphertext,
		); err != nil {
			return fmt.Errorf("insert remix lab slot: %w", err)
		}
	}
	for _, run := range runs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO remix_lab_runs(
				id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
				error_message, comment, output_dir, adopted_project_id, started_at, finished_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID, run.ExperimentID, run.SlotID, run.RunIndex, run.Status, run.ContinuousScript, run.TitlesJSON,
			run.ErrorMessage, run.Comment, run.OutputDir, run.AdoptedProjectID, run.StartedAt, run.FinishedAt,
		); err != nil {
			return fmt.Errorf("insert remix lab run: %w", err)
		}
	}
	return tx.Commit()
}

func (r *RemixLabRepository) ListExperiments(ctx context.Context) ([]RemixLabExperimentRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, source_text, prompt_stamp, status, created_at, updated_at
		FROM remix_lab_experiments
		ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RemixLabExperimentRecord, 0)
	for rows.Next() {
		var exp RemixLabExperimentRecord
		if err := rows.Scan(&exp.ID, &exp.Title, &exp.SourceText, &exp.PromptStamp, &exp.Status, &exp.CreatedAt, &exp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, exp)
	}
	return out, rows.Err()
}

func (r *RemixLabRepository) GetExperiment(ctx context.Context, id string) (RemixLabExperimentRecord, []RemixLabSlotRecord, []RemixLabRunRecord, error) {
	var exp RemixLabExperimentRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title, source_text, prompt_stamp, status, created_at, updated_at
		FROM remix_lab_experiments WHERE id=?`, id,
	).Scan(&exp.ID, &exp.Title, &exp.SourceText, &exp.PromptStamp, &exp.Status, &exp.CreatedAt, &exp.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabExperimentRecord{}, nil, nil, ErrRemixLabNotFound
	}
	if err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}

	slotRows, err := r.db.QueryContext(ctx, `
		SELECT id, experiment_id, sort_index, label, base_url, model, reasoning_effort, run_count, api_key_ciphertext
		FROM remix_lab_slots WHERE experiment_id=? ORDER BY sort_index, id`, id)
	if err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}
	defer slotRows.Close()
	slots := make([]RemixLabSlotRecord, 0)
	for slotRows.Next() {
		var slot RemixLabSlotRecord
		if err := slotRows.Scan(&slot.ID, &slot.ExperimentID, &slot.SortIndex, &slot.Label, &slot.BaseURL, &slot.Model, &slot.ReasoningEffort, &slot.RunCount, &slot.APIKeyCiphertext); err != nil {
			return RemixLabExperimentRecord{}, nil, nil, err
		}
		slots = append(slots, slot)
	}
	if err := slotRows.Err(); err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}

	runRows, err := r.db.QueryContext(ctx, `
		SELECT id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
			error_message, comment, output_dir, adopted_project_id, started_at, finished_at
		FROM remix_lab_runs WHERE experiment_id=? ORDER BY slot_id, run_index, id`, id)
	if err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}
	defer runRows.Close()
	runs := make([]RemixLabRunRecord, 0)
	for runRows.Next() {
		run, err := scanRemixLabRun(runRows)
		if err != nil {
			return RemixLabExperimentRecord{}, nil, nil, err
		}
		runs = append(runs, run)
	}
	if err := runRows.Err(); err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}
	return exp, slots, runs, nil
}

func (r *RemixLabRepository) GetRun(ctx context.Context, id string) (RemixLabRunRecord, error) {
	run, err := scanRemixLabRun(r.db.QueryRowContext(ctx, `
		SELECT id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
			error_message, comment, output_dir, adopted_project_id, started_at, finished_at
		FROM remix_lab_runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabRunRecord{}, ErrRemixLabNotFound
	}
	return run, err
}

func (r *RemixLabRepository) UpdateRun(ctx context.Context, run RemixLabRunRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE remix_lab_runs SET
			status=?, continuous_script=?, titles_json=?, error_message=?, comment=?,
			output_dir=?, adopted_project_id=?, started_at=?, finished_at=?
		WHERE id=?`,
		run.Status, run.ContinuousScript, run.TitlesJSON, run.ErrorMessage, run.Comment,
		run.OutputDir, run.AdoptedProjectID, run.StartedAt, run.FinishedAt, run.ID,
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
	var experimentID string
	if err := tx.QueryRowContext(ctx, `SELECT experiment_id FROM remix_lab_runs WHERE id=?`, run.ID).Scan(&experimentID); err != nil {
		return err
	}
	status, err := recomputeRemixLabExperimentStatusTx(ctx, tx, experimentID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE remix_lab_experiments SET status=?, updated_at=? WHERE id=?`, status, time.Now().UTC(), experimentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *RemixLabRepository) UpdateRunComment(ctx context.Context, id, comment string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE remix_lab_runs SET comment=? WHERE id=?`, comment, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var found string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM remix_lab_runs WHERE id=?`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRemixLabNotFound
		}
		return err
	}
	return nil
}

func (r *RemixLabRepository) UpdateRunAdoptedProject(ctx context.Context, id, projectID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE remix_lab_runs SET adopted_project_id=? WHERE id=?`, projectID, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var found string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM remix_lab_runs WHERE id=?`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRemixLabNotFound
		}
		return err
	}
	return nil
}

func (r *RemixLabRepository) UpdateExperimentStatus(ctx context.Context, id, status string, updatedAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE remix_lab_experiments SET status=?, updated_at=? WHERE id=?`, status, updatedAt, id)
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

func (r *RemixLabRepository) FailNonTerminal(ctx context.Context, message string, now time.Time) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	expRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT experiment_id FROM remix_lab_runs WHERE status IN ('queued', 'running')`)
	if err != nil {
		return 0, err
	}
	experimentIDs := make([]string, 0)
	for expRows.Next() {
		var id string
		if err := expRows.Scan(&id); err != nil {
			_ = expRows.Close()
			return 0, err
		}
		experimentIDs = append(experimentIDs, id)
	}
	if err := expRows.Err(); err != nil {
		_ = expRows.Close()
		return 0, err
	}
	if err := expRows.Close(); err != nil {
		return 0, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE remix_lab_runs
		SET status='failed', error_message=?, finished_at=?
		WHERE status IN ('queued', 'running')`, message, now)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	for _, experimentID := range experimentIDs {
		status, err := recomputeRemixLabExperimentStatusTx(ctx, tx, experimentID)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE remix_lab_experiments SET status=?, updated_at=? WHERE id=?`, status, now, experimentID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (r *RemixLabRepository) GetPresetJSON(ctx context.Context) (string, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, RemixLabPresetSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return raw, err
}

func (r *RemixLabRepository) PutPresetJSON(ctx context.Context, raw string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, RemixLabPresetSettingsKey, raw)
	return err
}

type remixLabRunScanner interface {
	Scan(dest ...any) error
}

func scanRemixLabRun(scanner remixLabRunScanner) (RemixLabRunRecord, error) {
	var run RemixLabRunRecord
	var startedAt, finishedAt sql.NullTime
	err := scanner.Scan(
		&run.ID, &run.ExperimentID, &run.SlotID, &run.RunIndex, &run.Status, &run.ContinuousScript, &run.TitlesJSON,
		&run.ErrorMessage, &run.Comment, &run.OutputDir, &run.AdoptedProjectID, &startedAt, &finishedAt,
	)
	if err != nil {
		return RemixLabRunRecord{}, err
	}
	run.StartedAt = nullTimePointer(startedAt)
	run.FinishedAt = nullTimePointer(finishedAt)
	return run, nil
}

func recomputeRemixLabExperimentStatusTx(ctx context.Context, tx *sql.Tx, experimentID string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT status FROM remix_lab_runs WHERE experiment_id=?`, experimentID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	statuses := make([]string, 0)
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return "", err
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return remixLabExperimentStatusFromRuns(statuses), nil
}

func remixLabExperimentStatusFromRuns(statuses []string) string {
	if len(statuses) == 0 {
		return "failed"
	}
	allCompleted := true
	allFailed := true
	for _, status := range statuses {
		switch status {
		case "queued", "running":
			return "running"
		case "completed":
			allFailed = false
		case "failed":
			allCompleted = false
		default:
			allCompleted = false
			allFailed = false
		}
	}
	if allCompleted {
		return "completed"
	}
	if allFailed {
		return "failed"
	}
	return "partial"
}
