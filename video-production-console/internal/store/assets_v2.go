package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

var (
	ErrInvalidAssetInput      = errors.New("invalid asset input")
	ErrInvalidAssetParent     = errors.New("invalid asset parent")
	ErrInvalidAssetDependency = errors.New("invalid asset dependency")
	ErrAssetVersionNotFound   = errors.New("asset version not found")
)

type AddAssetVersion struct {
	LogicalAssetID                   string
	ProjectID                        *string
	AccountID                        string
	Type                             domain.AssetType
	StorageKind                      domain.StorageKind
	Path, Filename, MIMEType, SHA256 string
	Size                             int64
	ParentVersionID, SourceTaskID    *string
	Dependencies                     []string
}

type UpgradeTopicCardVersion struct {
	AddAssetVersion
	ExpectedCurrentVersionID string
}

type AssetRepository struct {
	db     *sql.DB
	commit func(context.Context, *sql.Conn) error
}

func NewAssetRepository(db *sql.DB) *AssetRepository { return &AssetRepository{db: db} }

type assetDBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateAddAssetVersion(in AddAssetVersion) error {
	switch in.Type {
	case domain.AssetAudio, domain.AssetSubtitle:
		return ErrInvalidAssetInput
	}
	if in.ProjectID == nil && in.AccountID == "" {
		return ErrInvalidAssetInput
	}
	if in.AccountID != "" {
		if _, err := uuid.Parse(in.AccountID); err != nil {
			return fmt.Errorf("%w: account ID", ErrInvalidAssetInput)
		}
	}
	for name, value := range map[string]string{"logical asset ID": in.LogicalAssetID} {
		if value != "" {
			if _, err := uuid.Parse(value); err != nil {
				return fmt.Errorf("%w: %s", ErrInvalidAssetInput, name)
			}
		}
	}
	for name, value := range map[string]*string{"project ID": in.ProjectID, "parent version ID": in.ParentVersionID, "source task ID": in.SourceTaskID} {
		if value != nil {
			if _, err := uuid.Parse(*value); err != nil {
				return fmt.Errorf("%w: %s", ErrInvalidAssetInput, name)
			}
		}
	}
	for _, dependency := range in.Dependencies {
		if _, err := uuid.Parse(dependency); err != nil {
			return fmt.Errorf("%w: dependency ID", ErrInvalidAssetInput)
		}
	}
	if strings.TrimSpace(string(in.Type)) == "" || strings.TrimSpace(in.Path) == "" || strings.TrimSpace(in.Filename) == "" || strings.TrimSpace(in.MIMEType) == "" || strings.TrimSpace(in.SHA256) == "" || in.Size < 0 {
		return ErrInvalidAssetInput
	}
	if in.StorageKind != "" && in.StorageKind != domain.StorageFile && in.StorageKind != domain.StorageDirectory {
		return ErrInvalidAssetInput
	}
	if in.StorageKind == domain.StorageDirectory && in.Type == domain.AssetFinalVideo {
		return ErrInvalidAssetInput
	}
	return nil
}

func (r *AssetRepository) AddVersion(ctx context.Context, in AddAssetVersion) (out domain.AssetVersion, err error) {
	if err := validateAddAssetVersion(in); err != nil {
		return out, err
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return out, fmt.Errorf("begin add asset version: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	out, err = r.addVersion(ctx, conn, in, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if r.commit != nil {
		err = r.commit(ctx, conn)
	} else {
		_, err = conn.ExecContext(ctx, `COMMIT`)
	}
	if err != nil {
		return domain.AssetVersion{}, &CommitOutcomeError{Outcome: CommitUnknown, Err: fmt.Errorf("commit add asset version: %w", err)}
	}
	committed = true
	return out, nil
}

// UpgradeTopicCard creates a new current topic-card version and updates the
// project's compatibility path in the same transaction.
func (r *AssetRepository) UpgradeTopicCard(ctx context.Context, in UpgradeTopicCardVersion) (out domain.AssetVersion, err error) {
	if in.Type != domain.AssetTopicCard || in.ProjectID == nil || in.LogicalAssetID == "" || in.ExpectedCurrentVersionID == "" || in.ParentVersionID == nil || *in.ParentVersionID != in.ExpectedCurrentVersionID {
		return out, ErrInvalidAssetInput
	}
	if err := validateAddAssetVersion(in.AddAssetVersion); err != nil {
		return out, err
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return out, fmt.Errorf("begin upgrade topic card: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	var current string
	if err := conn.QueryRowContext(ctx, `SELECT current_version_id FROM asset_items WHERE id=? AND project_id=? AND account_id=? AND type=?`, in.LogicalAssetID, *in.ProjectID, in.AccountID, domain.AssetTopicCard).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return out, ErrInvalidAssetInput
	} else if err != nil {
		return out, err
	}
	if current != in.ExpectedCurrentVersionID {
		return out, ErrInvalidAssetParent
	}
	out, err = r.addVersion(ctx, conn, in.AddAssetVersion, time.Now().UTC())
	if err != nil {
		return out, err
	}
	result, err := conn.ExecContext(ctx, `UPDATE projects SET topic_card_path=?,updated_at=? WHERE id=?`, out.Path, out.CreatedAt, *in.ProjectID)
	if err != nil {
		return domain.AssetVersion{}, err
	}
	if n, rowsErr := result.RowsAffected(); rowsErr != nil || n != 1 {
		if rowsErr != nil {
			return domain.AssetVersion{}, rowsErr
		}
		return domain.AssetVersion{}, ErrProjectNotFound
	}
	if r.commit != nil {
		err = r.commit(ctx, conn)
	} else {
		_, err = conn.ExecContext(ctx, `COMMIT`)
	}
	if err != nil {
		return domain.AssetVersion{}, &CommitOutcomeError{Outcome: CommitUnknown, Err: fmt.Errorf("commit upgrade topic card: %w", err)}
	}
	committed = true
	return out, nil
}

// addVersion is the transaction-aware kernel. The caller owns transaction boundaries.
func (r *AssetRepository) addVersion(ctx context.Context, q assetDBTX, in AddAssetVersion, now time.Time) (domain.AssetVersion, error) {
	if err := validateAddAssetVersion(in); err != nil {
		return domain.AssetVersion{}, err
	}
	accountID := in.AccountID
	if in.ProjectID != nil {
		if err := q.QueryRowContext(ctx, `SELECT account_id FROM projects WHERE id=?`, *in.ProjectID).Scan(&accountID); errors.Is(err, sql.ErrNoRows) {
			return domain.AssetVersion{}, ErrProjectNotFound
		} else if err != nil {
			return domain.AssetVersion{}, err
		}
	}
	logicalID := in.LogicalAssetID
	var oldCurrent sql.NullString
	if logicalID == "" {
		var err error
		if in.ProjectID == nil {
			err = q.QueryRowContext(ctx, `SELECT id,current_version_id FROM asset_items WHERE project_id IS NULL AND account_id=? AND type=?`, accountID, in.Type).Scan(&logicalID, &oldCurrent)
		} else {
			err = q.QueryRowContext(ctx, `SELECT id,current_version_id FROM asset_items WHERE project_id=? AND account_id=? AND type=?`, *in.ProjectID, accountID, in.Type).Scan(&logicalID, &oldCurrent)
		}
		if errors.Is(err, sql.ErrNoRows) {
			logicalID = uuid.NewString()
			if _, err = q.ExecContext(ctx, `INSERT INTO asset_items(id,project_id,account_id,type,created_at,updated_at) VALUES(?,?,?,?,?,?)`, logicalID, in.ProjectID, accountID, in.Type, now, now); err != nil {
				return domain.AssetVersion{}, fmt.Errorf("create logical asset: %w", err)
			}
		} else if err != nil {
			return domain.AssetVersion{}, err
		}
	} else {
		var project sql.NullString
		var typ domain.AssetType
		var owner string
		if err := q.QueryRowContext(ctx, `SELECT project_id,account_id,type,current_version_id FROM asset_items WHERE id=?`, logicalID).Scan(&project, &owner, &typ, &oldCurrent); errors.Is(err, sql.ErrNoRows) {
			return domain.AssetVersion{}, ErrInvalidAssetInput
		} else if err != nil {
			return domain.AssetVersion{}, err
		}
		if owner != accountID || typ != in.Type || !sameNullableProject(project, in.ProjectID) {
			return domain.AssetVersion{}, ErrInvalidAssetInput
		}
	}
	if in.ParentVersionID != nil {
		var parentAsset string
		if err := q.QueryRowContext(ctx, `SELECT asset_id FROM asset_versions WHERE id=?`, *in.ParentVersionID).Scan(&parentAsset); errors.Is(err, sql.ErrNoRows) || parentAsset != logicalID {
			return domain.AssetVersion{}, ErrInvalidAssetParent
		} else if err != nil {
			return domain.AssetVersion{}, err
		}
	}
	for _, dependencyID := range in.Dependencies {
		var depProject sql.NullString
		var depAccount string
		var depType domain.AssetType
		if err := q.QueryRowContext(ctx, `SELECT project_id,account_id,type FROM asset_versions WHERE id=?`, dependencyID).Scan(&depProject, &depAccount, &depType); errors.Is(err, sql.ErrNoRows) {
			return domain.AssetVersion{}, ErrInvalidAssetDependency
		} else if err != nil {
			return domain.AssetVersion{}, err
		}
		allowedScope := depAccount == accountID && ((in.ProjectID != nil && depProject.Valid && depProject.String == *in.ProjectID) || (in.ProjectID != nil && !depProject.Valid && depType == domain.AssetAccountBackground) || (in.ProjectID == nil && !depProject.Valid))
		if !allowedScope || !slices.Contains(domain.InvalidatedAssetTypes(depType), in.Type) {
			return domain.AssetVersion{}, ErrInvalidAssetDependency
		}
	}
	var version int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM asset_versions WHERE asset_id=?`, logicalID).Scan(&version); err != nil {
		return domain.AssetVersion{}, err
	}
	storageKind := in.StorageKind
	if storageKind == "" {
		storageKind = domain.StorageFile
	}
	versionID := uuid.NewString()
	if _, err := q.ExecContext(ctx, `INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,storage_kind,path,filename,mime_type,size,sha256,parent_version_id,source_task_id,state,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, versionID, logicalID, in.ProjectID, accountID, in.Type, version, storageKind, in.Path, in.Filename, in.MIMEType, in.Size, in.SHA256, in.ParentVersionID, in.SourceTaskID, domain.AssetReady, now); err != nil {
		return domain.AssetVersion{}, fmt.Errorf("insert asset version: %w", err)
	}
	for _, dependencyID := range in.Dependencies {
		if _, err := q.ExecContext(ctx, `INSERT INTO asset_dependencies(asset_version_id,depends_on_version_id) VALUES(?,?)`, versionID, dependencyID); err != nil {
			return domain.AssetVersion{}, err
		}
	}
	if _, err := q.ExecContext(ctx, `UPDATE asset_items SET current_version_id=?,updated_at=? WHERE id=?`, versionID, now, logicalID); err != nil {
		return domain.AssetVersion{}, err
	}
	if oldCurrent.Valid {
		reason := "upstream version replaced: " + oldCurrent.String
		if _, err := q.ExecContext(ctx, `WITH RECURSIVE affected(id) AS (
			SELECT asset_version_id FROM asset_dependencies WHERE depends_on_version_id=?
			UNION SELECT d.asset_version_id FROM asset_dependencies d JOIN affected a ON d.depends_on_version_id=a.id
		) UPDATE asset_versions SET state='stale',stale_reason=? WHERE id IN (SELECT id FROM affected) AND id IN (SELECT current_version_id FROM asset_items)`, oldCurrent.String, reason); err != nil {
			return domain.AssetVersion{}, err
		}
	}
	projectID := in.ProjectID
	return domain.AssetVersion{ID: versionID, AssetID: logicalID, ProjectID: projectID, AccountID: accountID, Type: in.Type, Version: version, StorageKind: storageKind, Path: in.Path, Filename: in.Filename, MIMEType: in.MIMEType, Size: in.Size, SHA256: in.SHA256, ParentVersionID: in.ParentVersionID, SourceTaskID: in.SourceTaskID, State: domain.AssetReady, CreatedAt: now}, nil
}

func sameNullableProject(got sql.NullString, want *string) bool {
	return want == nil && !got.Valid || want != nil && got.Valid && got.String == *want
}

func (r *AssetRepository) CurrentByProject(ctx context.Context, projectID string) ([]domain.AssetVersion, error) {
	if _, err := uuid.Parse(projectID); err != nil {
		return nil, ErrInvalidAssetInput
	}
	return r.queryVersions(ctx, `SELECT v.id,v.asset_id,v.project_id,v.account_id,v.type,v.version,v.storage_kind,v.path,v.filename,v.mime_type,v.size,v.sha256,v.parent_version_id,v.source_task_id,v.state,v.stale_reason,v.created_at FROM asset_items i JOIN asset_versions v ON v.id=i.current_version_id WHERE i.project_id=? ORDER BY i.type`, projectID)
}

func (r *AssetRepository) ReadyMixDraftsByProject(ctx context.Context, projectID string) ([]domain.AssetVersion, error) {
	if _, err := uuid.Parse(projectID); err != nil {
		return nil, ErrInvalidAssetInput
	}
	return r.queryVersions(ctx, `SELECT id,asset_id,project_id,account_id,type,version,storage_kind,path,filename,mime_type,size,sha256,parent_version_id,source_task_id,state,stale_reason,created_at
		FROM asset_versions WHERE project_id=? AND type=? AND state=? ORDER BY created_at DESC,id DESC`, projectID, domain.AssetMixDraft, domain.AssetReady)
}

func (r *AssetRepository) History(ctx context.Context, logicalAssetID string) ([]domain.AssetVersion, error) {
	if _, err := uuid.Parse(logicalAssetID); err != nil {
		return nil, ErrInvalidAssetInput
	}
	return r.queryVersions(ctx, `SELECT id,asset_id,project_id,account_id,type,version,storage_kind,path,filename,mime_type,size,sha256,parent_version_id,source_task_id,state,stale_reason,created_at FROM asset_versions WHERE asset_id=? ORDER BY version DESC`, logicalAssetID)
}

func (r *AssetRepository) Version(ctx context.Context, id string) (domain.AssetVersion, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.AssetVersion{}, ErrInvalidAssetInput
	}
	versions, err := r.queryVersions(ctx, `SELECT id,asset_id,project_id,account_id,type,version,storage_kind,path,filename,mime_type,size,sha256,parent_version_id,source_task_id,state,stale_reason,created_at FROM asset_versions WHERE id=?`, id)
	if err != nil {
		return domain.AssetVersion{}, err
	}
	if len(versions) == 0 {
		return domain.AssetVersion{}, ErrAssetVersionNotFound
	}
	return versions[0], nil
}

func (r *AssetRepository) CurrentBackgroundForProject(ctx context.Context, projectID string) (domain.AssetVersion, error) {
	if _, err := uuid.Parse(projectID); err != nil {
		return domain.AssetVersion{}, ErrInvalidAssetInput
	}
	versions, err := r.queryVersions(ctx, `SELECT v.id,v.asset_id,v.project_id,v.account_id,v.type,v.version,v.storage_kind,v.path,v.filename,v.mime_type,v.size,v.sha256,v.parent_version_id,v.source_task_id,v.state,v.stale_reason,v.created_at FROM projects p JOIN accounts a ON a.id=p.account_id JOIN asset_items i ON i.id=a.background_asset_item_id JOIN asset_versions v ON v.id=i.current_version_id WHERE p.id=?`, projectID)
	if err != nil {
		return domain.AssetVersion{}, err
	}
	if len(versions) == 0 {
		return domain.AssetVersion{}, ErrAssetVersionNotFound
	}
	return versions[0], nil
}

func (r *AssetRepository) queryVersions(ctx context.Context, query string, args ...any) ([]domain.AssetVersion, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AssetVersion{}
	for rows.Next() {
		var v domain.AssetVersion
		if err := rows.Scan(&v.ID, &v.AssetID, &v.ProjectID, &v.AccountID, &v.Type, &v.Version, &v.StorageKind, &v.Path, &v.Filename, &v.MIMEType, &v.Size, &v.SHA256, &v.ParentVersionID, &v.SourceTaskID, &v.State, &v.StaleReason, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
