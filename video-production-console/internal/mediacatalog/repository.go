package mediacatalog

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// CatalogFileName is the fixed catalog database name inside the media root.
const CatalogFileName = "catalog.db"

// Repository owns media_root/catalog.db. All writes run inside BEGIN
// IMMEDIATE transactions and only relative paths are persisted.
type Repository struct {
	db   *sql.DB
	root string
	now  func() time.Time
}

var catalogSchema = []string{
	`CREATE TABLE IF NOT EXISTS media_sources (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL CHECK (kind IN ('movie','broll','image')),
		subtype TEXT NOT NULL CHECK (subtype IN ('video','photo','chart','illustration','generated')),
		origin TEXT NOT NULL CHECK (origin IN ('local','generated','pexels','pixabay')),
		relative_path TEXT NOT NULL,
		sha256 TEXT NOT NULL UNIQUE,
		size_bytes INTEGER NOT NULL DEFAULT 0,
		mime_type TEXT NOT NULL DEFAULT '',
		width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		fps REAL NOT NULL DEFAULT 0,
		status TEXT NOT NULL CHECK (status IN ('pending_probe','ready','failed')),
		error_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS media_shots (
		id TEXT PRIMARY KEY,
		source_id TEXT NOT NULL REFERENCES media_sources(id) ON DELETE CASCADE,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		source_in_ms INTEGER NOT NULL CHECK (source_in_ms >= 0),
		source_out_ms INTEGER NOT NULL CHECK (source_out_ms >= 0),
		duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
		analysis_status TEXT NOT NULL DEFAULT 'pending' CHECK (analysis_status IN ('pending','completed','failed')),
		summary TEXT NOT NULL DEFAULT '',
		mood TEXT NOT NULL DEFAULT '',
		setting TEXT NOT NULL DEFAULT '',
		people_count INTEGER NOT NULL DEFAULT 0,
		motion_level TEXT NOT NULL DEFAULT '',
		has_text INTEGER NOT NULL DEFAULT 0,
		embedding_model TEXT NOT NULL DEFAULT '',
		embedding_dimension INTEGER NOT NULL DEFAULT 0,
		embedding_blob BLOB,
		analysis_version TEXT NOT NULL DEFAULT '',
		UNIQUE (source_id, ordinal)
	)`,
	`CREATE TABLE IF NOT EXISTS media_keyframes (
		id TEXT PRIMARY KEY,
		shot_id TEXT NOT NULL REFERENCES media_shots(id) ON DELETE CASCADE,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		relative_path TEXT NOT NULL,
		at_ms INTEGER NOT NULL CHECK (at_ms >= 0),
		width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0,
		sha256 TEXT NOT NULL,
		UNIQUE (shot_id, ordinal)
	)`,
	`CREATE TABLE IF NOT EXISTS media_tags (
		shot_id TEXT NOT NULL REFERENCES media_shots(id) ON DELETE CASCADE,
		namespace TEXT NOT NULL,
		value TEXT NOT NULL,
		confidence REAL NOT NULL DEFAULT 0,
		PRIMARY KEY (shot_id, namespace, value)
	)`,
	`CREATE TABLE IF NOT EXISTS media_rights (
		source_id TEXT PRIMARY KEY REFERENCES media_sources(id) ON DELETE CASCADE,
		source_url TEXT NOT NULL DEFAULT '',
		creator TEXT NOT NULL DEFAULT '',
		license_code TEXT NOT NULL DEFAULT '',
		license_url TEXT NOT NULL DEFAULT '',
		attribution TEXT NOT NULL DEFAULT '',
		retrieved_at TEXT NOT NULL DEFAULT '',
		rights_notes TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS media_jobs (
		id TEXT PRIMARY KEY,
		source_id TEXT NOT NULL REFERENCES media_sources(id) ON DELETE CASCADE,
		phase TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed')),
		completed_units INTEGER NOT NULL DEFAULT 0,
		total_units INTEGER NOT NULL DEFAULT 0,
		error_code TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		started_at TEXT,
		finished_at TEXT,
		UNIQUE (source_id, phase)
	)`,
}

// Open creates or opens media_root/catalog.db. The media root must be an
// absolute existing directory; it becomes the containment boundary for every
// relative path stored in the catalog.
func Open(mediaRoot string) (*Repository, error) {
	if strings.TrimSpace(mediaRoot) == "" || !filepath.IsAbs(mediaRoot) {
		return nil, fmt.Errorf("media root must be an absolute path")
	}
	canonical, err := filepath.EvalSymlinks(mediaRoot)
	if err != nil {
		return nil, fmt.Errorf("canonicalize media root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("media root must be an existing directory")
	}
	dbPath := filepath.Join(canonical, CatalogFileName)
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsnPath := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(dsnPath, "/") {
		dsnPath = "/" + dsnPath
	}
	dsn := (&url.URL{Scheme: "file", Path: dsnPath, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open media catalog: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect media catalog: %w", err)
	}
	repo := &Repository{db: db, root: canonical, now: time.Now}
	if err := repo.withImmediateTx(context.Background(), func(conn *sql.Conn) error {
		for _, statement := range catalogSchema {
			if _, err := conn.ExecContext(context.Background(), statement); err != nil {
				return fmt.Errorf("create media catalog schema: %w", err)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *Repository) Close() error { return r.db.Close() }

// Root returns the canonical media root that bounds every stored path.
func (r *Repository) Root() string { return r.root }

// ResolvePath joins a stored relative path with the canonical media root and
// verifies the result is still a contained regular file. Absolute paths,
// ".." segments, and symlink escapes are rejected.
func (r *Repository) ResolvePath(relative string) (string, error) {
	if err := ValidateRelativePath(relative); err != nil {
		return "", err
	}
	joined := filepath.Join(r.root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("resolve media file %q: %w", relative, err)
	}
	if !catalogPathInside(r.root, resolved) {
		return "", fmt.Errorf("%w: %q", ErrUnsafeRelativePath, relative)
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %q must name a regular file", ErrInvalidValue, relative)
	}
	return resolved, nil
}

func catalogPathInside(root, path string) bool {
	if filepath.Separator == '\\' {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (r *Repository) withImmediateTx(ctx context.Context, fn func(conn *sql.Conn) error) (returnErr error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire media catalog connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin media catalog transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if _, rollbackErr := conn.ExecContext(context.Background(), `ROLLBACK`); rollbackErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("roll back media catalog transaction: %w", rollbackErr))
			}
		}
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit media catalog transaction: %w", err)
	}
	committed = true
	return nil
}

// UpsertSource inserts a new source or, when the same SHA-256 already exists,
// returns the stored row unchanged so repeated imports stay idempotent.
func (r *Repository) UpsertSource(ctx context.Context, source Source) (Source, bool, error) {
	if err := source.Validate(); err != nil {
		return Source{}, false, err
	}
	var stored Source
	created := false
	err := r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		existing, err := scanSource(conn.QueryRowContext(ctx, sourceSelect+` WHERE sha256=?`, strings.ToLower(source.SHA256)))
		if err == nil {
			stored = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read media source by sha256: %w", err)
		}
		if source.ID == "" {
			source.ID = uuid.NewString()
		}
		now := r.now().UTC()
		source.SHA256 = strings.ToLower(source.SHA256)
		source.CreatedAt, source.UpdatedAt = now, now
		if _, err := conn.ExecContext(ctx, `INSERT INTO media_sources
			(id,kind,subtype,origin,relative_path,sha256,size_bytes,mime_type,width,height,duration_ms,fps,status,error_code,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			source.ID, string(source.Kind), string(source.Subtype), string(source.Origin),
			source.RelativePath, source.SHA256, source.SizeBytes, source.MIMEType,
			source.Width, source.Height, source.DurationMS, source.FPS,
			string(source.Status), source.ErrorCode, formatTime(now), formatTime(now)); err != nil {
			return fmt.Errorf("insert media source: %w", err)
		}
		stored, created = source, true
		return nil
	})
	if err != nil {
		return Source{}, false, err
	}
	return stored, created, nil
}

const sourceSelect = `SELECT id,kind,subtype,origin,relative_path,sha256,size_bytes,mime_type,width,height,duration_ms,fps,status,error_code,created_at,updated_at FROM media_sources`

type rowScanner interface{ Scan(...any) error }

func scanSource(row rowScanner) (Source, error) {
	var source Source
	var createdAt, updatedAt string
	if err := row.Scan(&source.ID, &source.Kind, &source.Subtype, &source.Origin,
		&source.RelativePath, &source.SHA256, &source.SizeBytes, &source.MIMEType,
		&source.Width, &source.Height, &source.DurationMS, &source.FPS,
		&source.Status, &source.ErrorCode, &createdAt, &updatedAt); err != nil {
		return Source{}, err
	}
	source.CreatedAt = parseTime(createdAt)
	source.UpdatedAt = parseTime(updatedAt)
	return source, nil
}

func (r *Repository) SourceByID(ctx context.Context, id string) (Source, error) {
	source, err := scanSource(r.db.QueryRowContext(ctx, sourceSelect+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrSourceNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("read media source: %w", err)
	}
	return source, nil
}

func (r *Repository) SourceBySHA256(ctx context.Context, digest string) (Source, error) {
	source, err := scanSource(r.db.QueryRowContext(ctx, sourceSelect+` WHERE sha256=?`, strings.ToLower(digest)))
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrSourceNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("read media source: %w", err)
	}
	return source, nil
}

func (r *Repository) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := r.db.QueryContext(ctx, sourceSelect+` ORDER BY relative_path`)
	if err != nil {
		return nil, fmt.Errorf("list media sources: %w", err)
	}
	defer rows.Close()
	sources := []Source{}
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("read media source row: %w", err)
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (r *Repository) UpdateSourceStatus(ctx context.Context, id string, status SourceStatus, errorCode string) error {
	switch status {
	case SourceStatusPendingProbe, SourceStatusReady, SourceStatusFailed:
	default:
		return fmt.Errorf("%w: status %q", ErrInvalidValue, status)
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `UPDATE media_sources SET status=?, error_code=?, updated_at=? WHERE id=?`,
			string(status), errorCode, formatTime(r.now().UTC()), id)
		if err != nil {
			return fmt.Errorf("update media source status: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrSourceNotFound
		}
		return nil
	})
}

// InsertShot validates the shot window against its source inside the write
// transaction: 0 <= in < out <= source duration, no overlap with existing
// shots. Image sources never take ranged shots (see EnsureImageShot).
func (r *Repository) InsertShot(ctx context.Context, shot Shot) (Shot, error) {
	if err := shot.validateBasics(); err != nil {
		return Shot{}, err
	}
	err := r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		source, err := scanSource(conn.QueryRowContext(ctx, sourceSelect+` WHERE id=?`, shot.SourceID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSourceNotFound
		}
		if err != nil {
			return fmt.Errorf("read shot source: %w", err)
		}
		if source.Kind == SourceKindImage {
			return fmt.Errorf("%w: image sources only take the degenerate whole-image shot", ErrInvalidValue)
		}
		if shot.SourceInMS < 0 || shot.SourceInMS >= shot.SourceOutMS || shot.SourceOutMS > source.DurationMS {
			return fmt.Errorf("%w: shot window [%d,%d) escapes source duration %d", ErrInvalidValue, shot.SourceInMS, shot.SourceOutMS, source.DurationMS)
		}
		var overlapping int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_shots WHERE source_id=? AND source_in_ms < ? AND source_out_ms > ?`,
			shot.SourceID, shot.SourceOutMS, shot.SourceInMS).Scan(&overlapping); err != nil {
			return fmt.Errorf("check shot overlap: %w", err)
		}
		if overlapping > 0 {
			return fmt.Errorf("%w: shot window overlaps an existing shot", ErrInvalidValue)
		}
		return insertShotRow(ctx, conn, &shot)
	})
	if err != nil {
		return Shot{}, err
	}
	return shot, nil
}

// EnsureImageShot creates (or returns) the single degenerate whole-image shot
// (in=0, out=0) that carries an image's analysis result and embedding.
func (r *Repository) EnsureImageShot(ctx context.Context, sourceID string) (Shot, error) {
	var shot Shot
	err := r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		source, err := scanSource(conn.QueryRowContext(ctx, sourceSelect+` WHERE id=?`, sourceID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSourceNotFound
		}
		if err != nil {
			return fmt.Errorf("read image source: %w", err)
		}
		if source.Kind != SourceKindImage {
			return fmt.Errorf("%w: degenerate shots are reserved for image sources", ErrInvalidValue)
		}
		existing, err := scanShot(conn.QueryRowContext(ctx, shotSelect+` WHERE source_id=? ORDER BY ordinal LIMIT 1`, sourceID))
		if err == nil {
			shot = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read image shot: %w", err)
		}
		shot = Shot{SourceID: sourceID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 0}
		return insertShotRow(ctx, conn, &shot)
	})
	if err != nil {
		return Shot{}, err
	}
	return shot, nil
}

func insertShotRow(ctx context.Context, conn *sql.Conn, shot *Shot) error {
	if shot.ID == "" {
		shot.ID = uuid.NewString()
	}
	if shot.AnalysisStatus == "" {
		shot.AnalysisStatus = AnalysisPending
	}
	shot.DurationMS = shot.SourceOutMS - shot.SourceInMS
	if _, err := conn.ExecContext(ctx, `INSERT INTO media_shots
		(id,source_id,ordinal,source_in_ms,source_out_ms,duration_ms,analysis_status,summary,mood,setting,people_count,motion_level,has_text,analysis_version)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		shot.ID, shot.SourceID, shot.Ordinal, shot.SourceInMS, shot.SourceOutMS, shot.DurationMS,
		string(shot.AnalysisStatus), shot.Summary, shot.Mood, shot.Setting,
		shot.PeopleCount, shot.MotionLevel, shot.HasText, shot.AnalysisVersion); err != nil {
		return fmt.Errorf("insert media shot: %w", err)
	}
	return nil
}

const shotSelect = `SELECT id,source_id,ordinal,source_in_ms,source_out_ms,duration_ms,analysis_status,summary,mood,setting,people_count,motion_level,has_text,analysis_version FROM media_shots`

func scanShot(row rowScanner) (Shot, error) {
	var shot Shot
	if err := row.Scan(&shot.ID, &shot.SourceID, &shot.Ordinal, &shot.SourceInMS, &shot.SourceOutMS,
		&shot.DurationMS, &shot.AnalysisStatus, &shot.Summary, &shot.Mood, &shot.Setting,
		&shot.PeopleCount, &shot.MotionLevel, &shot.HasText, &shot.AnalysisVersion); err != nil {
		return Shot{}, err
	}
	return shot, nil
}

func (r *Repository) ShotsBySource(ctx context.Context, sourceID string) ([]Shot, error) {
	rows, err := r.db.QueryContext(ctx, shotSelect+` WHERE source_id=? ORDER BY ordinal`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list media shots: %w", err)
	}
	defer rows.Close()
	shots := []Shot{}
	for rows.Next() {
		shot, err := scanShot(rows)
		if err != nil {
			return nil, fmt.Errorf("read media shot row: %w", err)
		}
		shots = append(shots, shot)
	}
	return shots, rows.Err()
}

// SetShotEmbedding stores the vector as a little-endian float32 blob alongside
// the model name, dimension, and analysis version that make it reproducible.
func (r *Repository) SetShotEmbedding(ctx context.Context, shotID, model string, vector []float32, analysisVersion string) error {
	if strings.TrimSpace(model) == "" || len(vector) == 0 || strings.TrimSpace(analysisVersion) == "" {
		return fmt.Errorf("%w: embeddings require model, vector, and analysis version", ErrInvalidValue)
	}
	blob := make([]byte, 4*len(vector))
	for i, value := range vector {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(value))
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `UPDATE media_shots SET embedding_model=?, embedding_dimension=?, embedding_blob=?, analysis_version=? WHERE id=?`,
			model, len(vector), blob, analysisVersion, shotID)
		if err != nil {
			return fmt.Errorf("write shot embedding: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrShotNotFound
		}
		return nil
	})
}

func (r *Repository) ShotEmbedding(ctx context.Context, shotID string) (Embedding, error) {
	var embedding Embedding
	var blob []byte
	err := r.db.QueryRowContext(ctx, `SELECT embedding_model, embedding_dimension, embedding_blob, analysis_version FROM media_shots WHERE id=?`, shotID).
		Scan(&embedding.Model, &embedding.Dimension, &blob, &embedding.AnalysisVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Embedding{}, ErrShotNotFound
	}
	if err != nil {
		return Embedding{}, fmt.Errorf("read shot embedding: %w", err)
	}
	if len(blob) != 4*embedding.Dimension {
		return Embedding{}, fmt.Errorf("%w: embedding blob length %d does not match dimension %d", ErrInvalidValue, len(blob), embedding.Dimension)
	}
	embedding.Vector = make([]float32, embedding.Dimension)
	for i := range embedding.Vector {
		embedding.Vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return embedding, nil
}

func (r *Repository) InsertKeyframe(ctx context.Context, keyframe Keyframe) (Keyframe, error) {
	if err := keyframe.Validate(); err != nil {
		return Keyframe{}, err
	}
	if keyframe.ID == "" {
		keyframe.ID = uuid.NewString()
	}
	keyframe.SHA256 = strings.ToLower(keyframe.SHA256)
	err := r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `INSERT INTO media_keyframes
			(id,shot_id,ordinal,relative_path,at_ms,width,height,sha256) VALUES(?,?,?,?,?,?,?,?)`,
			keyframe.ID, keyframe.ShotID, keyframe.Ordinal, keyframe.RelativePath,
			keyframe.AtMS, keyframe.Width, keyframe.Height, keyframe.SHA256); err != nil {
			return fmt.Errorf("insert media keyframe: %w", err)
		}
		return nil
	})
	if err != nil {
		return Keyframe{}, err
	}
	return keyframe, nil
}

func (r *Repository) KeyframesByShot(ctx context.Context, shotID string) ([]Keyframe, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,shot_id,ordinal,relative_path,at_ms,width,height,sha256 FROM media_keyframes WHERE shot_id=? ORDER BY ordinal`, shotID)
	if err != nil {
		return nil, fmt.Errorf("list media keyframes: %w", err)
	}
	defer rows.Close()
	keyframes := []Keyframe{}
	for rows.Next() {
		var keyframe Keyframe
		if err := rows.Scan(&keyframe.ID, &keyframe.ShotID, &keyframe.Ordinal, &keyframe.RelativePath,
			&keyframe.AtMS, &keyframe.Width, &keyframe.Height, &keyframe.SHA256); err != nil {
			return nil, fmt.Errorf("read media keyframe row: %w", err)
		}
		keyframes = append(keyframes, keyframe)
	}
	return keyframes, rows.Err()
}

func (r *Repository) UpsertTags(ctx context.Context, shotID string, tags []Tag) error {
	for _, tag := range tags {
		if err := tag.Validate(); err != nil {
			return err
		}
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		for _, tag := range tags {
			if _, err := conn.ExecContext(ctx, `INSERT INTO media_tags(shot_id,namespace,value,confidence)
				VALUES(?,?,?,?) ON CONFLICT(shot_id,namespace,value) DO UPDATE SET confidence=excluded.confidence`,
				shotID, tag.Namespace, tag.Value, tag.Confidence); err != nil {
				return fmt.Errorf("write media tag: %w", err)
			}
		}
		return nil
	})
}

func (r *Repository) TagsByShot(ctx context.Context, shotID string) ([]Tag, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT shot_id,namespace,value,confidence FROM media_tags WHERE shot_id=? ORDER BY namespace,value`, shotID)
	if err != nil {
		return nil, fmt.Errorf("list media tags: %w", err)
	}
	defer rows.Close()
	tags := []Tag{}
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ShotID, &tag.Namespace, &tag.Value, &tag.Confidence); err != nil {
			return nil, fmt.Errorf("read media tag row: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (r *Repository) UpsertRights(ctx context.Context, rights Rights) error {
	if err := rights.Validate(); err != nil {
		return err
	}
	retrievedAt := ""
	if !rights.RetrievedAt.IsZero() {
		retrievedAt = formatTime(rights.RetrievedAt.UTC())
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `INSERT INTO media_rights
			(source_id,source_url,creator,license_code,license_url,attribution,retrieved_at,rights_notes)
			VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(source_id) DO UPDATE SET source_url=excluded.source_url, creator=excluded.creator,
			license_code=excluded.license_code, license_url=excluded.license_url, attribution=excluded.attribution,
			retrieved_at=excluded.retrieved_at, rights_notes=excluded.rights_notes`,
			rights.SourceID, rights.SourceURL, rights.Creator, rights.LicenseCode,
			rights.LicenseURL, rights.Attribution, retrievedAt, rights.RightsNotes); err != nil {
			return fmt.Errorf("write media rights: %w", err)
		}
		return nil
	})
}

func (r *Repository) RightsBySource(ctx context.Context, sourceID string) (Rights, error) {
	var rights Rights
	var retrievedAt string
	err := r.db.QueryRowContext(ctx, `SELECT source_id,source_url,creator,license_code,license_url,attribution,retrieved_at,rights_notes FROM media_rights WHERE source_id=?`, sourceID).
		Scan(&rights.SourceID, &rights.SourceURL, &rights.Creator, &rights.LicenseCode,
			&rights.LicenseURL, &rights.Attribution, &retrievedAt, &rights.RightsNotes)
	if errors.Is(err, sql.ErrNoRows) {
		return Rights{}, ErrRightsNotFound
	}
	if err != nil {
		return Rights{}, fmt.Errorf("read media rights: %w", err)
	}
	if retrievedAt != "" {
		rights.RetrievedAt = parseTime(retrievedAt)
	}
	return rights, nil
}

// EnsureJob creates a pending job for (source, phase) or returns the stored
// one, which keeps interrupted indexing runs resumable and idempotent.
func (r *Repository) EnsureJob(ctx context.Context, sourceID, phase string, totalUnits int) (Job, error) {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(phase) == "" || totalUnits < 0 {
		return Job{}, fmt.Errorf("%w: jobs require a source, phase, and non-negative units", ErrInvalidValue)
	}
	var job Job
	err := r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		existing, err := scanJob(conn.QueryRowContext(ctx, jobSelect+` WHERE source_id=? AND phase=?`, sourceID, phase))
		if err == nil {
			job = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read media job: %w", err)
		}
		job = Job{ID: uuid.NewString(), SourceID: sourceID, Phase: phase, Status: JobPending, TotalUnits: totalUnits}
		if _, err := conn.ExecContext(ctx, `INSERT INTO media_jobs(id,source_id,phase,status,completed_units,total_units) VALUES(?,?,?,?,0,?)`,
			job.ID, job.SourceID, job.Phase, string(job.Status), job.TotalUnits); err != nil {
			return fmt.Errorf("insert media job: %w", err)
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

const jobSelect = `SELECT id,source_id,phase,status,completed_units,total_units,error_code,error_message,started_at,finished_at FROM media_jobs`

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var startedAt, finishedAt sql.NullString
	if err := row.Scan(&job.ID, &job.SourceID, &job.Phase, &job.Status, &job.CompletedUnits,
		&job.TotalUnits, &job.ErrorCode, &job.ErrorMessage, &startedAt, &finishedAt); err != nil {
		return Job{}, err
	}
	if startedAt.Valid && startedAt.String != "" {
		parsed := parseTime(startedAt.String)
		job.StartedAt = &parsed
	}
	if finishedAt.Valid && finishedAt.String != "" {
		parsed := parseTime(finishedAt.String)
		job.FinishedAt = &parsed
	}
	return job, nil
}

func (r *Repository) JobBySourcePhase(ctx context.Context, sourceID, phase string) (Job, error) {
	job, err := scanJob(r.db.QueryRowContext(ctx, jobSelect+` WHERE source_id=? AND phase=?`, sourceID, phase))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("read media job: %w", err)
	}
	return job, nil
}

func (r *Repository) StartJob(ctx context.Context, jobID string) error {
	return r.updateJob(ctx, jobID, `UPDATE media_jobs SET status='running', started_at=?, error_code='', error_message='' WHERE id=?`,
		formatTime(r.now().UTC()), jobID)
}

func (r *Repository) CompleteJob(ctx context.Context, jobID string, completedUnits int) error {
	return r.updateJob(ctx, jobID, `UPDATE media_jobs SET status='completed', completed_units=?, finished_at=?, error_code='', error_message='' WHERE id=?`,
		completedUnits, formatTime(r.now().UTC()), jobID)
}

func (r *Repository) FailJob(ctx context.Context, jobID, errorCode, errorMessage string) error {
	return r.updateJob(ctx, jobID, `UPDATE media_jobs SET status='failed', finished_at=?, error_code=?, error_message=? WHERE id=?`,
		formatTime(r.now().UTC()), errorCode, errorMessage, jobID)
}

func (r *Repository) updateJob(ctx context.Context, jobID, statement string, args ...any) error {
	if strings.TrimSpace(jobID) == "" {
		return ErrJobNotFound
	}
	return r.withImmediateTx(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, statement, args...)
		if err != nil {
			return fmt.Errorf("update media job: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrJobNotFound
		}
		return nil
	})
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
