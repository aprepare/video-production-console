package catalogbuilder

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateListenRejectsNonLoopback(t *testing.T) {
	if err := ValidateListen("127.0.0.1:2031"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateListen("localhost:2031"); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"0.0.0.0:2031", ":2031", "192.168.1.8:2031", "example.com:2031"} {
		if err := ValidateListen(addr); err == nil {
			t.Fatalf("accepted %q", addr)
		}
	}
}

func TestServerPageConfigBuildPackAndMerge(t *testing.T) {
	mediaRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mediaRoot, "originals", "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "originals", "images", "still.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "catalog-builder.config.json")
	server, err := NewServer(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	page, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if page.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("素材建库工作台")) {
		t.Fatalf("page status=%d body=%s", page.StatusCode, body)
	}

	put, err := json.Marshal(Config{MediaRoot: mediaRoot, VisionAPIKey: "secret-key-xyz"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/config", bytes.NewReader(put))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var public Config
	if err := json.NewDecoder(resp.Body).Decode(&public); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || public.VisionAPIKey != "****-xyz" {
		t.Fatalf("public=%+v status=%d", public, resp.StatusCode)
	}

	masked, _ := json.Marshal(public)
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/config", bytes.NewReader(masked))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("masked put status=%d", resp.StatusCode)
	}
	saved, err := LoadConfig(configPath)
	if err != nil || saved.VisionAPIKey != "secret-key-xyz" {
		t.Fatalf("masked put overwrote the key: %+v err=%v", saved, err)
	}

	resp, err = http.Post(ts.URL+"/api/build", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	buildBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("build status=%d body=%s", resp.StatusCode, buildBody)
	}
	if bytes.Contains(buildBody, []byte("secret-key")) || bytes.Contains(buildBody, []byte(mediaRoot)) {
		t.Fatalf("build response leaked secrets or paths: %s", buildBody)
	}
	waitStatus(t, ts.URL, "ready")

	packResp, err := http.Get(ts.URL + "/api/pack")
	if err != nil {
		t.Fatal(err)
	}
	packBytes, _ := io.ReadAll(packResp.Body)
	_ = packResp.Body.Close()
	if packResp.StatusCode != http.StatusOK {
		t.Fatalf("pack status=%d body=%s", packResp.StatusCode, packBytes)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(packBytes), int64(len(packBytes)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zipReader.File {
		if strings.Contains(file.Name, "originals/") {
			t.Fatalf("downloaded pack contains original %q", file.Name)
		}
	}

	hostRoot := t.TempDir()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("media_root", hostRoot)
	part, err := writer.CreateFormFile("pack", "catalog-pack.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(packBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	mergeResp, err := http.Post(ts.URL+"/api/merge", writer.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var summary MergeSummary
	if err := json.NewDecoder(mergeResp.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	_ = mergeResp.Body.Close()
	if mergeResp.StatusCode != http.StatusOK || summary.ImportedSources != 1 {
		t.Fatalf("merge status=%d summary=%+v", mergeResp.StatusCode, summary)
	}
}

func TestServerRejectsNonLoopbackAndHidesInternalErrors(t *testing.T) {
	if err := ValidateListen("8.8.8.8:80"); err == nil {
		t.Fatal("public listen accepted")
	}
	configPath := filepath.Join(t.TempDir(), "catalog-builder.config.json")
	server, err := NewServer(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/api/merge", "application/x-www-form-urlencoded", strings.NewReader("media_root=relative&pack_path=nope"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("invalid merge accepted")
	}
	if bytes.Contains(body, []byte("secret")) {
		t.Fatalf("error leaked a secret: %s", body)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil || payload["error"] == "" {
		t.Fatalf("error payload=%s", body)
	}
}

func TestServerStatusExposesLogPathAndBuildDetail(t *testing.T) {
	mediaRoot := filepath.Join(t.TempDir(), "missing-media")
	configPath := filepath.Join(t.TempDir(), "catalog-builder.config.json")
	server, err := NewServer(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	put, err := json.Marshal(Config{MediaRoot: mediaRoot})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/config", bytes.NewReader(put))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status=%d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, "originals", "movies")); err != nil {
		t.Fatalf("save should create movie dir: %v", err)
	}

	resp, err = http.Post(ts.URL+"/api/build", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("build status=%d", resp.StatusCode)
	}
	waitStatus(t, ts.URL, "failed")

	statusResp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	_ = statusResp.Body.Close()
	if status["last_error"] != errNoMedia.Error() {
		t.Fatalf("status=%+v", status)
	}
	detail, _ := status["last_error_detail"].(string)
	if !strings.Contains(detail, "originals") {
		t.Fatalf("missing detail: %+v", status)
	}
	logPath, _ := status["log_path"].(string)
	if logPath == "" {
		t.Fatal("log_path missing")
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(logBytes), "no media") && !strings.Contains(string(logBytes), "originals") {
		t.Fatalf("log=%q err=%v", logBytes, err)
	}

	logResp, err := http.Get(ts.URL + "/api/log")
	if err != nil {
		t.Fatal(err)
	}
	logBody, _ := io.ReadAll(logResp.Body)
	_ = logResp.Body.Close()
	if logResp.StatusCode != http.StatusOK || !bytes.Contains(logBody, []byte("originals")) {
		t.Fatalf("log api status=%d body=%s", logResp.StatusCode, logBody)
	}
}

func waitStatus(t *testing.T, base, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		var status map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			_ = resp.Body.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if status["state"] == want {
			return
		}
		if status["state"] == "failed" {
			t.Fatalf("build failed: %+v", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status did not become %s", want)
}
