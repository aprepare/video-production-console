package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/security"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type setupFixture struct {
	Handler          http.Handler
	AppRoot          string
	DataRoot         string
	MediaRoot        string
	JianyingRoot     string
	RestartRequested chan struct{}
}

func (f *setupFixture) ProfileExists() bool {
	_, err := os.Stat(filepath.Join(f.DataRoot, "config", "machine-profile.json"))
	return err == nil
}

func (f *setupFixture) MediaIndexExists() bool {
	_, err := os.Stat(filepath.Join(f.DataRoot, "media", "index.json"))
	return err == nil
}

func newSetupFixture(t *testing.T) *setupFixture {
	t.Helper()
	appRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(appRoot, "runtime", "python", "python.exe"), []byte("py"))
	mustWriteFile(t, filepath.Join(appRoot, "runtime", "python", "Lib", "site-packages", "pyJianYingDraft", "__init__.py"), []byte("#"))
	mustWriteFile(t, filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffmpeg.exe"), []byte("ff"))
	mustWriteFile(t, filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffprobe.exe"), []byte("fp"))
	montageResources := filepath.Join(appRoot, "resources", "montage")
	mustWriteFile(t, filepath.Join(montageResources, "bgm", "yawaraka_hikari.mp3"), []byte("bgm"))
	for _, name := range []string{"opening_hit", "water_drop", "whoosh", "conclusion_hit"} {
		mustWriteFile(t, filepath.Join(montageResources, "sfx", name+".mp3"), []byte("sfx"))
	}
	if err := os.MkdirAll(filepath.Join(montageResources, "transitions", "cross_dissolve"), 0o700); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(t.TempDir(), "数据")
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(t.TempDir(), "风景 素材")
	if err := os.MkdirAll(filepath.Join(mediaRoot, "originals", "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(mediaRoot, "originals", "images", "clip.jpg"), []byte("jpg"))
	jianyingRoot := filepath.Join(t.TempDir(), "剪映草稿")
	if err := os.MkdirAll(jianyingRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(filepath.Join(dataRoot, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service := consoleSettings.NewService(store.NewSettingsRepository(database), security.NewSecretProtector())
	if err := service.InitializeBootSettings(context.Background(), consoleSettings.BootSettings{
		ListenAddr:      "127.0.0.1:2030",
		DataRoot:        dataRoot,
		CodexBinaryPath: filepath.Join(t.TempDir(), "codex.exe"),
	}); err != nil {
		t.Fatal(err)
	}
	restart := make(chan struct{}, 1)
	handler := NewPartnerSetupHandler(PartnerSetupOptions{
		AppRoot:  appRoot,
		DataRoot: dataRoot,
		Settings: service,
		Restart: func() {
			select {
			case restart <- struct{}{}:
			default:
			}
		},
	})
	return &setupFixture{
		Handler:          handler,
		AppRoot:          appRoot,
		DataRoot:         dataRoot,
		MediaRoot:        mediaRoot,
		JianyingRoot:     jianyingRoot,
		RestartRequested: restart,
	}
}

func TestPartnerSetupCreatesProfileIndexesMediaAndRequestsRestart(t *testing.T) {
	fixture := newSetupFixture(t)
	body := fmt.Sprintf(`{"jianying_root":%q,"media_root":%q}`, fixture.JianyingRoot, fixture.MediaRoot)
	w := httptest.NewRecorder()
	fixture.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/partner/setup", strings.NewReader(body)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !fixture.ProfileExists() || !fixture.MediaIndexExists() {
		t.Fatal("setup artifacts missing")
	}
	indexPath := filepath.Join(fixture.DataRoot, "media", "index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		t.Fatalf("setup must write usable clips, index=%s", raw)
	}
	select {
	case <-fixture.RestartRequested:
	case <-time.After(2 * time.Second):
		t.Fatal("restart not requested")
	}
}

func TestPartnerSetupGETReturnsSanitizedStatus(t *testing.T) {
	fixture := newSetupFixture(t)
	w := httptest.NewRecorder()
	fixture.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/partner/setup", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Complete             bool     `json:"complete"`
		DetectedJianyingRoot string   `json:"detected_jianying_root"`
		Codes                []string `json:"codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Complete {
		t.Fatal("expected incomplete setup")
	}
	for _, code := range payload.Codes {
		if strings.Contains(code, `\`) || strings.Contains(code, "/") || strings.Contains(strings.ToLower(code), "secret") {
			t.Fatalf("unsanitized code %q", code)
		}
	}
}

func TestPartnerSetupPOSTRejectsUnknownFields(t *testing.T) {
	fixture := newSetupFixture(t)
	w := httptest.NewRecorder()
	body := fmt.Sprintf(`{"jianying_root":%q,"media_root":%q,"catalog_db":"C:\\catalog.db"}`, fixture.JianyingRoot, fixture.MediaRoot)
	fixture.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/partner/setup", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func mustWriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
