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
	// WorkflowJSON 非空 = 工作流实验：执行按该快照的节点图走。
	WorkflowJSON string
	// ProduceAccountID/ProduceAuto：开跑时选的生产账号与全自动开关。
	ProduceAccountID     string
	ProduceAuto          bool
	CreatedAt, UpdatedAt time.Time
}

type RemixLabSlotRecord struct {
	ServiceTier                                                                string
	ID, ExperimentID, Label, BaseURL, Model, ReasoningEffort, APIKeyCiphertext string
	// Pipeline 为空走单模型写手；"multi_agent" 先跑三路情报agent再写。
	Pipeline            string
	SortIndex, RunCount int
}

type RemixLabRunRecord struct {
	ID, ExperimentID, SlotID, Status, ContinuousScript, TitlesJSON string
	ErrorMessage, Comment, OutputDir, AdoptedProjectID             string
	PromptID, PromptStamp, PromptName                              string
	// PackageJSON 当前定稿的完整写手 JSON（含正文与发布字段，可被操作员编辑覆盖）；
	// DraftV1JSON 写手初稿留档；ReviewJSON 最近一轮审稿结论。
	PackageJSON, DraftV1JSON, ReviewJSON string
	RunIndex                             int
	StartedAt, FinishedAt                *time.Time
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
		INSERT INTO remix_lab_experiments(id, title, source_text, prompt_stamp, status, workflow_json, produce_account_id, produce_auto, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		exp.ID, exp.Title, exp.SourceText, exp.PromptStamp, exp.Status, exp.WorkflowJSON, exp.ProduceAccountID, exp.ProduceAuto, exp.CreatedAt, exp.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert remix lab experiment: %w", err)
	}
	for _, slot := range slots {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO remix_lab_slots(id, experiment_id, sort_index, label, base_url, model, reasoning_effort, run_count, api_key_ciphertext, pipeline, service_tier)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			slot.ID, slot.ExperimentID, slot.SortIndex, slot.Label, slot.BaseURL, slot.Model, slot.ReasoningEffort, slot.RunCount, slot.APIKeyCiphertext, slot.Pipeline, slot.ServiceTier,
		); err != nil {
			return fmt.Errorf("insert remix lab slot: %w", err)
		}
	}
	for _, run := range runs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO remix_lab_runs(
				id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
				error_message, comment, output_dir, adopted_project_id, started_at, finished_at,
				prompt_id, prompt_stamp, prompt_name, package_json, draft_v1_json, review_json
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID, run.ExperimentID, run.SlotID, run.RunIndex, run.Status, run.ContinuousScript, run.TitlesJSON,
			run.ErrorMessage, run.Comment, run.OutputDir, run.AdoptedProjectID, run.StartedAt, run.FinishedAt,
			run.PromptID, run.PromptStamp, run.PromptName, run.PackageJSON, run.DraftV1JSON, run.ReviewJSON,
		); err != nil {
			return fmt.Errorf("insert remix lab run: %w", err)
		}
	}
	return tx.Commit()
}

func (r *RemixLabRepository) ListExperiments(ctx context.Context) ([]RemixLabExperimentRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, source_text, prompt_stamp, status, workflow_json, produce_account_id, produce_auto, created_at, updated_at
		FROM remix_lab_experiments
		ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RemixLabExperimentRecord, 0)
	for rows.Next() {
		var exp RemixLabExperimentRecord
		if err := rows.Scan(&exp.ID, &exp.Title, &exp.SourceText, &exp.PromptStamp, &exp.Status, &exp.WorkflowJSON, &exp.ProduceAccountID, &exp.ProduceAuto, &exp.CreatedAt, &exp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, exp)
	}
	return out, rows.Err()
}

func (r *RemixLabRepository) GetExperiment(ctx context.Context, id string) (RemixLabExperimentRecord, []RemixLabSlotRecord, []RemixLabRunRecord, error) {
	var exp RemixLabExperimentRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title, source_text, prompt_stamp, status, workflow_json, produce_account_id, produce_auto, created_at, updated_at
		FROM remix_lab_experiments WHERE id=?`, id,
	).Scan(&exp.ID, &exp.Title, &exp.SourceText, &exp.PromptStamp, &exp.Status, &exp.WorkflowJSON, &exp.ProduceAccountID, &exp.ProduceAuto, &exp.CreatedAt, &exp.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabExperimentRecord{}, nil, nil, ErrRemixLabNotFound
	}
	if err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}

	slotRows, err := r.db.QueryContext(ctx, `
		SELECT id, experiment_id, sort_index, label, base_url, model, reasoning_effort, run_count, api_key_ciphertext, pipeline, service_tier
		FROM remix_lab_slots WHERE experiment_id=? ORDER BY sort_index, id`, id)
	if err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}
	defer slotRows.Close()
	slots := make([]RemixLabSlotRecord, 0)
	for slotRows.Next() {
		var slot RemixLabSlotRecord
		if err := slotRows.Scan(&slot.ID, &slot.ExperimentID, &slot.SortIndex, &slot.Label, &slot.BaseURL, &slot.Model, &slot.ReasoningEffort, &slot.RunCount, &slot.APIKeyCiphertext, &slot.Pipeline, &slot.ServiceTier); err != nil {
			return RemixLabExperimentRecord{}, nil, nil, err
		}
		slots = append(slots, slot)
	}
	if err := slotRows.Err(); err != nil {
		return RemixLabExperimentRecord{}, nil, nil, err
	}

	runRows, err := r.db.QueryContext(ctx, `
		SELECT id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
			error_message, comment, output_dir, adopted_project_id, started_at, finished_at,
			prompt_id, prompt_stamp, prompt_name, package_json, draft_v1_json, review_json
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

func (r *RemixLabRepository) GetLatestRunByAdoptedProject(ctx context.Context, projectID string) (RemixLabRunRecord, error) {
	run, err := scanRemixLabRun(r.db.QueryRowContext(ctx, `
		SELECT id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
			error_message, comment, output_dir, adopted_project_id, started_at, finished_at,
			prompt_id, prompt_stamp, prompt_name, package_json, draft_v1_json, review_json
		FROM remix_lab_runs
		WHERE adopted_project_id=? AND adopted_project_id!=''
		ORDER BY COALESCE(finished_at, started_at) DESC, run_index DESC, id DESC
		LIMIT 1`, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabRunRecord{}, ErrRemixLabNotFound
	}
	return run, err
}

func (r *RemixLabRepository) GetRun(ctx context.Context, id string) (RemixLabRunRecord, error) {
	run, err := scanRemixLabRun(r.db.QueryRowContext(ctx, `
		SELECT id, experiment_id, slot_id, run_index, status, continuous_script, titles_json,
			error_message, comment, output_dir, adopted_project_id, started_at, finished_at,
			prompt_id, prompt_stamp, prompt_name, package_json, draft_v1_json, review_json
		FROM remix_lab_runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RemixLabRunRecord{}, ErrRemixLabNotFound
	}
	return run, err
}

func (r *RemixLabRepository) UpdateRun(ctx context.Context, run RemixLabRunRecord) error {
	// Comments belong to UpdateRunComment. A runner holds an older snapshot and
	// must not overwrite operator annotations when persisting progress/results.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE remix_lab_runs SET
			status=?, continuous_script=?, titles_json=?, error_message=?,
			output_dir=?, adopted_project_id=?, started_at=?, finished_at=?,
			package_json=?, draft_v1_json=?, review_json=?
		WHERE id=?`,
		run.Status, run.ContinuousScript, run.TitlesJSON, run.ErrorMessage,
		run.OutputDir, run.AdoptedProjectID, run.StartedAt, run.FinishedAt,
		run.PackageJSON, run.DraftV1JSON, run.ReviewJSON, run.ID,
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

// RemixLabPublishMetrics 是一条已发布文案手填的成绩：播放、点赞、出单、备注。
type RemixLabPublishMetrics struct {
	RunID     string
	Views     int
	Likes     int
	Orders    int
	Notes     string
	UpdatedAt time.Time
}

// RemixLabPublishedRow 是文案库一行：成稿 + 所属实验/账号/项目 + 成绩。
type RemixLabPublishedRow struct {
	Run        RemixLabRunRecord
	Experiment RemixLabExperimentRecord
	SlotModel  string
	ProjectID  string
	AccountID  string
	ProducedAt time.Time
	Metrics    *RemixLabPublishMetrics
}

// ListPublishedRows 返回所有已经出过剪映草稿的成稿（生产段 completed），
// 最新在前，带手填成绩。文案库的"已发布"以此为候选集。
func (r *RemixLabRepository) ListPublishedRows(ctx context.Context) ([]RemixLabPublishedRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT run.id, run.experiment_id, run.slot_id, run.run_index, run.status, run.continuous_script, run.titles_json,
		       run.error_message, run.comment, run.output_dir, run.adopted_project_id, run.started_at, run.finished_at,
		       run.prompt_id, run.prompt_stamp, run.prompt_name, run.package_json, run.draft_v1_json, run.review_json,
		       exp.id, exp.title, exp.source_text, exp.prompt_stamp, exp.status, exp.workflow_json, exp.produce_account_id, exp.produce_auto, exp.created_at, exp.updated_at,
		       slot.model, prod.project_id, prod.account_id, prod.updated_at,
		       m.views, m.likes, m.orders, m.notes, m.updated_at
		FROM remix_lab_productions prod
		JOIN remix_lab_runs run ON run.id = prod.run_id
		JOIN remix_lab_experiments exp ON exp.id = run.experiment_id
		JOIN remix_lab_slots slot ON slot.id = run.slot_id
		LEFT JOIN remix_lab_publish_metrics m ON m.run_id = run.id
		WHERE prod.status = 'completed'
		ORDER BY prod.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemixLabPublishedRow, 0)
	for rows.Next() {
		var row RemixLabPublishedRow
		var startedAt, finishedAt sql.NullTime
		var mViews, mLikes, mOrders sql.NullInt64
		var mNotes sql.NullString
		var mUpdated sql.NullTime
		run := &row.Run
		if err := rows.Scan(
			&run.ID, &run.ExperimentID, &run.SlotID, &run.RunIndex, &run.Status, &run.ContinuousScript, &run.TitlesJSON,
			&run.ErrorMessage, &run.Comment, &run.OutputDir, &run.AdoptedProjectID, &startedAt, &finishedAt,
			&run.PromptID, &run.PromptStamp, &run.PromptName, &run.PackageJSON, &run.DraftV1JSON, &run.ReviewJSON,
			&row.Experiment.ID, &row.Experiment.Title, &row.Experiment.SourceText, &row.Experiment.PromptStamp, &row.Experiment.Status,
			&row.Experiment.WorkflowJSON, &row.Experiment.ProduceAccountID, &row.Experiment.ProduceAuto, &row.Experiment.CreatedAt, &row.Experiment.UpdatedAt,
			&row.SlotModel, &row.ProjectID, &row.AccountID, &row.ProducedAt,
			&mViews, &mLikes, &mOrders, &mNotes, &mUpdated,
		); err != nil {
			return nil, err
		}
		run.StartedAt = nullTimePointer(startedAt)
		run.FinishedAt = nullTimePointer(finishedAt)
		if mUpdated.Valid {
			row.Metrics = &RemixLabPublishMetrics{
				RunID: row.Run.ID, Views: int(mViews.Int64), Likes: int(mLikes.Int64), Orders: int(mOrders.Int64),
				Notes: mNotes.String, UpdatedAt: mUpdated.Time,
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// UpsertPublishMetrics 保存手填成绩；run 不存在时返回 ErrRemixLabNotFound。
func (r *RemixLabRepository) UpsertPublishMetrics(ctx context.Context, m RemixLabPublishMetrics) error {
	var found string
	if err := r.db.QueryRowContext(ctx, `SELECT id FROM remix_lab_runs WHERE id=?`, m.RunID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRemixLabNotFound
		}
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO remix_lab_publish_metrics(run_id, views, likes, orders, notes, updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET views=excluded.views, likes=excluded.likes, orders=excluded.orders, notes=excluded.notes, updated_at=excluded.updated_at`,
		m.RunID, m.Views, m.Likes, m.Orders, m.Notes, m.UpdatedAt)
	return err
}

// UpdateSlotModel 换掉槽位的模型：断点重试时原模型已不可用，改这里让本次
// 与后续所有重试、返工都用新模型（同实验的其余运行也跟着换）。
func (r *RemixLabRepository) UpdateSlotModel(ctx context.Context, id, model string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE remix_lab_slots SET model=? WHERE id=?`, model, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var found string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM remix_lab_slots WHERE id=?`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRemixLabNotFound
		}
		return err
	}
	return nil
}

// UpdateExperimentWorkflowJSON 更新实验的工作流快照（重试时替换失败节点的模型）。
func (r *RemixLabRepository) UpdateExperimentWorkflowJSON(ctx context.Context, id, workflowJSON string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE remix_lab_experiments SET workflow_json=? WHERE id=?`, workflowJSON, id)
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

// UpdateRunPackage 保存操作员在创作台编辑后的定稿：正文、标题镜像列和完整发布包。
func (r *RemixLabRepository) UpdateRunPackage(ctx context.Context, id, script, titlesJSON, packageJSON string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE remix_lab_runs SET continuous_script=?, titles_json=?, package_json=? WHERE id=?`,
		script, titlesJSON, packageJSON, id)
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

func (r *RemixLabRepository) DeleteExperiment(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var found string
	err = tx.QueryRowContext(ctx, `SELECT id FROM remix_lab_experiments WHERE id=?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRemixLabNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM remix_lab_runs WHERE experiment_id=?`, id); err != nil {
		return fmt.Errorf("delete remix lab runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM remix_lab_slots WHERE experiment_id=?`, id); err != nil {
		return fmt.Errorf("delete remix lab slots: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM remix_lab_experiments WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete remix lab experiment: %w", err)
	}
	return tx.Commit()
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
		&run.PromptID, &run.PromptStamp, &run.PromptName, &run.PackageJSON, &run.DraftV1JSON, &run.ReviewJSON,
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
