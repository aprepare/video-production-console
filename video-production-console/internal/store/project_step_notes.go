package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrStepNotesNotFound = errors.New("project step notes not found")
	ErrInvalidStepNotes  = errors.New("invalid project step")
)

const projectStepRemix = "remix"

// ProjectStepNotes is the project-scoped revision guidance for one production step.
type ProjectStepNotes struct {
	ProjectID string
	Step      string
	Notes     string
	UpdatedAt time.Time
}

// ProjectStepNotesRepository persists lightweight per-step notes that are not assets.
type ProjectStepNotesRepository struct {
	db *sql.DB
}

func NewProjectStepNotesRepository(db *sql.DB) *ProjectStepNotesRepository {
	return &ProjectStepNotesRepository{db: db}
}

func normalizeProjectStep(step string) (string, error) {
	switch strings.TrimSpace(step) {
	case projectStepRemix:
		return projectStepRemix, nil
	default:
		return "", ErrInvalidStepNotes
	}
}

func (r *ProjectStepNotesRepository) Get(ctx context.Context, projectID, step string) (ProjectStepNotes, error) {
	normalized, err := normalizeProjectStep(step)
	if err != nil {
		return ProjectStepNotes{}, err
	}
	var notes ProjectStepNotes
	err = r.db.QueryRowContext(ctx, `SELECT project_id, step, notes, updated_at FROM project_step_notes WHERE project_id=? AND step=?`, projectID, normalized).
		Scan(&notes.ProjectID, &notes.Step, &notes.Notes, &notes.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectStepNotes{}, ErrStepNotesNotFound
	}
	if err != nil {
		return ProjectStepNotes{}, fmt.Errorf("read project step notes: %w", err)
	}
	return notes, nil
}

func (r *ProjectStepNotesRepository) Upsert(ctx context.Context, projectID, step, notes string, updatedAt time.Time) (ProjectStepNotes, error) {
	normalized, err := normalizeProjectStep(step)
	if err != nil {
		return ProjectStepNotes{}, err
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	notes = strings.TrimSpace(notes)
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id=?`, projectID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ProjectStepNotes{}, ErrProjectNotFound
	} else if err != nil {
		return ProjectStepNotes{}, fmt.Errorf("verify project for step notes: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO project_step_notes(project_id, step, notes, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(project_id, step) DO UPDATE SET notes=excluded.notes, updated_at=excluded.updated_at`,
		projectID, normalized, notes, updatedAt); err != nil {
		return ProjectStepNotes{}, fmt.Errorf("upsert project step notes: %w", err)
	}
	return ProjectStepNotes{ProjectID: projectID, Step: normalized, Notes: notes, UpdatedAt: updatedAt}, nil
}
