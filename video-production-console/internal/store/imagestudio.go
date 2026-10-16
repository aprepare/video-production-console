package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

var ErrImageGenerationNotFound = errors.New("image generation not found")
var ErrImageGenerationRunning = errors.New("image generation is running")

type ImageStudioRepository struct{ db *sql.DB }

func NewImageStudioRepository(db *sql.DB) *ImageStudioRepository {
	return &ImageStudioRepository{db: db}
}

// Fixed precision preserves chronological ordering for the TEXT pagination key.
const studioTimeFormat = "2006-01-02T15:04:05.000000000Z"

func studioTime(t time.Time) string { return t.UTC().Format(studioTimeFormat) }

const generationColumns = `id,prompt,final_prompt,enhance,style,custom_style,model,ratio,count,status,error,favorite,created_at,updated_at`
const outputColumns = `id,generation_id,position,status,path,mime_type,width,height,bytes,error,created_at,updated_at`

type studioScanner interface{ Scan(...any) error }

func scanGeneration(s studioScanner) (g domain.ImageGeneration, err error) {
	var created, updated string
	err = s.Scan(&g.ID, &g.Prompt, &g.FinalPrompt, &g.Enhance, &g.Style, &g.CustomStyle, &g.Model, &g.Ratio, &g.Count, &g.Status, &g.Error, &g.Favorite, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrImageGenerationNotFound
	}
	if err != nil {
		return
	}
	g.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return
	}
	g.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	g.Outputs = []domain.ImageGenerationOutput{}
	return
}
func scanOutput(s studioScanner) (o domain.ImageGenerationOutput, err error) {
	var created, updated string
	err = s.Scan(&o.ID, &o.GenerationID, &o.Position, &o.Status, &o.Path, &o.MIMEType, &o.Width, &o.Height, &o.Bytes, &o.Error, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrImageGenerationNotFound
	}
	if err != nil {
		return
	}
	o.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return
	}
	o.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if o.Status == "ready" {
		o.URL = "/api/image-studio/outputs/" + o.ID + "/image"
	}
	return
}

func (r *ImageStudioRepository) Create(ctx context.Context, g domain.ImageGeneration) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO image_generations (`+generationColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, g.ID, g.Prompt, g.FinalPrompt, g.Enhance, g.Style, g.CustomStyle, g.Model, g.Ratio, g.Count, g.Status, g.Error, g.Favorite, studioTime(g.CreatedAt), studioTime(g.UpdatedAt))
	if err != nil {
		return err
	}
	for _, o := range g.Outputs {
		_, err = tx.ExecContext(ctx, `INSERT INTO image_generation_outputs (`+outputColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, o.ID, g.ID, o.Position, o.Status, o.Path, o.MIMEType, o.Width, o.Height, o.Bytes, o.Error, studioTime(g.CreatedAt), studioTime(g.UpdatedAt))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ImageStudioRepository) Get(ctx context.Context, id string) (domain.ImageGeneration, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.ImageGeneration{}, err
	}
	defer tx.Rollback()
	g, err := scanGeneration(tx.QueryRowContext(ctx, `SELECT `+generationColumns+` FROM image_generations WHERE id=?`, id))
	if err != nil {
		return g, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+outputColumns+` FROM image_generation_outputs WHERE generation_id=? ORDER BY position`, id)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		o, e := scanOutput(rows)
		if e != nil {
			return g, e
		}
		g.Outputs = append(g.Outputs, o)
	}
	if err := rows.Err(); err != nil {
		return g, err
	}
	if err := rows.Close(); err != nil {
		return g, err
	}
	return g, tx.Commit()
}

type ImageGenerationListOptions struct {
	Limit    int
	Before   time.Time
	Favorite *bool
	Query    string
}

func (r *ImageStudioRepository) List(ctx context.Context, opt ImageGenerationListOptions) ([]domain.ImageGeneration, bool, error) {
	if opt.Limit < 1 || opt.Limit > 100 {
		opt.Limit = 30
	}
	where := []string{"1=1"}
	args := []any{}
	if !opt.Before.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, studioTime(opt.Before))
	}
	if opt.Favorite != nil {
		where = append(where, "favorite = ?")
		args = append(args, *opt.Favorite)
	}
	if opt.Query != "" {
		where = append(where, "instr(lower(prompt),lower(?)) > 0")
		args = append(args, opt.Query)
	}
	args = append(args, opt.Limit+1)
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM image_generations WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	more := len(ids) > opt.Limit
	if more {
		ids = ids[:opt.Limit]
	}
	items := make([]domain.ImageGeneration, 0, len(ids))
	for _, id := range ids {
		g, e := r.Get(ctx, id)
		if errors.Is(e, ErrImageGenerationNotFound) {
			continue
		}
		if e != nil {
			return nil, false, e
		}
		items = append(items, g)
	}
	return items, more, nil
}
func (r *ImageStudioRepository) Output(ctx context.Context, id string) (domain.ImageGenerationOutput, error) {
	return scanOutput(r.db.QueryRowContext(ctx, `SELECT `+outputColumns+` FROM image_generation_outputs WHERE id=?`, id))
}
func studioAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrImageGenerationNotFound
	}
	return nil
}
func (r *ImageStudioRepository) Favorite(ctx context.Context, id string, value bool) error {
	return studioAffected(r.db.ExecContext(ctx, `UPDATE image_generations SET favorite=?,updated_at=? WHERE id=?`, value, studioTime(time.Now()), id))
}
func (r *ImageStudioRepository) Delete(ctx context.Context, id string) error {
	return studioAffected(r.db.ExecContext(ctx, `DELETE FROM image_generations WHERE id=?`, id))
}
func (r *ImageStudioRepository) Running(ctx context.Context, id string) error {
	return studioAffected(r.db.ExecContext(ctx, `UPDATE image_generations SET status='running',error='',updated_at=? WHERE id=?`, studioTime(time.Now()), id))
}
func (r *ImageStudioRepository) FinalPrompt(ctx context.Context, id, prompt string) error {
	return studioAffected(r.db.ExecContext(ctx, `UPDATE image_generations SET final_prompt=?,updated_at=? WHERE id=?`, prompt, studioTime(time.Now()), id))
}
func (r *ImageStudioRepository) SaveOutput(ctx context.Context, o domain.ImageGenerationOutput) error {
	return studioAffected(r.db.ExecContext(ctx, `UPDATE image_generation_outputs SET status=?,path=?,mime_type=?,width=?,height=?,bytes=?,error=?,updated_at=? WHERE id=? AND status='pending'`, o.Status, o.Path, o.MIMEType, o.Width, o.Height, o.Bytes, o.Error, studioTime(time.Now()), o.ID))
}
func (r *ImageStudioRepository) FailPending(ctx context.Context, id, message string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE image_generation_outputs SET status='failed',error=?,updated_at=? WHERE generation_id=? AND status='pending'`, message, studioTime(time.Now()), id)
	return err
}
func (r *ImageStudioRepository) Finish(ctx context.Context, id, status, message string) error {
	return studioAffected(r.db.ExecContext(ctx, `UPDATE image_generations SET status=?,error=?,updated_at=? WHERE id=?`, status, message, studioTime(time.Now()), id))
}
func (r *ImageStudioRepository) Retry(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM image_generations WHERE id=?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrImageGenerationNotFound
		}
		return err
	}
	if status == "running" || status == "queued" {
		return ErrImageGenerationRunning
	}
	now := studioTime(time.Now())
	_, err = tx.ExecContext(ctx, `UPDATE image_generation_outputs SET status='pending',error='',updated_at=? WHERE generation_id=? AND status='failed'`, now, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_generations SET status='queued',error='',updated_at=? WHERE id=?`, now, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ImageStudioRepository) RecoverInterrupted(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := studioTime(time.Now())
	const message = "控制台重启中断"
	_, err = tx.ExecContext(ctx, `UPDATE image_generation_outputs SET status='failed',error=?,updated_at=? WHERE status='pending' AND generation_id IN (SELECT id FROM image_generations WHERE status IN ('queued','running'))`, message, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE image_generations SET status='failed',error=?,updated_at=? WHERE status IN ('queued','running')`, message, now)
	if err != nil {
		return err
	}
	return tx.Commit()
}
