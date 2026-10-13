package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/config"
	"video-production-console/internal/remixlab"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func TestAppMigratesExistingAccountWorkflowsOnlyOnce(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	insertAccount := func(name string) string {
		t.Helper()
		id := uuid.NewString()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := db.ExecContext(context.Background(), `INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, id, name, now, now); err != nil {
			t.Fatal(err)
		}
		return id
	}
	missingID, savedID := insertAccount("未保存工作流的老账号"), insertAccount("已保存工作流的老账号")
	st := remixlab.Store{DataRoot: root}
	global := remixlab.DefaultWorkflow(remixlab.AgentPrompts{})
	global.Name = "旧全局财经工作流"
	if err := st.SaveWorkflow(global); err != nil {
		t.Fatal(err)
	}
	personal := global
	personal.Name = "账号冻结配置"
	if err := st.SaveWorkflowForAccount(savedID, personal); err != nil {
		t.Fatal(err)
	}
	savedPath := filepath.Join(root, "remix_lab", "workflows", savedID+".json")
	original, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := consoleSettings.NewService(store.NewSettingsRepository(db), nil, consoleSettings.Options{})
	options := Options{DB: db, Config: config.Config{DataRoot: root}, Settings: settings}
	first := New(options)
	t.Cleanup(first.Close)
	migrated, err := st.LoadWorkflowForAccount(missingID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Name != "旧全局财经工作流" || migrated.Style != "" || !migrated.FinanceMode() {
		t.Fatalf("existing account switched behavior: name=%q style=%q", migrated.Name, migrated.Style)
	}
	unchanged, err := os.ReadFile(savedPath)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("existing account file overwritten: %v", err)
	}
	freshID := insertAccount("升级后新建账号")
	global.Name = "后来编辑的全局工作流"
	if err := st.SaveWorkflow(global); err != nil {
		t.Fatal(err)
	}
	second := New(options)
	t.Cleanup(second.Close)
	migrated, err = st.LoadWorkflowForAccount(missingID)
	if err != nil || migrated.Name != "旧全局财经工作流" {
		t.Fatalf("migration not idempotent: %q, %v", migrated.Name, err)
	}
	fresh, err := st.LoadWorkflowForAccount(freshID)
	if err != nil || fresh.Style != "universal_remix" || fresh.Brief != nil {
		t.Fatalf("post-upgrade account inherited finance: %q, %v", fresh.Style, err)
	}
}
