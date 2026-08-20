package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"video-production-console/internal/portable"
)

const (
	productDirName     = "VideoProductionConsole"
	childBinaryName    = "video-production-console.exe"
	loopbackConsoleURL = "http://127.0.0.1:2030"
	loopbackHealthURL  = loopbackConsoleURL + "/api/health"
	restartExitCode    = 75
	maxRestarts        = 2
	restartWindow      = 60 * time.Second
)

var errLoopbackBusy = errors.New("本机 2030 端口已被其他程序占用，且不是可用的伙伴控制台。请结束占用该端口的程序后重试。")

type commandSpec struct {
	Path string
	Env  []string
	Dir  string
}

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func (e exitError) ExitCode() int { return e.code }

type launcher struct {
	executable    string
	lookupEnv     func(string) (string, bool)
	environ       func() []string
	start         func(spec commandSpec) error
	health        func(ctx context.Context, versionDir string) error
	loopbackReady func(ctx context.Context) error
	portBusy      func() bool
	openUI        func(url string) error
	now           func() time.Time
}

func main() {
	if handled, err := maybeInspectPayload(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	instance, err := newProductionLauncher()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := instance.Run(ctx); err != nil {
		var exited exitError
		if errors.As(err, &exited) {
			os.Exit(exited.code)
		}
		reportLaunchError(err)
		os.Exit(1)
	}
}

func newProductionLauncher() (*launcher, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &launcher{
		executable:    executable,
		lookupEnv:     os.LookupEnv,
		environ:       os.Environ,
		now:           time.Now,
		health:        func(ctx context.Context, _ string) error { return pollHealthUntil(ctx, loopbackHealthURL) },
		loopbackReady: func(ctx context.Context) error { return pollHealthOnce(ctx, loopbackHealthURL) },
		portBusy:      loopbackPortBusy,
		openUI:        defaultOpenUI,
	}, nil
}

func maybeInspectPayload(args []string, stdout io.Writer) (bool, error) {
	if len(args) != 1 || args[0] != "--inspect-payload" {
		return false, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return true, err
	}
	raw, err := inspectPayloadJSON(executable)
	if err != nil {
		return true, err
	}
	_, err = stdout.Write(append(raw, '\n'))
	return true, err
}

func inspectPayloadJSON(executable string) ([]byte, error) {
	file, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	overlay, err := portable.ReadOverlay(file, info.Size())
	if err != nil {
		return nil, err
	}
	return json.Marshal(overlay.Manifest)
}

func pollLoopbackHealth(ctx context.Context, _ string) error {
	return pollHealthURL(ctx, loopbackHealthURL)
}

func loopbackPortBusy() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:2030", 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func pollHealthOnce(ctx context.Context, rawURL string) error {
	return probeHealthURL(ctx, rawURL)
}

func pollHealthUntil(ctx context.Context, rawURL string) error {
	return pollHealthURLWindow(ctx, rawURL, 0)
}

func pollHealthURL(ctx context.Context, rawURL string) error {
	return pollHealthURLWindow(ctx, rawURL, 8*time.Second)
}

func probeHealthURL(ctx context.Context, rawURL string) error {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

func pollHealthURLWindow(ctx context.Context, rawURL string, window time.Duration) error {
	deadline := time.Time{}
	if window > 0 {
		deadline = time.Now().Add(window)
	}
	var last error
	for {
		if err := ctx.Err(); err != nil {
			if last != nil {
				return last
			}
			return err
		}
		if window > 0 && !deadline.IsZero() && time.Now().After(deadline) {
			break
		}
		if err := probeHealthURL(ctx, rawURL); err != nil {
			last = err
		} else {
			return nil
		}
		if window > 0 && !deadline.IsZero() && !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return last
			}
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if last == nil {
		last = errors.New("health check timed out")
	}
	return last
}

func watchHealthUntilChild(ctx context.Context, health func(context.Context) error, child <-chan error, onHealthy func()) (bool, error) {
	if health == nil {
		return false, <-child
	}
	healthCh := make(chan error, 1)
	go func() {
		healthCh <- health(ctx)
	}()
	healthy := false
	for {
		select {
		case err := <-child:
			return healthy, err
		case err := <-healthCh:
			healthCh = nil
			if err == nil {
				healthy = true
				if onHealthy != nil {
					onHealthy()
				}
			}
		}
	}
}

func (l *launcher) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.loopbackReady != nil && l.loopbackReady(ctx) == nil {
		l.openConsole()
		return nil
	}
	if l.portBusy != nil && l.portBusy() {
		return errLoopbackBusy
	}
	spec, appRoot, versionDir, err := l.install(ctx)
	if err != nil {
		return err
	}
	return l.supervise(ctx, spec, appRoot, versionDir)
}

func (l *launcher) install(ctx context.Context) (commandSpec, string, string, error) {
	local, err := l.localAppData()
	if err != nil {
		return commandSpec{}, "", "", err
	}
	appRoot := filepath.Join(local, productDirName, "app")
	dataRoot := filepath.Join(local, productDirName, "data")
	if err := os.MkdirAll(appRoot, 0o700); err != nil {
		return commandSpec{}, "", "", err
	}
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return commandSpec{}, "", "", err
	}

	overlay, err := l.readOverlay()
	if err != nil {
		return commandSpec{}, "", "", err
	}
	if err := portable.ExtractToStaging(ctx, overlay, appRoot); err != nil {
		return commandSpec{}, "", "", err
	}
	version := overlay.Manifest.AppVersion
	if err := portable.SwitchVersion(appRoot, version); err != nil {
		return commandSpec{}, "", "", err
	}
	versionDir := filepath.Join(appRoot, version)
	child := filepath.Join(versionDir, "bin", childBinaryName)
	if err := assertRegularChild(child); err != nil {
		return commandSpec{}, "", "", err
	}
	spec := commandSpec{
		Path: child,
		Dir:  versionDir,
		Env:  l.childEnv(versionDir, dataRoot),
	}
	return spec, appRoot, versionDir, nil
}

func (l *launcher) supervise(ctx context.Context, spec commandSpec, appRoot, versionDir string) error {
	now := l.now
	if now == nil {
		now = time.Now
	}
	restarts := 0
	windowStart := now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := l.runChild(ctx, spec, appRoot, versionDir)
		if exitCodeOf(err) != restartExitCode {
			return err
		}
		at := now()
		if at.Sub(windowStart) > restartWindow {
			windowStart = at
			restarts = 0
		}
		if restarts >= maxRestarts {
			return err
		}
		restarts++
	}
}

func (l *launcher) runChild(ctx context.Context, spec commandSpec, appRoot, versionDir string) error {
	if l.start != nil {
		err := l.start(spec)
		if err == nil {
			l.maybeCleanup(ctx, appRoot, versionDir)
		}
		return err
	}
	return l.execChild(ctx, spec, appRoot, versionDir)
}

func (l *launcher) execChild(ctx context.Context, spec commandSpec, appRoot, versionDir string) error {
	cmd := exec.CommandContext(ctx, spec.Path)
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cleanupLog := attachChildOutput(cmd)
	hideChildConsole(cmd)
	if err := cmd.Start(); err != nil {
		cleanupLog()
		return err
	}
	started := make(chan error, 1)
	healthCtx, cancel := context.WithCancel(ctx)
	go func() {
		waitErr := cmd.Wait()
		cancel()
		started <- waitErr
	}()
	var healthFn func(context.Context) error
	if l.health != nil {
		healthFn = func(c context.Context) error { return l.health(c, versionDir) }
	}
	healthy, err := watchHealthUntilChild(healthCtx, healthFn, started, func() {
		l.afterHealthy(appRoot)
	})
	cleanupLog()
	if err == nil {
		return nil
	}
	if !healthy {
		reportLaunchError(fmt.Errorf("控制台未能在本机启动。可查看 %%LOCALAPPDATA%%\\VideoProductionConsole\\console.log"))
		var exited *exec.ExitError
		if errors.As(err, &exited) {
			return exitError{code: exited.ExitCode()}
		}
		return exitError{code: 1}
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return exitError{code: exited.ExitCode()}
	}
	return err
}

func attachChildOutput(cmd *exec.Cmd) func() {
	cache, err := os.UserCacheDir()
	if err != nil {
		return func() {}
	}
	logDir := filepath.Join(cache, productDirName)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return func() {}
	}
	file, err := os.OpenFile(filepath.Join(logDir, "console.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return func() {}
	}
	cmd.Stdout = file
	cmd.Stderr = file
	return func() { _ = file.Close() }
}

func (l *launcher) maybeCleanup(ctx context.Context, appRoot, versionDir string) {
	if l.health == nil {
		return
	}
	if err := l.health(ctx, versionDir); err != nil {
		return
	}
	l.afterHealthy(appRoot)
}

func (l *launcher) afterHealthy(appRoot string) {
	l.cleanup(appRoot)
	l.openConsole()
}

func (l *launcher) openConsole() {
	if l.openUI == nil {
		return
	}
	_ = l.openUI(loopbackConsoleURL)
}

func (l *launcher) cleanup(appRoot string) {
	state, err := portable.ReadState(appRoot)
	if err != nil {
		return
	}
	_ = portable.CleanupUnusedVersions(appRoot, state)
}

func (l *launcher) readOverlay() (*portable.Overlay, error) {
	file, err := os.Open(l.executable)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return portable.ReadOverlay(file, info.Size())
}

func (l *launcher) localAppData() (string, error) {
	lookup := l.lookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, ok := lookup("LOCALAPPDATA")
	if !ok || strings.TrimSpace(value) == "" {
		return "", errors.New("LOCALAPPDATA is required")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func (l *launcher) childEnv(versionDir, dataRoot string) []string {
	environ := os.Environ
	if l.environ != nil {
		environ = l.environ
	}
	env := stripBlockedEnv(append([]string{}, environ()...))
	env = setEnv(env, "VIDEO_CONSOLE_APP_ROOT", versionDir)
	env = setEnv(env, "VIDEO_CONSOLE_DATA_ROOT", dataRoot)
	env = setEnv(env, "VIDEO_CONSOLE_LAUNCHED", "1")
	return env
}

func assertRegularChild(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("child binary is not a regular file: %s", path)
	}
	return nil
}

func stripBlockedEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, found := strings.Cut(item, "=")
		if !found || blockedEnvKey(key) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func blockedEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	switch {
	case strings.Contains(upper, "API_KEY"),
		strings.Contains(upper, "TOKEN"),
		strings.Contains(upper, "SECRET"),
		strings.Contains(upper, "PASSWORD"),
		strings.Contains(upper, "GATEWAY"):
		return true
	case strings.HasPrefix(upper, "VIDEO_CONSOLE_") && strings.HasSuffix(upper, "_BASE_URL"):
		return true
	default:
		return false
	}
}

func setEnv(env []string, key, value string) []string {
	replaced := false
	for i, item := range env {
		existing, _, found := strings.Cut(item, "=")
		if !found || !strings.EqualFold(existing, key) {
			continue
		}
		env[i] = key + "=" + value
		replaced = true
	}
	if !replaced {
		env = append(env, key+"="+value)
	}
	return env
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exited exitError
	if errors.As(err, &exited) {
		return exited.code
	}
	return -1
}
