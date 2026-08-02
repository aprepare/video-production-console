package codex

import (
	"fmt"
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
func BuildPrompt(ctx TaskContext) string {
	skill, err := SkillForTask(ctx.TaskType)
	if err != nil {
		skill = "unsupported-task"
	}
	allowed := ctx.AllowedDir
	if allowed == "" {
		allowed = ctx.ProjectDir
	}
	var assets strings.Builder
	for _, path := range ctx.AssetPaths {
		if path != "" {
			fmt.Fprintf(&assets, "- %s\n", path)
		}
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
`, ctx.TaskType, ctx.ProjectID, ctx.AccountName, skill, ctx.ProjectDir, allowed, assets.String())
}
