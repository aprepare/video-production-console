package codex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type MCPStatus struct {
	Name      string    `json:"name"`
	Available bool      `json:"available"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
}

func CheckBaokuanMCP(ctx context.Context, binary string) MCPStatus {
	s := MCPStatus{Name: "baokuan_mcp", CheckedAt: time.Now().UTC()}
	if binary == "" {
		binary = "codex"
	}
	out, err := exec.CommandContext(ctx, binary, "mcp", "list").CombinedOutput()
	if err != nil {
		s.Message = fmt.Sprintf("codex mcp list: %v", err)
		return s
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.EqualFold(strings.Trim(f, "|`*"), "baokuan") {
				s.Available = true
				s.Message = "registered"
				return s
			}
		}
	}
	s.Message = "baokuan MCP is not registered"
	return s
}
