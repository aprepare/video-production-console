package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

var (
	ErrProjectNotFound      = errors.New("project not found")
	ErrAssetNotFound        = errors.New("asset not found")
	ErrAccountInactive      = errors.New("account must be active")
	ErrProjectStageConflict = errors.New("project stage changed")
	ErrProjectBusy          = errors.New("project has an active task")
)

type ProjectRepository struct {
	db     *sql.DB
	assets *AssetRepository
}

func NewProjectRepository(db *sql.DB) *ProjectRepository {
	return &ProjectRepository{db: db, assets: NewAssetRepository(db)}
}

func (r *ProjectRepository) CreateProject(ctx context.Context, project domain.Project) error {
	if strings.TrimSpace(project.Title) == "" {
		project.Title = "Untitled-" + project.ID[:8]
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO projects(id,account_id,title,stage,created_at,updated_at)
		SELECT ?,id,?,'script',?,? FROM accounts WHERE id=? AND status='active'`, project.ID, project.Title, project.CreatedAt, project.UpdatedAt, project.AccountID)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrAccountInactive
	}
	return nil
}

func (r *ProjectRepository) DeleteProject(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id=?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrProjectNotFound
	} else if err != nil {
		return err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_tasks WHERE project_id=? AND status IN ('queued','running','awaiting_input','resuming','waiting_input')`, id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrProjectBusy
	}
	// Remove dependency edges first because downstream-version references use
	// ON DELETE RESTRICT while the asset versions themselves cascade.
	if _, err := tx.ExecContext(ctx, `DELETE FROM asset_dependencies WHERE asset_version_id IN (SELECT id FROM asset_versions WHERE project_id=?) OR depends_on_version_id IN (SELECT id FROM asset_versions WHERE project_id=?)`, id, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncStageFromAssets advances the project card to the next meaningful
// production lane. It never regresses, publishes, or unarchives a project.
func (r *ProjectRepository) SyncStageFromAssets(ctx context.Context, id string, now time.Time) (domain.Project, error) {
	project, err := r.GetProject(ctx, id)
	if err != nil {
		return domain.Project{}, err
	}
	if project.Stage == domain.StageArchived || project.Stage == domain.StagePublished {
		return project, nil
	}
	versions, err := r.assets.CurrentByProject(ctx, id)
	if err != nil {
		return domain.Project{}, err
	}
	ready := map[domain.AssetType]bool{}
	for _, version := range versions {
		if version.State == domain.AssetReady {
			ready[version.Type] = true
		}
	}
	target := domain.StageScript
	if ready[domain.AssetContinuousScript] {
		target = domain.StageAssets
	}
	if ready[domain.AssetNarration] && ready[domain.AssetSubtitleSRT] {
		target = domain.StageMixing
	}
	if ready[domain.AssetMixDraft] || ready[domain.AssetFinalVideo] {
		target = domain.StageReview
	}
	order := map[domain.ProjectStage]int{
		domain.StageScript: 0, domain.StageAssets: 1, domain.StageMixing: 2,
		domain.StageReview: 3,
	}
	if order[target] <= order[project.Stage] {
		return project, nil
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE projects SET stage=?,updated_at=? WHERE id=?`, target, now, id); err != nil {
		return domain.Project{}, err
	}
	return r.GetProject(ctx, id)
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
	if to == domain.StageReview {
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
	if asset == nil || asset.ProjectID == nil {
		return CommitNotCommitted, ErrInvalidAssetInput
	}
	typ := asset.Type
	if typ == domain.AssetAudio {
		typ = domain.AssetNarration
	}
	if typ == domain.AssetSubtitle {
		typ = domain.AssetSubtitleSRT
	}
	version, err := r.assets.AddVersion(ctx, AddAssetVersion{ProjectID: asset.ProjectID, AccountID: asset.AccountID, Type: typ, StorageKind: domain.StorageFile, Path: asset.Path, Filename: asset.Filename, MIMEType: asset.MIMEType, Size: asset.Size, SHA256: asset.SHA256, SourceTaskID: asset.SourceTaskID})
	if err != nil {
		return commitOutcome(err), err
	}
	asset.ID, asset.AccountID, asset.Type, asset.Version, asset.Status, asset.CreatedAt = version.ID, version.AccountID, version.Type, version.Version, string(version.State), version.CreatedAt
	return CommitCommitted, nil
}
func (r *ProjectRepository) ListAssets(ctx context.Context, projectID string) ([]domain.Asset, error) {
	current, err := r.assets.CurrentByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Asset, 0)
	for _, item := range current {
		history, historyErr := r.assets.History(ctx, item.AssetID)
		if historyErr != nil {
			return nil, historyErr
		}
		for _, version := range history {
			out = append(out, assetVersionToLegacy(version))
		}
	}
	return out, nil
}

// GetAsset returns an asset by its stable identifier. Asset paths must not be
// exposed without first resolving the owning database record.
func (r *ProjectRepository) GetAsset(ctx context.Context, id string) (domain.Asset, error) {
	if _, parseErr := uuid.Parse(id); parseErr != nil {
		return domain.Asset{}, ErrAssetNotFound
	}
	var a domain.Asset
	err := r.db.QueryRowContext(ctx, `SELECT id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,state,created_at,source_task_id FROM asset_versions WHERE id=?`, id).
		Scan(&a.ID, &a.ProjectID, &a.AccountID, &a.Type, &a.Path, &a.Filename, &a.MIMEType, &a.Size, &a.SHA256, &a.Version, &a.Status, &a.CreatedAt, &a.SourceTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Asset{}, ErrAssetNotFound
	}
	return a, err
}
func (r *ProjectRepository) Background(ctx context.Context, projectID string) (domain.Asset, error) {
	v, err := r.assets.CurrentBackgroundForProject(ctx, projectID)
	if errors.Is(err, ErrAssetVersionNotFound) {
		return domain.Asset{}, sql.ErrNoRows
	}
	if err != nil {
		return domain.Asset{}, err
	}
	return assetVersionToLegacy(v), nil
}

func assetVersionToLegacy(v domain.AssetVersion) domain.Asset {
	return domain.Asset{ID: v.ID, ProjectID: v.ProjectID, AccountID: v.AccountID, Type: v.Type, Path: v.Path, Filename: v.Filename, MIMEType: v.MIMEType, Size: v.Size, SHA256: v.SHA256, Version: v.Version, Status: string(v.State), CreatedAt: v.CreatedAt, SourceTaskID: v.SourceTaskID}
}

type projectScanner interface{ Scan(...any) error }

func scanProject(s projectScanner) (domain.Project, error) {
	var p domain.Project
	err := s.Scan(&p.ID, &p.AccountID, &p.Title, &p.Stage, &p.TopicCardPath, &p.CreatedAt, &p.UpdatedAt, &p.ReadyAt, &p.PublishedAt, &p.PublishNote)
	return p, err
}
