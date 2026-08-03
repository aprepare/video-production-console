package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var skills = map[string]string{
	"topic_select":  "finance-topic-selector",
	"topic_deepen":  "finance-topic-selector",
	"remix":         "finance-viral-remix",
	"spoken_format": "finance-viral-remix",
	"montage":       "jianying-montage-draft",
}

var legacyWireActions = map[string]string{
	"topic_select": "brainstorm", "topic_deepen": "deepen", "remix": "standard",
	"spoken_format": "spoken_format", "montage": "plan",
}

// TaskContext is the complete, project-scoped context supplied to one Codex run.
type TaskContext struct {
	ProjectID       string
	AccountName     string
	TaskType        string
	WorkspaceDir    string
	ProjectDir      string
	AllowedDir      string
	AssetPaths      []string
	ProjectDirGuard *ProjectDirGuard
	OtherProject    string // test/support metadata; never included in the prompt
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
	return buildPrompt(normalized, skill)
}

func buildPrompt(normalized TaskContext, skill string) (string, error) {
	manifestPath := filepath.Join(normalized.AllowedDir, "task_manifest.json")
	return formatManifestPrompt(skill, legacyWireActions[normalized.TaskType], manifestPath)
}

// BuildManifestPrompt returns the complete bounded instruction for a V2 task.
// The manifest is the only source of input paths; none are copied into Prompt.
func BuildManifestPrompt(manifest TaskManifest, manifestPath string) (string, error) {
	resolved, err := ResolveAction(manifest.Action)
	if err != nil {
		return "", err
	}
	if manifest.Skill != resolved.Skill {
		return "", fmt.Errorf("manifest skill %q does not match action %q", manifest.Skill, manifest.Action)
	}
	if strings.TrimSpace(manifestPath) == "" || secretValue.MatchString(strings.ReplaceAll(strings.ToLower(manifestPath), "%20", " ")) {
		return "", fmt.Errorf("manifest path is empty or resembles secret material")
	}
	return formatManifestPrompt(manifest.Skill, resolved.WireAction, manifestPath)
}

func formatManifestPrompt(skill, wireAction, manifestPath string) (string, error) {
	if skill == "" || wireAction == "" {
		return "", fmt.Errorf("skill and wire action are required")
	}
	prompt := fmt.Sprintf(`Use the $%s skill.
Execute action=%s using the task manifest at %s.
Treat manifest inputs as authoritative and do not ask for paths already present.
Write declared artifacts only under output_dir.
Return exactly one JSON object matching the configured result schema.
Do not open WeChat Channels.
Do not launch Jianying; this manifest protocol contains no UI authorization.`, skill, wireAction, manifestPath)
	if len(prompt) > 1200 {
		return "", fmt.Errorf("prompt exceeds bounded length")
	}
	return prompt, nil
}

func normalizeContext(ctx TaskContext) (TaskContext, error) {
	if strings.TrimSpace(ctx.WorkspaceDir) == "" {
		return TaskContext{}, fmt.Errorf("workspace boundary is required")
	}
	workspace, err := resolvePath(ctx.WorkspaceDir)
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
	allowed, err = normalizeInside("allowed directory", allowed, project)
	if err != nil {
		return TaskContext{}, err
	}
	assets := make([]string, 0, len(ctx.AssetPaths))
	for _, path := range ctx.AssetPaths {
		if path == "" {
			continue
		}
		normalized, err := normalizeInside("asset path", path, project)
		if err != nil {
			return TaskContext{}, err
		}
		assets = append(assets, normalized)
	}
	ctx.WorkspaceDir, ctx.ProjectDir, ctx.AllowedDir, ctx.AssetPaths = workspace, project, allowed, assets
	return ctx, nil
}

func normalizeInside(label, path, boundary string) (string, error) {
	normalized, err := resolvePath(path)
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

// resolvePath canonicalizes every existing component. For a new output path,
// it resolves the nearest existing parent before appending the missing tail.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	probe := abs
	var missing []string
	for {
		if _, err := os.Lstat(probe); err == nil {
			resolved, err := filepath.EvalSymlinks(probe)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Abs(filepath.Clean(resolved))
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent, base := filepath.Dir(probe), filepath.Base(probe)
		if parent == probe {
			return "", fmt.Errorf("no existing parent for %q", path)
		}
		missing = append(missing, base)
		probe = parent
	}
}
