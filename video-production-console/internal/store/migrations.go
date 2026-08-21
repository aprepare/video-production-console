package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
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
    type TEXT NOT NULL CHECK (type IN ('continuous_script', 'spoken_script', 'word_timing', 'audio', 'subtitle', 'account_background', 'mix_draft', 'final_video')),
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
	`CREATE UNIQUE INDEX assets_project_type_version_uq ON assets(project_id, type, version) WHERE project_id IS NOT NULL;`,
	`CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    password_changed_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE auth_sessions (
    id TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    csrf_hash TEXT NOT NULL,
    remote_addr TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX auth_sessions_admin_idx ON auth_sessions(admin_id, expires_at);

CREATE TABLE encrypted_secrets (
    key TEXT PRIMARY KEY,
    ciphertext TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    updated_at DATETIME NOT NULL
);

CREATE TABLE skill_snapshots (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    files_json TEXT NOT NULL,
    modified_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX skill_snapshots_name_created_idx ON skill_snapshots(name, created_at DESC);

ALTER TABLE projects ADD COLUMN publication_status TEXT NOT NULL DEFAULT 'draft'
    CHECK (publication_status IN ('draft', 'producing', 'ready_to_publish', 'published', 'archived'));
UPDATE projects SET publication_status = CASE stage
    WHEN 'topic' THEN 'draft'
    WHEN 'script' THEN 'draft'
    WHEN 'assets' THEN 'draft'
    WHEN 'mixing' THEN 'producing'
    WHEN 'review' THEN 'producing'
    WHEN 'ready' THEN 'ready_to_publish'
    WHEN 'published' THEN 'published'
    WHEN 'archived' THEN 'archived'
END;
CREATE INDEX projects_account_publication_idx ON projects(account_id, publication_status, created_at);

CREATE TABLE asset_items (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('source_script', 'topic_card', 'continuous_script', 'spoken_script', 'narration', 'word_timing', 'subtitle_srt', 'account_background', 'mix_draft', 'final_video')),
    current_version_id TEXT REFERENCES asset_versions(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX asset_items_scope_type_uq
    ON asset_items(IFNULL(project_id, ''), account_id, type);

CREATE TABLE asset_versions (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL REFERENCES asset_items(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('source_script', 'topic_card', 'continuous_script', 'spoken_script', 'narration', 'word_timing', 'subtitle_srt', 'account_background', 'mix_draft', 'final_video')),
    version INTEGER NOT NULL CHECK (version > 0),
    storage_kind TEXT NOT NULL DEFAULT 'file' CHECK (storage_kind IN ('file', 'directory')),
    path TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    parent_version_id TEXT REFERENCES asset_versions(id) ON DELETE SET NULL,
    source_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    state TEXT NOT NULL CHECK (state IN ('missing', 'ready', 'stale', 'generating', 'failed')),
    stale_reason TEXT,
    created_at DATETIME NOT NULL,
    UNIQUE(asset_id, version)
);
CREATE INDEX asset_versions_asset_created_idx ON asset_versions(asset_id, version DESC);
CREATE INDEX asset_versions_project_type_idx ON asset_versions(project_id, type, created_at DESC);

CREATE TABLE asset_dependencies (
    asset_version_id TEXT NOT NULL REFERENCES asset_versions(id) ON DELETE CASCADE,
    depends_on_version_id TEXT NOT NULL REFERENCES asset_versions(id) ON DELETE RESTRICT,
    PRIMARY KEY(asset_version_id, depends_on_version_id),
    CHECK (asset_version_id <> depends_on_version_id)
);
CREATE INDEX asset_dependencies_upstream_idx ON asset_dependencies(depends_on_version_id);

INSERT INTO asset_items(id, project_id, account_id, type, created_at, updated_at)
SELECT MIN(id), project_id, account_id,
       CASE type WHEN 'audio' THEN 'narration' WHEN 'subtitle' THEN 'subtitle_srt' ELSE type END,
       MIN(created_at), MAX(created_at)
FROM assets
GROUP BY project_id, account_id,
         CASE type WHEN 'audio' THEN 'narration' WHEN 'subtitle' THEN 'subtitle_srt' ELSE type END;

WITH ranked_legacy AS (
    SELECT legacy.*,
           item.id AS logical_asset_id,
           ROW_NUMBER() OVER (
               PARTITION BY item.id
               ORDER BY legacy.version, legacy.created_at, legacy.id
           ) AS logical_version
    FROM assets AS legacy
    JOIN asset_items AS item
      ON item.project_id IS legacy.project_id
     AND item.account_id = legacy.account_id
     AND item.type = CASE legacy.type WHEN 'audio' THEN 'narration' WHEN 'subtitle' THEN 'subtitle_srt' ELSE legacy.type END
)
INSERT INTO asset_versions(
    id, asset_id, project_id, account_id, type, version, storage_kind,
    path, filename, mime_type, size, sha256, source_task_id, state, created_at
)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))),2) || '-' ||
       substr('89ab',abs(random()) % 4 + 1,1) || substr(lower(hex(randomblob(2))),2) || '-' ||
       lower(hex(randomblob(6))),
       logical_asset_id, project_id, account_id,
       CASE legacy.type WHEN 'audio' THEN 'narration' WHEN 'subtitle' THEN 'subtitle_srt' ELSE legacy.type END,
       logical_version, 'file', path, filename, mime_type,
       size, sha256, source_task_id,
       CASE
           WHEN status = 'active' THEN 'ready'
           WHEN status IN ('missing', 'ready', 'stale', 'generating', 'failed') THEN status
           ELSE 'ready'
       END,
       created_at
FROM ranked_legacy AS legacy;

UPDATE asset_versions AS child
SET parent_version_id = (
    SELECT parent.id
    FROM asset_versions AS parent
    WHERE parent.asset_id = child.asset_id AND parent.version < child.version
    ORDER BY parent.version DESC, parent.created_at DESC, parent.id DESC
    LIMIT 1
);
UPDATE asset_items AS item
SET current_version_id = (
        SELECT version.id FROM asset_versions AS version
        WHERE version.asset_id = item.id
        ORDER BY version.version DESC, version.created_at DESC, version.id DESC
        LIMIT 1
    ),
    updated_at = COALESCE((
        SELECT MAX(version.created_at) FROM asset_versions AS version
        WHERE version.asset_id = item.id
    ), item.updated_at);

ALTER TABLE accounts ADD COLUMN background_asset_item_id TEXT REFERENCES asset_items(id) ON DELETE SET NULL;
UPDATE accounts
SET background_asset_item_id = (
    SELECT item.id
    FROM assets AS legacy
    JOIN asset_items AS item
      ON item.project_id IS legacy.project_id
     AND item.account_id = legacy.account_id
     AND item.type = CASE legacy.type WHEN 'audio' THEN 'narration' WHEN 'subtitle' THEN 'subtitle_srt' ELSE legacy.type END
    WHERE legacy.id = accounts.background_asset_id
    LIMIT 1
);

CREATE TABLE codex_tasks_v2 (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    action TEXT,
    write_scope TEXT,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'awaiting_input', 'resuming', 'completed', 'failed', 'canceled', 'interrupted', 'waiting_input')),
    codex_session_id TEXT,
    skill_snapshot_id TEXT REFERENCES skill_snapshots(id) ON DELETE SET NULL,
    config_snapshot_json TEXT,
    manifest_path TEXT,
    prompt_path TEXT,
    output_schema_path TEXT,
    output_last_message_path TEXT,
    queue_reason TEXT,
    prompt_snapshot TEXT NOT NULL,
    result_summary TEXT,
    error_code TEXT,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    started_at DATETIME,
    finished_at DATETIME
);
INSERT INTO codex_tasks_v2(
    id, project_id, account_id, type, skill_name, status, codex_session_id,
    prompt_snapshot, result_summary, error_code, error_message, created_at,
    started_at, finished_at
)
SELECT id, project_id, account_id, type, skill_name,
       CASE status WHEN 'waiting_input' THEN 'awaiting_input' WHEN 'cancelled' THEN 'canceled' ELSE status END,
       codex_session_id, prompt_snapshot, result_summary, error_code, error_message,
       created_at, started_at, finished_at
FROM codex_tasks;
DROP TABLE codex_tasks;
ALTER TABLE codex_tasks_v2 RENAME TO codex_tasks;
CREATE INDEX tasks_status_created_idx ON codex_tasks(status, created_at);
CREATE INDEX tasks_project_created_idx ON codex_tasks(project_id, created_at DESC);

CREATE TABLE task_messages_v2 (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    question_schema TEXT,
    created_at DATETIME NOT NULL
);
INSERT INTO task_messages_v2(id, task_id, role, content, question_schema, created_at)
SELECT id, task_id, role, content, question_schema, created_at FROM task_messages;
DROP TABLE task_messages;
ALTER TABLE task_messages_v2 RENAME TO task_messages;
CREATE INDEX task_messages_task_created_idx ON task_messages(task_id, created_at, id);

CREATE TABLE idea_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    selected_id TEXT REFERENCES idea_candidates(id) ON DELETE SET NULL,
    topic_card_path TEXT,
    topic_card_state TEXT,
    topic_card_sha256 TEXT,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idea_sessions_updated_idx ON idea_sessions(updated_at DESC);

CREATE TABLE idea_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES idea_sessions(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX idea_messages_session_created_idx ON idea_messages(session_id, created_at, id);

CREATE TABLE idea_candidates (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES idea_sessions(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    position INTEGER NOT NULL,
    title TEXT NOT NULL,
    summary TEXT NOT NULL,
    score REAL NOT NULL,
    source TEXT NOT NULL,
    selected INTEGER NOT NULL DEFAULT 0 CHECK (selected IN (0, 1)),
    created_at DATETIME NOT NULL,
    UNIQUE(session_id, position)
);

CREATE TABLE task_artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    path TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX task_artifacts_task_kind_idx ON task_artifacts(task_id, kind, created_at);

CREATE TABLE task_relations (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    related_task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE(task_id, related_task_id, kind),
    CHECK (task_id <> related_task_id)
);
CREATE INDEX task_relations_related_idx ON task_relations(related_task_id, kind);

INSERT INTO settings(key, value) VALUES
    ('max_codex_concurrency', '2'),
    ('listen_addr', '127.0.0.1:2030'),
    ('baokuan_base_url', 'http://127.0.0.1:2022'),
    ('codex_binary_path', 'codex')
ON CONFLICT(key) DO NOTHING;`,
	`CREATE TEMP TABLE migration4_asset_integrity_guard (
    valid INTEGER NOT NULL CHECK (valid = 1)
);
INSERT INTO migration4_asset_integrity_guard(valid)
SELECT 0
WHERE EXISTS (
    SELECT 1
    FROM asset_versions AS version
    LEFT JOIN asset_items AS item ON item.id = version.asset_id
    WHERE item.id IS NULL
       OR NOT (
           item.project_id IS version.project_id
           AND item.account_id = version.account_id
           AND item.type = version.type
       )
)
OR EXISTS (
    SELECT 1
    FROM asset_items AS item
    LEFT JOIN asset_versions AS version ON version.id = item.current_version_id
    WHERE item.current_version_id IS NOT NULL
      AND (version.id IS NULL OR version.asset_id <> item.id)
);
DROP TABLE migration4_asset_integrity_guard;

CREATE TRIGGER asset_versions_scope_insert
BEFORE INSERT ON asset_versions
WHEN NOT EXISTS (
    SELECT 1 FROM asset_items AS item
    WHERE item.id = NEW.asset_id
      AND item.project_id IS NEW.project_id
      AND item.account_id = NEW.account_id
      AND item.type = NEW.type
)
BEGIN
    SELECT RAISE(ABORT, 'asset version scope does not match asset item');
END;

CREATE TRIGGER asset_versions_scope_update
BEFORE UPDATE OF asset_id, project_id, account_id, type ON asset_versions
WHEN NOT EXISTS (
    SELECT 1 FROM asset_items AS item
    WHERE item.id = NEW.asset_id
      AND item.project_id IS NEW.project_id
      AND item.account_id = NEW.account_id
      AND item.type = NEW.type
)
OR EXISTS (
    SELECT 1 FROM asset_items AS item
    WHERE item.current_version_id = OLD.id AND item.id <> NEW.asset_id
)
BEGIN
    SELECT RAISE(ABORT, 'asset version scope does not match asset item');
END;

CREATE TRIGGER asset_items_current_version_insert
BEFORE INSERT ON asset_items
WHEN NEW.current_version_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM asset_versions AS version
    WHERE version.id = NEW.current_version_id AND version.asset_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'current asset version does not belong to asset item');
END;

CREATE TRIGGER asset_items_current_version_update
BEFORE UPDATE OF current_version_id ON asset_items
WHEN NEW.current_version_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM asset_versions AS version
    WHERE version.id = NEW.current_version_id AND version.asset_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'current asset version does not belong to asset item');
END;

CREATE TRIGGER asset_items_scope_update
BEFORE UPDATE OF project_id, account_id, type ON asset_items
WHEN EXISTS (
    SELECT 1 FROM asset_versions AS version
    WHERE version.asset_id = NEW.id
      AND NOT (
          version.project_id IS NEW.project_id
          AND version.account_id = NEW.account_id
          AND version.type = NEW.type
      )
)
BEGIN
    SELECT RAISE(ABORT, 'asset item scope does not match existing versions');
END;`,
	`CREATE TEMP TABLE migration5_asset_parent_guard (
    valid INTEGER NOT NULL CHECK (valid = 1)
);
INSERT INTO migration5_asset_parent_guard(valid)
SELECT 0
WHERE EXISTS (
    SELECT 1
    FROM asset_versions AS child
    LEFT JOIN asset_versions AS parent ON parent.id = child.parent_version_id
    WHERE child.parent_version_id IS NOT NULL
      AND (parent.id IS NULL OR parent.asset_id <> child.asset_id)
);
DROP TABLE migration5_asset_parent_guard;

DROP TRIGGER asset_versions_scope_insert;
DROP TRIGGER asset_versions_scope_update;

CREATE TRIGGER asset_versions_scope_insert
BEFORE INSERT ON asset_versions
WHEN NOT EXISTS (
    SELECT 1 FROM asset_items AS item
    WHERE item.id = NEW.asset_id
      AND item.project_id IS NEW.project_id
      AND item.account_id = NEW.account_id
      AND item.type = NEW.type
)
OR (NEW.parent_version_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM asset_versions AS parent
    WHERE parent.id = NEW.parent_version_id
      AND parent.asset_id = NEW.asset_id
))
BEGIN
    SELECT RAISE(ABORT, 'asset version scope or parent does not match asset item');
END;

CREATE TRIGGER asset_versions_scope_update
BEFORE UPDATE OF asset_id, project_id, account_id, type, parent_version_id ON asset_versions
WHEN NOT EXISTS (
    SELECT 1 FROM asset_items AS item
    WHERE item.id = NEW.asset_id
      AND item.project_id IS NEW.project_id
      AND item.account_id = NEW.account_id
      AND item.type = NEW.type
)
OR (NEW.parent_version_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM asset_versions AS parent
    WHERE parent.id = NEW.parent_version_id
      AND parent.asset_id = NEW.asset_id
))
OR EXISTS (
    SELECT 1 FROM asset_items AS item
    WHERE item.current_version_id = OLD.id AND item.id <> NEW.asset_id
)
OR EXISTS (
    SELECT 1 FROM asset_versions AS child
    WHERE child.parent_version_id = OLD.id AND child.asset_id <> NEW.asset_id
)
BEGIN
    SELECT RAISE(ABORT, 'asset version scope or parent does not match asset item');
END;`,
	`ALTER TABLE admins ADD COLUMN singleton INTEGER NOT NULL DEFAULT 1 CHECK (singleton = 1);
CREATE UNIQUE INDEX admins_singleton_uq ON admins(singleton);`,
	`ALTER TABLE codex_tasks ADD COLUMN model_name TEXT NOT NULL DEFAULT 'gpt-5.6-sol';
ALTER TABLE codex_tasks ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT 'medium';
UPDATE codex_tasks SET model_name='gpt-5.6-sol' WHERE trim(model_name)='';
UPDATE codex_tasks SET reasoning_effort='medium' WHERE trim(reasoning_effort)='';`,
	`CREATE TABLE chat_sessions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    source TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('general', 'idea', 'project', 'history')),
    status TEXT NOT NULL CHECK (status IN ('idle', 'running', 'awaiting_input', 'failed')),
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    idea_session_id TEXT REFERENCES idea_sessions(id) ON DELETE SET NULL,
    codex_thread_id TEXT,
    working_directory TEXT NOT NULL,
    model TEXT NOT NULL,
    reasoning_effort TEXT NOT NULL,
    skill_names_json TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX chat_sessions_updated_idx ON chat_sessions(updated_at DESC, id);
CREATE INDEX chat_sessions_project_idx ON chat_sessions(project_id, updated_at DESC);
CREATE UNIQUE INDEX chat_sessions_thread_uq ON chat_sessions(codex_thread_id) WHERE codex_thread_id IS NOT NULL;

CREATE TABLE chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    kind TEXT NOT NULL,
    content TEXT NOT NULL,
    delivery_status TEXT NOT NULL CHECK (delivery_status IN ('pending', 'sending', 'accepted', 'queued', 'failed')),
    client_key TEXT NOT NULL,
    codex_item_id TEXT,
    turn_id TEXT,
    sequence INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE(session_id, sequence)
);
CREATE INDEX chat_messages_session_created_idx ON chat_messages(session_id, sequence, created_at, id);
CREATE UNIQUE INDEX chat_messages_client_key_uq ON chat_messages(session_id, client_key) WHERE client_key <> '';

CREATE TABLE chat_turns (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    codex_turn_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('idle', 'running', 'awaiting_input', 'completed', 'failed')),
    delivery_mode TEXT NOT NULL CHECK (delivery_mode IN ('auto', 'steer', 'queue')),
    input_message_id TEXT REFERENCES chat_messages(id) ON DELETE SET NULL,
    error_code TEXT,
    error_message TEXT,
    started_at DATETIME,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX chat_turns_session_updated_idx ON chat_turns(session_id, updated_at DESC, id);
CREATE UNIQUE INDEX chat_turns_codex_uq ON chat_turns(session_id, codex_turn_id) WHERE codex_turn_id IS NOT NULL;

CREATE TABLE chat_outbox (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    client_key TEXT NOT NULL,
    content TEXT NOT NULL,
    delivery_mode TEXT NOT NULL CHECK (delivery_mode IN ('auto', 'steer', 'queue')),
    delivery_status TEXT NOT NULL CHECK (delivery_status IN ('pending', 'sending', 'accepted', 'queued', 'failed')),
    expected_turn_id TEXT,
    codex_turn_id TEXT,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at DATETIME NOT NULL,
    claimed_at DATETIME,
    last_error TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(session_id, client_key)
);
CREATE INDEX chat_outbox_delivery_idx ON chat_outbox(delivery_status, available_at, created_at, id);

CREATE TABLE semantic_events (
    id TEXT PRIMARY KEY,
    session_id TEXT REFERENCES chat_sessions(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES codex_tasks(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    kind TEXT NOT NULL,
    phase TEXT NOT NULL,
    level TEXT NOT NULL,
    title TEXT NOT NULL,
    detail TEXT NOT NULL,
    raw_json TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    CHECK (session_id IS NOT NULL OR task_id IS NOT NULL)
);
CREATE UNIQUE INDEX semantic_events_session_seq_uq ON semantic_events(session_id, sequence) WHERE session_id IS NOT NULL;
CREATE UNIQUE INDEX semantic_events_task_seq_uq ON semantic_events(task_id, sequence) WHERE task_id IS NOT NULL;

CREATE TABLE thread_leases (
    thread_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    owner_id TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX thread_leases_expiry_idx ON thread_leases(expires_at);

ALTER TABLE codex_tasks ADD COLUMN chat_session_id TEXT REFERENCES chat_sessions(id) ON DELETE SET NULL;
ALTER TABLE codex_tasks ADD COLUMN codex_thread_id TEXT;
ALTER TABLE codex_tasks ADD COLUMN codex_turn_id TEXT;
ALTER TABLE codex_tasks ADD COLUMN completion_phase TEXT NOT NULL DEFAULT 'agent_running'
    CHECK (completion_phase IN ('agent_running', 'plaintext_ready', 'registering', 'registered'));
ALTER TABLE codex_tasks ADD COLUMN transport TEXT NOT NULL DEFAULT 'legacy_exec'
    CHECK (transport IN ('legacy_exec', 'app_server'));
CREATE INDEX codex_tasks_chat_session_idx ON codex_tasks(chat_session_id, created_at DESC);
CREATE INDEX codex_tasks_thread_turn_idx ON codex_tasks(codex_thread_id, codex_turn_id);`,
	`CREATE TABLE montage_registration_attempts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    manifest_path TEXT NOT NULL,
    workspace_path TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued','running','succeeded','failed','interrupted')),
    attempt INTEGER NOT NULL,
    registered_path TEXT,
    receipt_path TEXT,
    error_code TEXT,
    error_message TEXT,
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    UNIQUE(task_id, attempt)
);
CREATE INDEX montage_registration_state_idx ON montage_registration_attempts(state, started_at);`,
	`WITH ranked AS (
    SELECT id,ROW_NUMBER() OVER (
        PARTITION BY project_id
        ORDER BY CASE WHEN title LIKE '% (fork)' THEN 1 ELSE 0 END,
                 updated_at DESC,created_at DESC,id DESC
    ) AS position
    FROM chat_sessions
    WHERE source='console' AND kind='project' AND project_id IS NOT NULL
)
UPDATE chat_sessions
SET source='console_fork'
WHERE id IN (SELECT id FROM ranked WHERE position>1);

CREATE UNIQUE INDEX chat_sessions_project_main_uq
ON chat_sessions(project_id)
WHERE source='console' AND kind='project' AND project_id IS NOT NULL;`,
	`CREATE TABLE thread_cleanup_intents (
    thread_id TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);`,
	`CREATE TABLE chat_completion_inbox (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    codex_turn_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending','processing','done')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at DATETIME NOT NULL,
    claimed_at DATETIME,
    last_error TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    completed_at DATETIME
);
CREATE INDEX chat_completion_inbox_pending_idx
ON chat_completion_inbox(status,available_at,created_at,id);`,
	`CREATE TABLE projects_lifecycle_v2 (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('script', 'assets', 'mixing', 'review', 'published', 'archived')),
    topic_card_path TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    ready_at DATETIME,
    published_at DATETIME,
    publish_note TEXT,
    publication_status TEXT NOT NULL DEFAULT 'draft'
        CHECK (publication_status IN ('draft', 'producing', 'ready_to_publish', 'published', 'archived'))
);
INSERT INTO projects_lifecycle_v2(
    id,account_id,title,stage,topic_card_path,created_at,updated_at,
    ready_at,published_at,publish_note,publication_status
)
SELECT id,account_id,title,
    CASE stage WHEN 'topic' THEN 'script' WHEN 'ready' THEN 'review' ELSE stage END,
    topic_card_path,created_at,updated_at,ready_at,published_at,publish_note,publication_status
FROM projects;
DROP TABLE projects;
ALTER TABLE projects_lifecycle_v2 RENAME TO projects;
CREATE INDEX projects_account_stage_idx ON projects(account_id, stage);
CREATE INDEX projects_account_publication_idx ON projects(account_id, publication_status, created_at);`,
	`CREATE TABLE project_workflow_runs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('remix')),
    state TEXT NOT NULL CHECK (state IN ('running','completed','failed','canceled')),
    current_step TEXT NOT NULL CHECK (current_step IN ('topic_card','remix','completed')),
    topic_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    remix_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    model_name TEXT NOT NULL,
    reasoning_effort TEXT NOT NULL,
    error_code TEXT,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    finished_at DATETIME
);
CREATE UNIQUE INDEX project_workflow_active_uq
ON project_workflow_runs(project_id,kind) WHERE state='running';`,
	`ALTER TABLE codex_tasks ADD COLUMN queued_at DATETIME;

CREATE TABLE task_phase_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES codex_tasks(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    phase_key TEXT NOT NULL,
    display_name TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('host','app_server','skill')),
    state TEXT NOT NULL CHECK (state IN ('queued','running','completed','failed','canceled','interrupted')),
    started_at DATETIME NOT NULL,
    running_at DATETIME,
    finished_at DATETIME,
    duration_ms INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
    external_id TEXT,
    detail_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail_json)),
    created_at DATETIME NOT NULL,
    CHECK (running_at IS NULL OR running_at >= started_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (finished_at IS NULL OR running_at IS NULL OR finished_at >= running_at)
);
CREATE UNIQUE INDEX task_phase_one_running_uq
ON task_phase_runs(task_id,attempt,phase_key) WHERE state='running';
CREATE UNIQUE INDEX task_phase_external_event_uq
ON task_phase_runs(task_id,attempt,phase_key,source,external_id)
WHERE external_id IS NOT NULL;
CREATE INDEX task_phase_task_attempt_idx
ON task_phase_runs(task_id,attempt,started_at,id);`,
	`ALTER TABLE montage_registration_attempts ADD COLUMN draft_id TEXT;`,
	`CREATE TABLE project_step_notes (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    step TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (project_id, step),
    CHECK (step IN ('remix'))
);`,
	// App Server turn lookups filter on codex_turn_id without codex_thread_id, so
	// codex_tasks_thread_turn_idx cannot serve them: SQLite needs the leading
	// column constrained. Without this index every turn claim, interrupt and
	// cancel scans codex_tasks end to end.
	`CREATE INDEX codex_tasks_turn_idx ON codex_tasks(codex_turn_id);`,
	`CREATE TABLE image_projects (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    script TEXT NOT NULL,
    image_count INTEGER NOT NULL CHECK (image_count BETWEEN 1 AND 60),
    ratio TEXT NOT NULL CHECK (ratio IN ('3:4','4:3','9:16','1:1')),
    style TEXT NOT NULL,
    custom_style TEXT NOT NULL DEFAULT '',
    concurrency INTEGER NOT NULL CHECK (concurrency BETWEEN 1 AND 5),
    status TEXT NOT NULL CHECK (status IN ('draft','generating','ready','partial','failed')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE image_project_items (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES image_projects(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    source_text TEXT NOT NULL,
    title TEXT NOT NULL,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','generating','ready','failed')),
    image_path TEXT,
    mime_type TEXT,
    width INTEGER CHECK (width IS NULL OR width > 0),
    height INTEGER CHECK (height IS NULL OR height > 0),
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(project_id, sequence)
);
CREATE INDEX image_projects_updated_idx ON image_projects(updated_at DESC, id);
CREATE INDEX image_project_items_project_idx ON image_project_items(project_id, sequence);`,
	`CREATE TABLE image_projects_v2 (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    script TEXT NOT NULL,
    image_count INTEGER NOT NULL CHECK (image_count BETWEEN 1 AND 60),
    ratio TEXT NOT NULL CHECK (ratio IN ('3:4','4:3','9:16','1:1')),
    style TEXT NOT NULL,
    custom_style TEXT NOT NULL DEFAULT '',
    concurrency INTEGER NOT NULL CHECK (concurrency BETWEEN 1 AND 18),
    status TEXT NOT NULL CHECK (status IN ('draft','generating','ready','partial','failed')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
INSERT INTO image_projects_v2(id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at)
SELECT id,title,script,image_count,
       ratio,style,custom_style,
       CASE WHEN concurrency > 18 THEN 18 ELSE concurrency END,
       status,created_at,updated_at
FROM image_projects;
CREATE TABLE image_project_items_v2 (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES image_projects_v2(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    role TEXT NOT NULL DEFAULT 'content' CHECK (role IN ('cover','content')),
    source_text TEXT NOT NULL,
    title TEXT NOT NULL,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','generating','ready','failed')),
    image_path TEXT,
    mime_type TEXT,
    width INTEGER CHECK (width IS NULL OR width > 0),
    height INTEGER CHECK (height IS NULL OR height > 0),
    error_message TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(project_id, sequence)
);
INSERT INTO image_project_items_v2(id,project_id,sequence,role,source_text,title,prompt,status,image_path,mime_type,width,height,error_message,created_at,updated_at)
SELECT id,project_id,sequence,
       CASE WHEN sequence = 1 THEN 'cover' ELSE 'content' END,
       source_text,title,prompt,status,image_path,mime_type,width,height,error_message,created_at,updated_at
FROM image_project_items;
DROP TABLE image_project_items;
DROP TABLE image_projects;
ALTER TABLE image_projects_v2 RENAME TO image_projects;
ALTER TABLE image_project_items_v2 RENAME TO image_project_items;
CREATE INDEX image_projects_updated_idx ON image_projects(updated_at DESC, id);
CREATE INDEX image_project_items_project_idx ON image_project_items(project_id, sequence);`,
	`ALTER TABLE image_projects ADD COLUMN selected_position INTEGER;
CREATE TABLE image_project_publishing_candidates (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES image_projects(id) ON DELETE CASCADE,
 position INTEGER NOT NULL CHECK(position BETWEEN 1 AND 5),
 title TEXT NOT NULL,
 description TEXT NOT NULL,
 UNIQUE(project_id, position)
);
CREATE INDEX image_project_publishing_project_idx ON image_project_publishing_candidates(project_id, position);`,
	`ALTER TABLE image_projects ADD COLUMN run_mode TEXT NOT NULL DEFAULT 'manual' CHECK(run_mode IN ('manual','quick'));
ALTER TABLE image_projects ADD COLUMN run_phase TEXT NOT NULL DEFAULT 'idle' CHECK(run_phase IN ('idle','planning','prompting','imaging','completed'));
ALTER TABLE image_projects ADD COLUMN run_status TEXT NOT NULL DEFAULT 'idle' CHECK(run_status IN ('idle','running','failed','completed','interrupted'));
ALTER TABLE image_projects ADD COLUMN phase_error TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN publishing_error TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count >= 0);
ALTER TABLE image_projects ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0 CHECK(failure_count >= 0);
ALTER TABLE image_projects ADD COLUMN image_attempts INTEGER NOT NULL DEFAULT 2 CHECK(image_attempts BETWEEN 1 AND 4);
ALTER TABLE image_projects ADD COLUMN text_model TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
ALTER TABLE image_projects ADD COLUMN image_model TEXT NOT NULL DEFAULT '';
ALTER TABLE image_project_items ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0);`,
	`CREATE TABLE assets_word_timing_backup AS SELECT * FROM assets;
CREATE TABLE asset_items_word_timing_backup AS SELECT * FROM asset_items;
CREATE TABLE asset_versions_word_timing_backup AS SELECT * FROM asset_versions;

DROP TABLE asset_versions;
DROP TABLE asset_items;
DROP TABLE assets;

CREATE TABLE assets (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('continuous_script', 'spoken_script', 'word_timing', 'audio', 'subtitle', 'account_background', 'mix_draft', 'final_video')),
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
INSERT INTO assets SELECT * FROM assets_word_timing_backup;
CREATE INDEX assets_project_type_idx ON assets(project_id, type, version DESC);
CREATE UNIQUE INDEX assets_project_type_version_uq ON assets(project_id, type, version) WHERE project_id IS NOT NULL;

CREATE TABLE asset_items (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('source_script', 'topic_card', 'continuous_script', 'spoken_script', 'narration', 'word_timing', 'subtitle_srt', 'account_background', 'mix_draft', 'final_video')),
    current_version_id TEXT REFERENCES asset_versions(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
INSERT INTO asset_items SELECT * FROM asset_items_word_timing_backup;
CREATE UNIQUE INDEX asset_items_scope_type_uq ON asset_items(IFNULL(project_id, ''), account_id, type);

CREATE TABLE asset_versions (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL REFERENCES asset_items(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('source_script', 'topic_card', 'continuous_script', 'spoken_script', 'narration', 'word_timing', 'subtitle_srt', 'account_background', 'mix_draft', 'final_video')),
    version INTEGER NOT NULL CHECK (version > 0),
    storage_kind TEXT NOT NULL DEFAULT 'file' CHECK (storage_kind IN ('file', 'directory')),
    path TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    parent_version_id TEXT REFERENCES asset_versions(id) ON DELETE SET NULL,
    source_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    state TEXT NOT NULL CHECK (state IN ('missing', 'ready', 'stale', 'generating', 'failed')),
    stale_reason TEXT,
    created_at DATETIME NOT NULL,
    UNIQUE(asset_id, version)
);
INSERT INTO asset_versions SELECT * FROM asset_versions_word_timing_backup;
CREATE INDEX asset_versions_asset_created_idx ON asset_versions(asset_id, version DESC);
CREATE INDEX asset_versions_project_type_idx ON asset_versions(project_id, type, created_at DESC);

CREATE TRIGGER asset_versions_scope_insert
BEFORE INSERT ON asset_versions
WHEN NOT EXISTS (SELECT 1 FROM asset_items AS item WHERE item.id = NEW.asset_id AND item.project_id IS NEW.project_id AND item.account_id = NEW.account_id AND item.type = NEW.type)
OR (NEW.parent_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS parent WHERE parent.id = NEW.parent_version_id AND parent.asset_id = NEW.asset_id))
BEGIN SELECT RAISE(ABORT, 'asset version scope or parent does not match asset item'); END;
CREATE TRIGGER asset_versions_scope_update
BEFORE UPDATE OF asset_id, project_id, account_id, type, parent_version_id ON asset_versions
WHEN NOT EXISTS (SELECT 1 FROM asset_items AS item WHERE item.id = NEW.asset_id AND item.project_id IS NEW.project_id AND item.account_id = NEW.account_id AND item.type = NEW.type)
OR (NEW.parent_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS parent WHERE parent.id = NEW.parent_version_id AND parent.asset_id = NEW.asset_id))
OR EXISTS (SELECT 1 FROM asset_items AS item WHERE item.current_version_id = OLD.id AND item.id <> NEW.asset_id)
OR EXISTS (SELECT 1 FROM asset_versions AS child WHERE child.parent_version_id = OLD.id AND child.asset_id <> NEW.asset_id)
BEGIN SELECT RAISE(ABORT, 'asset version scope or parent does not match asset item'); END;
CREATE TRIGGER asset_items_current_version_insert
BEFORE INSERT ON asset_items
WHEN NEW.current_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS version WHERE version.id = NEW.current_version_id AND version.asset_id = NEW.id)
BEGIN SELECT RAISE(ABORT, 'current asset version does not belong to asset item'); END;
CREATE TRIGGER asset_items_current_version_update
BEFORE UPDATE OF current_version_id ON asset_items
WHEN NEW.current_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS version WHERE version.id = NEW.current_version_id AND version.asset_id = NEW.id)
BEGIN SELECT RAISE(ABORT, 'current asset version does not belong to asset item'); END;
CREATE TRIGGER asset_items_scope_update
BEFORE UPDATE OF project_id, account_id, type ON asset_items
WHEN EXISTS (SELECT 1 FROM asset_versions AS version WHERE version.asset_id = NEW.id AND NOT (version.project_id IS NEW.project_id AND version.account_id = NEW.account_id AND version.type = NEW.type))
BEGIN SELECT RAISE(ABORT, 'asset item scope does not match existing versions'); END;

DROP TABLE assets_word_timing_backup;
DROP TABLE asset_items_word_timing_backup;
DROP TABLE asset_versions_word_timing_backup;`,
	`ALTER TABLE image_projects ADD COLUMN output_mode TEXT NOT NULL DEFAULT 'image_slideshow' CHECK (output_mode IN ('image_slideshow','image_to_video'));
ALTER TABLE image_projects ADD COLUMN output_mode_locked_at DATETIME;
ALTER TABLE image_projects ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE image_projects ADD COLUMN template_version TEXT;
ALTER TABLE image_projects ADD COLUMN template_fingerprint TEXT;
CREATE TABLE image_video_jobs (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES image_projects(id) ON DELETE CASCADE,
 output_mode TEXT NOT NULL CHECK(output_mode IN ('image_slideshow','image_to_video')), account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL,
 template_version TEXT NOT NULL, template_fingerprint TEXT NOT NULL,
 idempotency_key TEXT NOT NULL UNIQUE, status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','canceled')),
 model TEXT NOT NULL, resolution TEXT NOT NULL, concurrency_limit INTEGER NOT NULL CHECK(concurrency_limit BETWEEN 1 AND 6), retry_round INTEGER NOT NULL DEFAULT 0 CHECK(retry_round >= 0),
 phase TEXT NOT NULL CHECK(phase IN ('preparing','narration','media','draft','registration','completed')),
 draft_status TEXT, registration_status TEXT, lease_owner TEXT, lease_expires_at DATETIME, version INTEGER NOT NULL DEFAULT 1,
 narration_relative_path TEXT, narration_fingerprint TEXT, timing_relative_path TEXT, timing_fingerprint TEXT, draft_relative_path TEXT, draft_fingerprint TEXT, manifest_relative_path TEXT, manifest_fingerprint TEXT, receipt_relative_path TEXT, receipt_fingerprint TEXT, error_code TEXT, error_message TEXT, started_at DATETIME, finished_at DATETIME,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX image_video_one_active_job ON image_video_jobs(project_id) WHERE status IN ('pending','running');
CREATE TABLE image_video_job_items (
 id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES image_video_jobs(id) ON DELETE CASCADE,
 image_project_item_id TEXT NOT NULL REFERENCES image_project_items(id) ON DELETE RESTRICT, ordinal INTEGER NOT NULL CHECK(ordinal > 0), status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','canceled')), retry_round INTEGER NOT NULL DEFAULT 0 CHECK(retry_round >= 0), attempt INTEGER NOT NULL DEFAULT 0 CHECK(attempt >= 0), max_attempts INTEGER NOT NULL DEFAULT 3 CHECK(max_attempts BETWEEN 1 AND 3),
 timeline_duration_us INTEGER NOT NULL CHECK(timeline_duration_us > 0), requested_duration_seconds INTEGER CHECK(requested_duration_seconds IS NULL OR requested_duration_seconds IN (6,10,15)), actual_duration_us INTEGER CHECK(actual_duration_us IS NULL OR actual_duration_us > 0),
 input_image_relative_path TEXT NOT NULL, input_image_sha256 TEXT NOT NULL, output_video_relative_path TEXT, output_video_sha256 TEXT, provider_request_id TEXT, lease_owner TEXT, lease_expires_at DATETIME, version INTEGER NOT NULL DEFAULT 1, error_code TEXT, error_message TEXT, started_at DATETIME, finished_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(job_id,ordinal)
);
CREATE TRIGGER image_video_job_items_project_match_insert
BEFORE INSERT ON image_video_job_items
WHEN NOT EXISTS (
 SELECT 1 FROM image_video_jobs AS job
 JOIN image_project_items AS item ON item.project_id = job.project_id
 WHERE job.id = NEW.job_id AND item.id = NEW.image_project_item_id
)
BEGIN SELECT RAISE(ABORT, 'image video job item project mismatch'); END;
CREATE TRIGGER image_video_job_items_project_match_update
BEFORE UPDATE OF job_id, image_project_item_id ON image_video_job_items
WHEN NOT EXISTS (
 SELECT 1 FROM image_video_jobs AS job
 JOIN image_project_items AS item ON item.project_id = job.project_id
 WHERE job.id = NEW.job_id AND item.id = NEW.image_project_item_id
)
BEGIN SELECT RAISE(ABORT, 'image video job item project mismatch'); END;
CREATE TABLE image_video_job_attempts (
 id TEXT PRIMARY KEY, job_item_id TEXT NOT NULL REFERENCES image_video_job_items(id) ON DELETE CASCADE,
 retry_round INTEGER NOT NULL CHECK(retry_round >= 0), attempt INTEGER NOT NULL CHECK(attempt > 0), idempotency_key TEXT NOT NULL UNIQUE,
 request_fingerprint TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','canceled')), error_code TEXT, error_message TEXT, provider_request_id TEXT, output_video_relative_path TEXT, output_video_sha256 TEXT, started_at DATETIME, finished_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(job_item_id,retry_round,attempt)
 );`,
	`DROP TRIGGER IF EXISTS image_video_job_items_project_match_insert;
DROP TRIGGER IF EXISTS image_video_job_items_project_match_update;
ALTER TABLE image_video_job_attempts RENAME TO image_video_job_attempts_v24;
ALTER TABLE image_video_job_items RENAME TO image_video_job_items_v24;
CREATE TABLE image_video_job_items (
 id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES image_video_jobs(id) ON DELETE CASCADE,
 image_project_item_id TEXT NOT NULL REFERENCES image_project_items(id) ON DELETE RESTRICT, ordinal INTEGER NOT NULL CHECK(ordinal > 0), status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','canceled')), retry_round INTEGER NOT NULL DEFAULT 0 CHECK(retry_round >= 0), attempt INTEGER NOT NULL DEFAULT 0 CHECK(attempt >= 0), max_attempts INTEGER NOT NULL DEFAULT 3 CHECK(max_attempts BETWEEN 1 AND 3),
 timeline_duration_us INTEGER NOT NULL CHECK(timeline_duration_us > 0), requested_duration_seconds INTEGER CHECK(requested_duration_seconds IS NULL OR requested_duration_seconds IN (6,10,15)), actual_duration_us INTEGER CHECK(actual_duration_us IS NULL OR actual_duration_us > 0),
 input_image_relative_path TEXT NOT NULL, input_image_sha256 TEXT NOT NULL, output_video_relative_path TEXT, output_video_sha256 TEXT, provider_request_id TEXT, lease_owner TEXT, lease_expires_at DATETIME, version INTEGER NOT NULL DEFAULT 1, error_code TEXT, error_message TEXT, started_at DATETIME, finished_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(job_id,ordinal)
);
INSERT INTO image_video_job_items SELECT * FROM image_video_job_items_v24;
CREATE TRIGGER image_video_job_items_project_match_insert
BEFORE INSERT ON image_video_job_items
WHEN NOT EXISTS (SELECT 1 FROM image_video_jobs AS job JOIN image_project_items AS item ON item.project_id = job.project_id WHERE job.id = NEW.job_id AND item.id = NEW.image_project_item_id)
BEGIN SELECT RAISE(ABORT, 'image video job item project mismatch'); END;
CREATE TRIGGER image_video_job_items_project_match_update
BEFORE UPDATE OF job_id, image_project_item_id ON image_video_job_items
WHEN NOT EXISTS (SELECT 1 FROM image_video_jobs AS job JOIN image_project_items AS item ON item.project_id = job.project_id WHERE job.id = NEW.job_id AND item.id = NEW.image_project_item_id)
BEGIN SELECT RAISE(ABORT, 'image video job item project mismatch'); END;
CREATE TABLE image_video_job_attempts (
 id TEXT PRIMARY KEY, job_item_id TEXT NOT NULL REFERENCES image_video_job_items(id) ON DELETE CASCADE,
 retry_round INTEGER NOT NULL CHECK(retry_round >= 0), attempt INTEGER NOT NULL CHECK(attempt > 0), idempotency_key TEXT NOT NULL UNIQUE,
 request_fingerprint TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed','canceled')), error_code TEXT, error_message TEXT, provider_request_id TEXT, output_video_relative_path TEXT, output_video_sha256 TEXT, started_at DATETIME, finished_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(job_item_id,retry_round,attempt)
);
INSERT INTO image_video_job_attempts SELECT * FROM image_video_job_attempts_v24;
DROP TABLE image_video_job_attempts_v24;
DROP TABLE image_video_job_items_v24;`,
	`DROP INDEX IF EXISTS image_projects_updated_idx;
CREATE TABLE image_projects_v25 (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    script TEXT NOT NULL,
    image_count INTEGER NOT NULL CHECK (image_count BETWEEN 1 AND 60),
    ratio TEXT NOT NULL CHECK (ratio IN ('3:4','4:3','9:16','1:1')),
    style TEXT NOT NULL,
    custom_style TEXT NOT NULL DEFAULT '',
    concurrency INTEGER NOT NULL CHECK (concurrency BETWEEN 1 AND 18),
    status TEXT NOT NULL CHECK (status IN ('draft','generating','ready','partial','failed')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    selected_position INTEGER,
    run_mode TEXT NOT NULL DEFAULT 'manual' CHECK(run_mode IN ('manual','quick','video')),
    run_phase TEXT NOT NULL DEFAULT 'idle' CHECK(run_phase IN ('idle','planning','prompting','imaging','completed')),
    run_status TEXT NOT NULL DEFAULT 'idle' CHECK(run_status IN ('idle','running','failed','completed','interrupted')),
    phase_error TEXT NOT NULL DEFAULT '',
    publishing_error TEXT NOT NULL DEFAULT '',
    success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count >= 0),
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK(failure_count >= 0),
    image_attempts INTEGER NOT NULL DEFAULT 2 CHECK(image_attempts BETWEEN 1 AND 4),
    text_model TEXT NOT NULL DEFAULT '',
    reasoning_effort TEXT NOT NULL DEFAULT '',
    image_model TEXT NOT NULL DEFAULT '',
    output_mode TEXT NOT NULL DEFAULT 'image_slideshow' CHECK (output_mode IN ('image_slideshow','image_to_video')),
    output_mode_locked_at DATETIME,
    account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL,
    template_version TEXT,
    template_fingerprint TEXT
);
INSERT INTO image_projects_v25(id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at,selected_position,run_mode,run_phase,run_status,phase_error,publishing_error,success_count,failure_count,image_attempts,text_model,reasoning_effort,image_model,output_mode,output_mode_locked_at,account_id,template_version,template_fingerprint)
SELECT id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at,selected_position,run_mode,run_phase,run_status,phase_error,publishing_error,success_count,failure_count,image_attempts,text_model,reasoning_effort,image_model,output_mode,output_mode_locked_at,account_id,template_version,template_fingerprint FROM image_projects;
DROP TABLE image_projects;
ALTER TABLE image_projects_v25 RENAME TO image_projects;
CREATE INDEX image_projects_updated_idx ON image_projects(updated_at DESC, id);`,
	// caption_keywords was added to the domain without widening the SQLite
	// CHECK constraint, so every 字幕关键词 registration failed with
	// result_persistence_failed on existing databases. Rebuild the two v2
	// asset tables with the type allowed.
	`CREATE TABLE asset_items_caption_backup AS SELECT * FROM asset_items;
CREATE TABLE asset_versions_caption_backup AS SELECT * FROM asset_versions;

DROP TABLE asset_versions;
DROP TABLE asset_items;

CREATE TABLE asset_items (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('source_script', 'topic_card', 'continuous_script', 'spoken_script', 'caption_keywords', 'narration', 'word_timing', 'subtitle_srt', 'account_background', 'mix_draft', 'final_video')),
    current_version_id TEXT REFERENCES asset_versions(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
INSERT INTO asset_items SELECT * FROM asset_items_caption_backup;
CREATE UNIQUE INDEX asset_items_scope_type_uq ON asset_items(IFNULL(project_id, ''), account_id, type);

CREATE TABLE asset_versions (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL REFERENCES asset_items(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('source_script', 'topic_card', 'continuous_script', 'spoken_script', 'caption_keywords', 'narration', 'word_timing', 'subtitle_srt', 'account_background', 'mix_draft', 'final_video')),
    version INTEGER NOT NULL CHECK (version > 0),
    storage_kind TEXT NOT NULL DEFAULT 'file' CHECK (storage_kind IN ('file', 'directory')),
    path TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    parent_version_id TEXT REFERENCES asset_versions(id) ON DELETE SET NULL,
    source_task_id TEXT REFERENCES codex_tasks(id) ON DELETE SET NULL,
    state TEXT NOT NULL CHECK (state IN ('missing', 'ready', 'stale', 'generating', 'failed')),
    stale_reason TEXT,
    created_at DATETIME NOT NULL,
    UNIQUE(asset_id, version)
);
INSERT INTO asset_versions SELECT * FROM asset_versions_caption_backup;
CREATE INDEX asset_versions_asset_created_idx ON asset_versions(asset_id, version DESC);
CREATE INDEX asset_versions_project_type_idx ON asset_versions(project_id, type, created_at DESC);

CREATE TRIGGER asset_versions_scope_insert
BEFORE INSERT ON asset_versions
WHEN NOT EXISTS (SELECT 1 FROM asset_items AS item WHERE item.id = NEW.asset_id AND item.project_id IS NEW.project_id AND item.account_id = NEW.account_id AND item.type = NEW.type)
OR (NEW.parent_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS parent WHERE parent.id = NEW.parent_version_id AND parent.asset_id = NEW.asset_id))
BEGIN SELECT RAISE(ABORT, 'asset version scope or parent does not match asset item'); END;
CREATE TRIGGER asset_versions_scope_update
BEFORE UPDATE OF asset_id, project_id, account_id, type, parent_version_id ON asset_versions
WHEN NOT EXISTS (SELECT 1 FROM asset_items AS item WHERE item.id = NEW.asset_id AND item.project_id IS NEW.project_id AND item.account_id = NEW.account_id AND item.type = NEW.type)
OR (NEW.parent_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS parent WHERE parent.id = NEW.parent_version_id AND parent.asset_id = NEW.asset_id))
OR EXISTS (SELECT 1 FROM asset_items AS item WHERE item.current_version_id = OLD.id AND item.id <> NEW.asset_id)
OR EXISTS (SELECT 1 FROM asset_versions AS child WHERE child.parent_version_id = OLD.id AND child.asset_id <> NEW.asset_id)
BEGIN SELECT RAISE(ABORT, 'asset version scope or parent does not match asset item'); END;
CREATE TRIGGER asset_items_current_version_insert
BEFORE INSERT ON asset_items
WHEN NEW.current_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS version WHERE version.id = NEW.current_version_id AND version.asset_id = NEW.id)
BEGIN SELECT RAISE(ABORT, 'current asset version does not belong to asset item'); END;
CREATE TRIGGER asset_items_current_version_update
BEFORE UPDATE OF current_version_id ON asset_items
WHEN NEW.current_version_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM asset_versions AS version WHERE version.id = NEW.current_version_id AND version.asset_id = NEW.id)
BEGIN SELECT RAISE(ABORT, 'current asset version does not belong to asset item'); END;
CREATE TRIGGER asset_items_scope_update
BEFORE UPDATE OF project_id, account_id, type ON asset_items
WHEN EXISTS (SELECT 1 FROM asset_versions AS version WHERE version.asset_id = NEW.id AND NOT (version.project_id IS NEW.project_id AND version.account_id = NEW.account_id AND version.type = NEW.type))
BEGIN SELECT RAISE(ABORT, 'asset item scope does not match existing versions'); END;

DROP TABLE asset_versions_caption_backup;
DROP TABLE asset_items_caption_backup;`,
}

const wordTimingAssetsMigrationVersion = 23

// migration2V1DuplicateAssetsCompatibilitySQL preserves migration 2's lookup
// intent for the one predecessor state where its unique index cannot be built.
// Migration 1 allowed duplicate project/type/version rows, and migration 3
// treats assets as a read-only audit table while deterministically renumbering
// them into asset_versions. Keeping the legacy rows unchanged is therefore
// both required for auditability and safe for new repositories.
const migration2V1DuplicateAssetsCompatibilitySQL = `CREATE INDEX assets_project_type_version_lookup_idx
    ON assets(project_id, type, version) WHERE project_id IS NOT NULL;`

func migrate(db *sql.DB) (returnErr error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	begun := false
	committed := false
	defer func() {
		if begun && !committed {
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("roll back migrations: %w", err))
			}
		}
		if err := setForeignKeys(context.Background(), conn, true); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("restore foreign keys after migrations: %w", err))
			if discardErr := conn.Raw(func(any) error { return driver.ErrBadConn }); discardErr != nil && !errors.Is(discardErr, driver.ErrBadConn) {
				returnErr = errors.Join(returnErr, fmt.Errorf("discard migration connection with unverified foreign keys: %w", discardErr))
			}
		}
		if err := conn.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release migration connection: %w", err))
		}
	}()
	if err := setForeignKeys(ctx, conn, false); err != nil {
		return fmt.Errorf("disable foreign keys for migrations: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	begun = true

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
    )`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	for i, migration := range migrations {
		version := i + 1
		var applied int
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied != 0 {
			continue
		}
		if version == 2 {
			compatible, err := requiresV1DuplicateAssetsCompatibility(ctx, conn)
			if err != nil {
				return fmt.Errorf("inspect migration 2 compatibility: %w", err)
			}
			if compatible {
				migration = migration2V1DuplicateAssetsCompatibilitySQL
			}
		}
		if version == wordTimingAssetsMigrationVersion {
			migration, err = adaptWordTimingAssetsIndex(ctx, conn, migration)
			if err != nil {
				return fmt.Errorf("inspect word timing asset indexes: %w", err)
			}
		}

		if _, err := conn.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
	}
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check migrated foreign keys: %w", err)
	}
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			readErr := fmt.Errorf("read migrated foreign key violation: %w", err)
			if closeErr := rows.Close(); closeErr != nil {
				return errors.Join(readErr, fmt.Errorf("close foreign key check: %w", closeErr))
			}
			return readErr
		}
		violationErr := fmt.Errorf("migrated foreign key violation: table=%s row=%v parent=%s foreign_key=%d", table, rowID, parent, foreignKeyID)
		if closeErr := rows.Close(); closeErr != nil {
			return errors.Join(violationErr, fmt.Errorf("close foreign key check: %w", closeErr))
		}
		return violationErr
	}
	if err := rows.Err(); err != nil {
		iterateErr := fmt.Errorf("iterate migrated foreign key check: %w", err)
		if closeErr := rows.Close(); closeErr != nil {
			return errors.Join(iterateErr, fmt.Errorf("close foreign key check: %w", closeErr))
		}
		return iterateErr
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close foreign key check: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

func adaptWordTimingAssetsIndex(ctx context.Context, conn *sql.Conn, migration string) (string, error) {
	var uniqueExists int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='assets_project_type_version_uq'`).Scan(&uniqueExists); err != nil {
		return "", err
	}
	if uniqueExists != 0 {
		return migration, nil
	}
	// Migration 2's duplicate-row compatibility path intentionally keeps a
	// non-unique lookup index. Recreate that shape after rebuilding assets.
	return strings.Replace(migration,
		"CREATE UNIQUE INDEX assets_project_type_version_uq ON assets(project_id, type, version) WHERE project_id IS NOT NULL;",
		"CREATE INDEX assets_project_type_version_lookup_idx ON assets(project_id, type, version) WHERE project_id IS NOT NULL;", 1), nil
}

func requiresV1DuplicateAssetsCompatibility(ctx context.Context, conn *sql.Conn) (bool, error) {
	var historyCount, currentVersion int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&historyCount, &currentVersion); err != nil {
		return false, err
	}
	if historyCount != 1 || currentVersion != 1 {
		return false, nil
	}
	var duplicateGroups int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
        SELECT 1
        FROM assets
        WHERE project_id IS NOT NULL
        GROUP BY project_id, type, version
        HAVING COUNT(*) > 1
    )`).Scan(&duplicateGroups); err != nil {
		return false, err
	}
	return duplicateGroups > 0, nil
}

func setForeignKeys(ctx context.Context, conn *sql.Conn, enabled bool) error {
	value := 0
	pragma := `PRAGMA foreign_keys=OFF`
	if enabled {
		value = 1
		pragma = `PRAGMA foreign_keys=ON`
	}
	if _, err := conn.ExecContext(ctx, pragma); err != nil {
		return err
	}
	var actual int
	if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&actual); err != nil {
		return fmt.Errorf("verify foreign_keys pragma: %w", err)
	}
	if actual != value {
		return fmt.Errorf("verify foreign_keys pragma: got %d, want %d", actual, value)
	}
	return nil
}
