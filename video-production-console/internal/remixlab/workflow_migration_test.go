package remixlab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowMigrationPreservesGlobalBytesAndSkipsExisting(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	oldID, savedID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	w := DefaultWorkflow(AgentPrompts{})
	rules := ""
	w.EditorialRules = &rules
	workflowWriter(w).Config.SystemPrompt = "账号正在使用的财经正文\n末尾保持原样。"
	workflowWriter(w).Config.UserTemplate = "原输入 {{SOURCE}}"
	if err := st.SaveWorkflow(w); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkflowForAccount(savedID, DefaultUniversalWorkflow()); err != nil {
		t.Fatal(err)
	}
	global, err := os.ReadFile(st.workflowPath())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(st.accountWorkflowPath(savedID))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateExistingAccountWorkflows([]string{oldID, savedID}); err != nil {
		t.Fatal(err)
	}
	migrated, err := os.ReadFile(st.accountWorkflowPath(oldID))
	if err != nil || string(migrated) != string(global) {
		t.Fatalf("global bytes changed: %v", err)
	}
	unchanged, err := os.ReadFile(st.accountWorkflowPath(savedID))
	if err != nil || string(unchanged) != string(saved) {
		t.Fatalf("account workflow overwritten: %v", err)
	}
	newID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	if err := st.MigrateExistingAccountWorkflows([]string{oldID, savedID, newID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.accountWorkflowPath(newID)); !os.IsNotExist(err) {
		t.Fatalf("new account migrated again: %v", err)
	}
}

func TestWorkflowMigrationRetriesFailureAndUsesLegacyDefault(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	path := st.accountWorkflowPath(id)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateExistingAccountWorkflows([]string{id}); err == nil {
		t.Fatal("directory accepted as migrated workflow")
	}
	if _, err := os.Stat(filepath.Join(st.DataRoot, "remix_lab", "workflow_style_migration.json")); !os.IsNotExist(err) {
		t.Fatalf("failed migration marked complete: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateExistingAccountWorkflows([]string{id}); err != nil {
		t.Fatal(err)
	}
	w, err := st.LoadWorkflowForAccount(id)
	if err != nil || !w.FinanceMode() || len(w.Nodes) != 5 {
		t.Fatalf("legacy fallback not preserved: %+v, %v", w, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".workflow-migration-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("migration temp files left: %v, %v", leftovers, err)
	}
}

func TestWorkflowMigrationRejectsInvalidAccountIDsBeforeWriting(t *testing.T) {
	for _, invalid := range []string{"", "../../outside"} {
		t.Run(invalid, func(t *testing.T) {
			st := Store{DataRoot: t.TempDir()}
			id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			if err := st.MigrateExistingAccountWorkflows([]string{id, invalid}); err == nil {
				t.Fatal("invalid account accepted")
			}
			if _, err := os.Stat(st.accountWorkflowPath(id)); !os.IsNotExist(err) {
				t.Fatalf("wrote before validating all IDs: %v", err)
			}
		})
	}
}
