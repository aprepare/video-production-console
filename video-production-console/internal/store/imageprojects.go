package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-production-console/internal/domain"
)

var ErrImageProjectNotFound = errors.New("image project not found")
var ErrImageProjectItemNotFound = errors.New("image project item not found")

type ImageProjectRepository struct{ db *sql.DB }

func NewImageProjectRepository(db *sql.DB) *ImageProjectRepository {
	return &ImageProjectRepository{db: db}
}

func (r *ImageProjectRepository) Create(ctx context.Context, project domain.ImageProject, items []domain.ImageProjectItem) (returnErr error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = tx.Rollback()
		}
	}()
	_, err = tx.ExecContext(ctx, `INSERT INTO image_projects(id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, project.ID, project.Title, project.Script, project.ImageCount, project.Ratio, project.Style, project.CustomStyle, project.Concurrency, project.Status, project.CreatedAt, project.UpdatedAt)
	if err != nil {
		return err
	}
	for _, item := range items {
		_, err = tx.ExecContext(ctx, `INSERT INTO image_project_items(id,project_id,sequence,source_text,title,prompt,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, item.ProjectID, item.Sequence, item.SourceText, item.Title, item.Prompt, item.Status, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ImageProjectRepository) List(ctx context.Context) ([]domain.ImageProject, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at FROM image_projects ORDER BY updated_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []domain.ImageProject{}
	for rows.Next() {
		var project domain.ImageProject
		if err := rows.Scan(&project.ID, &project.Title, &project.Script, &project.ImageCount, &project.Ratio, &project.Style, &project.CustomStyle, &project.Concurrency, &project.Status, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (r *ImageProjectRepository) Get(ctx context.Context, id string) (domain.ImageProject, []domain.ImageProjectItem, error) {
	var project domain.ImageProject
	err := r.db.QueryRowContext(ctx, `SELECT id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at FROM image_projects WHERE id=?`, id).Scan(&project.ID, &project.Title, &project.Script, &project.ImageCount, &project.Ratio, &project.Style, &project.CustomStyle, &project.Concurrency, &project.Status, &project.CreatedAt, &project.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ImageProject{}, nil, ErrImageProjectNotFound
	}
	if err != nil {
		return domain.ImageProject{}, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,project_id,sequence,source_text,title,prompt,status,image_path,mime_type,width,height,error_message,created_at,updated_at FROM image_project_items WHERE project_id=? ORDER BY sequence`, id)
	if err != nil {
		return domain.ImageProject{}, nil, err
	}
	defer rows.Close()
	items := []domain.ImageProjectItem{}
	for rows.Next() {
		var item domain.ImageProjectItem
		var imagePath, mimeType, errorMessage sql.NullString
		var width, height sql.NullInt64
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Sequence, &item.SourceText, &item.Title, &item.Prompt, &item.Status, &imagePath, &mimeType, &width, &height, &errorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return domain.ImageProject{}, nil, err
		}
		if imagePath.Valid {
			item.ImagePath = &imagePath.String
		}
		if mimeType.Valid {
			item.MIMEType = &mimeType.String
		}
		if errorMessage.Valid {
			item.ErrorMessage = &errorMessage.String
		}
		if width.Valid {
			item.Width = int(width.Int64)
		}
		if height.Valid {
			item.Height = int(height.Int64)
		}
		items = append(items, item)
	}
	return project, items, rows.Err()
}

func (r *ImageProjectRepository) Item(ctx context.Context, projectID, itemID string) (domain.ImageProjectItem, error) {
	_, items, err := r.Get(ctx, projectID)
	if err != nil {
		return domain.ImageProjectItem{}, err
	}
	for _, item := range items {
		if item.ID == itemID {
			return item, nil
		}
	}
	return domain.ImageProjectItem{}, ErrImageProjectItemNotFound
}

func (r *ImageProjectRepository) UpdateItemText(ctx context.Context, projectID, itemID, sourceText, title, prompt string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE image_project_items SET source_text=?,title=?,prompt=?,status='pending',error_message=NULL,updated_at=? WHERE id=? AND project_id=?`, sourceText, title, prompt, at, itemID, projectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrImageProjectItemNotFound
	}
	_, err = r.db.ExecContext(ctx, `UPDATE image_projects SET updated_at=? WHERE id=?`, at, projectID)
	return err
}

func (r *ImageProjectRepository) MarkProjectStatus(ctx context.Context, id, status string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE image_projects SET status=?,updated_at=? WHERE id=?`, status, at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return nil
}

func (r *ImageProjectRepository) MarkItemGenerating(ctx context.Context, id string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE image_project_items SET status='generating',error_message=NULL,updated_at=? WHERE id=?`, at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectItemNotFound
	}
	return nil
}

func (r *ImageProjectRepository) MarkItemReady(ctx context.Context, id, path, mimeType string, width, height int, at time.Time) error {
	return r.markItem(ctx, id, "ready", &path, &mimeType, &width, &height, nil, at)
}

func (r *ImageProjectRepository) MarkItemFailed(ctx context.Context, id, message string, at time.Time) error {
	return r.markItem(ctx, id, "failed", nil, nil, nil, nil, &message, at)
}

func (r *ImageProjectRepository) MarkItemRegenerationFailed(ctx context.Context, id, previousStatus, message string, at time.Time) error {
	if previousStatus != "ready" && previousStatus != "pending" {
		previousStatus = "pending"
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_project_items SET status=?,error_message=?,updated_at=? WHERE id=? AND image_path IS NOT NULL`, previousStatus, message, at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectItemNotFound
	}
	return nil
}

func (r *ImageProjectRepository) markItem(ctx context.Context, id, status string, path, mimeType *string, width, height *int, message *string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE image_project_items SET status=?,image_path=?,mime_type=?,width=?,height=?,error_message=?,updated_at=? WHERE id=?`, status, path, mimeType, width, height, message, at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectItemNotFound
	}
	return nil
}

func (r *ImageProjectRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM image_projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return nil
}
