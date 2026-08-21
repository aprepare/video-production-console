package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
	"unicode/utf8"

	"video-production-console/internal/domain"
)

var ErrImageProjectNotFound = errors.New("image project not found")
var ErrImageProjectItemNotFound = errors.New("image project item not found")
var ErrImageProjectOutputModeLocked = errors.New("image project output mode locked")

type ImageProjectRepository struct{ db *sql.DB }

func NewImageProjectRepository(db *sql.DB) *ImageProjectRepository {
	return &ImageProjectRepository{db: db}
}

const imageProjectSelectCols = `id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at,selected_position,run_mode,run_phase,run_status,phase_error,publishing_error,success_count,failure_count,image_attempts,text_model,reasoning_effort,image_model,output_mode,output_mode_locked_at,account_id,template_version,template_fingerprint`

func normalizeImageProject(project domain.ImageProject) domain.ImageProject {
	if project.OutputMode == "" {
		project.OutputMode = domain.ImageProjectOutputModeImageSlideshow
	}
	if project.RunMode == "" {
		project.RunMode = "manual"
	}
	if project.RunPhase == "" {
		project.RunPhase = "idle"
	}
	if project.RunStatus == "" {
		project.RunStatus = "idle"
	}
	if project.ImageAttempts < 1 || project.ImageAttempts > 4 {
		project.ImageAttempts = 2
	}
	return project
}

func scanImageProject(scanner interface{ Scan(dest ...any) error }, project *domain.ImageProject) error {
	var selected sql.NullInt64
	var outputMode, accountID, templateVersion, templateFingerprint sql.NullString
	var outputModeLockedAt sql.NullTime
	if err := scanner.Scan(
		&project.ID, &project.Title, &project.Script, &project.ImageCount, &project.Ratio, &project.Style, &project.CustomStyle,
		&project.Concurrency, &project.Status, &project.CreatedAt, &project.UpdatedAt, &selected,
		&project.RunMode, &project.RunPhase, &project.RunStatus, &project.PhaseError, &project.PublishingError,
		&project.SuccessCount, &project.FailureCount, &project.ImageAttempts, &project.TextModel, &project.ReasoningEffort, &project.ImageModel,
		&outputMode, &outputModeLockedAt, &accountID, &templateVersion, &templateFingerprint,
	); err != nil {
		return err
	}
	if selected.Valid {
		value := int(selected.Int64)
		project.SelectedPosition = &value
	}
	if outputMode.Valid {
		project.OutputMode = domain.ImageProjectOutputMode(outputMode.String)
	}
	if outputModeLockedAt.Valid {
		value := outputModeLockedAt.Time
		project.OutputModeLockedAt = &value
	}
	if accountID.Valid {
		value := accountID.String
		project.AccountID = &value
	}
	if templateVersion.Valid {
		project.TemplateVersion = templateVersion.String
	}
	if templateFingerprint.Valid {
		project.TemplateFingerprint = templateFingerprint.String
	}
	*project = normalizeImageProject(*project)
	return nil
}

func scanImageProjectItem(scanner interface{ Scan(dest ...any) error }, item *domain.ImageProjectItem) error {
	var imagePath, mimeType, errorMessage sql.NullString
	var width, height sql.NullInt64
	if err := scanner.Scan(
		&item.ID, &item.ProjectID, &item.Sequence, &item.Role, &item.SourceText, &item.Title, &item.Prompt, &item.Status,
		&imagePath, &mimeType, &width, &height, &errorMessage, &item.CreatedAt, &item.UpdatedAt, &item.AttemptCount,
	); err != nil {
		return err
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
	return nil
}

func validPublishingCandidates(candidates []domain.PublishingCandidate) bool {
	if len(candidates) != 5 {
		return false
	}
	seen := map[int]bool{}
	for _, candidate := range candidates {
		if candidate.Position < 1 || candidate.Position > 5 || seen[candidate.Position] || strings.TrimSpace(candidate.Title) == "" || utf8.RuneCountInString(candidate.Title) > 22 || utf8.RuneCountInString(candidate.Description) > 1000 {
			return false
		}
		seen[candidate.Position] = true
	}
	return true
}

func (r *ImageProjectRepository) Create(ctx context.Context, project domain.ImageProject, items []domain.ImageProjectItem) (returnErr error) {
	if len(project.PublishingCandidates) != 0 && len(project.PublishingCandidates) != 5 {
		return errors.New("publishing candidates must contain exactly 5 items")
	}
	seen := map[int]bool{}
	for _, c := range project.PublishingCandidates {
		if c.Position < 1 || c.Position > 5 || seen[c.Position] || strings.TrimSpace(c.Title) == "" || utf8.RuneCountInString(c.Title) > 22 || utf8.RuneCountInString(c.Description) > 1000 {
			return errors.New("invalid publishing candidate")
		}
		seen[c.Position] = true
	}
	project = normalizeImageProject(project)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = tx.Rollback()
		}
	}()
	_, err = tx.ExecContext(ctx, `INSERT INTO image_projects(id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at,selected_position,run_mode,run_phase,run_status,phase_error,publishing_error,success_count,failure_count,image_attempts,text_model,reasoning_effort,image_model,output_mode,output_mode_locked_at,account_id,template_version,template_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, project.ID, project.Title, project.Script, project.ImageCount, project.Ratio, project.Style, project.CustomStyle, project.Concurrency, project.Status, project.CreatedAt, project.UpdatedAt, project.SelectedPosition, project.RunMode, project.RunPhase, project.RunStatus, project.PhaseError, project.PublishingError, project.SuccessCount, project.FailureCount, project.ImageAttempts, project.TextModel, project.ReasoningEffort, project.ImageModel, project.OutputMode, project.OutputModeLockedAt, project.AccountID, project.TemplateVersion, project.TemplateFingerprint)
	if err != nil {
		return err
	}
	for _, item := range items {
		role := item.Role
		if role == "" {
			if item.Sequence == 1 {
				role = "cover"
			} else {
				role = "content"
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO image_project_items(id,project_id,sequence,role,source_text,title,prompt,status,created_at,updated_at,attempt_count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, item.ID, project.ID, item.Sequence, role, item.SourceText, item.Title, item.Prompt, item.Status, item.CreatedAt, item.UpdatedAt, item.AttemptCount)
		if err != nil {
			return err
		}
	}
	for _, c := range project.PublishingCandidates {
		_, err = tx.ExecContext(ctx, `INSERT INTO image_project_publishing_candidates(id,project_id,position,title,description) VALUES(?,?,?,?,?)`, uuid.NewString(), project.ID, c.Position, c.Title, c.Description)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ImageProjectRepository) List(ctx context.Context) ([]domain.ImageProject, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+imageProjectSelectCols+` FROM image_projects WHERE run_mode != 'video' ORDER BY updated_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []domain.ImageProject{}
	for rows.Next() {
		var project domain.ImageProject
		if err := scanImageProject(rows, &project); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (r *ImageProjectRepository) ListVideo(ctx context.Context) ([]domain.ImageProject, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+imageProjectSelectCols+` FROM image_projects WHERE run_mode = 'video' ORDER BY updated_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []domain.ImageProject{}
	for rows.Next() {
		var project domain.ImageProject
		if err := scanImageProject(rows, &project); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (r *ImageProjectRepository) Get(ctx context.Context, id string) (domain.ImageProject, []domain.ImageProjectItem, error) {
	var project domain.ImageProject
	err := scanImageProject(r.db.QueryRowContext(ctx, `SELECT `+imageProjectSelectCols+` FROM image_projects WHERE id=?`, id), &project)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ImageProject{}, nil, ErrImageProjectNotFound
	}
	if err != nil {
		return domain.ImageProject{}, nil, err
	}
	rowsPub, err := r.db.QueryContext(ctx, `SELECT position,title,description FROM image_project_publishing_candidates WHERE project_id=? ORDER BY position`, id)
	if err != nil {
		return domain.ImageProject{}, nil, err
	}
	for rowsPub.Next() {
		var c domain.PublishingCandidate
		if err := rowsPub.Scan(&c.Position, &c.Title, &c.Description); err != nil {
			rowsPub.Close()
			return domain.ImageProject{}, nil, err
		}
		project.PublishingCandidates = append(project.PublishingCandidates, c)
	}
	if err := rowsPub.Close(); err != nil {
		return domain.ImageProject{}, nil, err
	}
	if err := rowsPub.Err(); err != nil {
		return domain.ImageProject{}, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,project_id,sequence,role,source_text,title,prompt,status,image_path,mime_type,width,height,error_message,created_at,updated_at,attempt_count FROM image_project_items WHERE project_id=? ORDER BY sequence`, id)
	if err != nil {
		return domain.ImageProject{}, nil, err
	}
	defer rows.Close()
	items := []domain.ImageProjectItem{}
	for rows.Next() {
		var item domain.ImageProjectItem
		if err := scanImageProjectItem(rows, &item); err != nil {
			return domain.ImageProject{}, nil, err
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
	result, err := r.db.ExecContext(ctx, `UPDATE image_project_items SET
 source_text=?,title=?,prompt=?,
 status=CASE WHEN source_text<>? OR title<>? OR prompt<>? THEN 'pending' ELSE status END,
 error_message=CASE WHEN source_text<>? OR title<>? OR prompt<>? THEN NULL ELSE error_message END,
 updated_at=? WHERE id=? AND project_id=?`, sourceText, title, prompt, sourceText, title, prompt, sourceText, title, prompt, at, itemID, projectID)
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

func (r *ImageProjectRepository) UpdatePublishingCandidate(ctx context.Context, projectID string, c domain.PublishingCandidate) error {
	if c.Position < 1 || c.Position > 5 || strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Description) == "" || utf8.RuneCountInString(c.Title) > 22 || utf8.RuneCountInString(c.Description) > 1000 {
		return errors.New("invalid publishing candidate")
	}
	res, err := r.db.ExecContext(ctx, `UPDATE image_project_publishing_candidates SET title=?,description=? WHERE project_id=? AND position=?`, c.Title, c.Description, projectID, c.Position)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrImageProjectNotFound
	}
	return nil
}
func (r *ImageProjectRepository) SelectPublishingPosition(ctx context.Context, projectID string, pos int) error {
	res, err := r.db.ExecContext(ctx, `UPDATE image_projects SET selected_position=?,updated_at=? WHERE id=? AND EXISTS (SELECT 1 FROM image_project_publishing_candidates WHERE project_id=? AND position=?)`, pos, time.Now().UTC(), projectID, projectID, pos)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrImageProjectNotFound
	}
	return nil
}
func (r *ImageProjectRepository) ReplacePublishingCandidates(ctx context.Context, projectID string, cs []domain.PublishingCandidate) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_project_publishing_candidates WHERE project_id=?`, projectID); err != nil {
		return err
	}
	for _, c := range cs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_project_publishing_candidates(id,project_id,position,title,description) VALUES(?,?,?,?,?)`, uuid.NewString(), projectID, c.Position, c.Title, c.Description); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_projects SET selected_position=1 WHERE id=?`, projectID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ImageProjectRepository) SetRunState(ctx context.Context, id, phase, status, phaseError string, successCount, failureCount int, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE image_projects SET run_phase=?,run_status=?,phase_error=?,success_count=?,failure_count=?,updated_at=? WHERE id=?`, phase, status, phaseError, successCount, failureCount, at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return nil
}

func (r *ImageProjectRepository) SaveQuickPlan(ctx context.Context, id, title string, items []domain.ImageProjectItem, candidates []domain.PublishingCandidate, publishingError string, at time.Time) (returnErr error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 120 {
		return errors.New("invalid project title")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_project_items WHERE project_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_project_publishing_candidates WHERE project_id=?`, id); err != nil {
		return err
	}
	for _, item := range items {
		role := item.Role
		if role == "" {
			if item.Sequence == 1 {
				role = "cover"
			} else {
				role = "content"
			}
		}
		itemID := item.ID
		if itemID == "" {
			itemID = uuid.NewString()
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_project_items(id,project_id,sequence,role,source_text,title,prompt,status,created_at,updated_at,attempt_count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, itemID, id, item.Sequence, role, item.SourceText, item.Title, item.Prompt, item.Status, at, at, item.AttemptCount); err != nil {
			return err
		}
	}
	for _, candidate := range candidates {
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_project_publishing_candidates(id,project_id,position,title,description) VALUES(?,?,?,?,?)`, uuid.NewString(), id, candidate.Position, candidate.Title, candidate.Description); err != nil {
			return err
		}
	}
	var selected any
	if validPublishingCandidates(candidates) {
		selected = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE image_projects SET title=?,image_count=?,publishing_error=?,run_phase='prompting',selected_position=?,updated_at=? WHERE id=?`, title, len(items), publishingError, selected, at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return tx.Commit()
}

func (r *ImageProjectRepository) SavePrompts(ctx context.Context, projectID string, prompts []string, at time.Time) (returnErr error) {
	_, items, err := r.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if len(prompts) != len(items) {
		return errors.New("prompt count must match project items")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = tx.Rollback()
		}
	}()
	for index, item := range items {
		if _, err = tx.ExecContext(ctx, `UPDATE image_project_items SET prompt=?,updated_at=? WHERE id=? AND project_id=?`, prompts[index], at, item.ID, projectID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE image_projects SET run_phase='imaging',updated_at=? WHERE id=?`, at, projectID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return tx.Commit()
}

func (r *ImageProjectRepository) MarkItemAttempts(ctx context.Context, itemID string, attempts int, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE image_project_items SET attempt_count=?,updated_at=? WHERE id=?`, attempts, at, itemID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectItemNotFound
	}
	return nil
}

func (r *ImageProjectRepository) MarkRunningQuickProjectsInterrupted(ctx context.Context, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE image_projects SET run_status='interrupted',updated_at=? WHERE run_mode IN ('quick','video') AND run_status='running'`, at)
	return err
}

func (r *ImageProjectRepository) SaveVideoPlan(ctx context.Context, id, title string, items []domain.ImageProjectItem, at time.Time) (returnErr error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 120 {
		return errors.New("invalid project title")
	}
	if len(items) < 1 || len(items) > 60 {
		return errors.New("video scene count must be between 1 and 60")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM image_project_items WHERE project_id=?`, id); err != nil {
		return err
	}
	for _, item := range items {
		role := item.Role
		if role == "" {
			if item.Sequence == 1 {
				role = "cover"
			} else {
				role = "content"
			}
		}
		itemID := item.ID
		if itemID == "" {
			itemID = uuid.NewString()
		}
		if strings.TrimSpace(item.Prompt) == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.SourceText) == "" {
			return errors.New("video scene title, source text, and prompt are required")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_project_items(id,project_id,sequence,role,source_text,title,prompt,status,created_at,updated_at,attempt_count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, itemID, id, item.Sequence, role, item.SourceText, item.Title, item.Prompt, item.Status, at, at, item.AttemptCount); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE image_projects SET title=?,image_count=?,run_phase='imaging',updated_at=? WHERE id=? AND run_mode='video'`, title, len(items), at, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return tx.Commit()
}

func (r *ImageProjectRepository) UpdateProjectTitle(ctx context.Context, projectID, title string, at time.Time) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 120 {
		return errors.New("invalid project title")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_projects SET title=?,updated_at=? WHERE id=?`, title, at, projectID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrImageProjectNotFound
	}
	return nil
}

func (r *ImageProjectRepository) UpdateOutputMode(ctx context.Context, projectID string, outputMode domain.ImageProjectOutputMode, at time.Time) error {
	if outputMode != domain.ImageProjectOutputModeImageSlideshow && outputMode != domain.ImageProjectOutputModeImageToVideo {
		return errors.New("invalid image project output mode")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE image_projects SET output_mode=?,updated_at=? WHERE id=? AND output_mode_locked_at IS NULL`, outputMode, at, projectID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 1 {
		return nil
	}
	var lockedAt sql.NullTime
	err = r.db.QueryRowContext(ctx, `SELECT output_mode_locked_at FROM image_projects WHERE id=?`, projectID).Scan(&lockedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrImageProjectNotFound
	}
	if err != nil {
		return err
	}
	if lockedAt.Valid {
		return ErrImageProjectOutputModeLocked
	}
	return ErrImageProjectNotFound
}
