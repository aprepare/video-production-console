package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"

	_ "modernc.org/sqlite"
)

type jianyingRuntimeStub struct {
	runtime consoleSettings.Runtime
	err     error
}

func (s jianyingRuntimeStub) Runtime(context.Context) (consoleSettings.Runtime, error) {
	return s.runtime, s.err
}

func TestJianyingHealthEmptyRoot(t *testing.T) {
	handler := NewJianyingDraftsHandler(jianyingRuntimeStub{}, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		JianyingRoot     string   `json:"jianying_root"`
		RootExists       bool     `json:"root_exists"`
		InstalledVersion string   `json:"installed_version"`
		VerifiedVersions []string `json:"verified_versions"`
		Compatible       bool     `json:"compatible"`
		BackupCount      int      `json:"backup_count"`
		BackupBytes      int64    `json:"backup_bytes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RootExists || body.InstalledVersion != "" || body.Compatible || body.BackupCount != 0 || body.BackupBytes != 0 || len(body.VerifiedVersions) != 0 {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestJianyingHealthReportsInstallAndBackups(t *testing.T) {
	product := t.TempDir()
	draft := filepath.Join(product, "AppData", "User Data", "Projects", "com.lveditor.draft")
	if err := os.MkdirAll(filepath.Join(product, "11.1.0.9"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(product, "11.1.0.9", "JianyingPro.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(draft, "root_meta_info.json"), []byte(`{"all_draft_store":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(draft, "root_meta_info.one.bak.json"), []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewJianyingDraftsHandler(jianyingRuntimeStub{runtime: runtimeWith(t.TempDir(), draft, "")}, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		RootExists       bool     `json:"root_exists"`
		InstalledVersion string   `json:"installed_version"`
		VersionSource    string   `json:"version_source"`
		VerifiedVersions []string `json:"verified_versions"`
		Compatible       bool     `json:"compatible"`
		BackupCount      int      `json:"backup_count"`
		BackupBytes      int64    `json:"backup_bytes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.RootExists || body.InstalledVersion != "11.1.0.9" || body.VersionSource != "install_dir" || !body.Compatible || body.BackupCount != 1 || body.BackupBytes != 4 {
		t.Fatalf("body=%s", recorder.Body.String())
	}
	if len(body.VerifiedVersions) != 1 || body.VerifiedVersions[0] != "11.1" {
		t.Fatalf("verified=%v", body.VerifiedVersions)
	}
}

func TestJianyingPruneValidatesAndDefaultsToDryRun(t *testing.T) {
	draft := t.TempDir()
	old := time.Now().Add(-10 * 24 * time.Hour)
	for i := 0; i < 11; i++ {
		name := "root_meta_info.item-" + string(rune('a'+i)) + ".bak.json"
		path := filepath.Join(draft, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewJianyingDraftsHandler(jianyingRuntimeStub{runtime: runtimeWith(t.TempDir(), draft, "")}, nil)

	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/jianying-drafts/backups/prune", strings.NewReader(`{"keep_latest":0}`)))
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "invalid_keep_latest") {
		t.Fatalf("keep status=%d body=%s", bad.Code, bad.Body.String())
	}
	days := httptest.NewRecorder()
	handler.ServeHTTP(days, httptest.NewRequest(http.MethodPost, "/api/jianying-drafts/backups/prune", strings.NewReader(`{"older_than_days":366}`)))
	if days.Code != http.StatusBadRequest || !strings.Contains(days.Body.String(), "invalid_older_than_days") {
		t.Fatalf("days status=%d body=%s", days.Code, days.Body.String())
	}

	dry := httptest.NewRecorder()
	handler.ServeHTTP(dry, httptest.NewRequest(http.MethodPost, "/api/jianying-drafts/backups/prune", strings.NewReader(`{}`)))
	if dry.Code != http.StatusOK {
		t.Fatalf("dry status=%d body=%s", dry.Code, dry.Body.String())
	}
	var body struct {
		Deleted []struct {
			Name string `json:"name"`
		} `json:"deleted"`
		Kept int `json:"kept"`
	}
	if err := json.Unmarshal(dry.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Kept != 10 || len(body.Deleted) != 1 {
		t.Fatalf("dry body=%s", dry.Body.String())
	}
	left, err := os.ReadDir(draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 11 {
		t.Fatalf("dry run deleted files, left=%d", len(left))
	}

	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodPost, "/api/jianying-drafts/backups/prune", strings.NewReader(`{"dry_run":false}`)))
	if live.Code != http.StatusOK {
		t.Fatalf("live status=%d body=%s", live.Code, live.Body.String())
	}
	left, err = os.ReadDir(draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 10 {
		t.Fatalf("kept files=%d", len(left))
	}
}

func TestJianyingStoryboardReadsV2AndRejectsV1AndEscape(t *testing.T) {
	data := t.TempDir()
	db := openStoryboardDB(t)
	taskID := "11111111-1111-4111-8111-111111111111"
	output := filepath.Join(data, "out")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(data, "manifest.json"), map[string]any{"output_dir": output})
	sourceIn, sourceOut := 1.5, 4.5
	writeJSONFile(t, filepath.Join(output, "production_plan.json"), map[string]any{
		"plan_version": "2.0", "project_name": "项目", "project_duration_s": 8.5,
		"timeline": []any{
			map[string]any{"shot_no": 1, "start_s": 0, "end_s": 3, "media_kind": "broll", "source_path": filepath.Join(data, "clip.mp4"), "source_in_s": sourceIn, "source_out_s": sourceOut},
			map[string]any{"shot_no": 2, "start_s": 3, "end_s": 8.5, "media_kind": "image", "source_path": filepath.Join(data, "still.jpg")},
		},
		"graphics": map[string]any{
			"title": map[string]any{"text": "板上标题"},
			"captions": map[string]any{"items": []any{
				map[string]any{"text": "第一句", "start_s": 0.2, "end_s": 2.8},
				map[string]any{"text": "第二句", "start_s": 3.1, "end_s": 6},
			}},
		},
	})
	if _, err := db.Exec(`INSERT INTO codex_tasks(id, manifest_path) VALUES(?, ?)`, taskID, filepath.Join(data, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	handler := NewJianyingDraftsHandler(jianyingRuntimeStub{runtime: runtimeWith(data, "", "")}, db)

	okRec := httptest.NewRecorder()
	handler.ServeHTTP(okRec, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard?task_id="+taskID, nil))
	if okRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", okRec.Code, okRec.Body.String())
	}
	var body struct {
		Supported bool    `json:"supported"`
		Title     string  `json:"title"`
		DurationS float64 `json:"duration_s"`
		Shots     []struct {
			ShotNo     int      `json:"shot_no"`
			SourceInS  *float64 `json:"source_in_s"`
			SourceOutS *float64 `json:"source_out_s"`
			MediaKind  string   `json:"media_kind"`
			Caption    string   `json:"caption"`
		} `json:"shots"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Supported || body.Title != "板上标题" || body.DurationS != 8.5 || len(body.Shots) != 2 {
		t.Fatalf("body=%s", okRec.Body.String())
	}
	if body.Shots[0].Caption != "第一句" || body.Shots[0].MediaKind != "broll" || body.Shots[0].SourceInS == nil || *body.Shots[0].SourceInS != 1.5 {
		t.Fatalf("shot0=%+v", body.Shots[0])
	}
	if body.Shots[1].Caption != "第二句" || body.Shots[1].SourceInS != nil || body.Shots[1].MediaKind != "image" {
		t.Fatalf("shot1=%+v", body.Shots[1])
	}

	writeJSONFile(t, filepath.Join(output, "production_plan.json"), map[string]any{
		"plan_version": "1.0",
		"timeline":     []any{map[string]any{"shot_no": 1, "transition": "fade"}},
	})
	v1 := httptest.NewRecorder()
	handler.ServeHTTP(v1, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard?task_id="+taskID, nil))
	if v1.Code != http.StatusOK || strings.TrimSpace(v1.Body.String()) != `{"supported":false}` {
		t.Fatalf("v1 status=%d body=%s", v1.Code, v1.Body.String())
	}

	outside := t.TempDir()
	writeJSONFile(t, filepath.Join(data, "manifest.json"), map[string]any{"output_dir": outside})
	escaped := httptest.NewRecorder()
	handler.ServeHTTP(escaped, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard?task_id="+taskID, nil))
	if escaped.Code != http.StatusForbidden {
		t.Fatalf("escape status=%d body=%s", escaped.Code, escaped.Body.String())
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard?task_id=22222222-2222-4222-8222-222222222222", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestJianyingStoryboardFrameUsesFFmpegCache(t *testing.T) {
	data := t.TempDir()
	db := openStoryboardDB(t)
	taskID := "33333333-3333-4333-8333-333333333333"
	output := filepath.Join(data, "out")
	source := filepath.Join(data, "clip.mp4")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(data, "manifest.json"), map[string]any{"output_dir": output})
	writeJSONFile(t, filepath.Join(output, "production_plan.json"), map[string]any{
		"plan_version": "2.0", "project_duration_s": 3, "project_name": "t",
		"timeline": []any{map[string]any{"shot_no": 1, "start_s": 0, "end_s": 3, "media_kind": "broll", "source_path": source, "source_in_s": 1.5, "source_out_s": 4}},
		"graphics": map[string]any{"title": map[string]any{"text": "t"}},
	})
	if _, err := db.Exec(`INSERT INTO codex_tasks(id, manifest_path) VALUES(?, ?)`, taskID, filepath.Join(data, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	missingFFmpeg := NewJianyingDraftsHandler(jianyingRuntimeStub{runtime: runtimeWith(data, "", filepath.Join(data, "missing-ffmpeg.exe"))}, db)
	unavailable := httptest.NewRecorder()
	missingFFmpeg.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard/frame?task_id="+taskID+"&shot=1", nil))
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing ffmpeg status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}

	ffmpeg := buildFakeFFmpeg(t)
	handler := NewJianyingDraftsHandler(jianyingRuntimeStub{runtime: runtimeWith(data, "", ffmpeg)}, db)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard/frame?task_id="+taskID+"&shot=1", nil))
	if first.Code != http.StatusOK || first.Body.Len() < 2 || first.Body.Bytes()[0] != 0xFF {
		t.Fatalf("frame status=%d type=%s body=%q", first.Code, first.Header().Get("Content-Type"), first.Body.Bytes())
	}
	args, err := os.ReadFile(filepath.Join(data, "cache", "storyboard", taskID, "ffmpeg-args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(args)
	if !strings.Contains(text, "1.700") || !strings.Contains(text, "scale=320:-1") || !strings.Contains(text, source) {
		t.Fatalf("args=%s", text)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/jianying-drafts/storyboard/frame?task_id="+taskID+"&shot=1", nil))
	if second.Code != http.StatusOK {
		t.Fatalf("cached status=%d body=%s", second.Code, second.Body.String())
	}
	runs, err := os.ReadFile(filepath.Join(data, "cache", "storyboard", taskID, "ffmpeg-runs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(runs)) != "1" {
		t.Fatalf("ffmpeg runs=%s", runs)
	}
}

func runtimeWith(dataRoot, jianyingRoot, ffmpeg string) consoleSettings.Runtime {
	return consoleSettings.Runtime{PublicSettings: domain.PublicSettings{DataRoot: dataRoot, JianyingRoot: jianyingRoot, FFmpegPath: ffmpeg}}
}

func openStoryboardDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE codex_tasks (id TEXT PRIMARY KEY, manifest_path TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildFakeFFmpeg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package main
import (
  "os"
  "path/filepath"
  "strconv"
  "strings"
)
func main() {
  if len(os.Args) < 2 { os.Exit(2) }
  out := os.Args[len(os.Args)-1]
  dir := filepath.Dir(out)
  _ = os.WriteFile(filepath.Join(dir, "ffmpeg-args.txt"), []byte(strings.Join(os.Args[1:], "\n")), 0o644)
  counter := filepath.Join(dir, "ffmpeg-runs.txt")
  n := 0
  if b, err := os.ReadFile(counter); err == nil { n, _ = strconv.Atoi(strings.TrimSpace(string(b))) }
  _ = os.WriteFile(counter, []byte(strconv.Itoa(n+1)), 0o644)
  if err := os.WriteFile(out, []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil { os.Exit(1) }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fakeffmpeg\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "ffmpeg.exe")
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake ffmpeg: %v\n%s", err, out)
	}
	return exe
}
