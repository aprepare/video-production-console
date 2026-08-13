package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"video-production-console/internal/domain"
)

func TestTimingMigrationCreatesPhaseSchemaAndStateVocabulary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timings.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM pragma_table_info('codex_tasks') WHERE name='queued_at'`); got != "1" {
		t.Fatalf("queued_at columns=%s, want 1", got)
	}
	if !tableExists(t, db, "task_phase_runs") {
		t.Fatal("task_phase_runs table missing")
	}
	for name, fragment := range map[string]string{
		"task_phase_one_running_uq":   "WHERE state='running'",
		"task_phase_task_attempt_idx": "task_id,attempt,started_at,id",
	} {
		if got := strings.ReplaceAll(scalar(t, db, `SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, name), " ", ""); !strings.Contains(got, strings.ReplaceAll(fragment, " ", "")) {
			t.Fatalf("index %s=%q, want %q", name, got, fragment)
		}
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?);
		INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES('t','a','x','s','queued','p',?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	states := []domain.TaskPhaseState{domain.PhaseQueued, domain.PhaseRunning, domain.PhaseCompleted, domain.PhaseFailed, domain.PhaseCanceled, domain.PhaseInterrupted}
	for i, state := range states {
		finished, duration := any(nil), any(nil)
		if state != domain.PhaseQueued && state != domain.PhaseRunning {
			finished, duration = now, int64(0)
		}
		if _, err := db.Exec(`INSERT INTO task_phase_runs(id,task_id,attempt,phase_key,display_name,source,state,started_at,finished_at,duration_ms,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("p%d", i), "t", i+1, "phase", "任务准备", domain.PhaseSourceHost, state, now, finished, duration, now); err != nil {
			t.Fatalf("insert state %s: %v", state, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	if got := scalar(t, db, `SELECT COUNT(*) FROM task_phase_runs`); got != fmt.Sprint(len(states)) {
		t.Fatalf("phase rows after reopen=%s", got)
	}
}

func TestQueuedAtMigrationPreservesHistoricalTasksWithoutSyntheticPhases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing-upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:len(migrations)-1] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply predecessor migration %d: %v", i+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('a','A','#fff','active',?,?);
		INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES('old','a','x','s','completed','p',?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var queuedAt sql.NullTime
	if err := db.QueryRow(`SELECT queued_at FROM codex_tasks WHERE id='old'`).Scan(&queuedAt); err != nil {
		t.Fatal(err)
	}
	if queuedAt.Valid {
		t.Fatalf("historical queued_at=%v, want NULL", queuedAt.Time)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM task_phase_runs WHERE task_id='old'`); got != "0" {
		t.Fatalf("synthetic historical phases=%s, want 0", got)
	}
}

const legacyThreadCleanupMigrationVersion = 11

const legacyThreadCleanupMigrationSQL = `CREATE TABLE thread_cleanup_intents (
    thread_id TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);`

func TestOpenCreatesInitialSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "data", "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	wantTables := []string{
		"settings",
		"accounts",
		"projects",
		"assets",
		"codex_tasks",
		"task_events",
		"task_messages",
		"chat_sessions",
		"chat_turns",
		"chat_messages",
		"chat_outbox",
		"chat_completion_inbox",
		"thread_cleanup_intents",
		"semantic_events",
		"thread_leases",
		"project_workflow_runs",
		"image_projects",
		"image_project_items",
	}
	for _, table := range wantTables {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q does not exist: %v", table, err)
		}
	}

	var concurrency string
	if err := db.QueryRow(
		`SELECT value FROM settings WHERE key = 'max_codex_concurrency'`,
	).Scan(&concurrency); err != nil {
		t.Fatalf("read max_codex_concurrency: %v", err)
	}
	if concurrency != "2" {
		t.Errorf("max_codex_concurrency = %q, want %q", concurrency, "2")
	}
}

func TestWorkflowMigrationHasConstraintsIndexAndForeignKeys(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "workflow-schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := scalar(t, db, `SELECT sql FROM sqlite_master WHERE type='table' AND name='project_workflow_runs'`); !strings.Contains(got, "kind IN ('remix')") || !strings.Contains(got, "state IN ('running','completed','failed','canceled')") || !strings.Contains(got, "current_step IN ('topic_card','remix','completed')") {
		t.Fatalf("workflow schema constraints missing: %s", got)
	}
	if got := scalar(t, db, `SELECT sql FROM sqlite_master WHERE type='index' AND name='project_workflow_active_uq'`); !strings.Contains(got, "UNIQUE INDEX") || !strings.Contains(got, "WHERE state='running'") {
		t.Fatalf("workflow active index=%s", got)
	}
	rows, err := db.Query(`PRAGMA foreign_key_list('project_workflow_runs')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{"project_id": "CASCADE", "account_id": "CASCADE", "topic_task_id": "SET NULL", "remix_task_id": "SET NULL"}
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if expected, ok := want[from]; ok {
			if onDelete != expected {
				t.Fatalf("%s on delete=%s, want %s", from, onDelete, expected)
			}
			delete(want, from)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing workflow foreign keys: %v", want)
	}
}

func TestLegacyStageMigrationRebuildsProjectsWithoutDataLoss(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy-stages.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	migrationSet := append(append([]string(nil), migrations...), `SELECT 1;`)
	lifecycleIndex := -1
	for index, migration := range migrationSet {
		if strings.Contains(migration, "CREATE TABLE projects_lifecycle_v2") {
			lifecycleIndex = index
			break
		}
	}
	if lifecycleIndex < 0 {
		t.Fatal("lifecycle migration not found")
	}
	for index, migration := range migrationSet[:lifecycleIndex] {
		if _, err := legacy.Exec(migration); err != nil {
			t.Fatalf("apply predecessor migration %d: %v", index+1, err)
		}
		if _, err := legacy.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, index+1); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.August, 7, 8, 9, 10, 0, time.UTC)
	readyAt, publishedAt := now.Add(time.Hour), now.Add(2*time.Hour)
	if _, err := legacy.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account','Account','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for _, project := range []struct {
		id, stage, status string
	}{
		{id: "topic-project", stage: "topic", status: "draft"},
		{id: "ready-project", stage: "ready", status: "ready_to_publish"},
	} {
		if _, err := legacy.Exec(`INSERT INTO projects(id,account_id,title,stage,topic_card_path,created_at,updated_at,ready_at,published_at,publish_note,publication_status) VALUES(?,'account',?,?,?, ?,?,?,?,?,?)`,
			project.id, "Title "+project.id, project.stage, "cards/"+project.id+".md", now, now.Add(time.Minute), readyAt, publishedAt, "keep note", project.status); err != nil {
			t.Fatalf("insert %s: %v", project.id, err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES('task','topic-project','account','test','skill','completed','prompt',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at) VALUES('asset','ready-project','account','spoken_script','spoken.md','spoken.md','text/markdown',1,'sha',1,'active',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := OpenWithOptions(OpenOptions{Path: path, DataRoot: root, BackupDir: filepath.Join(root, "backups"), Now: time.Now})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer db.Close()
	if got := scalar(t, db, `SELECT stage FROM projects WHERE id='topic-project'`); got != "script" {
		t.Fatalf("topic stage migrated to %q", got)
	}
	if got := scalar(t, db, `SELECT stage FROM projects WHERE id='ready-project'`); got != "review" {
		t.Fatalf("ready stage migrated to %q", got)
	}
	for _, projectID := range []string{"topic-project", "ready-project"} {
		wantTitle := "Title " + projectID
		row := db.QueryRow(`SELECT account_id,title,topic_card_path,created_at,updated_at,ready_at,published_at,publish_note,publication_status FROM projects WHERE id=?`, projectID)
		var accountID, title, topicPath, note, status string
		var createdAt, updatedAt, gotReadyAt, gotPublishedAt time.Time
		if err := row.Scan(&accountID, &title, &topicPath, &createdAt, &updatedAt, &gotReadyAt, &gotPublishedAt, &note, &status); err != nil {
			t.Fatal(err)
		}
		if accountID != "account" || title != wantTitle || topicPath != "cards/"+projectID+".md" || !createdAt.Equal(now) || !updatedAt.Equal(now.Add(time.Minute)) || !gotReadyAt.Equal(readyAt) || !gotPublishedAt.Equal(publishedAt) || note != "keep note" {
			t.Fatalf("project fields changed: account=%q title=%q topic=%q created=%v updated=%v ready=%v published=%v note=%q", accountID, title, topicPath, createdAt, updatedAt, gotReadyAt, gotPublishedAt, note)
		}
		wantStatus := map[string]string{"topic-project": "draft", "ready-project": "ready_to_publish"}[projectID]
		if status != wantStatus {
			t.Fatalf("publication_status=%q, want %q", status, wantStatus)
		}
	}
	if got := scalar(t, db, `SELECT project_id FROM codex_tasks WHERE id='task'`); got != "topic-project" {
		t.Fatalf("task project=%q", got)
	}
	if got := scalar(t, db, `SELECT project_id FROM assets WHERE id='asset'`); got != "ready-project" {
		t.Fatalf("asset project=%q", got)
	}
	for _, stage := range []string{"script", "assets", "mixing", "review", "published", "archived"} {
		if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,'account',?,?,?,?)`, "allowed-"+stage, stage, stage, now, now); err != nil {
			t.Errorf("allowed stage %q rejected: %v", stage, err)
		}
	}
	for _, stage := range []string{"topic", "ready"} {
		if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,'account',?,?,?,?)`, "rejected-"+stage, stage, stage, now, now); err == nil {
			t.Errorf("legacy stage %q accepted", stage)
		}
	}
	for _, index := range []string{"projects_account_stage_idx", "projects_account_publication_idx"} {
		if got := scalar(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index); got != "1" {
			t.Errorf("index %s count=%s", index, got)
		}
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); got != "0" {
		t.Fatalf("foreign key violations=%s", got)
	}
}

func TestConversationMigrationAddsSessionExecutionAndTaskTransportColumns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "conversation-schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for table, columns := range map[string][]string{
		"chat_sessions": {"working_directory", "model", "reasoning_effort", "skill_names_json"},
		"codex_tasks":   {"chat_session_id", "codex_thread_id", "codex_turn_id", "completion_phase", "transport"},
	} {
		for _, column := range columns {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Errorf("%s.%s count=%d, want 1", table, column, count)
			}
		}
	}
}

func TestCompletionInboxMigrationCreatesDurableClaimQueue(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "completion-inbox-schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, column := range []string{"session_id", "codex_turn_id", "status", "attempts", "available_at", "claimed_at", "last_error", "completed_at"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('chat_completion_inbox') WHERE name=?`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("chat_completion_inbox.%s count=%d, want 1", column, count)
		}
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='chat_completion_inbox_pending_idx'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("completion inbox pending index count=%d, want 1", indexCount)
	}
}

func TestCompletionInboxMigrationUpgradesFixedCleanupVersion(t *testing.T) {
	if len(migrations) < legacyThreadCleanupMigrationVersion+1 {
		t.Fatalf("migrations=%d, want cleanup version %d plus an appended completion migration", len(migrations), legacyThreadCleanupMigrationVersion)
	}
	if strings.TrimSpace(migrations[legacyThreadCleanupMigrationVersion-1]) != strings.TrimSpace(legacyThreadCleanupMigrationSQL) {
		t.Fatal("thread cleanup migration SQL or version changed")
	}
	if !strings.Contains(migrations[legacyThreadCleanupMigrationVersion], "CREATE TABLE chat_completion_inbox") {
		t.Fatal("completion inbox migration is not appended immediately after the fixed cleanup migration")
	}
	path := filepath.Join(t.TempDir(), "completion-upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < legacyThreadCleanupMigrationVersion-1; index++ {
		migration := migrations[index]
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply fixed predecessor migration %d: %v", index+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, index+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(legacyThreadCleanupMigrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, legacyThreadCleanupMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, table := range []string{"thread_cleanup_intents", "chat_completion_inbox"} {
		if !tableExists(t, db, table) {
			t.Fatalf("upgraded database is missing %s", table)
		}
	}
	var count, maximum int
	if err := db.QueryRow(`SELECT COUNT(*),MAX(version) FROM schema_migrations`).Scan(&count, &maximum); err != nil {
		t.Fatal(err)
	}
	if count != len(migrations) || maximum != len(migrations) {
		t.Fatalf("migration history count/max=%d/%d", count, maximum)
	}
}

func TestTaskModelMigrationBackfillsHistoricalRowsAndDefaultsNewRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "model-migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE codex_tasks(id TEXT PRIMARY KEY); INSERT INTO codex_tasks(id) VALUES('old')`); err != nil {
		t.Fatal(err)
	}
	var modelMigration string
	for _, migration := range migrations {
		if strings.Contains(migration, `ALTER TABLE codex_tasks ADD COLUMN model_name`) {
			modelMigration = migration
			break
		}
	}
	if modelMigration == "" {
		t.Fatal("model migration not found")
	}
	if _, err := db.Exec(modelMigration); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE codex_tasks SET model_name='',reasoning_effort='' WHERE id='old'`); err != nil {
		t.Fatal(err)
	}
	// Re-run only the data repair statements to model legacy blank values found during upgrade.
	if _, err := db.Exec(`UPDATE codex_tasks SET model_name='gpt-5.6-sol' WHERE trim(model_name)=''; UPDATE codex_tasks SET reasoning_effort='medium' WHERE trim(reasoning_effort)=''`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id) VALUES('new')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "new"} {
		var model, effort string
		if err := db.QueryRow(`SELECT model_name,reasoning_effort FROM codex_tasks WHERE id=?`, id).Scan(&model, &effort); err != nil {
			t.Fatal(err)
		}
		if model != "gpt-5.6-sol" || effort != "medium" {
			t.Fatalf("%s=%q/%q", id, model, effort)
		}
	}
}

func TestOpenConfiguresEveryReplacementConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	assertForeignKeysEnabled(t, db)

	db.SetMaxIdleConns(0)
	assertPragmas := func() {
		t.Helper()
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("db.Conn() error = %v", err)
		}
		defer conn.Close()

		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("read foreign_keys pragma: %v", err)
		}
		if err := conn.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("read busy_timeout pragma: %v", err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Errorf("pragmas = foreign_keys:%d busy_timeout:%d, want 1 and 5000", foreignKeys, busyTimeout)
		}
	}

	assertPragmas()
	assertPragmas()

	_, err = db.Exec(`INSERT INTO projects (
        id, account_id, title, stage, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?)`,
		"project-1", "missing-account", "test", "topic", time.Now(), time.Now())
	if err == nil {
		t.Fatal("insert project with missing account succeeded, want foreign key error")
	}
}

func TestOpenCreatesRequiredIndexes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tests := []struct {
		name string
		want string
	}{
		{name: "accounts_name_uq", want: "WHERE status='active'"},
		{name: "assets_project_type_idx", want: "project_id, type, version DESC"},
		{name: "assets_project_type_version_uq", want: "project_id, type, version"},
	}
	for _, tt := range tests {
		var definition string
		if err := db.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
			tt.name,
		).Scan(&definition); err != nil {
			t.Fatalf("read index %q: %v", tt.name, err)
		}
		if !strings.Contains(definition, tt.want) {
			t.Errorf("index %q definition = %q, want it to contain %q", tt.name, definition, tt.want)
		}
	}
}

func TestConcurrentOpenAppliesMigrationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.db")
	const openers = 8

	start := make(chan struct{})
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := Open(path)
			if err == nil {
				err = db.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Open() error = %v", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var versions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&versions); err != nil {
		t.Fatalf("count migration versions: %v", err)
	}
	if versions != 1 {
		t.Errorf("migration version 1 rows = %d, want 1", versions)
	}
}

func TestMigrationPathLocksAreIndependentAndReleased(t *testing.T) {
	unlockFirst := lockMigrationPath("first.db")
	secondDone := make(chan struct{})
	go func() {
		unlockSecond := lockMigrationPath("second.db")
		unlockSecond()
		close(secondDone)
	}()

	select {
	case <-secondDone:
	case <-time.After(time.Second):
		unlockFirst()
		t.Fatal("independent database path was serialized behind first path")
	}
	unlockFirst()

	migrationPathLocks.Lock()
	remaining := len(migrationPathLocks.entries)
	migrationPathLocks.Unlock()
	if remaining != 0 {
		t.Fatalf("migration lock entries=%d, want no retained entries", remaining)
	}
}

func TestCanonicalPathPreservesResolvedCaseOnWindows(t *testing.T) {
	if filepath.Separator != '\\' {
		t.Skip("Windows-specific operation-path casing regression")
	}
	resolved := filepath.Join(t.TempDir(), "MiXeD-Data", "Console.DB")
	got := canonicalPathWithResolver("alias", func(string) (string, error) {
		return resolved, nil
	})
	if got != resolved {
		t.Fatalf("canonical operation path=%q, want original resolved case %q", got, resolved)
	}
}

func TestNormalizedPathKeyFoldsWindowsCase(t *testing.T) {
	if filepath.Separator != '\\' {
		t.Skip("Windows-specific path-key casing regression")
	}
	path := `C:\MiXeD-Data\Console.DB`
	if got, want := normalizedPathKey(path), strings.ToLower(filepath.Clean(path)); got != want {
		t.Fatalf("normalized path key=%q, want %q", got, want)
	}
}

func TestOpenRejectsInvalidMigrationHistoryBeforeBackup(t *testing.T) {
	tests := []struct {
		name     string
		versions []int
	}{
		{name: "future version", versions: []int{1, 2, len(migrations) + 1}},
		{name: "gap", versions: []int{1, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := filepath.Join(root, "console.db")
			seedMigrationHistory(t, dbPath, tt.versions...)
			backupDir := filepath.Join(root, "backups")

			db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: root, BackupDir: backupDir, Now: time.Now})
			if db != nil {
				_ = db.Close()
				t.Fatal("OpenWithOptions() returned a database for invalid migration history")
			}
			if err == nil || !strings.Contains(err.Error(), "invalid schema migration history") {
				t.Fatalf("OpenWithOptions() error = %v, want invalid schema migration history", err)
			}
			if backups := mustGlob(t, filepath.Join(backupDir, "*")); len(backups) != 0 {
				t.Fatalf("backup artifacts = %v, want none", backups)
			}

			check, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatalf("reopen invalid-history database: %v", err)
			}
			defer check.Close()
			if tableExists(t, check, "settings") {
				t.Fatal("migration ran despite invalid history")
			}
		})
	}
}

func TestMigrateFailureRollsBackAndRestoresForeignKeys(t *testing.T) {
	original := migrations
	migrations = append(append([]string(nil), original...), `CREATE TABLE migration_failure_fixture(id INTEGER); INSERT INTO missing_table(id) VALUES(1);`)
	t.Cleanup(func() { migrations = original })

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable fixture foreign keys: %v", err)
	}

	err = migrate(db)
	if err == nil || !strings.Contains(err.Error(), "apply migration") {
		t.Fatalf("migrate() error = %v, want injected migration failure", err)
	}
	assertForeignKeysEnabled(t, db)
	if tableExists(t, db, "schema_migrations") || tableExists(t, db, "migration_failure_fixture") {
		t.Fatal("failed migration left transactional schema changes behind")
	}
}

func TestOpenUpgradesV1ProjectAssets(t *testing.T) {
	for _, tt := range []struct {
		name       string
		assets     []legacyAssetFixture
		wantUnique bool
	}{
		{
			name: "duplicate versions use the compatibility bridge",
			assets: []legacyAssetFixture{
				{id: "audio-b", path: "audio/b.mp3", filename: "b.mp3", mimeType: "audio/mpeg", size: 22, sha256: "sha-b", version: 1, status: "active", createdAt: time.Date(2026, time.August, 2, 3, 5, 5, 123, time.UTC)},
				{id: "audio-a", path: "audio/a.mp3", filename: "a.mp3", mimeType: "audio/mpeg", size: 11, sha256: "sha-a", version: 1, status: "ready", createdAt: time.Date(2026, time.August, 2, 3, 4, 5, 456, time.UTC)},
			},
			wantUnique: false,
		},
		{
			name: "nonduplicate versions use published migration two",
			assets: []legacyAssetFixture{
				{id: "audio-a", path: "audio/a.mp3", filename: "a.mp3", mimeType: "audio/mpeg", size: 11, sha256: "sha-a", version: 1, status: "active", createdAt: time.Date(2026, time.August, 2, 3, 4, 5, 456, time.UTC)},
			},
			wantUnique: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := filepath.Join(root, "console.db")
			backupDir := filepath.Join(root, "backups")
			legacy := createV1PredecessorSchema(t, dbPath)
			seedV1ProjectAssets(t, legacy, tt.assets)
			before := readLegacyAssets(t, legacy)
			if err := legacy.Close(); err != nil {
				t.Fatalf("close V1 predecessor: %v", err)
			}

			opts := OpenOptions{Path: dbPath, DataRoot: root, BackupDir: backupDir, Now: func() time.Time {
				return time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
			}}
			db, err := OpenWithOptions(opts)
			if err != nil {
				t.Fatalf("OpenWithOptions() error = %v", err)
			}
			if got := readLegacyAssets(t, db); !reflect.DeepEqual(got, before) {
				t.Fatalf("legacy assets changed:\n got %#v\nwant %#v", got, before)
			}
			if got := tableCount(t, db, "asset_items"); got != 1 {
				t.Fatalf("asset_items=%d, want 1", got)
			}
			if got := tableCount(t, db, "asset_versions"); got != len(tt.assets) {
				t.Fatalf("asset_versions=%d, want %d", got, len(tt.assets))
			}
			wantCurrentVersion := "1"
			if len(tt.assets) == 2 {
				wantCurrentVersion = "2"
				if got := scalar(t, db, `SELECT group_concat(version, ',') FROM (SELECT version FROM asset_versions ORDER BY version)`); got != "1,2" {
					t.Fatalf("versions=%s, want 1,2", got)
				}
				if got := scalar(t, db, `SELECT group_concat(sha256, ',') FROM (SELECT sha256 FROM asset_versions ORDER BY version)`); got != "sha-a,sha-b" {
					t.Fatalf("deterministic version order=%s, want sha-a,sha-b", got)
				}
			}
			if got := scalar(t, db, `SELECT current.version FROM asset_items item JOIN asset_versions current ON current.id=item.current_version_id`); got != wantCurrentVersion {
				t.Fatalf("current version=%s, want %s", got, wantCurrentVersion)
			}
			if got := tableCount(t, db, "schema_migrations"); got != len(migrations) {
				t.Fatalf("schema migration rows=%d, want current count %d", got, len(migrations))
			}
			assertMigration2AssetIndex(t, db, tt.wantUnique)
			assertForeignKeysEnabled(t, db)
			assertForeignKeyCheckClean(t, db)
			if err := db.Close(); err != nil {
				t.Fatalf("close migrated database: %v", err)
			}

			if got := len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))); got != 1 {
				t.Fatalf("sqlite backups=%d, want exactly one", got)
			}
			if got := len(mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))); got != 1 {
				t.Fatalf("data manifests=%d, want exactly one", got)
			}
			backup := mustGlob(t, filepath.Join(backupDir, "console-*.db"))[0]
			backupDB, err := sql.Open("sqlite", backup)
			if err != nil {
				t.Fatalf("open backup: %v", err)
			}
			if got := scalar(t, backupDB, `SELECT MAX(version) FROM schema_migrations`); got != "1" {
				t.Fatalf("backup schema version=%s, want 1", got)
			}
			if got := readLegacyAssets(t, backupDB); !reflect.DeepEqual(got, before) {
				t.Fatalf("backup legacy assets changed:\n got %#v\nwant %#v", got, before)
			}
			if tableExists(t, backupDB, "asset_versions") {
				t.Fatal("backup contains post-migration asset_versions")
			}
			_ = backupDB.Close()

			reopened, err := OpenWithOptions(opts)
			if err != nil {
				t.Fatalf("second OpenWithOptions() error = %v", err)
			}
			defer reopened.Close()
			if got := tableCount(t, reopened, "asset_items"); got != 1 {
				t.Fatalf("asset_items after reopen=%d, want 1", got)
			}
			if got := tableCount(t, reopened, "asset_versions"); got != len(tt.assets) {
				t.Fatalf("asset_versions after reopen=%d, want %d", got, len(tt.assets))
			}
			if got := len(mustGlob(t, filepath.Join(backupDir, "console-*.db"))); got != 1 {
				t.Fatalf("sqlite backups after reopen=%d, want one", got)
			}
			if got := len(mustGlob(t, filepath.Join(backupDir, "data-manifest-*.json"))); got != 1 {
				t.Fatalf("data manifests after reopen=%d, want one", got)
			}
		})
	}
}

func TestV1DuplicateAssetsCompatibilityBridgeRollsBack(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	db := createV1PredecessorSchema(t, dbPath)
	defer db.Close()
	seedV1ProjectAssets(t, db, []legacyAssetFixture{
		{id: "audio-a", path: "a.mp3", filename: "a.mp3", mimeType: "audio/mpeg", size: 1, sha256: "a", version: 1, status: "active", createdAt: time.Date(2026, time.August, 2, 3, 4, 5, 0, time.UTC)},
		{id: "audio-b", path: "b.mp3", filename: "b.mp3", mimeType: "audio/mpeg", size: 2, sha256: "b", version: 1, status: "active", createdAt: time.Date(2026, time.August, 2, 3, 4, 6, 0, time.UTC)},
	})
	wantAssets := readLegacyAssets(t, db)
	original := migrations
	migrations = append([]string(nil), migrations...)
	migrations[2] = `CREATE TABLE bridge_failure_fixture(id INTEGER); INSERT INTO missing_table(id) VALUES(1);`
	t.Cleanup(func() { migrations = original })

	err := migrate(db)
	if err == nil || !strings.Contains(err.Error(), "apply migration 3") {
		t.Fatalf("migrate() error = %v, want injected migration 3 failure after bridge", err)
	}
	assertForeignKeysEnabled(t, db)
	if got := scalar(t, db, `SELECT MAX(version) FROM schema_migrations`); got != "1" {
		t.Fatalf("schema version after rollback=%s, want 1", got)
	}
	if got := readLegacyAssets(t, db); !reflect.DeepEqual(got, wantAssets) {
		t.Fatalf("legacy assets changed after rollback:\n got %#v\nwant %#v", got, wantAssets)
	}
	if tableExists(t, db, "bridge_failure_fixture") || tableExists(t, db, "asset_items") {
		t.Fatal("failed bridge transaction left schema changes behind")
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('assets_project_type_version_uq','assets_project_type_version_lookup_idx')`); got != "0" {
		t.Fatalf("migration 2 indexes after rollback=%s, want 0", got)
	}
}

func TestAssetVersionScopeAndCurrentPointerIntegrity(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	now := time.Date(2026, time.August, 3, 1, 2, 3, 0, time.UTC)
	for _, account := range []string{"account-1", "account-2"} {
		if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,'active',?,?)`, account, account, "#fff", now, now); err != nil {
			t.Fatalf("insert %s: %v", account, err)
		}
	}
	for _, project := range []struct{ id, account string }{{"project-1", "account-1"}, {"project-2", "account-1"}} {
		if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,'script',?,?)`, project.id, project.account, project.id, now, now); err != nil {
			t.Fatalf("insert %s: %v", project.id, err)
		}
	}
	items := []struct{ id, project, account, kind string }{
		{"project-item", "project-1", "account-1", "narration"},
		{"other-item", "project-1", "account-1", "subtitle_srt"},
		{"account-item", "", "account-1", "account_background"},
	}
	for _, item := range items {
		var project any = item.project
		if item.project == "" {
			project = nil
		}
		if _, err := db.Exec(`INSERT INTO asset_items(id,project_id,account_id,type,created_at,updated_at) VALUES(?,?,?,?,?,?)`, item.id, project, item.account, item.kind, now, now); err != nil {
			t.Fatalf("insert asset item %s: %v", item.id, err)
		}
	}

	insertVersion := func(id, assetID string, project any, account, kind string, version int) error {
		_, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,path,filename,mime_type,size,sha256,state,created_at)
			VALUES(?,?,?,?,?,?, ?,?,?,1,?,'ready',?)`, id, assetID, project, account, kind, version, id, id, "application/octet-stream", id, now)
		return err
	}
	if err := insertVersion("project-version", "project-item", "project-1", "account-1", "narration", 1); err != nil {
		t.Fatalf("insert valid project-level version: %v", err)
	}
	if err := insertVersion("account-version", "account-item", nil, "account-1", "account_background", 1); err != nil {
		t.Fatalf("insert valid account-level version: %v", err)
	}
	if err := insertVersion("other-version", "other-item", "project-1", "account-1", "subtitle_srt", 1); err != nil {
		t.Fatalf("insert valid second version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,path,filename,mime_type,size,sha256,parent_version_id,state,created_at)
		VALUES('cross-parent-insert','project-item','project-1','account-1','narration',2,'cross','cross','application/octet-stream',1,'cross','other-version','ready',?)`, now); err == nil {
		t.Fatal("insert with a cross-item parent version succeeded")
	}
	if _, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,path,filename,mime_type,size,sha256,parent_version_id,state,created_at)
		VALUES('project-child','project-item','project-1','account-1','narration',2,'child','child','application/octet-stream',1,'child','project-version','ready',?)`, now); err != nil {
		t.Fatalf("insert with same-item parent version failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE asset_versions SET parent_version_id='other-version' WHERE id='project-child'`); err == nil {
		t.Fatal("update to a cross-item parent version succeeded")
	}
	if _, err := db.Exec(`UPDATE asset_versions SET parent_version_id=NULL WHERE id='project-child'`); err != nil {
		t.Fatalf("update to NULL parent version failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE asset_versions SET parent_version_id='project-version' WHERE id='project-child'`); err != nil {
		t.Fatalf("update to same-item parent version failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,path,filename,mime_type,size,sha256,parent_version_id,state,created_at)
		VALUES('other-child','other-item','project-1','account-1','subtitle_srt',2,'other-child','other-child','application/octet-stream',1,'other-child','other-version','ready',?)`, now); err != nil {
		t.Fatalf("insert second same-item parent version failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE asset_versions SET asset_id='project-item', type='narration', version=3 WHERE id='other-version'`); err == nil {
		t.Fatal("reassigning a parent version away from its child asset item succeeded")
	}

	for _, tt := range []struct {
		name, id, assetID string
		project           any
		account, kind     string
		version           int
	}{
		{name: "project", id: "bad-project", assetID: "project-item", project: "project-2", account: "account-1", kind: "narration", version: 2},
		{name: "account", id: "bad-account", assetID: "project-item", project: "project-1", account: "account-2", kind: "narration", version: 3},
		{name: "type", id: "bad-type", assetID: "project-item", project: "project-1", account: "account-1", kind: "subtitle_srt", version: 4},
	} {
		t.Run("rejects mismatched "+tt.name, func(t *testing.T) {
			if err := insertVersion(tt.id, tt.assetID, tt.project, tt.account, tt.kind, tt.version); err == nil {
				t.Fatalf("insert version with mismatched %s succeeded", tt.name)
			}
		})
	}
	if _, err := db.Exec(`UPDATE asset_items SET current_version_id='other-version' WHERE id='project-item'`); err == nil {
		t.Fatal("cross-item current version pointer succeeded")
	}
	if _, err := db.Exec(`UPDATE asset_items SET current_version_id='project-version' WHERE id='project-item'`); err != nil {
		t.Fatalf("same-item current version pointer failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE asset_versions SET asset_id='other-item', type='subtitle_srt', version=2 WHERE id='project-version'`); err == nil {
		t.Fatal("reassigning a current version to another asset item succeeded")
	}
}

func TestMigration5RejectsExistingCrossItemParent(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	original := migrations
	if len(original) < 4 {
		t.Fatalf("migrations=%d, want migration 4 fixture support", len(original))
	}
	migrations = original[:4]
	db, err := Open(dbPath)
	migrations = original
	if err != nil {
		t.Fatalf("create migration 4 fixture: %v", err)
	}
	now := time.Date(2026, time.August, 3, 1, 2, 3, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account-1','Account','#fff','active',?,?)`, now, now); err != nil {
		t.Fatalf("insert fixture account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES('project-1','account-1','Project','topic',?,?)`, now, now); err != nil {
		t.Fatalf("insert fixture project: %v", err)
	}
	for _, item := range []struct{ id, kind string }{{"item-1", "narration"}, {"item-2", "subtitle_srt"}} {
		if _, err := db.Exec(`INSERT INTO asset_items(id,project_id,account_id,type,created_at,updated_at) VALUES(?,'project-1','account-1',?,?,?)`, item.id, item.kind, now, now); err != nil {
			t.Fatalf("insert fixture item %s: %v", item.id, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,path,filename,mime_type,size,sha256,state,created_at)
		VALUES('parent-2','item-2','project-1','account-1','subtitle_srt',1,'parent','parent','application/octet-stream',1,'parent','ready',?)`, now); err != nil {
		t.Fatalf("insert fixture parent: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,path,filename,mime_type,size,sha256,parent_version_id,state,created_at)
		VALUES('child-1','item-1','project-1','account-1','narration',1,'child','child','application/octet-stream',1,'child','parent-2','ready',?)`, now); err != nil {
		t.Fatalf("insert cross-item parent fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migration 4 fixture: %v", err)
	}

	db, err = OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: root, BackupDir: filepath.Join(root, "backups"), Now: time.Now})
	if db != nil {
		_ = db.Close()
		t.Fatal("migration 5 accepted an existing cross-item parent")
	}
	if err == nil || !strings.Contains(err.Error(), "apply migration 5") {
		t.Fatalf("OpenWithOptions() error = %v, want migration 5 guard failure", err)
	}
}

func TestV2MigrationBackfillsAssetsAndTasks(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	createV2Predecessor(t, dbPath)

	db, err := OpenWithOptions(OpenOptions{
		Path:      dbPath,
		DataRoot:  root,
		BackupDir: filepath.Join(root, "backups"),
		Now:       time.Now,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()

	for _, table := range []string{
		"admins", "auth_sessions", "encrypted_secrets", "skill_snapshots",
		"idea_sessions", "idea_messages", "idea_candidates", "asset_items",
		"asset_versions", "asset_dependencies", "codex_tasks", "task_messages",
		"task_events", "task_artifacts", "task_relations",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q does not exist", table)
		}
	}
	if got := tableCount(t, db, "asset_items"); got != 1 {
		t.Fatalf("asset_items=%d", got)
	}
	if got := tableCount(t, db, "asset_versions"); got != 1 {
		t.Fatalf("asset_versions=%d", got)
	}
	if got := tableCount(t, db, "assets"); got != 1 {
		t.Fatalf("legacy assets=%d", got)
	}
	if got := scalar(t, db, `SELECT type FROM asset_items LIMIT 1`); got != "narration" {
		t.Fatalf("type=%s", got)
	}
	if got := scalar(t, db, `SELECT type FROM asset_versions LIMIT 1`); got != "narration" {
		t.Fatalf("version type=%s", got)
	}
	if got := scalar(t, db, `SELECT state FROM asset_versions LIMIT 1`); got != "ready" {
		t.Fatalf("state=%s", got)
	}
	if got := scalar(t, db, `SELECT status FROM codex_tasks LIMIT 1`); got != "awaiting_input" {
		t.Fatalf("status=%s", got)
	}
	if got := scalar(t, db, `SELECT publication_status FROM projects LIMIT 1`); got != "draft" {
		t.Fatalf("publication_status=%s", got)
	}
	if got := scalar(t, db, `SELECT background_asset_item_id FROM accounts LIMIT 1`); got == "" {
		t.Fatal("background_asset_item_id was not backfilled")
	}
	if got := tableCount(t, db, "task_events"); got != 1 {
		t.Fatalf("task_events=%d", got)
	}
	if got := tableCount(t, db, "task_messages"); got != 1 {
		t.Fatalf("task_messages=%d", got)
	}
	if _, err := db.Exec(`INSERT INTO task_messages(id,task_id,role,content,created_at) VALUES('message-system','task-1','system','rules',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert system task message: %v", err)
	}

	wantSettings := map[string]string{
		"max_codex_concurrency": "2",
		"listen_addr":           "127.0.0.1:2030",
		"baokuan_base_url":      "http://127.0.0.1:2022",
		"codex_binary_path":     "codex",
	}
	for key, want := range wantSettings {
		if got := scalar(t, db, `SELECT value FROM settings WHERE key=?`, key); got != want {
			t.Errorf("setting %s=%q, want %q", key, got, want)
		}
	}
}

func TestV2MigrationRenumbersDuplicateLegacyVersions(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	legacy := createV2PredecessorSchema(t, dbPath)
	now := time.Date(2026, time.August, 2, 3, 4, 5, 0, time.UTC)
	if _, err := legacy.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account-1','Account','#fff','active',?,?)`, now, now); err != nil {
		t.Fatalf("insert legacy account: %v", err)
	}
	for _, asset := range []struct {
		id      string
		created time.Time
	}{
		{id: "background-b", created: now.Add(time.Minute)},
		{id: "background-a", created: now},
	} {
		if _, err := legacy.Exec(`INSERT INTO assets(
            id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at
        ) VALUES(?,NULL,'account-1','account_background',?,?, 'image/png',1,?,1,'active',?)`,
			asset.id, asset.id+".png", asset.id+".png", asset.id, asset.created); err != nil {
			t.Fatalf("insert duplicate legacy version %s: %v", asset.id, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close predecessor: %v", err)
	}

	db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: root, BackupDir: filepath.Join(root, "backups"), Now: time.Now})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	if got := tableCount(t, db, "assets"); got != 2 {
		t.Fatalf("legacy assets=%d", got)
	}
	if got := tableCount(t, db, "asset_items"); got != 1 {
		t.Fatalf("asset_items=%d", got)
	}
	if got := tableCount(t, db, "asset_versions"); got != 2 {
		t.Fatalf("asset_versions=%d", got)
	}
	if got := scalar(t, db, `SELECT group_concat(version, ',') FROM (SELECT version FROM asset_versions ORDER BY version)`); got != "1,2" {
		t.Fatalf("versions=%s", got)
	}
	if got := scalar(t, db, `SELECT group_concat(sha256, ',') FROM (SELECT sha256 FROM asset_versions ORDER BY version)`); got != "background-a,background-b" {
		t.Fatalf("deterministic version order=%s", got)
	}
	if got := scalar(t, db, `SELECT current.version FROM asset_items item JOIN asset_versions current ON current.id=item.current_version_id`); got != "2" {
		t.Fatalf("current version=%s", got)
	}
}

func TestV2MigrationTransformsLegacyValuesAndConstraints(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "console.db")
	legacy := createV2PredecessorSchema(t, dbPath)
	now := time.Date(2026, time.August, 2, 3, 4, 5, 0, time.UTC)
	if _, err := legacy.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account-1','Account','#fff','active',?,?)`, now, now); err != nil {
		t.Fatalf("insert legacy account: %v", err)
	}
	wantPublication := map[string]string{
		"topic": "draft", "script": "draft", "assets": "draft",
		"mixing": "producing", "review": "producing", "ready": "ready_to_publish",
		"published": "published", "archived": "archived",
	}
	wantStage := map[string]string{
		"topic": "script", "ready": "review",
	}
	for stage := range wantPublication {
		if _, err := legacy.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,'account-1',?,?,?,?)`, "project-"+stage, stage, stage, now, now); err != nil {
			t.Fatalf("insert %s project: %v", stage, err)
		}
	}
	for _, task := range []struct{ id, status string }{{"task-cancelled", "cancelled"}, {"task-waiting", "waiting_input"}} {
		if _, err := legacy.Exec(`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES(?,'project-topic','account-1','test','skill',?,'prompt',?)`, task.id, task.status, now); err != nil {
			t.Fatalf("insert legacy %s task: %v", task.status, err)
		}
	}
	if _, err := legacy.Exec(`INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id) VALUES('subtitle-1','project-topic','account-1','subtitle','one.srt','one.srt','text/plain',3,'sha',1,'active',?,'task-cancelled')`, now); err != nil {
		t.Fatalf("insert legacy subtitle: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO task_events(id,task_id,sequence,kind,level,display_text,raw_json,created_at) VALUES('event-1','task-cancelled',1,'message','info','event','{}',?)`, now); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO task_messages(id,task_id,role,content,created_at) VALUES('message-1','task-cancelled','assistant','message',?)`, now); err != nil {
		t.Fatalf("insert legacy message: %v", err)
	}
	if _, err := legacy.Exec(`UPDATE settings SET value='7' WHERE key='max_codex_concurrency'`); err != nil {
		t.Fatalf("customize existing concurrency setting: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO settings(key,value) VALUES('listen_addr','127.0.0.1:9999')`); err != nil {
		t.Fatalf("insert existing listen setting: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO settings(key,value) VALUES('baokuan_base_url','http://127.0.0.1:9998'),('codex_binary_path','custom-codex')`); err != nil {
		t.Fatalf("insert existing dependency settings: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close predecessor: %v", err)
	}

	db, err := OpenWithOptions(OpenOptions{Path: dbPath, DataRoot: root, BackupDir: filepath.Join(root, "backups"), Now: time.Now})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer db.Close()
	if got := scalar(t, db, `SELECT type FROM asset_items WHERE id='subtitle-1'`); got != "subtitle_srt" {
		t.Fatalf("asset item type=%s", got)
	}
	if got := scalar(t, db, `SELECT type FROM asset_versions LIMIT 1`); got != "subtitle_srt" {
		t.Fatalf("asset version type=%s", got)
	}
	if got := scalar(t, db, `SELECT status FROM codex_tasks WHERE id='task-cancelled'`); got != "canceled" {
		t.Fatalf("cancelled task status=%s", got)
	}
	if got := scalar(t, db, `SELECT status FROM codex_tasks WHERE id='task-waiting'`); got != "awaiting_input" {
		t.Fatalf("waiting task status=%s", got)
	}
	for stage, want := range wantPublication {
		if got := scalar(t, db, `SELECT publication_status FROM projects WHERE id=?`, "project-"+stage); got != want {
			t.Errorf("stage %s publication_status=%s, want %s", stage, got, want)
		}
		migratedStage := stage
		if replacement := wantStage[stage]; replacement != "" {
			migratedStage = replacement
		}
		if got := scalar(t, db, `SELECT stage FROM projects WHERE id=?`, "project-"+stage); got != migratedStage {
			t.Errorf("project %s stage=%s, want %s", stage, got, migratedStage)
		}
	}
	wantSettings := map[string]string{
		"max_codex_concurrency": "7",
		"listen_addr":           "127.0.0.1:9999",
		"baokuan_base_url":      "http://127.0.0.1:9998",
		"codex_binary_path":     "custom-codex",
	}
	for key, want := range wantSettings {
		if got := scalar(t, db, `SELECT value FROM settings WHERE key=?`, key); got != want {
			t.Errorf("setting %s=%s, want preserved %s", key, got, want)
		}
	}
	if got := scalar(t, db, `SELECT id FROM assets`); got != "subtitle-1" {
		t.Fatalf("legacy asset id=%s", got)
	}
	if got := scalar(t, db, `SELECT id FROM task_events`); got != "event-1" {
		t.Fatalf("event id=%s", got)
	}
	if got := scalar(t, db, `SELECT id FROM task_messages`); got != "message-1" {
		t.Fatalf("message id=%s", got)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if rows.Next() {
		_ = rows.Close()
		t.Fatal("foreign_key_check returned a violation")
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close foreign_key_check: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES('task-invalid','account-1','test','skill','cancelled','prompt',?)`, now); err == nil {
		t.Fatal("post-migration cancelled task insert succeeded")
	}
	if _, err := db.Exec(`INSERT INTO codex_tasks(id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES('task-legacy-wait','account-1','test','skill','waiting_input','prompt',?)`, now); err != nil {
		t.Fatalf("post-migration waiting_input task insert failed: %v", err)
	}
}

func createV2Predecessor(t *testing.T, path string) {
	t.Helper()
	db := createV2PredecessorSchema(t, path)
	defer db.Close()
	now := time.Date(2026, time.August, 2, 3, 4, 5, 0, time.UTC)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, []any{"account-1", "Account", "#fff", "active", now, now}},
		{`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,?,?,?,?)`, []any{"project-1", "account-1", "Project", "topic", now, now}},
		{`INSERT INTO codex_tasks(id,project_id,account_id,type,skill_name,status,prompt_snapshot,created_at) VALUES(?,?,?,?,?,?,?,?)`, []any{"task-1", "project-1", "account-1", "narrate", "tts-skill", "waiting_input", "prompt", now}},
		{`INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"asset-1", "project-1", "account-1", "audio", "audio/one.mp3", "one.mp3", "audio/mpeg", 7, "abc", 1, "active", now, "task-1"}},
		{`UPDATE accounts SET background_asset_id='asset-1' WHERE id='account-1'`, nil},
		{`INSERT INTO task_events(id,task_id,sequence,kind,level,display_text,raw_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, []any{"event-1", "task-1", 1, "message", "info", "waiting", `{}`, now}},
		{`INSERT INTO task_messages(id,task_id,role,content,created_at) VALUES(?,?,?,?,?)`, []any{"message-1", "task-1", "assistant", "question", now}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed predecessor with %q: %v", statement.query, err)
		}
	}
}

type legacyAssetFixture struct {
	id, projectID, accountID, assetType      string
	path, filename, mimeType, sha256, status string
	size, version                            int64
	createdAt                                time.Time
	sourceTaskID                             sql.NullString
}

func createV1PredecessorSchema(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create V1 predecessor directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open V1 predecessor database: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		_ = db.Close()
		t.Fatalf("create V1 migration table: %v", err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		_ = db.Close()
		t.Fatalf("apply migration 1 fixture: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(1)`); err != nil {
		_ = db.Close()
		t.Fatalf("record migration 1 fixture: %v", err)
	}
	return db
}

func seedV1ProjectAssets(t *testing.T, db *sql.DB, assets []legacyAssetFixture) {
	t.Helper()
	now := time.Date(2026, time.August, 2, 1, 2, 3, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account-1','Account','#fff','active',?,?)`, now, now); err != nil {
		t.Fatalf("insert V1 account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES('project-1','account-1','Project','assets',?,?)`, now, now); err != nil {
		t.Fatalf("insert V1 project: %v", err)
	}
	for _, asset := range assets {
		if _, err := db.Exec(`INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at)
			VALUES(?,'project-1','account-1','audio',?,?,?,?,?,?,?,?)`,
			asset.id, asset.path, asset.filename, asset.mimeType, asset.size, asset.sha256, asset.version, asset.status, asset.createdAt); err != nil {
			t.Fatalf("insert V1 asset %s: %v", asset.id, err)
		}
	}
}

func readLegacyAssets(t *testing.T, db *sql.DB) []legacyAssetFixture {
	t.Helper()
	rows, err := db.Query(`SELECT id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at,source_task_id FROM assets ORDER BY id`)
	if err != nil {
		t.Fatalf("query legacy assets: %v", err)
	}
	defer rows.Close()
	var assets []legacyAssetFixture
	for rows.Next() {
		var asset legacyAssetFixture
		if err := rows.Scan(&asset.id, &asset.projectID, &asset.accountID, &asset.assetType, &asset.path, &asset.filename, &asset.mimeType, &asset.size, &asset.sha256, &asset.version, &asset.status, &asset.createdAt, &asset.sourceTaskID); err != nil {
			t.Fatalf("scan legacy asset: %v", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate legacy assets: %v", err)
	}
	return assets
}

func assertMigration2AssetIndex(t *testing.T, db *sql.DB, wantUnique bool) {
	t.Helper()
	name := "assets_project_type_version_lookup_idx"
	unique := 0
	if wantUnique {
		name = "assets_project_type_version_uq"
		unique = 1
	}
	var gotUnique int
	if err := db.QueryRow(`SELECT "unique" FROM pragma_index_list('assets') WHERE name=?`, name).Scan(&gotUnique); err != nil {
		t.Fatalf("read migration 2 asset index %s: %v", name, err)
	}
	if gotUnique != unique {
		t.Fatalf("index %s unique=%d, want %d", name, gotUnique, unique)
	}
	if got := scalar(t, db, `SELECT group_concat(name, ',') FROM pragma_index_info(?) ORDER BY seqno`, name); got != "project_id,type,version" {
		t.Fatalf("index %s columns=%s, want project_id,type,version", name, got)
	}
	other := "assets_project_type_version_uq"
	if wantUnique {
		other = "assets_project_type_version_lookup_idx"
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, other); got != "0" {
		t.Fatalf("unexpected alternate migration 2 index %s exists", other)
	}
}

func assertForeignKeyCheckClean(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check returned a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
}

func seedMigrationHistory(t *testing.T, path string, versions ...int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open migration history fixture: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create migration history fixture: %v", err)
	}
	for _, version := range versions {
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
			t.Fatalf("insert migration version %d: %v", version, err)
		}
	}
}

func assertForeignKeysEnabled(t *testing.T, db *sql.DB) {
	t.Helper()
	var enabled int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("foreign_keys=%d, want 1", enabled)
	}
}

func createV2PredecessorSchema(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create predecessor directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open predecessor database: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		_ = db.Close()
		t.Fatalf("create predecessor migration table: %v", err)
	}
	if len(migrations) < 2 {
		t.Fatalf("migrations=%d, want at least two predecessor migrations", len(migrations))
	}
	for i, migration := range migrations[:2] {
		if _, err := db.Exec(migration); err != nil {
			_ = db.Close()
			t.Fatalf("apply predecessor migration %d: %v", i+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			_ = db.Close()
			t.Fatalf("record predecessor migration %d: %v", i+1, err)
		}
	}
	return db
}

func assertBackupIsPreMigration(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database backup: %v", err)
	}
	defer db.Close()
	if tableExists(t, db, "asset_versions") {
		t.Fatal("database backup already contains V2 migration")
	}
	if got := tableCount(t, db, "assets"); got != 1 {
		t.Fatalf("backup assets=%d, want 1", got)
	}
}

func TestImageProjectV2MigrationPreservesLegacyCardsWhileCappingConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image-project-v2-upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:len(migrations)-1] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply predecessor migration %d: %v", i+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO image_projects(id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at) VALUES('legacy','旧项目','原文',20,'3:4','finance_documentary','',5,'draft',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for sequence := 1; sequence <= 20; sequence++ {
		if _, err := db.Exec(`INSERT INTO image_project_items(id,project_id,sequence,source_text,title,prompt,status,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',?,?)`, fmt.Sprintf("item-%02d", sequence), "legacy", sequence, fmt.Sprintf("原文%d", sequence), fmt.Sprintf("标题%d", sequence), fmt.Sprintf("提示词%d", sequence), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(migrations[len(migrations)-1]); err != nil {
		t.Fatalf("apply image project v2 migration: %v", err)
	}
	if got := scalar(t, db, `SELECT image_count FROM image_projects WHERE id='legacy'`); got != "20" {
		t.Fatalf("image_count=%s, want 20", got)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM image_project_items WHERE project_id='legacy'`); got != "20" {
		t.Fatalf("item count=%s, want 20", got)
	}
	if got := scalar(t, db, `SELECT MAX(sequence) FROM image_project_items WHERE project_id='legacy'`); got != "20" {
		t.Fatalf("max sequence=%s, want 20", got)
	}
	if _, err := db.Exec(`INSERT INTO image_projects(id,title,script,image_count,ratio,style,custom_style,concurrency,status,created_at,updated_at) VALUES('too-concurrent','x','x',1,'3:4','finance_documentary','',19,'draft',?,?)`, now, now); err == nil {
		t.Fatal("migration accepted concurrency 19")
	}
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		t.Fatalf("check table %q: %v", table, err)
	}
	return count == 1
}

func tableCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count table %q: %v", table, err)
	}
	return count
}

func scalar(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query scalar %q: %v", query, err)
	}
	return value
}
