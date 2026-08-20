package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/portable"
)

func TestInstallAndLaunchUsesLocalAppDataAndSeparateDataRoot(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	var got commandSpec
	launcher.start = func(spec commandSpec) error { got = spec; return nil }
	if err := launcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantApp := filepath.Join(local, "VideoProductionConsole", "app", "0.1.0")
	if got.Path != filepath.Join(wantApp, "bin", "video-production-console.exe") {
		t.Fatalf("path=%q", got.Path)
	}
	assertEnv(t, got.Env, "VIDEO_CONSOLE_APP_ROOT", wantApp)
	assertEnv(t, got.Env, "VIDEO_CONSOLE_DATA_ROOT", filepath.Join(local, "VideoProductionConsole", "data"))
	assertEnv(t, got.Env, "VIDEO_CONSOLE_LAUNCHED", "1")
	if _, err := os.Stat(filepath.Join(local, "VideoProductionConsole", "data")); err != nil {
		t.Fatalf("data root missing: %v", err)
	}
}

func TestLaunchRejectsMissingLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	os.Unsetenv("LOCALAPPDATA")
	launcher := newLauncherFixture(t, "0.1.0")
	launcher.start = func(commandSpec) error { t.Fatal("started without LocalAppData"); return nil }
	if err := launcher.Run(context.Background()); err == nil {
		t.Fatal("accepted missing LocalAppData")
	}
}

func TestLaunchRejectsCorruptOverlay(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	if err := os.WriteFile(launcher.executable, []byte("MZ-corrupt-overlay"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher.start = func(commandSpec) error { t.Fatal("started corrupt overlay"); return nil }
	if err := launcher.Run(context.Background()); err == nil {
		t.Fatal("accepted corrupt overlay")
	}
}

func TestLaunchRejectsHashFailure(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	writeFixtureExe(t, launcher.executable, "0.1.0", map[string][]byte{"bin/video-production-console.exe": []byte("bad")}, func(m *portable.Manifest) {
		m.Entries = []portable.Entry{{Path: "bin/video-production-console.exe", Size: 3, SHA256: sha256Hex([]byte("app"))}}
	})
	launcher.start = func(commandSpec) error { t.Fatal("started hash-failed payload"); return nil }
	err := launcher.Run(context.Background())
	if err == nil || !errors.Is(err, portable.ErrEntryHash) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "VideoProductionConsole", "app", "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed bad version: %v", err)
	}
}

func TestLaunchRestartsOnExit75AtMostTwice(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	starts := 0
	launcher.now = func() time.Time { return time.Unix(1000, 0) }
	launcher.start = func(commandSpec) error {
		starts++
		return exitError{code: restartExitCode}
	}
	err := launcher.Run(context.Background())
	if err == nil {
		t.Fatal("expected restart-loop error")
	}
	if starts != 3 {
		t.Fatalf("starts=%d want 3 (initial + 2 restarts)", starts)
	}
}

func TestLaunchPropagatesNormalExit(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	launcher.start = func(commandSpec) error { return exitError{code: 3} }
	err := launcher.Run(context.Background())
	var got exitError
	if !errors.As(err, &got) || got.code != 3 {
		t.Fatalf("err=%v", err)
	}
}

func TestLaunchPreservesPreviousAfterFailedChildStart(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	first := newLauncherFixture(t, "0.1.0")
	first.start = func(commandSpec) error { return nil }
	if err := first.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(local, "VideoProductionConsole", "data", "project.txt"), []byte("keep"))

	second := newLauncherFixture(t, "0.2.0")
	second.start = func(commandSpec) error { return errors.New("child failed") }
	if err := second.Run(context.Background()); err == nil {
		t.Fatal("expected child start failure")
	}
	if _, err := os.Stat(filepath.Join(local, "VideoProductionConsole", "app", "0.1.0")); err != nil {
		t.Fatalf("previous version removed: %v", err)
	}
	state, err := portable.ReadState(filepath.Join(local, "VideoProductionConsole", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "0.2.0" || state.Previous != "0.1.0" {
		t.Fatalf("state=%+v", state)
	}
	if got := string(mustRead(t, filepath.Join(local, "VideoProductionConsole", "data", "project.txt"))); got != "keep" {
		t.Fatalf("data=%q", got)
	}
}

func TestLaunchCleansUpAfterHealthyChild(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	for _, version := range []string{"0.1.0", "0.2.0", "0.3.0"} {
		launcher := newLauncherFixture(t, version)
		launcher.start = func(commandSpec) error { return nil }
		launcher.health = func(context.Context, string) error { return nil }
		if err := launcher.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(local, "VideoProductionConsole", "app", "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old version survived healthy start")
	}
	if _, err := os.Stat(filepath.Join(local, "VideoProductionConsole", "app", "0.2.0")); err != nil {
		t.Fatalf("previous removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "VideoProductionConsole", "app", "0.3.0")); err != nil {
		t.Fatalf("current removed: %v", err)
	}
}

func TestInspectPayloadPrintsOverlayEntries(t *testing.T) {
	launcher := newLauncherFixture(t, "0.1.0")
	raw, err := inspectPayloadJSON(launcher.executable)
	if err != nil {
		t.Fatal(err)
	}
	var payload portable.Manifest
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json=%s err=%v", raw, err)
	}
	if payload.SchemaVersion != portable.SchemaVersion || payload.AppVersion != "0.1.0" || len(payload.Entries) == 0 {
		t.Fatalf("payload=%+v", payload)
	}
	found := false
	for _, entry := range payload.Entries {
		if entry.Path == "bin/video-production-console.exe" && entry.Size > 0 && len(entry.SHA256) == 64 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing overlay entry: %+v", payload.Entries)
	}
}

func TestPollHealthOnceFailsFastWhenPortIsClosed(t *testing.T) {
	start := time.Now()
	err := pollHealthOnce(context.Background(), "http://127.0.0.1:1/api/health")
	if err == nil {
		t.Fatal("expected closed-port health check to fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("reuse check took %s; cold start must not poll for 8s", elapsed)
	}
}

func TestWatchHealthUntilChildIgnoresFailedHealthWhileChildStillRuns(t *testing.T) {
	child := make(chan error)
	opened := false
	done := make(chan struct{})
	var healthy bool
	var childErr error
	go func() {
		defer close(done)
		healthy, childErr = watchHealthUntilChild(context.Background(), func(context.Context) error {
			return errors.New("health timeout")
		}, child, func() { opened = true })
	}()
	select {
	case <-done:
		t.Fatal("health failure reported while child was still running")
	case <-time.After(50 * time.Millisecond):
	}
	child <- errors.New("child still starting")
	<-done
	if opened || healthy {
		t.Fatalf("opened=%v healthy=%v", opened, healthy)
	}
	if childErr == nil || childErr.Error() != "child still starting" {
		t.Fatalf("childErr=%v", childErr)
	}
}

func TestWatchHealthUntilChildOpensUIWhenHealthSucceeds(t *testing.T) {
	child := make(chan error)
	opened := make(chan struct{})
	done := make(chan struct{})
	var healthy bool
	go func() {
		defer close(done)
		healthy, _ = watchHealthUntilChild(context.Background(), func(context.Context) error {
			return nil
		}, child, func() { close(opened) })
	}()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("UI was not opened after successful health")
	}
	select {
	case <-done:
		t.Fatal("returned before child exited")
	default:
	}
	close(child)
	<-done
	if !healthy {
		t.Fatal("expected healthy after successful health")
	}
}

func TestLaunchRefusesOccupiedUnhealthyLoopback(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	launcher.loopbackReady = func(context.Context) error { return errors.New("not healthy") }
	launcher.portBusy = func() bool { return true }
	launcher.start = func(commandSpec) error { t.Fatal("started child on a busy unhealthy port"); return nil }
	err := launcher.Run(context.Background())
	if !errors.Is(err, errLoopbackBusy) {
		t.Fatalf("err=%v", err)
	}
}

func TestPollLoopbackHealthAcceptsAPIHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","edition":"partner"}`))
	}))
	t.Cleanup(server.Close)
	if err := pollHealthURL(context.Background(), server.URL+"/api/health"); err != nil {
		t.Fatal(err)
	}
}

func TestNewProductionLauncherInstallsHealthCheck(t *testing.T) {
	instance, err := newProductionLauncher()
	if err != nil {
		t.Fatal(err)
	}
	if instance.health == nil {
		t.Fatal("production health hook is nil; unused versions would never be cleaned")
	}
	if instance.loopbackReady == nil {
		t.Fatal("production loopback reuse hook is nil; a second launch would hide a running console")
	}
	if instance.portBusy == nil {
		t.Fatal("production busy-port hook is nil; a second console would collide on 2030")
	}
}

func TestLaunchReusesHealthyLoopbackWithoutStartingChild(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	opened := ""
	launcher.loopbackReady = func(context.Context) error { return nil }
	launcher.openUI = func(url string) error { opened = url; return nil }
	launcher.start = func(commandSpec) error { t.Fatal("started child while loopback was healthy"); return nil }
	if err := launcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opened != loopbackConsoleURL {
		t.Fatalf("opened=%q", opened)
	}
}

func TestLaunchDoesNotPassAPIKeysOrGatewayOverrides(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("VIDEO_CONSOLE_OPENAI_API_KEY", "secret-key")
	t.Setenv("VIDEO_CONSOLE_INTENT_API_KEY", "intent-key")
	t.Setenv("VIDEO_CONSOLE_INTENT_BASE_URL", "http://127.0.0.1:9")
	t.Setenv("PARTNER_GATEWAY_UPSTREAM", "http://127.0.0.1:2001/v1")
	launcher := newLauncherFixture(t, "0.1.0")
	var got commandSpec
	launcher.start = func(spec commandSpec) error { got = spec; return nil }
	if err := launcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"VIDEO_CONSOLE_OPENAI_API_KEY",
		"VIDEO_CONSOLE_INTENT_API_KEY",
		"VIDEO_CONSOLE_INTENT_BASE_URL",
		"PARTNER_GATEWAY_UPSTREAM",
	} {
		if value, ok := lookupEnv(got.Env, key); ok {
			t.Fatalf("leaked %s=%q", key, value)
		}
	}
}

func newLauncherFixture(t *testing.T, version string) *launcher {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "partner.exe")
	writeFixtureExe(t, exe, version, map[string][]byte{"bin/video-production-console.exe": []byte("console")}, nil)
	return &launcher{
		executable: exe,
		lookupEnv:  os.LookupEnv,
		environ:    os.Environ,
		now:        time.Now,
	}
}

func writeFixtureExe(t *testing.T, path, version string, files map[string][]byte, mutate func(*portable.Manifest)) {
	t.Helper()
	zipBytes := mustZip(t, files)
	manifest := portable.Manifest{SchemaVersion: portable.SchemaVersion, AppVersion: version, PayloadSHA256: sha256Hex(zipBytes)}
	for name, body := range files {
		manifest.Entries = append(manifest.Entries, portable.Entry{Path: name, Size: int64(len(body)), SHA256: sha256Hex(body)})
	}
	sortManifestEntries(&manifest)
	if mutate != nil {
		mutate(&manifest)
		if manifest.PayloadSHA256 == "" {
			manifest.PayloadSHA256 = sha256Hex(zipBytes)
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte("MZ-stub"), zipBytes...)
	out = append(out, raw...)
	trailer := make([]byte, 32)
	copy(trailer[:16], []byte("VPCPARTNERPAY01!"))
	binary.LittleEndian.PutUint64(trailer[16:24], uint64(len(zipBytes)))
	binary.LittleEndian.PutUint64(trailer[24:32], uint64(len(raw)))
	out = append(out, trailer...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, name := range names {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sortManifestEntries(manifest *portable.Manifest) {
	for i := 0; i < len(manifest.Entries); i++ {
		for j := i + 1; j < len(manifest.Entries); j++ {
			if manifest.Entries[j].Path < manifest.Entries[i].Path {
				manifest.Entries[i], manifest.Entries[j] = manifest.Entries[j], manifest.Entries[i]
			}
		}
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func assertEnv(t *testing.T, env []string, key, want string) {
	t.Helper()
	got, ok := lookupEnv(env, key)
	if !ok || got != want {
		t.Fatalf("%s=%q want %q env=%v", key, got, want, env)
	}
}

func lookupEnv(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix), true
		}
	}
	return "", false
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
