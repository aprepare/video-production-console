package agentruntime

import (
	"context"

	"video-production-console/internal/domain"
)

type RuntimeName string

const (
	RuntimeCodex  RuntimeName = "codex"
	RuntimeScript RuntimeName = "script"
)

// EnvMontageRuntime selects the montage.execute backend.
// Values: "script" (default) or "codex".
const EnvMontageRuntime = "VIDEO_CONSOLE_MONTAGE_RUNTIME"

type ModelRef struct {
	Provider  string
	Model     string
	Effort    string
	BaseURL   string
	APIKeyEnv string
}

type ToolPolicy struct {
	AllowWebSearch  bool
	AllowSubagents  bool
	AllowShell      bool
	MaxSteps        int
	AllowedCommands []string
}

type LaunchRequest struct {
	TaskID             string
	Action             domain.TaskAction
	Skill              string
	ManifestPath       string
	WorkDir            string
	OutputLastMessage  string
	SkillRoot          string
	Model              ModelRef
	EnvAllowlist       map[string]string
	ToolPolicy         ToolPolicy
}

type Event struct {
	Kind    string
	Phase   string
	Title   string
	Detail  string
	RawJSON []byte
}

type RunHandle interface {
	Wait(ctx context.Context) error
	Cancel(ctx context.Context) error
}

// Runtime is the pluggable worker contract. Week 1 wires script/codex through
// CommandFactory; the interface documents the longer-term boundary.
type Runtime interface {
	Name() RuntimeName
	Supports(action domain.TaskAction) bool
}
