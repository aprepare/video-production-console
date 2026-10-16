package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrReplicateNotFound = errors.New("复刻项目或镜头不存在")
var ErrInvalidReplicate = errors.New("复刻项目数据无效")

type ReplicateProject struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	ReferenceID    string          `json:"reference_id"`
	AnalysisID     string          `json:"analysis_id"`
	Product        string          `json:"product"`
	StyleLock      string          `json:"style_lock"`
	Ratio          string          `json:"ratio"`
	VideoProvider  string          `json:"video_provider"`
	ImageModel     string          `json:"image_model"`
	Variants       int             `json:"variants"`
	Status         string          `json:"status"`
	FinalPath      string          `json:"-"`
	FinalDurationS float64         `json:"final_duration_s"`
	QCID           string          `json:"qc_id"`
	Error          string          `json:"error"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Shots          []ReplicateShot `json:"shots,omitempty"`
}

type ReplicateShot struct {
	ID               string          `json:"id"`
	ProjectID        string          `json:"project_id"`
	Position         int             `json:"position"`
	Purpose          string          `json:"purpose"`
	SourceStartS     float64         `json:"source_start_s"`
	SourceEndS       float64         `json:"source_end_s"`
	DurationS        int             `json:"duration_s"`
	FirstFramePrompt string          `json:"first_frame_prompt"`
	MotionPrompt     string          `json:"motion_prompt"`
	Negative         string          `json:"negative"`
	Keep             string          `json:"keep"`
	FirstFramePath   string          `json:"-"`
	FirstFrameSource string          `json:"first_frame_source"`
	FirstFrameStatus string          `json:"first_frame_status"`
	FirstFrameError  string          `json:"first_frame_error"`
	SelectedTakeID   string          `json:"selected_take_id"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Takes            []ReplicateTake `json:"takes"`
}

type ReplicateTake struct {
	ID             string    `json:"id"`
	ShotID         string    `json:"shot_id"`
	Provider       string    `json:"provider"`
	Prompt         string    `json:"prompt"`
	Status         string    `json:"status"`
	ProviderTaskID string    `json:"provider_task_id"`
	KeySlot        int       `json:"key_slot"`
	Path           string    `json:"-"`
	DurationS      float64   `json:"duration_s"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	CostNote       string    `json:"cost_note"`
	Error          string    `json:"error"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ReplicateRepository struct{ db *sql.DB }

func NewReplicateRepository(db *sql.DB) *ReplicateRepository { return &ReplicateRepository{db: db} }

const replicateTimeFormat = "2006-01-02T15:04:05.000000000Z"
const replicateProjectColumns = `id,title,reference_id,analysis_id,product,style_lock,ratio,video_provider,image_model,variants,status,final_path,final_duration_s,qc_id,error,created_at,updated_at`
const replicateShotColumns = `id,project_id,position,purpose,source_start_s,source_end_s,duration_s,first_frame_prompt,motion_prompt,negative,keep,first_frame_path,first_frame_source,first_frame_status,first_frame_error,selected_take_id,created_at,updated_at`
const replicateTakeColumns = `id,shot_id,provider,prompt,status,provider_task_id,key_slot,path,duration_s,width,height,cost_note,error,created_at,updated_at`

type replicateScanner interface{ Scan(...any) error }

func replicateTime(t time.Time) string { return t.UTC().Format(replicateTimeFormat) }

func scanReplicateProject(s replicateScanner) (p ReplicateProject, err error) {
	var created, updated string
	err = s.Scan(&p.ID, &p.Title, &p.ReferenceID, &p.AnalysisID, &p.Product, &p.StyleLock, &p.Ratio, &p.VideoProvider, &p.ImageModel, &p.Variants, &p.Status, &p.FinalPath, &p.FinalDurationS, &p.QCID, &p.Error, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrReplicateNotFound
	}
	if err != nil {
		return p, err
	}
	if p.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return p, err
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return p, err
}

func scanReplicateShot(s replicateScanner) (shot ReplicateShot, err error) {
	var created, updated string
	err = s.Scan(&shot.ID, &shot.ProjectID, &shot.Position, &shot.Purpose, &shot.SourceStartS, &shot.SourceEndS, &shot.DurationS, &shot.FirstFramePrompt, &shot.MotionPrompt, &shot.Negative, &shot.Keep, &shot.FirstFramePath, &shot.FirstFrameSource, &shot.FirstFrameStatus, &shot.FirstFrameError, &shot.SelectedTakeID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return shot, ErrReplicateNotFound
	}
	if err != nil {
		return shot, err
	}
	if shot.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return shot, err
	}
	shot.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	shot.Takes = []ReplicateTake{}
	return shot, err
}

func scanReplicateTake(s replicateScanner) (take ReplicateTake, err error) {
	var created, updated string
	err = s.Scan(&take.ID, &take.ShotID, &take.Provider, &take.Prompt, &take.Status, &take.ProviderTaskID, &take.KeySlot, &take.Path, &take.DurationS, &take.Width, &take.Height, &take.CostNote, &take.Error, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return take, ErrReplicateNotFound
	}
	if err != nil {
		return take, err
	}
	if take.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return take, err
	}
	take.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return take, err
}

func (r *ReplicateRepository) Create(ctx context.Context, p ReplicateProject, shots []ReplicateShot) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if _, err = tx.ExecContext(ctx, `INSERT INTO replicate_projects (`+replicateProjectColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.Title, p.ReferenceID, p.AnalysisID, p.Product, p.StyleLock, p.Ratio, p.VideoProvider, p.ImageModel, p.Variants, p.Status, p.FinalPath, p.FinalDurationS, p.QCID, p.Error, replicateTime(p.CreatedAt), replicateTime(p.UpdatedAt)); err != nil {
		return err
	}
	for i, shot := range shots {
		if shot.ProjectID != p.ID || shot.Position != i+1 {
			return fmt.Errorf("%w: 镜头项目或顺序不正确", ErrInvalidReplicate)
		}
		if shot.CreatedAt.IsZero() {
			shot.CreatedAt = now
		}
		shot.UpdatedAt = now
		if err = insertReplicateShot(ctx, tx, shot); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertReplicateShot(ctx context.Context, tx *sql.Tx, shot ReplicateShot) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO replicate_shots (`+replicateShotColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, shot.ID, shot.ProjectID, shot.Position, shot.Purpose, shot.SourceStartS, shot.SourceEndS, shot.DurationS, shot.FirstFramePrompt, shot.MotionPrompt, shot.Negative, shot.Keep, shot.FirstFramePath, shot.FirstFrameSource, shot.FirstFrameStatus, shot.FirstFrameError, shot.SelectedTakeID, replicateTime(shot.CreatedAt), replicateTime(shot.UpdatedAt))
	return err
}

func (r *ReplicateRepository) Get(ctx context.Context, id string) (ReplicateProject, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ReplicateProject{}, err
	}
	defer tx.Rollback()
	p, err := scanReplicateProject(tx.QueryRowContext(ctx, `SELECT `+replicateProjectColumns+` FROM replicate_projects WHERE id=?`, id))
	if err != nil {
		return p, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+replicateShotColumns+` FROM replicate_shots WHERE project_id=? ORDER BY position`, id)
	if err != nil {
		return p, err
	}
	p.Shots = []ReplicateShot{}
	for rows.Next() {
		shot, scanErr := scanReplicateShot(rows)
		if scanErr != nil {
			rows.Close()
			return p, scanErr
		}
		p.Shots = append(p.Shots, shot)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return p, err
	}
	if err = rows.Close(); err != nil {
		return p, err
	}
	index := make(map[string]int, len(p.Shots))
	for i := range p.Shots {
		index[p.Shots[i].ID] = i
	}
	rows, err = tx.QueryContext(ctx, `SELECT t.`+strings.ReplaceAll(replicateTakeColumns, ",", ",t.")+` FROM replicate_takes t JOIN replicate_shots s ON s.id=t.shot_id WHERE s.project_id=? ORDER BY s.position,t.created_at,t.id`, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		take, scanErr := scanReplicateTake(rows)
		if scanErr != nil {
			rows.Close()
			return p, scanErr
		}
		p.Shots[index[take.ShotID]].Takes = append(p.Shots[index[take.ShotID]].Takes, take)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return p, err
	}
	if err = rows.Close(); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func (r *ReplicateRepository) List(ctx context.Context, limit int) ([]ReplicateProject, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+replicateProjectColumns+` FROM replicate_projects ORDER BY created_at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReplicateProject{}
	for rows.Next() {
		p, scanErr := scanReplicateProject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (r *ReplicateRepository) InterruptedProjects(ctx context.Context) ([]ReplicateProject, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+replicateProjectColumns+` FROM replicate_projects p
		WHERE p.status='stitching'
		   OR EXISTS (SELECT 1 FROM replicate_shots s WHERE s.project_id=p.id AND s.first_frame_status='generating')
		ORDER BY p.created_at DESC,p.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReplicateProject{}
	for rows.Next() {
		p, scanErr := scanReplicateProject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func replicateAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrReplicateNotFound
	}
	return nil
}

func (r *ReplicateRepository) SaveProject(ctx context.Context, p ReplicateProject) error {
	return replicateAffected(r.db.ExecContext(ctx, `UPDATE replicate_projects SET title=?,reference_id=?,analysis_id=?,product=?,style_lock=?,ratio=?,video_provider=?,image_model=?,variants=?,status=?,final_path=?,final_duration_s=?,qc_id=?,error=?,updated_at=? WHERE id=?`, p.Title, p.ReferenceID, p.AnalysisID, p.Product, p.StyleLock, p.Ratio, p.VideoProvider, p.ImageModel, p.Variants, p.Status, p.FinalPath, p.FinalDurationS, p.QCID, p.Error, replicateTime(time.Now()), p.ID))
}

func (r *ReplicateRepository) GetShot(ctx context.Context, id string) (ReplicateShot, error) {
	shot, err := scanReplicateShot(r.db.QueryRowContext(ctx, `SELECT `+replicateShotColumns+` FROM replicate_shots WHERE id=?`, id))
	if err != nil {
		return shot, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+replicateTakeColumns+` FROM replicate_takes WHERE shot_id=? ORDER BY created_at,id`, id)
	if err != nil {
		return shot, err
	}
	defer rows.Close()
	for rows.Next() {
		take, scanErr := scanReplicateTake(rows)
		if scanErr != nil {
			return shot, scanErr
		}
		shot.Takes = append(shot.Takes, take)
	}
	return shot, rows.Err()
}

func (r *ReplicateRepository) AddShot(ctx context.Context, shot ReplicateShot, afterPosition int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM replicate_shots WHERE project_id=?`, shot.ProjectID).Scan(&count); err != nil {
		return err
	}
	if afterPosition < 0 || afterPosition > count {
		return fmt.Errorf("%w: 插入位置超出范围", ErrInvalidReplicate)
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET position=-position,updated_at=? WHERE project_id=? AND position>?`, replicateTime(now), shot.ProjectID, afterPosition); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET position=(-position)+1 WHERE project_id=? AND position<0`, shot.ProjectID); err != nil {
		return err
	}
	shot.Position = afterPosition + 1
	if shot.CreatedAt.IsZero() {
		shot.CreatedAt = now
	}
	shot.UpdatedAt = now
	if err = insertReplicateShot(ctx, tx, shot); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ReplicateRepository) SaveShot(ctx context.Context, shot ReplicateShot) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err = tx.QueryRowContext(ctx, `SELECT project_id FROM replicate_shots WHERE id=?`, shot.ID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
		return ErrReplicateNotFound
	} else if err != nil {
		return err
	}
	if projectID != shot.ProjectID {
		return fmt.Errorf("%w: 不能移动镜头所属项目", ErrInvalidReplicate)
	}
	if shot.SelectedTakeID != "" {
		var valid int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM replicate_takes WHERE id=? AND shot_id=? AND status='succeeded'`, shot.SelectedTakeID, shot.ID).Scan(&valid); err != nil {
			return err
		}
		if valid != 1 {
			return fmt.Errorf("%w: 只能选择本镜头已成功的版本", ErrInvalidReplicate)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE replicate_shots SET purpose=?,source_start_s=?,source_end_s=?,duration_s=?,first_frame_prompt=?,motion_prompt=?,negative=?,keep=?,first_frame_path=?,first_frame_source=?,first_frame_status=?,first_frame_error=?,selected_take_id=?,updated_at=? WHERE id=?`, shot.Purpose, shot.SourceStartS, shot.SourceEndS, shot.DurationS, shot.FirstFramePrompt, shot.MotionPrompt, shot.Negative, shot.Keep, shot.FirstFramePath, shot.FirstFrameSource, shot.FirstFrameStatus, shot.FirstFrameError, shot.SelectedTakeID, replicateTime(time.Now()), shot.ID)
	if err = replicateAffected(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ReplicateRepository) DeleteShot(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	var position int
	if err = tx.QueryRowContext(ctx, `SELECT project_id,position FROM replicate_shots WHERE id=?`, id).Scan(&projectID, &position); errors.Is(err, sql.ErrNoRows) {
		return ErrReplicateNotFound
	} else if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM replicate_shots WHERE id=?`, id); err != nil {
		return err
	}
	now := replicateTime(time.Now())
	if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET position=-position,updated_at=? WHERE project_id=? AND position>?`, now, projectID, position); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET position=(-position)-1 WHERE project_id=? AND position<0`, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ReplicateRepository) ReorderShots(ctx context.Context, projectID string, shotIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM replicate_shots WHERE project_id=?`, projectID)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(existing) != len(shotIDs) {
		return fmt.Errorf("%w: 镜头列表必须完整", ErrInvalidReplicate)
	}
	seen := map[string]bool{}
	for _, id := range shotIDs {
		if !existing[id] || seen[id] {
			return fmt.Errorf("%w: 镜头不存在或重复", ErrInvalidReplicate)
		}
		seen[id] = true
	}
	now := replicateTime(time.Now())
	if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET position=-position,updated_at=? WHERE project_id=?`, now, projectID); err != nil {
		return err
	}
	for i, id := range shotIDs {
		if result, updateErr := tx.ExecContext(ctx, `UPDATE replicate_shots SET position=? WHERE id=? AND project_id=?`, i+1, id, projectID); updateErr != nil {
			return updateErr
		} else if err = replicateAffected(result, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ReplicateRepository) CreateTakes(ctx context.Context, takes []ReplicateTake) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, take := range takes {
		if take.CreatedAt.IsZero() {
			take.CreatedAt = now
		}
		take.UpdatedAt = now
		if _, err = tx.ExecContext(ctx, `INSERT INTO replicate_takes (`+replicateTakeColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, take.ID, take.ShotID, take.Provider, take.Prompt, take.Status, take.ProviderTaskID, take.KeySlot, take.Path, take.DurationS, take.Width, take.Height, take.CostNote, take.Error, replicateTime(take.CreatedAt), replicateTime(take.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ReplicateRepository) GetTake(ctx context.Context, id string) (ReplicateTake, error) {
	return scanReplicateTake(r.db.QueryRowContext(ctx, `SELECT `+replicateTakeColumns+` FROM replicate_takes WHERE id=?`, id))
}

func (r *ReplicateRepository) SaveTake(ctx context.Context, take ReplicateTake) error {
	updated, err := r.saveTake(ctx, take, nil)
	if err == nil && !updated {
		return ErrReplicateNotFound
	}
	return err
}

func (r *ReplicateRepository) SaveTakeIfStatus(ctx context.Context, take ReplicateTake, allowed ...string) (bool, error) {
	if len(allowed) == 0 {
		return false, fmt.Errorf("%w: CAS 状态不能为空", ErrInvalidReplicate)
	}
	return r.saveTake(ctx, take, allowed)
}

func (r *ReplicateRepository) saveTake(ctx context.Context, take ReplicateTake, allowed []string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var shotID, currentStatus string
	if err = tx.QueryRowContext(ctx, `SELECT shot_id,status FROM replicate_takes WHERE id=?`, take.ID).Scan(&shotID, &currentStatus); errors.Is(err, sql.ErrNoRows) {
		return false, ErrReplicateNotFound
	} else if err != nil {
		return false, err
	}
	if shotID != take.ShotID {
		return false, fmt.Errorf("%w: 不能移动版本所属镜头", ErrInvalidReplicate)
	}
	if len(allowed) > 0 {
		matched := false
		for _, status := range allowed {
			matched = matched || currentStatus == status
		}
		if !matched {
			return false, nil
		}
	}
	now := replicateTime(time.Now())
	result, err := tx.ExecContext(ctx, `UPDATE replicate_takes SET provider=?,prompt=?,status=?,provider_task_id=?,key_slot=?,path=?,duration_s=?,width=?,height=?,cost_note=?,error=?,updated_at=? WHERE id=?`, take.Provider, take.Prompt, take.Status, take.ProviderTaskID, take.KeySlot, take.Path, take.DurationS, take.Width, take.Height, take.CostNote, take.Error, now, take.ID)
	if err = replicateAffected(result, err); err != nil {
		return false, err
	}
	if take.Status == "succeeded" {
		if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET selected_take_id=?,updated_at=? WHERE id=? AND selected_take_id=''`, take.ID, now, take.ShotID); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *ReplicateRepository) DeleteTake(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM replicate_takes WHERE id=?`, id)
	if err = replicateAffected(result, err); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE replicate_shots SET selected_take_id='',updated_at=? WHERE selected_take_id=?`, replicateTime(time.Now()), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ReplicateRepository) DeleteProject(ctx context.Context, id string) error {
	return replicateAffected(r.db.ExecContext(ctx, `DELETE FROM replicate_projects WHERE id=?`, id))
}

func (r *ReplicateRepository) ActiveTakes(ctx context.Context) ([]ReplicateTake, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+replicateTakeColumns+` FROM replicate_takes WHERE status IN ('queued','submitting','running') ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReplicateTake{}
	for rows.Next() {
		take, scanErr := scanReplicateTake(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, take)
	}
	return items, rows.Err()
}
