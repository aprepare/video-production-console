package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAIGCJobNotFound = errors.New("aigc job not found")
	ErrAIGCJobConflict = errors.New("aigc job conflict")
)

// AIGCJob records one persisted AIGC pipeline execution.
type AIGCJob struct {
	ID                string    `json:"id"`
	MerchantID        string    `json:"merchant_id"`
	OrderID           string    `json:"order_id"`
	Kind              string    `json:"kind"`
	Status            string    `json:"status"`
	InputMaterialIDs  []string  `json:"input_material_ids"`
	OutputMaterialIDs []string  `json:"output_material_ids"`
	ParamsJSON        string    `json:"params_json"`
	Error             string    `json:"error"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// AIGCJobRepository persists AIGC jobs.
type AIGCJobRepository struct{ db *sql.DB }

// NewAIGCJobRepository creates an AIGC job repository backed by db.
func NewAIGCJobRepository(db *sql.DB) *AIGCJobRepository { return &AIGCJobRepository{db: db} }

const aigcJobColumns = `id,merchant_id,order_id,kind,status,input_material_ids,output_material_ids,params_json,error,created_at,updated_at`

// CreateJob inserts an AIGC job and returns its stored representation.
func (r *AIGCJobRepository) CreateJob(ctx context.Context, value AIGCJob) (AIGCJob, error) {
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	if value.Status == "" {
		value.Status = "queued"
	}
	if value.ParamsJSON == "" {
		value.ParamsJSON = "{}"
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	} else {
		value.CreatedAt = value.CreatedAt.UTC()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	} else {
		value.UpdatedAt = value.UpdatedAt.UTC()
	}
	inputs, err := json.Marshal(nonNilStrings(value.InputMaterialIDs))
	if err != nil {
		return AIGCJob{}, fmt.Errorf("encode AIGC job inputs: %w", err)
	}
	outputs, err := json.Marshal(nonNilStrings(value.OutputMaterialIDs))
	if err != nil {
		return AIGCJob{}, fmt.Errorf("encode AIGC job outputs: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO aigc_jobs(`+aigcJobColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.MerchantID, value.OrderID, value.Kind, value.Status, string(inputs), string(outputs), value.ParamsJSON, value.Error, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return AIGCJob{}, fmt.Errorf("create AIGC job: %w", err)
	}
	return r.GetJob(ctx, value.ID)
}

// GetJob returns an AIGC job by ID.
func (r *AIGCJobRepository) GetJob(ctx context.Context, id string) (AIGCJob, error) {
	value, err := scanAIGCJob(r.db.QueryRowContext(ctx, `SELECT `+aigcJobColumns+` FROM aigc_jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AIGCJob{}, ErrAIGCJobNotFound
	}
	if err != nil {
		return AIGCJob{}, fmt.Errorf("get AIGC job: %w", err)
	}
	return value, nil
}

// ListJobs returns a merchant's AIGC jobs newest first.
func (r *AIGCJobRepository) ListJobs(ctx context.Context, merchantID string) ([]AIGCJob, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+aigcJobColumns+` FROM aigc_jobs WHERE merchant_id=? ORDER BY created_at DESC,id`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("list AIGC jobs: %w", err)
	}
	defer rows.Close()
	return scanAIGCJobs(rows)
}

// UpdateJobStatus changes an AIGC job's status and error message.
func (r *AIGCJobRepository) UpdateJobStatus(ctx context.Context, id, status, errMsg string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE aigc_jobs SET status=?,error=?,updated_at=? WHERE id=?`, status, errMsg, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update AIGC job status: %w", err)
	}
	return requireAIGCJobAffected(result)
}

// SetJobOutputs replaces an AIGC job's output material IDs.
func (r *AIGCJobRepository) SetJobOutputs(ctx context.Context, id string, ids []string) error {
	encoded, err := json.Marshal(nonNilStrings(ids))
	if err != nil {
		return fmt.Errorf("encode AIGC job outputs: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE aigc_jobs SET output_material_ids=?,updated_at=? WHERE id=?`, string(encoded), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set AIGC job outputs: %w", err)
	}
	return requireAIGCJobAffected(result)
}

// Running returns queued and running jobs that need restart reconciliation.
func (r *AIGCJobRepository) Running(ctx context.Context) ([]AIGCJob, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+aigcJobColumns+` FROM aigc_jobs WHERE status IN ('queued','running') ORDER BY created_at,id`)
	if err != nil {
		return nil, fmt.Errorf("list runnable AIGC jobs: %w", err)
	}
	defer rows.Close()
	return scanAIGCJobs(rows)
}

// ClaimJob atomically transitions an awaiting-confirmation job to running.
func (r *AIGCJobRepository) ClaimJob(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE aigc_jobs SET status='running',updated_at=? WHERE id=? AND status='awaiting_confirm'`, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("claim AIGC job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("claim AIGC job result: %w", err)
	}
	if affected == 1 {
		return nil
	}
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM aigc_jobs WHERE id=?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check AIGC job after claim: %w", err)
	}
	if exists == 0 {
		return ErrAIGCJobNotFound
	}
	return ErrAIGCJobConflict
}

type aigcJobScanner interface{ Scan(...any) error }

func scanAIGCJob(scanner aigcJobScanner) (AIGCJob, error) {
	var value AIGCJob
	var inputs, outputs string
	if err := scanner.Scan(&value.ID, &value.MerchantID, &value.OrderID, &value.Kind, &value.Status, &inputs, &outputs, &value.ParamsJSON, &value.Error, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return AIGCJob{}, err
	}
	if err := json.Unmarshal([]byte(inputs), &value.InputMaterialIDs); err != nil {
		return AIGCJob{}, fmt.Errorf("decode AIGC job inputs: %w", err)
	}
	if err := json.Unmarshal([]byte(outputs), &value.OutputMaterialIDs); err != nil {
		return AIGCJob{}, fmt.Errorf("decode AIGC job outputs: %w", err)
	}
	return value, nil
}

func scanAIGCJobs(rows *sql.Rows) ([]AIGCJob, error) {
	items := []AIGCJob{}
	for rows.Next() {
		value, err := scanAIGCJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan AIGC job: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate AIGC jobs: %w", err)
	}
	return items, nil
}

func requireAIGCJobAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrAIGCJobNotFound
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
