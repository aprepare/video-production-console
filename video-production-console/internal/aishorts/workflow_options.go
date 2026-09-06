package aishorts

import (
	"errors"
	"strings"
	"time"
	"video-production-console/internal/agentruntime/openaicompat"
)

func reasoningOf(s string) string {
	if strings.TrimSpace(s) == "" {
		return "low"
	}
	return strings.TrimSpace(s)
}
func validateReasoning(s string) error {
	switch strings.TrimSpace(s) {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
		return nil
	}
	return errors.New("分镜思考强度无效")
}

type reasoningClient struct {
	openaicompat.ChatClient
	effort string
}

func (c reasoningClient) Chat(req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	req.ReasoningEffort = c.effort
	return c.ChatClient.Chat(req)
}

type AssemblyProgress struct {
	Stage     int       `json:"stage"`
	Message   string    `json:"message"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func assemblyStep(message string, previous *AssemblyProgress) *AssemblyProgress {
	p := &AssemblyProgress{Message: message, StartedAt: now(), UpdatedAt: now()}
	if previous != nil {
		p.StartedAt = previous.StartedAt
		p.Stage = previous.Stage
	}
	stage := 0
	switch {
	case strings.Contains(message, "导入剪映"):
		stage = 3
	case strings.Contains(message, "生成剪映草稿"):
		stage = 2
	case strings.Contains(message, "对齐") || strings.Contains(message, "复用"):
		stage = 1
	}
	if stage > p.Stage {
		p.Stage = stage
	}
	return p
}
