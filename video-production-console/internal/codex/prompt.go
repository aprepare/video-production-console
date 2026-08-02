package codex

import (
	"fmt"
	"path/filepath"
	"strings"
)

var skills = map[string]string{
	"topic_select":  "finance-topic-selector",
	"topic_deepen":  "finance-topic-selector",
	"remix":         "finance-viral-remix",
	"spoken_format": "finance-viral-remix",
	"montage":       "chatcut-finance-video",
}

// TaskContext is the complete, project-scoped context supplied to one Codex run.
type TaskContext struct {
	ProjectID    string
	AccountName  string
	TaskType     string
	WorkspaceDir string
	ProjectDir   string
	AllowedDir   string
	AssetPaths   []string
	OtherProject string // test/support metadata; never included in the prompt
}

// SkillForTask resolves the small set of supported workflows.
func SkillForTask(taskType string) (string, error) {
	skill, ok := skills[taskType]
	if !ok {
		return "", fmt.Errorf("unsupported codex task type %q", taskType)
	}
	return skill, nil
}

// BuildPrompt creates a bounded instruction for one project. It intentionally
// does not read process environment variables or any files outside the context.
func BuildPrompt(ctx TaskContext) (string, error) {
	skill, err := SkillForTask(ctx.TaskType)
	if err != nil {
		return "", err
	}
	normalized, err := normalizeContext(ctx)
	if err != nil {
		return "", err
	}
	allowed := normalized.AllowedDir
	var assets strings.Builder
	for _, path := range normalized.AssetPaths {
		fmt.Fprintf(&assets, "- %s\n", path)
	}
	return fmt.Sprintf(`You are running one isolated video-production task.

Task type: %s
Project ID: %s
Account name: %s
Use the skill: $%s
Project directory: %s
Allowed output directory: %s
Project assets:
%s

Rules:
- Work only on this project and its listed assets. Do not access any other project, workspace, vault, or unrelated path.
- Write every artifact only inside the allowed output directory.
- Return exactly one JSON object matching the configured codex-result schema.
- If a human decision is required, return status "needs_input" with a question and end this turn.
- Do not open, automate, or test WeChat Channels.
- Do not start, stop, modify, or proxy the existing baokuan process.
`, normalized.TaskType, normalized.ProjectID, normalized.AccountName, skill, normalized.ProjectDir, allowed, assets.String()), nil
}

func normalizeContext(ctx TaskContext) (TaskContext, error) {
	if strings.TrimSpace(ctx.WorkspaceDir) == "" {
		return TaskContext{}, fmt.Errorf("workspace boundary is required")
	}
	workspace, err := filepath.Abs(filepath.Clean(ctx.WorkspaceDir))
	if err != nil {
		return TaskContext{}, fmt.Errorf("normalize workspace boundary: %w", err)
	}
	project, err := normalizeInside("project directory", ctx.ProjectDir, workspace)
	if err != nil {
		return TaskContext{}, err
	}
	allowed := ctx.AllowedDir
	if allowed == "" {
		allowed = project
	}
	allowed, err = normalizeInside("allowed directory", allowed, workspace)
	if err != nil {
		return TaskContext{}, err
	}
	assets := make([]string, 0, len(ctx.AssetPaths))
	for _, path := range ctx.AssetPaths {
		if path == "" {
			continue
		}
		normalized, err := normalizeInside("asset path", path, workspace)
		if err != nil {
			return TaskContext{}, err
		}
		assets = append(assets, normalized)
	}
	ctx.WorkspaceDir, ctx.ProjectDir, ctx.AllowedDir, ctx.AssetPaths = workspace, project, allowed, assets
	return ctx, nil
}

func normalizeInside(label, path, boundary string) (string, error) {
	normalized, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("normalize %s: %w", label, err)
	}
	rel, err := filepath.Rel(boundary, normalized)
	if err != nil {
		return "", fmt.Errorf("check %s: %w", label, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s %q is outside workspace boundary %q", label, path, boundary)
	}
	return normalized, nil
}
