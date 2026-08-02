package codex

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

type Config struct {
	CodexBinaryPath string
	ResultSchema    string
}

// SafeEnvironment passes only process basics needed to locate and run Codex.
func (c Config) SafeEnvironment() []string {
	allowed := map[string]bool{"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "TEMP": true, "TMP": true, "USERPROFILE": true}
	result := make([]string, 0, len(allowed))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[strings.ToUpper(key)] {
			result = append(result, entry)
		}
	}
	return result
}

func (c Config) binary() string {
	if c.CodexBinaryPath == "" {
		return "codex"
	}
	return c.CodexBinaryPath
}

func BuildExecCommand(cfg Config, ctx TaskContext) *exec.Cmd {
	args := []string{"exec", "--json", "--skip-git-repo-check", "--output-schema", cfg.ResultSchema, "-C", ctx.ProjectDir, "-"}
	cmd := exec.Command(cfg.binary(), args...)
	cmd.Stdin = strings.NewReader(BuildPrompt(ctx))
	cmd.Env = cfg.SafeEnvironment()
	return cmd
}

func BuildResumeCommand(cfg Config, sessionID, answer string) *exec.Cmd {
	args := []string{"exec", "resume", sessionID, "--json", "--output-schema", cfg.ResultSchema, "-"}
	cmd := exec.Command(cfg.binary(), args...)
	cmd.Stdin = strings.NewReader(answer)
	cmd.Env = cfg.SafeEnvironment()
	return cmd
}

// StdinText is useful to verify or forward a command's stdin without exposing
// shell interpolation.
func StdinText(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	return string(b), err
}
