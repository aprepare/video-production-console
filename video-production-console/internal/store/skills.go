package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"video-production-console/internal/domain"
)

var ErrSkillSnapshotNotFound = errors.New("skill snapshot not found")

type SkillRepository struct{ db *sql.DB }

func NewSkillRepository(db *sql.DB) *SkillRepository { return &SkillRepository{db: db} }

func (r *SkillRepository) Save(ctx context.Context, snapshot domain.SkillSnapshot) error {
	files, err := json.Marshal(snapshot.Files)
	if err != nil {
		return fmt.Errorf("encode skill snapshot files: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO skill_snapshots(id,name,path,sha256,files_json,modified_at,created_at) VALUES(?,?,?,?,?,?,?)`,
		snapshot.ID, snapshot.Name, snapshot.Path, snapshot.SHA256, string(files), snapshot.ModifiedAt, snapshot.CreatedAt)
	if err != nil {
		return fmt.Errorf("save skill snapshot: %w", err)
	}
	return nil
}

func (r *SkillRepository) Get(ctx context.Context, id string) (domain.SkillSnapshot, error) {
	return scanSkillSnapshot(r.db.QueryRowContext(ctx, `SELECT id,name,path,sha256,files_json,modified_at,created_at FROM skill_snapshots WHERE id=?`, id))
}

func (r *SkillRepository) Latest(ctx context.Context, name string) (domain.SkillSnapshot, error) {
	return scanSkillSnapshot(r.db.QueryRowContext(ctx, `SELECT id,name,path,sha256,files_json,modified_at,created_at FROM skill_snapshots WHERE name=? ORDER BY created_at DESC,rowid DESC LIMIT 1`, name))
}

func (r *SkillRepository) ListLatest(ctx context.Context) ([]domain.SkillSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,path,sha256,files_json,modified_at,created_at FROM (
        SELECT id,name,path,sha256,files_json,modified_at,created_at,
               ROW_NUMBER() OVER (PARTITION BY name ORDER BY created_at DESC,rowid DESC) AS rank
        FROM skill_snapshots
    ) WHERE rank=1 ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list skill snapshots: %w", err)
	}
	defer rows.Close()
	var snapshots []domain.SkillSnapshot
	for rows.Next() {
		snapshot, err := scanSkillSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill snapshots: %w", err)
	}
	return snapshots, nil
}

type skillSnapshotScanner interface{ Scan(...any) error }

func scanSkillSnapshot(row skillSnapshotScanner) (domain.SkillSnapshot, error) {
	var snapshot domain.SkillSnapshot
	var files string
	err := row.Scan(&snapshot.ID, &snapshot.Name, &snapshot.Path, &snapshot.SHA256, &files, &snapshot.ModifiedAt, &snapshot.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SkillSnapshot{}, ErrSkillSnapshotNotFound
	}
	if err != nil {
		return domain.SkillSnapshot{}, fmt.Errorf("read skill snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(files), &snapshot.Files); err != nil {
		return domain.SkillSnapshot{}, errors.New("stored skill snapshot file list is invalid")
	}
	return snapshot, nil
}
