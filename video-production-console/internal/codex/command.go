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

	"video-production-console/internal/security"
)

var (
	systemEnvironmentKeys = []string{"COMSPEC", "PATH", "PATHEXT", "SYSTEMROOT", "TEMP", "TMP", "USERPROFILE", "WINDIR"}
	secretEnvironmentKeys = []string{"GROK_SEARCH_API_KEY", "GROK_SEARCH_BASE_URL", "GROK_SEARCH_MODEL", "PEXELS_API_KEY"}
	validSessionID        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

type Config struct {
	CodexBinaryPath   string
	ResultSchema      string
	OutputLastMessage string
	WorkingDirectory  string
	SecretEnvironment map[string]string
	Redactor          *security.Redactor
	BinaryResolver    func(string) (string, error)
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

func (c Config) resolvedBinary() (string, string, error) {
	display := c.binary()
	resolver := c.BinaryResolver
	if resolver == nil && runtime.GOOS != "windows" {
		return display, display, nil
	}
	if resolver == nil {
		resolver = exec.LookPath
	}
	resolved, err := resolveBinaryPath(display, resolver, runtime.GOOS == "windows")
	if err != nil {
		return "", "", err
	}
	return display, resolved, nil
}

func resolveBinaryPath(binary string, resolver func(string) (string, error), windows bool) (string, error) {
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
	if windows {
		if err := validateWindowsBinaryExtension(binary, resolved); err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("canonicalize CodexBinaryPath %q: %w", resolved, err)
	}
	resolved, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("canonicalize CodexBinaryPath %q: %w", resolved, err)
	}
	if windows {
		if err := validateWindowsBinaryExtension(binary, resolved); err != nil {
			return "", err
		}
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
	display, resolved, err := cfg.resolvedBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(resolved, args...)
	cmd.Args[0] = display
	return cmd, nil
}

func (c Config) normalized() (Config, error) {
	var err error
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
	args := []string{"exec", "--json", "--skip-git-repo-check"}
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
	return cmd, nil
}

func BuildResumeCommand(cfg Config, sessionID, answer string) (*exec.Cmd, error) {
	if !validSessionID.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid codex session id")
	}
	var err error
	cfg, err = cfg.normalized()
	if err != nil {
		return nil, err
	}
	if cfg.Redactor != nil {
		cfg.Redactor.Register(sessionID)
	}
	args := []string{"exec", "resume", "--json"}
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
