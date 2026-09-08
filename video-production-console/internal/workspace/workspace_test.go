package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRejectsEscapeAndWritesBack(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte("# 入口\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "项目", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "项目", "demo", "成稿.md"), []byte("---\n主题: 测\n---\n\n口播正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := Store{Root: root}

	if _, err := store.Resolve("..\\secret.md"); err == nil {
		t.Fatal("expected escape to fail")
	}
	if _, err := store.Resolve("/etc/passwd"); err == nil {
		t.Fatal("expected absolute path to fail")
	}
	if _, err := store.Resolve("agent.go"); err == nil {
		t.Fatal("expected non-markdown to fail")
	}

	tree, err := store.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if tree.RootName != "二创工作区" || len(tree.Entries) == 0 {
		t.Fatalf("tree=%+v", tree)
	}

	file, err := store.Read("项目/demo/成稿.md")
	if err != nil {
		t.Fatal(err)
	}
	if file.SpokenBody != "口播正文" {
		t.Fatalf("spoken=%q", file.SpokenBody)
	}

	if err := store.Write("项目/demo/成稿.md", "---\n主题: 测\n---\n\n改过的口播\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "项目", "demo", "成稿.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "改过的口播") {
		t.Fatalf("disk=%q", got)
	}
}

func TestFindRootWalksParents(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo", DirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(base, "repo", "cmd", "console")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := FindRoot("", []string{cwd})
	if err != nil {
		t.Fatal(err)
	}
	if found != root {
		t.Fatalf("found=%q want=%q", found, root)
	}
}
