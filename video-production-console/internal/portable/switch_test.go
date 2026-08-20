package portable

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSwitchKeepsCurrentAndPreviousButNeverTouchesData(t *testing.T) {
	base := t.TempDir()
	appRoot := filepath.Join(base, "app")
	dataRoot := filepath.Join(base, "data")
	mustWrite(t, filepath.Join(dataRoot, "project.txt"), []byte("keep"))
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0"} {
		mustMkdir(t, filepath.Join(appRoot, v))
	}
	if err := SwitchVersion(appRoot, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	state := readState(t, appRoot)
	if state.Current != "0.3.0" || state.Previous != "0.2.0" {
		t.Fatalf("state=%+v", state)
	}
	if got := string(mustRead(t, filepath.Join(dataRoot, "project.txt"))); got != "keep" {
		t.Fatalf("data=%q", got)
	}
}

func TestSwitchUpdatesPreviousFromExistingCurrent(t *testing.T) {
	appRoot := filepath.Join(t.TempDir(), "app")
	for _, v := range []string{"0.1.0", "0.2.0"} {
		mustMkdir(t, filepath.Join(appRoot, v))
	}
	if err := SwitchVersion(appRoot, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := SwitchVersion(appRoot, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	state := readState(t, appRoot)
	if state.Current != "0.2.0" || state.Previous != "0.1.0" {
		t.Fatalf("state=%+v", state)
	}
}

func TestSwitchRejectsNonSemverAndNestedVersions(t *testing.T) {
	appRoot := filepath.Join(t.TempDir(), "app")
	mustMkdir(t, filepath.Join(appRoot, "0.1.0"))
	mustMkdir(t, filepath.Join(appRoot, "not-semver"))
	mustMkdir(t, filepath.Join(appRoot, "0.1.0", "nested"))
	if err := SwitchVersion(appRoot, "not-semver"); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("err=%v", err)
	}
	if err := SwitchVersion(appRoot, filepath.Join("0.1.0", "nested")); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("nested err=%v", err)
	}
	if err := SwitchVersion(appRoot, "../0.1.0"); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("escape err=%v", err)
	}
}

func TestSwitchFailedStateWriteLeavesVersions(t *testing.T) {
	appRoot := filepath.Join(t.TempDir(), "app")
	mustMkdir(t, filepath.Join(appRoot, "0.1.0"))
	mustMkdir(t, filepath.Join(appRoot, "0.2.0"))
	previous := atomicWrite
	t.Cleanup(func() { atomicWrite = previous })
	atomicWrite = func(string, []byte) error { return ErrStateWrite }
	if err := SwitchVersion(appRoot, "0.2.0"); !errors.Is(err, ErrStateWrite) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "0.1.0")); err != nil {
		t.Fatalf("previous version removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "0.2.0")); err != nil {
		t.Fatalf("current version removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial state written: %v", err)
	}
}

func TestCleanupKeepsCurrentAndPrevious(t *testing.T) {
	base := t.TempDir()
	appRoot := filepath.Join(base, "app")
	dataRoot := filepath.Join(base, "data")
	mustWrite(t, filepath.Join(dataRoot, "project.txt"), []byte("keep"))
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0"} {
		mustMkdir(t, filepath.Join(appRoot, v))
	}
	if err := SwitchVersion(appRoot, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := CleanupUnusedVersions(appRoot, readState(t, appRoot)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old version not cleaned")
	}
	if _, err := os.Stat(filepath.Join(appRoot, "0.2.0")); err != nil {
		t.Fatalf("previous removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "0.3.0")); err != nil {
		t.Fatalf("current removed: %v", err)
	}
	if got := string(mustRead(t, filepath.Join(dataRoot, "project.txt"))); got != "keep" {
		t.Fatalf("data=%q", got)
	}
}

func TestCleanupRejectsTargetOutsideAppRoot(t *testing.T) {
	base := t.TempDir()
	appRoot := filepath.Join(base, "app")
	dataRoot := filepath.Join(base, "data")
	mustMkdir(t, appRoot)
	mustWrite(t, filepath.Join(dataRoot, "project.txt"), []byte("keep"))
	if err := removeVersionDir(appRoot, dataRoot); err == nil || !errors.Is(err, ErrOutsideAppRoot) {
		t.Fatalf("err=%v", err)
	}
	if err := removeVersionDir(appRoot, filepath.Join(appRoot, "..", "data")); err == nil || !errors.Is(err, ErrOutsideAppRoot) {
		t.Fatalf("rel err=%v", err)
	}
	if err := removeVersionDir(dataRoot, "0.1.0"); err == nil {
		t.Fatal("accepted dataRoot as appRoot argument")
	}
	if got := string(mustRead(t, filepath.Join(dataRoot, "project.txt"))); got != "keep" {
		t.Fatalf("data=%q", got)
	}
}

func TestCleanupRejectsSymlinkVersion(t *testing.T) {
	appRoot := filepath.Join(t.TempDir(), "app")
	outside := filepath.Join(t.TempDir(), "outside")
	mustMkdir(t, appRoot)
	mustMkdir(t, outside)
	mustWrite(t, filepath.Join(outside, "secret.txt"), []byte("secret"))
	link := filepath.Join(appRoot, "0.9.0")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	if err := removeVersionDir(appRoot, "0.9.0"); err == nil || !(errors.Is(err, ErrSymlink) || errors.Is(err, ErrOutsideAppRoot)) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("followed symlink and damaged outside data: %v", err)
	}
}

func readState(t *testing.T, appRoot string) VersionState {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(appRoot, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state VersionState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
