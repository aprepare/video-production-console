package codex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func ConfigureBaokuanMCP(ctx context.Context, codexBinary, mcpExecutable, baseURL string) error {
	if codexBinary == "" {
		codexBinary = "codex"
	}
	if strings.TrimSpace(mcpExecutable) == "" || strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("mcp executable and base URL are required")
	}
	out, err := exec.CommandContext(ctx, codexBinary, "mcp", "list").CombinedOutput()
	text := string(out)
	if err == nil && strings.Contains(strings.ToLower(text), "baokuan") {
		if strings.Contains(text, mcpExecutable) && strings.Contains(text, baseURL) {
			return nil
		}
		return fmt.Errorf("mcp_config_conflict: baokuan is registered with different command")
	}
	cmd := exec.CommandContext(ctx, codexBinary, "mcp", "add", "baokuan", "--", mcpExecutable, "mcp", "--base", baseURL)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("configure baokuan MCP: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
