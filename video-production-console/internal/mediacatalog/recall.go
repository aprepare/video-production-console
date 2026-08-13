package mediacatalog

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrCatalogUnavailable is the sentinel montagescript and the planner use
// when the catalog file cannot be opened or queried.
var ErrCatalogUnavailable = errors.New("media_catalog_unavailable")

const recallMaxCandidates = 50

// RecalledShot is one catalog row plus the tags and optional embedding the
// planner needs for four-level recall. It never includes absolute movie paths.
type RecalledShot struct {
	Shot      Shot
	Source    Source
	Tags      []Tag
	Embedding []float32
}

// RecallByTags returns up to limit ready+analyzed shots whose tag values
// match any of the supplied terms (case-insensitive).
func (r *Repository) RecallByTags(ctx context.Context, tags []string, limit int) ([]RecalledShot, error) {
	terms := uniqueFolded(tags)
	if len(terms) == 0 {
		return nil, nil
	}
	limit = clampRecallLimit(limit)
	placeholders := strings.Repeat("?,", len(terms))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(terms)+1)
	for _, term := range terms {
		args = append(args, term)
	}
	args = append(args, limit)
	query := `SELECT DISTINCT s.id FROM media_shots s
		JOIN media_tags t ON t.shot_id = s.id
		JOIN media_sources src ON src.id = s.source_id
		WHERE src.status = 'ready' AND s.analysis_status = 'completed'
		  AND LOWER(t.value) IN (` + placeholders + `)
		ORDER BY s.id
		LIMIT ?`
	return r.hydrateShotIDs(ctx, query, args...)
}

// RecallByMoodSetting returns up to limit ready+analyzed shots whose mood
// or setting matches (case-insensitive). Empty filters are ignored.
func (r *Repository) RecallByMoodSetting(ctx context.Context, mood, setting string, limit int) ([]RecalledShot, error) {
	mood = strings.ToLower(strings.TrimSpace(mood))
	setting = strings.ToLower(strings.TrimSpace(setting))
	if mood == "" && setting == "" {
		return nil, nil
	}
	limit = clampRecallLimit(limit)
	var clauses []string
	args := make([]any, 0, 3)
	if mood != "" {
		clauses = append(clauses, "LOWER(s.mood) = ?")
		args = append(args, mood)
	}
	if setting != "" {
		clauses = append(clauses, "LOWER(s.setting) = ?")
		args = append(args, setting)
	}
	args = append(args, limit)
	query := `SELECT s.id FROM media_shots s
		JOIN media_sources src ON src.id = s.source_id
		WHERE src.status = 'ready' AND s.analysis_status = 'completed'
		  AND (` + strings.Join(clauses, " OR ") + `)
		ORDER BY s.id
		LIMIT ?`
	return r.hydrateShotIDs(ctx, query, args...)
}

// RecallReadyShots returns up to limit ready-source shots for embedding
// scans and the neutral fallback pool.
func (r *Repository) RecallReadyShots(ctx context.Context, limit int) ([]RecalledShot, error) {
	limit = clampRecallLimit(limit)
	query := `SELECT s.id FROM media_shots s
		JOIN media_sources src ON src.id = s.source_id
		WHERE src.status = 'ready'
		ORDER BY s.id
		LIMIT ?`
	return r.hydrateShotIDs(ctx, query, limit)
}

func clampRecallLimit(limit int) int {
	if limit <= 0 || limit > recallMaxCandidates {
		return recallMaxCandidates
	}
	return limit
}

func uniqueFolded(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		folded := strings.ToLower(strings.TrimSpace(value))
		if folded == "" || seen[folded] {
			continue
		}
		seen[folded] = true
		out = append(out, folded)
	}
	return out
}

func (r *Repository) hydrateShotIDs(ctx context.Context, query string, args ...any) ([]RecalledShot, error) {
	if r == nil || r.db == nil {
		return nil, ErrCatalogUnavailable
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	defer rows.Close()
	ids := make([]string, 0, recallMaxCandidates)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	out := make([]RecalledShot, 0, len(ids))
	for _, id := range ids {
		recalled, err := r.loadRecalledShot(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, recalled)
	}
	return out, nil
}

func (r *Repository) loadRecalledShot(ctx context.Context, shotID string) (RecalledShot, error) {
	shot, err := scanShot(r.db.QueryRowContext(ctx, shotSelect+` WHERE id=?`, shotID))
	if errors.Is(err, sql.ErrNoRows) {
		return RecalledShot{}, ErrShotNotFound
	}
	if err != nil {
		return RecalledShot{}, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	source, err := r.SourceByID(ctx, shot.SourceID)
	if err != nil {
		return RecalledShot{}, err
	}
	tags, err := r.TagsByShot(ctx, shot.ID)
	if err != nil {
		return RecalledShot{}, err
	}
	var blob []byte
	var dimension int
	if err := r.db.QueryRowContext(ctx, `SELECT embedding_dimension, embedding_blob FROM media_shots WHERE id=?`, shot.ID).
		Scan(&dimension, &blob); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RecalledShot{}, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	var embedding []float32
	if dimension > 0 && len(blob) == 4*dimension {
		embedding = make([]float32, dimension)
		for i := range embedding {
			embedding[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
		}
	}
	return RecalledShot{Shot: shot, Source: source, Tags: tags, Embedding: embedding}, nil
}
