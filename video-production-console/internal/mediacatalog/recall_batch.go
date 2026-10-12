package mediacatalog

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// hydrateRecallBatch uses a bounded number of queries, in the caller's read
// transaction. At most 400 parameters avoids old SQLite variable limits.
func hydrateRecallBatch(ctx context.Context, tx *sql.Tx, ids []string) ([]RecalledShot, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	shots := map[string]Shot{}
	rows, err := tx.QueryContext(ctx, shotSelect+" WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		shot, e := scanShot(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		shots[shot.ID] = shot
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	sources := map[string]Source{}
	rows, err = tx.QueryContext(ctx, sourceSelect+" WHERE id IN (SELECT source_id FROM media_shots WHERE id IN ("+placeholders+"))", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		source, e := scanSource(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		sources[source.ID] = source
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	tags := map[string][]Tag{}
	rows, err = tx.QueryContext(ctx, "SELECT shot_id,namespace,value,confidence FROM media_tags WHERE shot_id IN ("+placeholders+") ORDER BY shot_id,namespace,value", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tag Tag
		if e := rows.Scan(&tag.ShotID, &tag.Namespace, &tag.Value, &tag.Confidence); e != nil {
			rows.Close()
			return nil, e
		}
		tags[tag.ShotID] = append(tags[tag.ShotID], tag)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	vectors := map[string][]float32{}
	rows, err = tx.QueryContext(ctx, "SELECT id,embedding_dimension,embedding_blob FROM media_shots WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var dimension int
		var blob []byte
		if e := rows.Scan(&id, &dimension, &blob); e != nil {
			rows.Close()
			return nil, e
		}
		if dimension > 0 && dimension <= len(blob)/4 && len(blob) == 4*dimension {
			vector := make([]float32, dimension)
			for i := range vector {
				vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
			}
			vectors[id] = vector
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]RecalledShot, 0, len(ids))
	for _, id := range ids {
		shot, ok := shots[id]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrShotNotFound, id)
		}
		source, ok := sources[shot.SourceID]
		if !ok {
			return nil, ErrSourceNotFound
		}
		shotTags := tags[id]
		if shotTags == nil {
			shotTags = []Tag{}
		}
		out = append(out, RecalledShot{Shot: shot, Source: source, Tags: shotTags, Embedding: vectors[id]})
	}
	return out, nil
}
