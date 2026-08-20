package partnerprofile

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildProfileUsesOnlyBundledExecutablesAndSelectedRoots(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	dataRoot := filepath.Join(t.TempDir(), "数据")
	mediaRoot := filepath.Join(t.TempDir(), "风景 素材")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "剪映草稿")
	mustMkdir(t, jianyingRoot)

	profile, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: dataRoot, MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err != nil {
		t.Fatal(err)
	}
	if profile.PythonBinary != filepath.Join(appRoot, "runtime", "python", "python.exe") {
		t.Fatalf("python=%q", profile.PythonBinary)
	}
	if profile.MediaIndexPath != filepath.Join(dataRoot, "media", "index.json") {
		t.Fatalf("index=%q", profile.MediaIndexPath)
	}
	if !filepath.IsAbs(profile.JianyingRoot) {
		t.Fatalf("jianying=%q", profile.JianyingRoot)
	}
	if profile.MediaRoot != mediaRoot && profile.MediaRoot != filepath.Clean(mediaRoot) {
		t.Fatalf("media=%q", profile.MediaRoot)
	}
	if profile.MontageResourcesPath != filepath.Join(appRoot, "resources", "montage") {
		t.Fatalf("resources=%q", profile.MontageResourcesPath)
	}

	raw, err := os.ReadFile(filepath.Join(dataRoot, "config", "machine-profile.json"))
	if err != nil {
		t.Fatalf("profile file: %v", err)
	}
	var persisted MachineProfile
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.PythonBinary != profile.PythonBinary {
		t.Fatalf("persisted python=%q", persisted.PythonBinary)
	}
	if strings.EqualFold(filepath.Base(persisted.PythonBinary), "python") && !strings.Contains(persisted.PythonBinary, "runtime") {
		t.Fatalf("partner profile fell back to PATH python: %q", persisted.PythonBinary)
	}
	if persisted.SchemaVersion != ProfileSchemaVersion {
		t.Fatalf("schema_version=%q", persisted.SchemaVersion)
	}
	for _, key := range []string{
		"bgm_yawaraka_hikari", "sfx_opening_hit", "sfx_water_drop",
		"sfx_whoosh", "sfx_conclusion_hit", "transition_cross_dissolve",
	} {
		path := persisted.CachePaths[key]
		if path == "" {
			t.Fatalf("cache path %s is empty", key)
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("cache path %s does not exist: %v", key, err)
		}
	}
}

func TestBuildProfileRejectsMissingCacheResources(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	if err := os.Remove(filepath.Join(appRoot, "resources", "montage", "bgm", "yawaraka_hikari.mp3")); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	_, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: t.TempDir(), MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err == nil {
		t.Fatal("accepted missing bgm cache resource")
	}
	if CodeOf(err) != CodeMissingMontageResources {
		t.Fatalf("err=%v", err)
	}
}

func TestRefreshBundledRuntimeBackfillsSchemaAndCachePaths(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	dataRoot := t.TempDir()
	legacy := `{
  "python_binary": ` + jsonString(filepath.Join(appRoot, "runtime", "python", "python.exe")) + `,
  "jianying_root": ` + jsonString(t.TempDir()) + `,
  "media_root": ` + jsonString(t.TempDir()) + `,
  "media_index_path": ` + jsonString(filepath.Join(dataRoot, "media", "index.json")) + `,
  "montage_resources_path": ` + jsonString(filepath.Join(appRoot, "resources", "montage")) + `
}`
	mustWrite(t, ProfilePath(dataRoot), []byte(legacy))

	profile, changed, err := RefreshBundledRuntime(dataRoot, appRoot)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if profile.SchemaVersion != ProfileSchemaVersion {
		t.Fatalf("schema_version=%q", profile.SchemaVersion)
	}
	if len(profile.CachePaths) != 6 {
		t.Fatalf("cache paths=%v", profile.CachePaths)
	}
	for key, path := range profile.CachePaths {
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Fatalf("cache path %s missing: %v", key, statErr)
		}
	}
}

func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestBuildProfileRejectsWindowsAndDriveRoots(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	dataRoot := t.TempDir()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	opts := BuildOptions{AppRoot: appRoot, DataRoot: dataRoot, MediaRoot: mediaRoot, JianyingRoot: jianyingRoot}

	volumeRoot := filepath.VolumeName(t.TempDir()) + string(os.PathSeparator)
	for _, root := range []string{volumeRoot, `C:\`, `D:\`} {
		bad := opts
		bad.JianyingRoot = root
		if _, err := Build(bad); err == nil {
			t.Fatalf("accepted jianying root %q", root)
		}
		bad = opts
		bad.MediaRoot = root
		if _, err := Build(bad); err == nil {
			t.Fatalf("accepted media root %q", root)
		}
	}
}

func TestBuildProfileRejectsMissingOriginals(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, mediaRoot)
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	_, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: t.TempDir(), MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err == nil {
		t.Fatal("accepted media root without originals")
	}
	if !strings.Contains(err.Error(), "originals") && CodeOf(err) != CodeMissingOriginals {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildProfileRejectsUnwritableDraftDirectory(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	blocked := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: t.TempDir(), MediaRoot: mediaRoot, JianyingRoot: blocked})
	if err == nil {
		t.Fatal("accepted unwritable draft path")
	}
}

func TestBuildProfileRejectsReparsePoint(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("reparse validation is Windows-specific")
	}
	appRoot := createBundledRuntimeFixture(t)
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	target := filepath.Join(t.TempDir(), "real-drafts")
	mustMkdir(t, target)
	junction := filepath.Join(t.TempDir(), "alias-drafts")
	cmd := exec.Command("cmd", "/c", "mklink", "/J", junction, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not create junction: %v %s", err, out)
	}
	_, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: t.TempDir(), MediaRoot: mediaRoot, JianyingRoot: junction})
	if err == nil {
		t.Fatal("accepted reparse point draft directory")
	}
}

func TestBuildProfileRejectsAbsentPyJianYingDraft(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	if err := os.RemoveAll(filepath.Join(appRoot, "runtime", "python", "Lib", "site-packages", "pyJianYingDraft")); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	_, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: t.TempDir(), MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err == nil {
		t.Fatal("accepted missing pyJianYingDraft")
	}
}

func TestBuildProfileRejectsAbsentFFmpeg(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	if err := os.Remove(filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffmpeg.exe")); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	_, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: t.TempDir(), MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err == nil {
		t.Fatal("accepted missing ffmpeg")
	}
}

func TestBuildProfileDoesNotFallBackToPATHPython(t *testing.T) {
	dataRoot := t.TempDir()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	_, err := Build(BuildOptions{AppRoot: t.TempDir(), DataRoot: dataRoot, MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err == nil {
		t.Fatal("accepted missing bundled python")
	}
	if _, statErr := os.Stat(filepath.Join(dataRoot, "config", "machine-profile.json")); statErr == nil {
		t.Fatal("wrote a profile after falling back to PATH python")
	}
}

func TestRefreshBundledRuntimeRewritesStaleAppVersionPaths(t *testing.T) {
	oldApp := createBundledRuntimeFixture(t)
	newApp := createBundledRuntimeFixture(t)
	dataRoot := t.TempDir()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "drafts")
	mustMkdir(t, jianyingRoot)
	if _, err := Build(BuildOptions{AppRoot: oldApp, DataRoot: dataRoot, MediaRoot: mediaRoot, JianyingRoot: jianyingRoot}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(oldApp); err != nil {
		t.Fatal(err)
	}
	profile, changed, err := RefreshBundledRuntime(dataRoot, newApp)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	wantPython := filepath.Join(newApp, "runtime", "python", "python.exe")
	if profile.PythonBinary != wantPython {
		t.Fatalf("python=%q want %q", profile.PythonBinary, wantPython)
	}
	if profile.JianyingRoot == "" || profile.MediaRoot == "" {
		t.Fatal("refresh must keep user directories")
	}
}

func TestDetectJianyingRootPrefersWritableDraftCandidate(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	preferred := filepath.Join(local, "JianyingPro", "User Data", "Projects", "com.lveditor.draft")
	fallback := filepath.Join(local, "JianyingPro", "User Data", "Projects")
	mustMkdir(t, preferred)
	mustMkdir(t, fallback)
	got, err := DetectJianyingRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != preferred {
		t.Fatalf("detected=%q want %q", got, preferred)
	}
}

func createBundledRuntimeFixture(t *testing.T) string {
	t.Helper()
	appRoot := t.TempDir()
	mustWrite(t, filepath.Join(appRoot, "runtime", "python", "python.exe"), []byte("python"))
	mustWrite(t, filepath.Join(appRoot, "runtime", "python", "Lib", "site-packages", "pyJianYingDraft", "__init__.py"), []byte("#"))
	mustWrite(t, filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffmpeg.exe"), []byte("ffmpeg"))
	mustWrite(t, filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffprobe.exe"), []byte("ffprobe"))
	resources := filepath.Join(appRoot, "resources", "montage")
	mustWrite(t, filepath.Join(resources, "bgm", "yawaraka_hikari.mp3"), []byte("bgm"))
	for _, name := range []string{"opening_hit", "water_drop", "whoosh", "conclusion_hit"} {
		mustWrite(t, filepath.Join(resources, "sfx", name+".mp3"), []byte("sfx"))
	}
	mustMkdir(t, filepath.Join(resources, "transitions", "cross_dissolve"))
	return appRoot
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
