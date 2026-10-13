package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-production-console/internal/platformapi"
)

var ErrReferenceNotFound = errors.New("reference post not found")

// ReferencePost is one collected 同行 post. Caption is the share text the
// author typed; Transcript is the spoken script from 通义听悟 and is the
// only field suitable as 二创原文.
type ReferencePost struct {
	ID               string    `json:"id"`
	Platform         string    `json:"platform"`
	ExternalID       string    `json:"external_id"`
	URL              string    `json:"url"`
	Title            string    `json:"title"`
	Caption          string    `json:"caption"`
	Author           string    `json:"author"`
	CoverURL         string    `json:"cover_url"`
	VideoURL         string    `json:"-"`
	MediaStatus      string    `json:"media_status"`
	MediaPath        string    `json:"-"`
	MediaSize        int64     `json:"media_size"`
	MediaError       string    `json:"media_error"`
	CoverPath        string    `json:"-"`
	DurationS        float64   `json:"duration_s"`
	Likes            int64     `json:"likes"`
	Comments         int64     `json:"comments"`
	Shares           int64     `json:"shares"`
	Collects         int64     `json:"collects"`
	Plays            int64     `json:"plays"`
	PostedAt         int64     `json:"posted_at"`
	Keyword          string    `json:"keyword"`
	MerchantID       string    `json:"merchant_id"`
	TranscriptStatus string    `json:"transcript_status"`
	TranscriptTaskID string    `json:"-"`
	Transcript       string    `json:"transcript"`
	TranscriptJSON   string    `json:"-"`
	TranscriptError  string    `json:"transcript_error"`
	Notes            string    `json:"notes"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// HasVideo reports whether a video URL or a cached source is available.
func (p ReferencePost) HasVideo() bool {
	return strings.TrimSpace(p.VideoURL) != "" || p.MediaStatus == "ready"
}

func (p ReferencePost) MarshalJSON() ([]byte, error) {
	type record ReferencePost
	sentences := []platformapi.Sentence{}
	if err := json.Unmarshal([]byte(p.TranscriptJSON), &sentences); err != nil || sentences == nil {
		sentences = []platformapi.Sentence{}
	}
	return json.Marshal(struct {
		record
		HasVideo  bool                   `json:"has_video"`
		HasCover  bool                   `json:"has_cover"`
		Sentences []platformapi.Sentence `json:"sentences"`
	}{record(p), p.HasVideo(), strings.TrimSpace(p.CoverURL) != "" || p.CoverPath != "", sentences})
}

type ReferenceRepository struct{ db *sql.DB }

func NewReferenceRepository(db *sql.DB) *ReferenceRepository { return &ReferenceRepository{db: db} }

const referenceColumns = `id, platform, external_id, url, title, caption, author, cover_url, video_url, duration_s,
	likes, comments, shares, collects, plays, posted_at, keyword, merchant_id, transcript_status, transcript_task_id,
	transcript, transcript_json, transcript_error, notes, created_at, updated_at,
	media_status, media_path, media_size, media_error, cover_path`

func scanReference(row interface{ Scan(...any) error }) (ReferencePost, error) {
	var p ReferencePost
	err := row.Scan(&p.ID, &p.Platform, &p.ExternalID, &p.URL, &p.Title, &p.Caption, &p.Author, &p.CoverURL, &p.VideoURL, &p.DurationS,
		&p.Likes, &p.Comments, &p.Shares, &p.Collects, &p.Plays, &p.PostedAt, &p.Keyword, &p.MerchantID, &p.TranscriptStatus, &p.TranscriptTaskID,
		&p.Transcript, &p.TranscriptJSON, &p.TranscriptError, &p.Notes, &p.CreatedAt, &p.UpdatedAt,
		&p.MediaStatus, &p.MediaPath, &p.MediaSize, &p.MediaError, &p.CoverPath)
	return p, err
}

// Upsert stores a post keyed by (platform, external_id). Re-collecting the
// same post refreshes its counts and media URL but never discards an
// existing transcript, notes, merchant link or cached media.
func (r *ReferenceRepository) Upsert(ctx context.Context, p ReferencePost) (ReferencePost, error) {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.TranscriptStatus == "" {
		p.TranscriptStatus = "none"
	}
	if p.MediaStatus == "" {
		p.MediaStatus = "none"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO reference_posts(`+referenceColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(platform, external_id) DO UPDATE SET
			url=excluded.url, title=excluded.title, caption=excluded.caption, author=excluded.author,
			cover_url=excluded.cover_url,
			video_url=CASE WHEN excluded.video_url<>'' THEN excluded.video_url ELSE reference_posts.video_url END,
			duration_s=excluded.duration_s, likes=excluded.likes, comments=excluded.comments, shares=excluded.shares,
			collects=excluded.collects, plays=excluded.plays, posted_at=excluded.posted_at,
			keyword=CASE WHEN reference_posts.keyword='' THEN excluded.keyword ELSE reference_posts.keyword END,
			updated_at=excluded.updated_at`,
		p.ID, p.Platform, p.ExternalID, p.URL, p.Title, p.Caption, p.Author, p.CoverURL, p.VideoURL, p.DurationS,
		p.Likes, p.Comments, p.Shares, p.Collects, p.Plays, p.PostedAt, p.Keyword, p.MerchantID, p.TranscriptStatus, p.TranscriptTaskID,
		p.Transcript, p.TranscriptJSON, p.TranscriptError, p.Notes, p.CreatedAt, p.UpdatedAt,
		p.MediaStatus, p.MediaPath, p.MediaSize, p.MediaError, p.CoverPath)
	if err != nil {
		return ReferencePost{}, fmt.Errorf("upsert reference post: %w", err)
	}
	return r.getBy(ctx, `platform=? AND external_id=?`, p.Platform, p.ExternalID)
}

func (r *ReferenceRepository) Get(ctx context.Context, id string) (ReferencePost, error) {
	return r.getBy(ctx, `id=?`, id)
}

func (r *ReferenceRepository) getBy(ctx context.Context, where string, args ...any) (ReferencePost, error) {
	p, err := scanReference(r.db.QueryRowContext(ctx, `SELECT `+referenceColumns+` FROM reference_posts WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return ReferencePost{}, ErrReferenceNotFound
	}
	if err != nil {
		return ReferencePost{}, fmt.Errorf("read reference post: %w", err)
	}
	return p, nil
}

// List returns posts newest first, optionally filtered by text, platform,
// merchant or transcript state.
func (r *ReferenceRepository) List(ctx context.Context, query, platform, merchantID, status string, limit int) ([]ReferencePost, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where := []string{"1=1"}
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		where = append(where, `(title LIKE ? OR caption LIKE ? OR author LIKE ? OR transcript LIKE ? OR keyword LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like, like)
	}
	if platform != "" {
		where = append(where, `platform=?`)
		args = append(args, platform)
	}
	if merchantID != "" {
		where = append(where, `merchant_id=?`)
		args = append(args, merchantID)
	}
	if status != "" {
		where = append(where, `transcript_status=?`)
		args = append(args, status)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `SELECT `+referenceColumns+` FROM reference_posts WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC, id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list reference posts: %w", err)
	}
	defer rows.Close()
	out := []ReferencePost{}
	for rows.Next() {
		p, err := scanReference(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reference post: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Running lists posts whose transcription was submitted but not finished,
// so a restarted console can keep polling them.
func (r *ReferenceRepository) Running(ctx context.Context) ([]ReferencePost, error) {
	return r.List(ctx, "", "", "", "running", 500)
}

func (r *ReferenceRepository) MarkTranscriptRunning(ctx context.Context, id, taskID string) error {
	return r.update(ctx, `transcript_status='running', transcript_task_id=?, transcript_error=''`, id, taskID)
}

func (r *ReferenceRepository) CompleteTranscript(ctx context.Context, id, text, sentencesJSON string) error {
	return r.update(ctx, `transcript_status='completed', transcript=?, transcript_json=?, transcript_error=''`, id, text, sentencesJSON)
}

func (r *ReferenceRepository) FailTranscript(ctx context.Context, id, message string) error {
	return r.update(ctx, `transcript_status='failed', transcript_error=?`, id, message)
}

func (r *ReferenceRepository) MarkMediaDownloading(ctx context.Context, id string) error {
	return r.update(ctx, `media_status='downloading', media_error=''`, id)
}

func (r *ReferenceRepository) CompleteMedia(ctx context.Context, id, path string, size int64) error {
	return r.update(ctx, `media_status='ready', media_path=?, media_size=?, media_error=''`, id, path, size)
}

func (r *ReferenceRepository) FailMedia(ctx context.Context, id, message string) error {
	return r.update(ctx, `media_status='failed', media_path='', media_size=0, media_error=?`, id, message)
}

func (r *ReferenceRepository) SetCoverPath(ctx context.Context, id, path string) error {
	return r.update(ctx, `cover_path=?`, id, path)
}

// FailInterruptedMedia leaves downloads started in this process untouched.
func (r *ReferenceRepository) FailInterruptedMedia(ctx context.Context, active []string) error {
	query := `UPDATE reference_posts SET media_status='failed', media_error=?, updated_at=? WHERE media_status='downloading'`
	args := []any{"控制台重启，下载中断，请点重新下载", time.Now().UTC()}
	if len(active) > 0 {
		query += ` AND id NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(active)), ",") + `)`
		for _, id := range active {
			args = append(args, id)
		}
	}
	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("fail interrupted reference media: %w", err)
	}
	return nil
}

// UpdateEditable changes the operator-owned fields.
func (r *ReferenceRepository) UpdateEditable(ctx context.Context, id, transcript, notes, merchantID string) error {
	return r.update(ctx, `transcript=?, notes=?, merchant_id=?,
		transcript_status=CASE WHEN ?<>'' AND transcript_status<>'running' THEN 'completed' ELSE transcript_status END`,
		id, transcript, notes, merchantID, strings.TrimSpace(transcript))
}

func (r *ReferenceRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM reference_posts WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete reference post: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrReferenceNotFound
	}
	return nil
}

func (r *ReferenceRepository) update(ctx context.Context, set, id string, args ...any) error {
	args = append(args, time.Now().UTC(), id)
	result, err := r.db.ExecContext(ctx, `UPDATE reference_posts SET `+set+`, updated_at=? WHERE id=?`, args...)
	if err != nil {
		return fmt.Errorf("update reference post: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrReferenceNotFound
	}
	return nil
}
