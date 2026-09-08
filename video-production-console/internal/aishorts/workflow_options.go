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

// Chat 只给没写思考强度的请求补上用户选的强度。分大段、拆镜重试这类明确写了 "low" 的调用保留原值——
// 之前是无条件覆盖，导致这些辅助调用也按 high/xhigh 跑，一篇稿的拆分镜多耗一两分钟（09-08 排查）。
func (c reasoningClient) Chat(req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	if strings.TrimSpace(req.ReasoningEffort) == "" {
		req.ReasoningEffort = c.effort
	}
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
