package jianyingdraft

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListAndPruneRegistrationBackups(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeBackup(t, root, "root_meta_info.json", now.Add(-240*time.Hour), []byte(`{"keep":true}`))
	writeBackup(t, root, "root_meta_info.newest.bak.json", now.Add(-1*time.Hour), []byte("new"))
	writeBackup(t, root, "root_meta_info.recent.backup.json", now.Add(-2*time.Hour), []byte("recent!!"))
	writeBackup(t, root, "root_meta_info.old.bak.json", now.Add(-48*time.Hour), []byte("old"))
	writeBackup(t, root, "root_meta_info.older.backup.json", now.Add(-240*time.Hour), []byte("older-bytes"))
	writeBackup(t, root, "notes.bak.json", now.Add(-240*time.Hour), []byte("nope"))
	writeBackup(t, root, "root_meta_info.json.bak", now.Add(-240*time.Hour), []byte("nope"))
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBackup(t, nested, "root_meta_info.nested.bak.json", now.Add(-240*time.Hour), []byte("nested"))
	if err := os.Mkdir(filepath.Join(root, "root_meta_info.dir.backup.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "link-target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "root_meta_info.link.bak.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	listed, err := ListRegistrationBackups(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 4 {
		t.Fatalf("listed %d files: %+v", len(listed), namesOf(listed))
	}
	if listed[0].Name != "root_meta_info.newest.bak.json" || listed[0].Size != 3 {
		t.Fatalf("newest=%+v", listed[0])
	}

	dry, err := PruneRegistrationBackups(root, 1, 24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Kept != 2 || len(dry.Deleted) != 2 || dry.FreedBytes != int64(len("old")+len("older-bytes")) {
		t.Fatalf("dry=%+v", dry)
	}
	if _, err := os.Stat(filepath.Join(root, "root_meta_info.old.bak.json")); err != nil {
		t.Fatal("dry run removed a backup")
	}

	pruned, err := PruneRegistrationBackups(root, 1, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned.Kept != 2 || len(pruned.Deleted) != 2 || pruned.FreedBytes != dry.FreedBytes {
		t.Fatalf("pruned=%+v", pruned)
	}
	if _, err := os.Stat(filepath.Join(root, "root_meta_info.old.bak.json")); !os.IsNotExist(err) {
		t.Fatal("old backup still present")
	}
	if _, err := os.Stat(filepath.Join(root, "root_meta_info.recent.backup.json")); err != nil {
		t.Fatal("recent backup inside the age window was removed")
	}
	mustRemain(t, filepath.Join(root, "root_meta_info.json"))
	mustRemain(t, filepath.Join(root, "notes.bak.json"))
	mustRemain(t, filepath.Join(root, "root_meta_info.json.bak"))
	mustRemain(t, filepath.Join(root, "root_meta_info.dir.backup.json"))
	mustRemain(t, link)
	mustRemain(t, target)
	mustRemain(t, filepath.Join(nested, "root_meta_info.nested.bak.json"))
	meta, err := os.ReadFile(filepath.Join(root, "root_meta_info.json"))
	if err != nil || string(meta) != `{"keep":true}` {
		t.Fatalf("root meta changed: %q %v", meta, err)
	}
}

func TestPruneKeepsLatestEvenWhenTheyAreOld(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeBackup(t, root, "root_meta_info.a.bak.json", now.Add(-100*time.Hour), []byte("a"))
	writeBackup(t, root, "root_meta_info.b.bak.json", now.Add(-90*time.Hour), []byte("bb"))
	writeBackup(t, root, "root_meta_info.c.backup.json", now.Add(-80*time.Hour), []byte("ccc"))

	got, err := PruneRegistrationBackups(root, 2, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kept != 2 || len(got.Deleted) != 1 || got.Deleted[0].Name != "root_meta_info.a.bak.json" || got.FreedBytes != 1 {
		t.Fatalf("result=%+v", got)
	}
	mustRemain(t, filepath.Join(root, "root_meta_info.b.bak.json"))
	mustRemain(t, filepath.Join(root, "root_meta_info.c.backup.json"))
}

func writeBackup(t *testing.T, dir, name string, mod time.Time, body []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func namesOf(files []BackupFile) []string {
	out := make([]string, len(files))
	for i, file := range files {
		out[i] = file.Name
	}
	return out
}

func mustRemain(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s missing: %v", path, err)
	}
}
