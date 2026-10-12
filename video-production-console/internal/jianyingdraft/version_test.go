package jianyingdraft

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInstalledVersionPrefersHighestExeBesideUserData(t *testing.T) {
	root := t.TempDir()
	draft := filepath.Join(root, "AppData", "User Data", "Projects", "com.lveditor.draft")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExe(t, filepath.Join(root, "9.9.0.1"))
	writeExe(t, filepath.Join(root, "11.1.0.14287"))
	writeExe(t, filepath.Join(root, "Apps", "5.9.0.11632"))
	// A higher folder without the executable must not win.
	if err := os.MkdirAll(filepath.Join(root, "99.0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"all_draft_store":[{"draft_new_version":"164.0.0"}]}`)
	if err := os.WriteFile(filepath.Join(draft, "root_meta_info.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectInstalledVersion(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "11.1.0.14287" || got.Source != "install_dir" {
		t.Fatalf("version=%+v", got)
	}
	if !CompatibleWithVerified(got.Version, VerifiedJianyingVersions) {
		t.Fatalf("installed %s should match verified %v", got.Version, VerifiedJianyingVersions)
	}
}

func TestDetectInstalledVersionReadsAppsLayoutNumerically(t *testing.T) {
	root := t.TempDir()
	draft := filepath.Join(root, "AppData", "User Data", "Projects", "com.lveditor.draft")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExe(t, filepath.Join(root, "Apps", "5.9.0.9"))
	writeExe(t, filepath.Join(root, "Apps", "5.10.0.2"))

	got, err := DetectInstalledVersion(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "5.10.0.2" || got.Source != "install_dir" {
		t.Fatalf("version=%+v", got)
	}
}

func TestDetectInstalledVersionFallsBackToDraftNewVersion(t *testing.T) {
	root := t.TempDir()
	draft := filepath.Join(root, "AppData", "User Data", "Projects", "com.lveditor.draft")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"all_draft_store":[{"draft_new_version":""},{"draft_new_version":"160.0.0"},{"draft_new_version":"164.0.0"}]}`)
	if err := os.WriteFile(filepath.Join(draft, "root_meta_info.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstalledVersion(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "164.0.0" || got.Source != "root_meta_info" {
		t.Fatalf("version=%+v", got)
	}
	if CompatibleWithVerified(got.Version, VerifiedJianyingVersions) {
		t.Fatal("draft schema 164.0.0 is not app 11.1")
	}
}

func TestDetectInstalledVersionMissIsNotAnError(t *testing.T) {
	got, err := DetectInstalledVersion(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "" || got.Note == "" {
		t.Fatalf("version=%+v", got)
	}
	empty, err := DetectInstalledVersion("  ")
	if err != nil || empty.Version != "" || empty.Note == "" {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}

func TestCompatibleWithVerifiedComparesMajorMinor(t *testing.T) {
	verified := []string{"11.1", "5.9.0"}
	if !CompatibleWithVerified("11.1.0.14287", verified) {
		t.Fatal("11.1.0.14287 should match 11.1")
	}
	if !CompatibleWithVerified("5.9.0.11632", verified) {
		t.Fatal("5.9.0.11632 should match 5.9.0")
	}
	if CompatibleWithVerified("11.10.0.1", verified) {
		t.Fatal("11.10 must not match the 11.1 prefix")
	}
	if CompatibleWithVerified("11", verified) || CompatibleWithVerified("", verified) {
		t.Fatal("incomplete versions are not compatible")
	}
}

func writeExe(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "JianyingPro.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
