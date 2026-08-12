package settings

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

const (
	SecretGrokAPIKey       = "grok_api_key"
	SecretPexelsAPIKey     = "pexels_api_key"
	SecretVolcSpeechAPIKey = "volc_speech_api_key"

	secretMask           = "********"
	probeTimeout         = 5 * time.Second
	maxProbeBodySize     = 64 << 10
	maxCommandOutputSize = 64 << 10
)

var (
	ErrInvalidSettings = errors.New("settings are invalid")
	ErrUnknownSecret   = errors.New("secret key is not supported")
	ErrNotConfigured   = errors.New("settings are not configured")
)

var secretKeys = []string{SecretGrokAPIKey, SecretPexelsAPIKey, SecretVolcSpeechAPIKey}

type Repository interface {
	Public(context.Context) (map[string]string, int64, error)
	InitializeBoot(context.Context, map[string]string) error
	UpdatePublic(context.Context, map[string]string) (int64, error)
	UpdateAtomic(context.Context, map[string]string, map[string]string, time.Time) (int64, error)
	PutSecret(context.Context, string, string, time.Time) (int64, error)
	Secret(context.Context, string) (store.EncryptedSecret, error)
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	Now        func() time.Time
	Runner     CommandRunner
	HTTPClient HTTPClient
}

type Service struct {
	repo      Repository
	protector security.Protector
	now       func() time.Time
	runner    CommandRunner
	http      HTTPClient
	activeMu  sync.RWMutex
	active    *Runtime
}

type BootSettings struct {
	ListenAddr           string
	DataRoot             string
	CodexBinaryPath      string
	BaokuanMCPExecutable string
	ObsidianVault        string
	TopicCardsDir        string
	MediaIndexPath       string
	MediaRoot            string
	JianyingRoot         string
	MachineProfilePath   string
	CodexTaskProjectRoot string
	CodexWorkspaceRoots  []string
}

type View struct {
	Public           domain.PublicSettings          `json:"public"`
	ConfiguredPublic domain.PublicSettings          `json:"configured_public"`
	ActivePublic     domain.PublicSettings          `json:"active_public"`
	SettingsVersion  int64                          `json:"settings_version"`
	Secrets          map[string]domain.SecretStatus `json:"secrets"`
	RestartRequired  bool                           `json:"restart_required"`
}

// Runtime is process-only configuration. It intentionally has no JSON tags
// and must never be used as an HTTP response or task snapshot.
type Runtime struct {
	domain.PublicSettings
	GrokAPIKey       string           `json:"-"`
	PexelsAPIKey     string           `json:"-"`
	VolcSpeechAPIKey string           `json:"-"`
	SecretVersions   map[string]int64 `json:"-"`
}

type HealthStatus string

const (
	HealthOK            HealthStatus = "ok"
	HealthNotConfigured HealthStatus = "not_configured"
	HealthOffline       HealthStatus = "offline"
)

type Health struct {
	Status  HealthStatus `json:"status"`
	Message string       `json:"message"`
}

func NewService(repo Repository, protector security.Protector, optionValues ...Options) *Service {
	options := Options{}
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Runner == nil {
		options.Runner = directCommandRunner{}
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: probeTimeout}
	}
	options.HTTPClient = noRedirectHTTPClient(options.HTTPClient)
	return &Service{repo: repo, protector: protector, now: options.Now, runner: options.Runner, http: options.HTTPClient}
}

func (s *Service) InitializeBootSettings(ctx context.Context, boot BootSettings) error {
	if boot.DataRoot == "" || boot.CodexBinaryPath == "" {
		return ErrNotConfigured
	}
	if boot.ListenAddr != "" {
		if err := validateListenAddr(boot.ListenAddr); err != nil {
			return invalid("listen_addr")
		}
	}
	paths := []struct{ name, value string }{
		{"data_root", boot.DataRoot},
		{"codex_binary_path", boot.CodexBinaryPath},
		{"baokuan_mcp_executable", boot.BaokuanMCPExecutable},
		{"obsidian_vault", boot.ObsidianVault},
		{"topic_cards_dir", boot.TopicCardsDir},
		{"media_index_path", boot.MediaIndexPath},
		{"media_root", boot.MediaRoot},
		{"jianying_root", boot.JianyingRoot},
		{"machine_profile_path", boot.MachineProfilePath},
		{"codex_task_project_root", boot.CodexTaskProjectRoot},
	}
	values := make(map[string]string, len(paths)+1)
	if boot.ListenAddr != "" {
		values["listen_addr"] = boot.ListenAddr
	}
	for _, path := range paths {
		if path.value == "" {
			continue
		}
		if err := validateCanonicalAbsolutePath(path.value); err != nil {
			return invalid(path.name)
		}
		values[path.name] = path.value
	}
	if len(boot.CodexWorkspaceRoots) > 0 {
		roots := make([]string, 0, len(boot.CodexWorkspaceRoots))
		for _, root := range boot.CodexWorkspaceRoots {
			if err := validateCanonicalAbsolutePath(root); err != nil {
				return invalid("codex_workspace_roots")
			}
			roots = append(roots, root)
		}
		encoded, err := json.Marshal(roots)
		if err != nil {
			return invalid("codex_workspace_roots")
		}
		values["codex_workspace_roots"] = string(encoded)
	}
	// The console's primary workflow is now conversational. New installations
	// should be able to create a Codex conversation without a hidden opt-in.
	values["app_server_enabled"] = "true"
	values["codex_history_limit"] = "10"
	return s.repo.InitializeBoot(ctx, values)
}

func (s *Service) Get(ctx context.Context) (View, error) {
	values, version, err := s.repo.Public(ctx)
	if err != nil {
		return View{}, err
	}
	view := View{
		Public:          publicFromValues(values),
		SettingsVersion: version,
		Secrets:         make(map[string]domain.SecretStatus, len(secretKeys)),
	}
	view.ConfiguredPublic = clonePublic(view.Public)
	configuredSecretVersions := map[string]int64{}
	for _, key := range secretKeys {
		secret, err := s.repo.Secret(ctx, key)
		switch {
		case err == nil:
			view.Secrets[key] = domain.SecretStatus{Configured: true, Masked: secretMask}
			configuredSecretVersions[key] = secret.Version
		case errors.Is(err, store.ErrSecretNotFound):
			view.Secrets[key] = domain.SecretStatus{}
		default:
			return View{}, err
		}
	}
	s.activeMu.RLock()
	if s.active == nil {
		view.ActivePublic = view.Public
	} else {
		view.ActivePublic = clonePublic(s.active.PublicSettings)
		view.RestartRequired = restartSensitiveChanged(view.Public, s.active.PublicSettings) || !sameSecretVersions(configuredSecretVersions, s.active.SecretVersions)
	}
	s.activeMu.RUnlock()
	return view, nil
}

func (s *Service) PutPublic(ctx context.Context, value domain.PublicSettings) (int64, error) {
	if err := validatePublic(value); err != nil {
		return 0, err
	}
	version, err := s.repo.UpdatePublic(ctx, publicValues(value))
	if err == nil {
		s.applyHotSettings(value)
	}
	return version, err
}

func (s *Service) ResolveTaskModel(ctx context.Context, override taskmodel.Selection) (taskmodel.Selection, error) {
	runtime, err := s.Runtime(ctx)
	if err != nil {
		return taskmodel.Selection{}, err
	}
	defaults := taskmodel.Selection{
		Model:           runtime.CodexDefaultModel,
		ReasoningEffort: runtime.CodexDefaultReasoningEffort,
	}
	return taskmodel.Resolve(defaults, override)
}

// Update validates the complete request before changing public settings.
// Empty secret values deliberately mean "leave unchanged".
func (s *Service) Update(ctx context.Context, public domain.PublicSettings, secrets map[string]string) (View, error) {
	if err := validatePublic(public); err != nil {
		return View{}, err
	}
	for key, value := range secrets {
		if err := validateSecretUpdate(key, value); err != nil {
			return View{}, err
		}
	}
	encrypted := make(map[string]string, len(secrets))
	for _, key := range secretKeys {
		if value, ok := secrets[key]; ok && value != "" {
			ciphertext, err := s.protectSecret(value)
			if err != nil {
				return View{}, err
			}
			encrypted[key] = ciphertext
		}
	}
	if _, err := s.repo.UpdateAtomic(ctx, publicValues(public), encrypted, s.now().UTC()); err != nil {
		return View{}, errors.New("settings could not be stored")
	}
	s.applyHotSettings(public)
	return s.Get(ctx)
}

func (s *Service) applyHotSettings(configured domain.PublicSettings) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active == nil {
		return
	}
	s.active.MaxCodexConcurrency = configured.MaxCodexConcurrency
	s.active.CodexHistoryLimit = configured.CodexHistoryLimit
	s.active.CodexDefaultModel = configured.CodexDefaultModel
	s.active.CodexDefaultReasoningEffort = configured.CodexDefaultReasoningEffort
}

func (s *Service) PutSecret(ctx context.Context, key, value string) error {
	if err := validateSecretUpdate(key, value); err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	encoded, err := s.protectSecret(value)
	if err != nil {
		return err
	}
	if _, err := s.repo.PutSecret(ctx, key, encoded, s.now().UTC()); err != nil {
		return errors.New("encrypted secret could not be stored")
	}
	return nil
}

func (s *Service) protectSecret(value string) (string, error) {
	if s.protector == nil {
		return "", security.ErrSecretStoreUnsupported
	}
	plain := []byte(value)
	defer clear(plain)
	ciphertext, err := s.protector.Protect(plain)
	if err != nil {
		if errors.Is(err, security.ErrSecretStoreUnsupported) {
			return "", security.ErrSecretStoreUnsupported
		}
		return "", security.ErrSecretProtection
	}
	defer clear(ciphertext)
	if len(ciphertext) == 0 {
		return "", security.ErrSecretProtection
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (s *Service) Runtime(ctx context.Context) (Runtime, error) {
	s.activeMu.RLock()
	if s.active != nil {
		runtime := cloneRuntime(*s.active)
		s.activeMu.RUnlock()
		return runtime, nil
	}
	s.activeMu.RUnlock()
	runtime, err := s.configuredRuntime(ctx)
	if err != nil {
		return Runtime{}, err
	}
	s.activeMu.Lock()
	if s.active == nil {
		copy := cloneRuntime(runtime)
		s.active = &copy
	}
	result := cloneRuntime(*s.active)
	s.activeMu.Unlock()
	return result, nil
}

func (s *Service) configuredRuntime(ctx context.Context) (Runtime, error) {
	view, err := s.Get(ctx)
	if err != nil {
		return Runtime{}, err
	}
	if view.Public.DataRoot == "" || view.Public.CodexBinaryPath == "" || !filepath.IsAbs(view.Public.DataRoot) || !filepath.IsAbs(view.Public.CodexBinaryPath) {
		return Runtime{}, ErrNotConfigured
	}
	if err := validatePublic(view.Public); err != nil {
		return Runtime{}, err
	}
	runtime := Runtime{PublicSettings: view.Public, SecretVersions: make(map[string]int64, len(secretKeys))}
	for _, key := range secretKeys {
		value, version, configured, err := s.secretValue(ctx, key)
		if !configured && err == nil {
			continue
		}
		if err != nil {
			return Runtime{}, err
		}
		switch key {
		case SecretGrokAPIKey:
			runtime.GrokAPIKey = value
		case SecretPexelsAPIKey:
			runtime.PexelsAPIKey = value
		case SecretVolcSpeechAPIKey:
			runtime.VolcSpeechAPIKey = value
		}
		runtime.SecretVersions[key] = version
	}
	return runtime, nil
}

func clonePublic(value domain.PublicSettings) domain.PublicSettings {
	value.CodexWorkspaceRoots = append([]string(nil), value.CodexWorkspaceRoots...)
	return value
}

func cloneRuntime(value Runtime) Runtime {
	value.PublicSettings = clonePublic(value.PublicSettings)
	versions := make(map[string]int64, len(value.SecretVersions))
	for key, version := range value.SecretVersions {
		versions[key] = version
	}
	value.SecretVersions = versions
	return value
}

func restartSensitiveChanged(configured, active domain.PublicSettings) bool {
	configured.MaxCodexConcurrency, active.MaxCodexConcurrency = 0, 0
	configured.CodexHistoryLimit, active.CodexHistoryLimit = 0, 0
	return !reflect.DeepEqual(configured, active)
}

func sameSecretVersions(configured, active map[string]int64) bool {
	return reflect.DeepEqual(configured, active)
}

func (s *Service) secretValue(ctx context.Context, key string) (string, int64, bool, error) {
	secret, err := s.repo.Secret(ctx, key)
	if errors.Is(err, store.ErrSecretNotFound) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, errors.New("encrypted secret could not be read")
	}
	decoded, err := base64.StdEncoding.DecodeString(secret.Ciphertext)
	if err != nil || len(decoded) == 0 || s.protector == nil {
		clear(decoded)
		return "", 0, false, security.ErrSecretInvalid
	}
	plain, err := s.protector.Unprotect(decoded)
	clear(decoded)
	if err != nil || len(plain) == 0 || len(plain) > security.MaxSecretSize {
		clear(plain)
		return "", 0, false, errors.New("encrypted secret could not be decrypted")
	}
	value := string(plain)
	clear(plain)
	return value, secret.Version, true, nil
}

func validateSecretUpdate(key, value string) error {
	known := false
	for _, allowed := range secretKeys {
		if key == allowed {
			known = true
			break
		}
	}
	if !known {
		return ErrUnknownSecret
	}
	if len([]byte(value)) > security.MaxSecretSize {
		return security.ErrSecretTooLarge
	}
	return nil
}

func validatePublic(value domain.PublicSettings) error {
	if value.MaxCodexConcurrency < 1 || value.MaxCodexConcurrency > 4 {
		return invalid("max_codex_concurrency")
	}
	if value.CodexHistoryLimit != 0 && (value.CodexHistoryLimit < 5 || value.CodexHistoryLimit > 50) {
		return invalid("codex_history_limit")
	}
	normalizedModel, err := taskmodel.Normalize(taskmodel.Selection{Model: value.CodexDefaultModel, ReasoningEffort: taskmodel.DefaultReasoningEffort})
	if err != nil || normalizedModel.Model != value.CodexDefaultModel {
		return invalid("codex_default_model")
	}
	normalizedEffort, err := taskmodel.Normalize(taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: value.CodexDefaultReasoningEffort})
	if err != nil || normalizedEffort.ReasoningEffort != value.CodexDefaultReasoningEffort {
		return invalid("codex_default_reasoning_effort")
	}
	if err := validateListenAddr(value.ListenAddr); err != nil {
		return invalid("listen_addr")
	}
	if value.BaokuanBaseURL != "" {
		if err := validateLoopbackURL(value.BaokuanBaseURL); err != nil {
			return invalid("baokuan_base_url")
		}
	}
	if value.GrokBaseURL != "" {
		if err := validateHTTPURL(value.GrokBaseURL); err != nil {
			return invalid("grok_base_url")
		}
	}
	paths := []struct {
		name     string
		value    string
		required bool
	}{
		{"data_root", value.DataRoot, true},
		{"baokuan_mcp_executable", value.BaokuanMCPExecutable, false},
		{"obsidian_vault", value.ObsidianVault, false},
		{"topic_cards_dir", value.TopicCardsDir, false},
		{"codex_binary_path", value.CodexBinaryPath, false},
		{"media_index_path", value.MediaIndexPath, false},
		{"media_root", value.MediaRoot, false},
		{"jianying_root", value.JianyingRoot, false},
		{"machine_profile_path", value.MachineProfilePath, false},
		{"codex_task_project_root", value.CodexTaskProjectRoot, false},
	}
	for _, path := range paths {
		if path.value == "" && !path.required {
			continue
		}
		if err := validateCanonicalAbsolutePath(path.value); err != nil {
			return invalid(path.name)
		}
	}
	if value.TopicCardsDir != "" && (value.ObsidianVault == "" || !pathWithin(value.ObsidianVault, value.TopicCardsDir)) {
		return invalid("topic_cards_dir")
	}
	if value.MediaIndexPath != "" && (value.MediaRoot == "" || !pathWithin(value.MediaRoot, value.MediaIndexPath)) {
		return invalid("media_index_path")
	}
	for _, root := range value.CodexWorkspaceRoots {
		if err := validateCanonicalAbsolutePath(root); err != nil {
			return invalid("codex_workspace_roots")
		}
	}
	return nil
}

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidSettings, field) }

func validateListenAddr(value string) error {
	if value == "" || value != strings.TrimSpace(value) || strings.Contains(value, "://") {
		return errors.New("invalid listen address")
	}
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return errors.New("invalid listen address")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid listen port")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return errors.New("listen host must be an IP address or localhost")
	}
	return nil
}

func validateLoopbackURL(value string) error {
	u, err := parseHTTPURL(value)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("URL host must be loopback")
	}
	return nil
}

func validateHTTPURL(value string) error {
	_, err := parseHTTPURL(value)
	return err
}

func parseHTTPURL(value string) (*url.URL, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return nil, errors.New("invalid URL")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid HTTP URL")
	}
	return u, nil
}

func validateCanonicalAbsolutePath(value string) error {
	if value == "" || value != strings.TrimSpace(value) || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return errors.New("path must be canonical and absolute")
	}
	if err := validatePlatformLocalPath(value); err != nil {
		return err
	}
	resolved := canonicalPath(value)
	if !samePath(resolved, value) {
		return errors.New("path resolves through an alias")
	}
	return nil
}

func canonicalPath(value string) string {
	current := filepath.Clean(value)
	remaining := make([]string, 0)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(remaining) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, remaining[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(value)
		}
		remaining = append(remaining, filepath.Base(current))
		current = parent
	}
}

func samePath(left, right string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func pathWithin(root, target string) bool {
	root = canonicalPath(root)
	target = canonicalPath(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func publicValues(value domain.PublicSettings) map[string]string {
	workspaceRoots, _ := json.Marshal(value.CodexWorkspaceRoots)
	historyLimit := value.CodexHistoryLimit
	if historyLimit == 0 {
		historyLimit = 10
	}
	return map[string]string{
		"listen_addr": value.ListenAddr, "data_root": value.DataRoot,
		"max_codex_concurrency": strconv.Itoa(value.MaxCodexConcurrency),
		"codex_default_model":   value.CodexDefaultModel, "codex_default_reasoning_effort": value.CodexDefaultReasoningEffort,
		"baokuan_base_url": value.BaokuanBaseURL, "baokuan_mcp_executable": value.BaokuanMCPExecutable,
		"obsidian_vault": value.ObsidianVault, "topic_cards_dir": value.TopicCardsDir,
		"grok_base_url": value.GrokBaseURL, "grok_model": value.GrokModel,
		"codex_binary_path": value.CodexBinaryPath, "media_index_path": value.MediaIndexPath,
		"media_root": value.MediaRoot, "jianying_root": value.JianyingRoot,
		"machine_profile_path":    value.MachineProfilePath,
		"app_server_enabled":      strconv.FormatBool(value.AppServerEnabled),
		"codex_workspace_roots":   string(workspaceRoots),
		"codex_task_project_root": value.CodexTaskProjectRoot,
		"codex_history_limit":     strconv.Itoa(historyLimit),
		"volc_speech_speaker_id":  value.VolcSpeechSpeakerID,
		"volc_speech_resource_id": value.VolcSpeechResourceID,
	}
}

func publicFromValues(values map[string]string) domain.PublicSettings {
	concurrency, _ := strconv.Atoi(values["max_codex_concurrency"])
	codexDefaultModel := values["codex_default_model"]
	if strings.TrimSpace(codexDefaultModel) == "" {
		codexDefaultModel = taskmodel.DefaultModel
	}
	codexDefaultReasoningEffort := values["codex_default_reasoning_effort"]
	if strings.TrimSpace(codexDefaultReasoningEffort) == "" {
		codexDefaultReasoningEffort = taskmodel.DefaultReasoningEffort
	}
	workspaceRoots := []string{}
	if raw := strings.TrimSpace(values["codex_workspace_roots"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &workspaceRoots)
	}
	appServerEnabled, _ := strconv.ParseBool(values["app_server_enabled"])
	historyLimit, _ := strconv.Atoi(values["codex_history_limit"])
	if historyLimit < 5 || historyLimit > 50 {
		historyLimit = 10
	}
	return domain.PublicSettings{
		ListenAddr: values["listen_addr"], DataRoot: values["data_root"], MaxCodexConcurrency: concurrency,
		CodexDefaultModel: codexDefaultModel, CodexDefaultReasoningEffort: codexDefaultReasoningEffort,
		BaokuanBaseURL: values["baokuan_base_url"], BaokuanMCPExecutable: values["baokuan_mcp_executable"],
		ObsidianVault: values["obsidian_vault"], TopicCardsDir: values["topic_cards_dir"],
		GrokBaseURL: values["grok_base_url"], GrokModel: values["grok_model"],
		CodexBinaryPath: values["codex_binary_path"], MediaIndexPath: values["media_index_path"],
		MediaRoot: values["media_root"], JianyingRoot: values["jianying_root"],
		MachineProfilePath: values["machine_profile_path"],
		AppServerEnabled:   appServerEnabled, CodexWorkspaceRoots: workspaceRoots,
		CodexTaskProjectRoot: values["codex_task_project_root"], CodexHistoryLimit: historyLimit,
		VolcSpeechSpeakerID:  values["volc_speech_speaker_id"],
		VolcSpeechResourceID: values["volc_speech_resource_id"],
	}
}

func (s *Service) TestDependency(ctx context.Context, dependency string) Health {
	values, _, err := s.repo.Public(ctx)
	if err != nil {
		return offlineHealth()
	}
	public := publicFromValues(values)
	switch dependency {
	case "baokuan", "baokuan-mcp", "baokuan_http":
		if public.BaokuanBaseURL == "" || public.BaokuanMCPExecutable == "" || public.CodexBinaryPath == "" {
			return notConfiguredHealth()
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, strings.TrimRight(public.BaokuanBaseURL, "/")+"/api/channels/library/materials/search?limit=1", nil)
		if err != nil {
			return offlineHealth()
		}
		response, err := s.http.Do(request)
		if err != nil {
			return offlineHealth()
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 || !s.baokuanMCPRegistered(probeCtx, public.CodexBinaryPath) {
			return offlineHealth()
		}
		return okHealth()
	case "codex":
		if public.CodexBinaryPath == "" {
			return notConfiguredHealth()
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		if _, err := s.runner.Run(probeCtx, public.CodexBinaryPath, "--version"); err != nil {
			return offlineHealth()
		}
		return okHealth()
	case "obsidian":
		return directoryHealth(public.ObsidianVault)
	case "media":
		return directoryHealth(public.MediaRoot)
	case "jianying":
		return directoryHealth(public.JianyingRoot)
	case "grok":
		if public.GrokBaseURL == "" || public.GrokModel == "" {
			return notConfiguredHealth()
		}
		key, _, configured, err := s.secretValue(ctx, SecretGrokAPIKey)
		if !configured && err == nil {
			return notConfiguredHealth()
		}
		if err != nil {
			return offlineHealth()
		}
		probeURL, err := grokModelsURL(public.GrokBaseURL)
		if err != nil {
			return offlineHealth()
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, nil)
		if err != nil {
			return offlineHealth()
		}
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Accept", "application/json")
		response, err := s.http.Do(request)
		if err != nil {
			return offlineHealth()
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeBodySize))
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return offlineHealth()
		}
		return okHealth()
	default:
		return notConfiguredHealth()
	}
}

func (s *Service) RepairBaokuanMCP(ctx context.Context) Health {
	values, _, err := s.repo.Public(ctx)
	if err != nil {
		return offlineHealth()
	}
	public := publicFromValues(values)
	if public.BaokuanBaseURL == "" || public.BaokuanMCPExecutable == "" || public.CodexBinaryPath == "" {
		return notConfiguredHealth()
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err = s.runner.Run(probeCtx, public.CodexBinaryPath, "mcp", "add", "baokuan", "--", public.BaokuanMCPExecutable, "mcp", "--base", public.BaokuanBaseURL)
	if err != nil || !s.baokuanMCPRegistered(probeCtx, public.CodexBinaryPath) {
		return offlineHealth()
	}
	return okHealth()
}

var baokuanMCPPattern = regexp.MustCompile(`(?mi)(^|\s)baokuan(\s|$)`)

func (s *Service) baokuanMCPRegistered(ctx context.Context, executable string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := s.runner.Run(probeCtx, executable, "mcp", "list")
	return err == nil && baokuanMCPPattern.Match(output)
}

func directoryHealth(path string) Health {
	if path == "" {
		return notConfiguredHealth()
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return offlineHealth()
	}
	return okHealth()
}

func okHealth() Health { return Health{Status: HealthOK, Message: "Available."} }
func notConfiguredHealth() Health {
	return Health{Status: HealthNotConfigured, Message: "Not configured."}
}
func offlineHealth() Health { return Health{Status: HealthOffline, Message: "Offline."} }

type directCommandRunner struct{}

func (directCommandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	command := exec.CommandContext(probeCtx, executable, args...)
	output := newBoundedOutput(maxCommandOutputSize)
	command.Stdout = output
	command.Stderr = io.Discard
	err := command.Run()
	return append([]byte(nil), output.Bytes()...), err
}

type boundedOutput struct {
	limit  int
	buffer bytes.Buffer
}

func newBoundedOutput(limit int) *boundedOutput { return &boundedOutput{limit: limit} }

func (output *boundedOutput) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := output.limit - output.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = output.buffer.Write(value)
	}
	return originalLength, nil
}

func (output *boundedOutput) Bytes() []byte { return output.buffer.Bytes() }

func noRedirectHTTPClient(client HTTPClient) HTTPClient {
	concrete, ok := client.(*http.Client)
	if !ok {
		return client
	}
	clone := *concrete
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if clone.Timeout <= 0 || clone.Timeout > probeTimeout {
		clone.Timeout = probeTimeout
	}
	return &clone
}

func grokModelsURL(base string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), "/v1") {
		parsed.Path = path + "/models"
	} else {
		parsed.Path = path + "/v1/models"
	}
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
