package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

var (
	ErrProjectNotFound      = errors.New("project not found")
	ErrAssetNotFound        = errors.New("asset not found")
	ErrAccountInactive      = errors.New("account must be active")
	ErrProjectStageConflict = errors.New("project stage changed")
)

type ProjectRepository struct{ db *sql.DB }

func NewProjectRepository(db *sql.DB) *ProjectRepository { return &ProjectRepository{db: db} }

func (r *ProjectRepository) CreateProject(ctx context.Context, project domain.Project) error {
	if strings.TrimSpace(project.Title) == "" {
		project.Title = "Untitled-" + project.ID[:8]
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO projects(id,account_id,title,stage,created_at,updated_at)
		SELECT ?,id,?,'topic',?,? FROM accounts WHERE id=? AND status='active'`, project.ID, project.Title, project.CreatedAt, project.UpdatedAt, project.AccountID)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrAccountInactive
	}
	return nil
}

func (r *ProjectRepository) ListProjects(ctx context.Context, accountID string, stage domain.ProjectStage, q string) ([]domain.Project, error) {
	query := `SELECT id,account_id,title,stage,topic_card_path,created_at,updated_at,ready_at,published_at,publish_note FROM projects WHERE 1=1`
	args := []any{}
	if accountID != "" {
		query += " AND account_id=?"
		args = append(args, accountID)
	}
	if stage != "" {
		query += " AND stage=?"
		args = append(args, stage)
	}
	if q != "" {
		query += " AND lower(title) LIKE ?"
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	query += " ORDER BY created_at,id"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *ProjectRepository) GetProject(ctx context.Context, id string) (domain.Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx, `SELECT id,account_id,title,stage,topic_card_path,created_at,updated_at,ready_at,published_at,publish_note FROM projects WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrProjectNotFound
	}
	return p, err
}
func (r *ProjectRepository) MoveProject(ctx context.Context, id string, expectedFrom, to domain.ProjectStage, now time.Time) (domain.Project, error) {
	if expectedFrom == to {
		p, err := r.GetProject(ctx, id)
		if err != nil {
			return domain.Project{}, err
		}
		if p.Stage != expectedFrom {
			return domain.Project{}, ErrProjectStageConflict
		}
		return p, nil
	}
	ready, published := "", ""
	if to == domain.StageReady {
		ready = ", ready_at=COALESCE(ready_at,?)"
	}
	if to == domain.StagePublished {
		published = ", published_at=COALESCE(published_at,?)"
	}
	args := []any{to, now}
	if ready != "" || published != "" {
		args = append(args, now)
	}
	args = append(args, id, expectedFrom)
	result, err := r.db.ExecContext(ctx, "UPDATE projects SET stage=?, updated_at=?"+ready+published+" WHERE id=? AND stage=?", args...)
	if err != nil {
		return domain.Project{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		if _, getErr := r.GetProject(ctx, id); errors.Is(getErr, ErrProjectNotFound) {
			return domain.Project{}, ErrProjectNotFound
		} else if getErr != nil {
			return domain.Project{}, getErr
		}
		return domain.Project{}, ErrProjectStageConflict
	}
	return r.GetProject(ctx, id)
}
func (r *ProjectRepository) SetTopicCardPath(ctx context.Context, id, path string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET topic_card_path=?,updated_at=? WHERE id=?`, path, now, id)
	if err == nil {
		n, _ := result.RowsAffected()
		if n == 0 {
			return ErrProjectNotFound
		}
	}
	return err
}

func (r *ProjectRepository) AddAsset(ctx context.Context, asset *domain.Asset) (state CommitState, err error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return CommitNotCommitted, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return CommitNotCommitted, err
	}
	defer func() {
		if state != CommitCommitted {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var accountID string
	if err = conn.QueryRowContext(ctx, `SELECT account_id FROM projects WHERE id=?`, *asset.ProjectID).Scan(&accountID); errors.Is(err, sql.ErrNoRows) {
		return CommitNotCommitted, ErrProjectNotFound
	}
	if err != nil {
		return CommitNotCommitted, err
	}
	asset.AccountID = accountID
	if err = conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM assets WHERE project_id=? AND type=?`, *asset.ProjectID, asset.Type).Scan(&asset.Version); err != nil {
		return CommitNotCommitted, err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,'active',?)`, asset.ID, *asset.ProjectID, accountID, asset.Type, asset.Path, asset.Filename, asset.MIMEType, asset.Size, asset.SHA256, asset.Version, asset.CreatedAt)
	if err != nil {
		return CommitNotCommitted, err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return CommitUnknown, err
	}
	state = CommitCommitted
	return state, nil
}
func (r *ProjectRepository) ListAssets(ctx context.Context, projectID string) ([]domain.Asset, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id FROM assets WHERE project_id=? ORDER BY type,version DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Asset{}
	for rows.Next() {
		var a domain.Asset
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.AccountID, &a.Type, &a.Path, &a.Filename, &a.MIMEType, &a.Size, &a.SHA256, &a.Version, &a.Status, &a.CreatedAt, &a.SourceTaskID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAsset returns an asset by its stable identifier. Asset paths must not be
// exposed without first resolving the owning database record.
func (r *ProjectRepository) GetAsset(ctx context.Context, id string) (domain.Asset, error) {
	var a domain.Asset
	err := r.db.QueryRowContext(ctx, `SELECT id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id FROM assets WHERE id=?`, id).
		Scan(&a.ID, &a.ProjectID, &a.AccountID, &a.Type, &a.Path, &a.Filename, &a.MIMEType, &a.Size, &a.SHA256, &a.Version, &a.Status, &a.CreatedAt, &a.SourceTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Asset{}, ErrAssetNotFound
	}
	return a, err
}
func (r *ProjectRepository) Background(ctx context.Context, projectID string) (domain.Asset, error) {
	var a domain.Asset
	err := r.db.QueryRowContext(ctx, `SELECT b.id,NULL,b.account_id,b.type,b.path,b.filename,b.mime_type,b.size,b.sha256,b.version,b.status,b.created_at,b.source_task_id FROM projects p JOIN accounts ac ON ac.id=p.account_id JOIN assets b ON b.id=ac.background_asset_id WHERE p.id=?`, projectID).Scan(&a.ID, &a.ProjectID, &a.AccountID, &a.Type, &a.Path, &a.Filename, &a.MIMEType, &a.Size, &a.SHA256, &a.Version, &a.Status, &a.CreatedAt, &a.SourceTaskID)
	return a, err
}

type projectScanner interface{ Scan(...any) error }

func scanProject(s projectScanner) (domain.Project, error) {
	var p domain.Project
	err := s.Scan(&p.ID, &p.AccountID, &p.Title, &p.Stage, &p.TopicCardPath, &p.CreatedAt, &p.UpdatedAt, &p.ReadyAt, &p.PublishedAt, &p.PublishNote)
	return p, err
}
