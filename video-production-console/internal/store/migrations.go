package store

import (
	"database/sql"
	"fmt"
)

var migrations = []string{
	`CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    background_asset_id TEXT REFERENCES assets(id) ON DELETE SET NULL,
    color TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX accounts_name_uq ON accounts(name) WHERE status='active';

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('topic', 'script', 'assets', 'mixing', 'review', 'ready', 'published', 'archived')),
    topic_card_path TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    ready_at DATETIME,
    published_at DATETIME,
    publish_note TEXT
);
CREATE INDEX projects_account_stage_idx ON projects(account_id, stage);

CREATE TABLE assets (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('continuous_script', 'spoken_script', 'audio', 'subtitle', 'account_background', 'mix_draft', 'final_video')),
    path TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    version INTEGER NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    source_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL
);
CREATE INDEX assets_project_type_idx ON assets(project_id, type, version DESC);

CREATE TABLE codex_tasks (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'waiting_input', 'completed', 'failed', 'cancelled')),
    codex_session_id TEXT,
    prompt_snapshot TEXT NOT NULL,
    result_summary TEXT,
    error_code TEXT,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    started_at DATETIME,
    finished_at DATETIME
);
CREATE INDEX tasks_status_created_idx ON codex_tasks(status, created_at);

CREATE TABLE task_events (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    kind TEXT NOT NULL,
    level TEXT NOT NULL,
    display_text TEXT NOT NULL,
    raw_json TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX task_events_seq_uq ON task_events(task_id, sequence);

CREATE TABLE task_messages (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    question_schema TEXT,
    created_at DATETIME NOT NULL
);

INSERT INTO settings(key, value) VALUES ('max_codex_concurrency', '2')
ON CONFLICT(key) DO NOTHING;`,
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
    )`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	for i, migration := range migrations {
		version := i + 1
		var applied int
		err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied != 0 {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.Exec(migration); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}
