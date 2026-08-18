package codex

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/taskmodel"
)

var (
	systemEnvironmentKeys = []string{"COMSPEC", "PATH", "PATHEXT", "SYSTEMROOT", "TEMP", "TMP", "USERPROFILE", "WINDIR"}
	secretEnvironmentKeys = []string{"GROK_SEARCH_API_KEY", "GROK_SEARCH_BASE_URL", "GROK_SEARCH_MODEL", "PEXELS_API_KEY"}
	validSessionID        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

const taskManifestEnvironmentKey = "VIDEO_CONSOLE_TASK_MANIFEST"

type Config struct {
	CodexBinaryPath    string
	ModelName          string
	ReasoningEffort    string
	ResultSchema       string
	OutputLastMessage  string
	WorkingDirectory   string
	MachineProfilePath string
	SecretEnvironment  map[string]string
	Redactor           *security.Redactor
	BinaryResolver     func(string) (string, error)
}

// CommandLaunch describes a native process plus any fixed arguments required
// to invoke Codex. Windows npm installations expose codex.cmd, which cannot be
// started as a managed child process; in that case the native Node executable
// runs the installed Codex JavaScript entry point directly.
type CommandLaunch struct {
	Executable string
	PrefixArgs []string
}

// CommandSnapshot is safe to persist: it contains no environment values and
// all user-controlled strings have passed through the configured redactor.
type CommandSnapshot struct {
	Binary           string   `json:"binary"`
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"working_directory"`
	EnvironmentKeys  []string `json:"environment_keys"`
}

// SafeEnvironment constructs an environment from explicit allowlists. It
// never copies the complete parent environment or reads secret keys from it.
func (c Config) SafeEnvironment() []string {
	values := make(map[string]string, len(systemEnvironmentKeys)+len(secretEnvironmentKeys))
	for _, key := range systemEnvironmentKeys {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	for _, key := range secretEnvironmentKeys {
		value, ok := c.SecretEnvironment[key]
		if !ok {
			continue
		}
		values[key] = value
		if c.Redactor != nil {
			c.Redactor.Register(value)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func (c Config) binary() string {
	if strings.TrimSpace(c.CodexBinaryPath) == "" {
		return "codex"
	}
	return c.CodexBinaryPath
}

func (c Config) resolvedLaunch() (string, CommandLaunch, error) {
	display := c.binary()
	resolver := c.BinaryResolver
	if resolver == nil && runtime.GOOS != "windows" {
		return display, CommandLaunch{Executable: display}, nil
	}
	if resolver == nil {
		resolver = exec.LookPath
	}
	launch, err := resolveCommandLaunch(display, resolver, runtime.GOOS == "windows")
	if err != nil {
		return "", CommandLaunch{}, err
	}
	return display, launch, nil
}

// ResolveCommandLaunch converts a configured Codex executable into a native
// process launch. It is shared by the task runner and App Server process.
func ResolveCommandLaunch(binary string) (CommandLaunch, error) {
	return resolveCommandLaunch(binary, exec.LookPath, runtime.GOOS == "windows")
}

func resolveCommandLaunch(binary string, resolver func(string) (string, error), windows bool) (CommandLaunch, error) {
	resolved, err := resolveBinaryCandidate(binary, resolver, windows)
	if err != nil {
		return CommandLaunch{}, err
	}
	if windows && strings.EqualFold(filepath.Ext(resolved), ".cmd") {
		return resolveNPMCodexShim(binary, resolved, resolver)
	}
	if windows {
		if err := validateWindowsBinaryExtension(binary, resolved); err != nil {
			return CommandLaunch{}, err
		}
	}
	return CommandLaunch{Executable: resolved}, nil
}

func resolveBinaryCandidate(binary string, resolver func(string) (string, error), windows bool) (string, error) {
	var resolved string
	var err error
	if windows && filepath.Ext(binary) == "" {
		resolved, err = resolver(binary + ".exe")
		if err != nil {
			resolved, err = resolver(binary)
		}
	} else {
		resolved, err = resolver(binary)
	}
	if err != nil {
		return "", fmt.Errorf("resolve CodexBinaryPath %q: %w", binary, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("canonicalize CodexBinaryPath %q: %w", resolved, err)
	}
	resolved, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("canonicalize CodexBinaryPath %q: %w", resolved, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect CodexBinaryPath %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("CodexBinaryPath %q is not a regular file", resolved)
	}
	return resolved, nil
}

func resolveNPMCodexShim(binary, shim string, resolver func(string) (string, error)) (CommandLaunch, error) {
	if !strings.EqualFold(filepath.Base(shim), "codex.cmd") {
		return CommandLaunch{}, fmt.Errorf("CodexBinaryPath %q resolved to unsupported command script %q; set CodexBinaryPath to a native .exe or the npm codex.cmd shim", binary, shim)
	}
	entry := filepath.Join(filepath.Dir(shim), "node_modules", "@openai", "codex", "bin", "codex.js")
	entryInfo, err := os.Stat(entry)
	if err != nil {
		return CommandLaunch{}, fmt.Errorf("inspect npm Codex entry point for CodexBinaryPath %q: %w", binary, err)
	}
	if !entryInfo.Mode().IsRegular() {
		return CommandLaunch{}, fmt.Errorf("npm Codex entry point %q is not a regular file", entry)
	}
	node, err := resolveBinaryCandidate("node.exe", resolver, true)
	if err != nil {
		return CommandLaunch{}, fmt.Errorf("resolve native Node executable for %q: %w", binary, err)
	}
	if err := validateWindowsBinaryExtension("node.exe", node); err != nil {
		return CommandLaunch{}, err
	}
	return CommandLaunch{Executable: node, PrefixArgs: []string{entry}}, nil
}

func validateWindowsBinaryExtension(binary, resolved string) error {
	switch extension := strings.ToLower(filepath.Ext(resolved)); extension {
	case ".cmd", ".bat", ".ps1":
		return fmt.Errorf("CodexBinaryPath %q resolved to command script %q; set CodexBinaryPath to a native .exe", binary, resolved)
	case ".exe":
		return nil
	default:
		return fmt.Errorf("CodexBinaryPath %q must resolve to a native .exe, got %q", binary, resolved)
	}
}

func commandForConfig(cfg Config, args []string) (*exec.Cmd, error) {
	display, launch, err := cfg.resolvedLaunch()
	if err != nil {
		return nil, err
	}
	commandArgs := append(append([]string(nil), launch.PrefixArgs...), args...)
	cmd := exec.Command(launch.Executable, commandArgs...)
	cmd.Args[0] = display
	return cmd, nil
}

func (c Config) normalized() (Config, error) {
	var err error
	selection, err := taskmodel.Resolve(taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: taskmodel.DefaultReasoningEffort}, taskmodel.Selection{Model: c.ModelName, ReasoningEffort: c.ReasoningEffort})
	if err != nil {
		return Config{}, err
	}
	c.ModelName, c.ReasoningEffort = selection.Model, selection.ReasoningEffort
	if strings.TrimSpace(c.ResultSchema) != "" {
		c.ResultSchema, err = normalizedAbsolutePath("result schema", c.ResultSchema)
		if err != nil {
			return Config{}, err
		}
	}
	c.WorkingDirectory, err = normalizedAbsolutePath("working directory", c.WorkingDirectory)
	if err != nil {
		return Config{}, err
	}
	info, err := os.Stat(c.WorkingDirectory)
	if err != nil {
		return Config{}, fmt.Errorf("inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return Config{}, fmt.Errorf("working directory %q is not a directory", c.WorkingDirectory)
	}
	c.OutputLastMessage, err = normalizedAbsolutePath("output last message", c.OutputLastMessage)
	if err != nil {
		return Config{}, err
	}
	if !pathInside(c.WorkingDirectory, c.OutputLastMessage) {
		return Config{}, fmt.Errorf("output last message %q is outside working directory %q", c.OutputLastMessage, c.WorkingDirectory)
	}
	return c, nil
}

func normalizedAbsolutePath(label, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be absolute", label)
	}
	normalized, err := resolvePath(path)
	if err != nil {
		return "", fmt.Errorf("normalize %s: %w", label, err)
	}
	return normalized, nil
}

func BuildExecCommand(cfg Config, ctx TaskContext) (*exec.Cmd, error) {
	skill, err := SkillForTask(ctx.TaskType)
	if err != nil {
		return nil, err
	}
	if !ctx.ProjectDirGuard.open() {
		return nil, fmt.Errorf("project directory guard is required and must remain open")
	}
	normalized := ctx.ProjectDirGuard.normalized
	normalized.ProjectID = ctx.ProjectID
	normalized.AccountName = ctx.AccountName
	normalized.TaskType = ctx.TaskType
	cfg, err = cfg.normalized()
	if err != nil {
		return nil, err
	}
	if rel, err := filepath.Rel(normalized.ProjectDir, cfg.WorkingDirectory); err != nil || rel != "." {
		return nil, fmt.Errorf("working directory must be the guarded project root")
	}
	var prompt string
	var decodedManifest *TaskManifest
	if strings.TrimSpace(normalized.ManifestPath) != "" {
		// Read no manifest contents here: the manifest path is passed to the
		// bounded prompt builder, and the Skill reads the file under its
		// already-guarded task directory.
		manifest, err := os.ReadFile(normalized.ManifestPath)
		if err != nil {
			return nil, fmt.Errorf("read task manifest: %w", err)
		}
		var decoded TaskManifest
		if err := decodeStrictJSON(manifest, &decoded); err != nil {
			return nil, fmt.Errorf("decode task manifest: %w", err)
		}
		decodedManifest = &decoded
		prompt, err = BuildManifestPrompt(decoded, normalized.ManifestPath)
		if err != nil {
			return nil, err
		}
	} else {
		prompt, err = buildPrompt(normalized, skill)
	}
	if err != nil {
		return nil, err
	}
	args := []string{"--ask-for-approval", "never", "--sandbox", "workspace-write", "exec", "--json", "--skip-git-repo-check", "-m", cfg.ModelName, "-c", `model_reasoning_effort="` + cfg.ReasoningEffort + `"`}
	if decodedManifest != nil && (decodedManifest.Action == domain.ActionTopicCommit || decodedManifest.Action == domain.ActionTopicDeepen) {
		if vault := strings.TrimSpace(decodedManifest.NonSecretSettings.ObsidianVault); vault != "" {
			args = append(args, "--add-dir", vault)
		}
	}
	if cfg.ResultSchema != "" {
		args = append(args, "--output-schema", cfg.ResultSchema)
	}
	args = append(args, "--output-last-message", cfg.OutputLastMessage, "-C", cfg.WorkingDirectory, "-")
	cmd, err := commandForConfig(cfg, args)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = cfg.WorkingDirectory
	cmd.Env = cfg.SafeEnvironment()
	if normalized.ManifestPath != "" {
		cmd.Env = append(cmd.Env, taskManifestEnvironmentKey+"="+normalized.ManifestPath)
	}
	return cmd, nil
}

func BuildResumeCommand(cfg Config, sessionID, answer string, taskManifestPath ...string) (*exec.Cmd, error) {
	if !validSessionID.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid codex session id")
	}
	if len(taskManifestPath) > 1 {
		return nil, fmt.Errorf("at most one task manifest path may be supplied")
	}
	var err error
	cfg, err = cfg.normalized()
	if err != nil {
		return nil, err
	}
	manifestPath := ""
	if len(taskManifestPath) == 1 && strings.TrimSpace(taskManifestPath[0]) != "" {
		manifestPath, err = normalizedAbsolutePath("task manifest", taskManifestPath[0])
		if err != nil {
			return nil, err
		}
		if !pathInside(cfg.WorkingDirectory, manifestPath) {
			return nil, fmt.Errorf("task manifest %q is outside working directory %q", manifestPath, cfg.WorkingDirectory)
		}
		info, statErr := os.Lstat(manifestPath)
		if statErr != nil {
			return nil, fmt.Errorf("inspect task manifest: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("task manifest must be a regular file")
		}
	}
	if cfg.Redactor != nil {
		cfg.Redactor.Register(sessionID)
	}
	args := []string{"--ask-for-approval", "never", "--sandbox", "workspace-write", "exec", "resume", "--json", "-m", cfg.ModelName, "-c", `model_reasoning_effort="` + cfg.ReasoningEffort + `"`}
	if cfg.ResultSchema != "" {
		args = append(args, "--output-schema", cfg.ResultSchema)
	}
	args = append(args, "--output-last-message", cfg.OutputLastMessage, sessionID, "-")
	cmd, err := commandForConfig(cfg, args)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = strings.NewReader(answer)
	cmd.Dir = cfg.WorkingDirectory
	cmd.Env = cfg.SafeEnvironment()
	if manifestPath != "" {
		cmd.Env = append(cmd.Env, taskManifestEnvironmentKey+"="+manifestPath)
	}
	return cmd, nil
}

// SnapshotCommand produces the separately persistable, redacted description
// of a command. Environment values are intentionally discarded.
func SnapshotCommand(cmd *exec.Cmd, redactor *security.Redactor) CommandSnapshot {
	if cmd == nil {
		return CommandSnapshot{}
	}
	redact := func(value string) string { return value }
	if redactor != nil {
		redact = redactor.Redact
	}
	binary := ""
	args := []string(nil)
	if len(cmd.Args) > 0 {
		binary = redact(cmd.Args[0])
		args = make([]string, 0, len(cmd.Args)-1)
		for _, arg := range cmd.Args[1:] {
			args = append(args, redact(arg))
		}
	}
	return CommandSnapshot{
		Binary:           binary,
		Args:             args,
		WorkingDirectory: redact(cmd.Dir),
		EnvironmentKeys:  sortedEnvironmentKeys(cmd.Env),
	}
}

func sortedEnvironmentKeys(environment []string) []string {
	keys := make([]string, 0, len(environment))
	seen := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// StdinText is useful to verify or forward a command's stdin without exposing
// shell interpolation.
func StdinText(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	return string(b), err
}
