package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestProjectMainMigrationDemotesDuplicatesWithoutDeletingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-main.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for index, migration := range migrations {
		if strings.Contains(migration, "chat_sessions_project_main_uq") {
			break
		}
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply predecessor migration %d: %v", index+1, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, index+1); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES('account','A','#fff','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES('project','account','P','topic',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO chat_sessions(id,title,source,kind,status,project_id,codex_thread_id,working_directory,model,reasoning_effort,skill_names_json,created_at,updated_at) VALUES(?,?,'console','project','idle','project',?,?,'','','[]',?,?)`
	if _, err := db.Exec(insert, "old", "old", "thread-old", "/old", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "winner", "winner", "thread-winner", "/winner", now.Add(time.Second), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "fork", "winner (fork)", "thread-fork", "/fork", now.Add(2*time.Second), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO chat_messages(id,session_id,role,kind,content,delivery_status,client_key,sequence,created_at) VALUES('message','old','user','input','kept','accepted','key',1,?)`, now); err != nil {
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
	var mainID string
	if err := db.QueryRow(`SELECT id FROM chat_sessions WHERE project_id='project' AND source='console' AND kind='project'`).Scan(&mainID); err != nil {
		t.Fatal(err)
	}
	if mainID != "winner" {
		t.Fatalf("main=%q", mainID)
	}
	var demoted, preserved, threads int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE project_id='project' AND source='console_fork'`).Scan(&demoted); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE id='message' AND session_id='old'`).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(DISTINCT codex_thread_id) FROM chat_sessions WHERE project_id='project'`).Scan(&threads); err != nil {
		t.Fatal(err)
	}
	if demoted != 2 || preserved != 1 || threads != 3 {
		t.Fatalf("demoted=%d preserved messages=%d threads=%d", demoted, preserved, threads)
	}
	if _, err := db.Exec(insert, "duplicate", "duplicate", "thread-duplicate", "/duplicate", now, now); err == nil {
		t.Fatal("partial unique index accepted a second project main")
	}
}
