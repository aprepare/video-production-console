package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/domain"
)

var skills = map[string]string{
	"topic_select": "finance-topic-selector",
	"topic_commit": "finance-topic-selector",
	"topic_deepen": "finance-topic-selector",
	"remix":        "finance-viral-remix",
	"montage":      "jianying-montage-draft",
}

var legacyWireActions = map[string]string{
	"topic_select": "brainstorm", "topic_commit": "commit_topic", "topic_deepen": "deepen", "remix": "standard",
	"montage": "execute",
}

var legacyTaskActions = map[string]domain.TaskAction{
	"topic_select": domain.ActionTopicBrainstorm,
	"topic_commit": domain.ActionTopicCommit,
	"topic_deepen": domain.ActionTopicDeepen,
	"remix":        domain.ActionRemixStandard,
	"montage":      domain.ActionMontageExecute,
}

// ResolveTaskAction bridges the original HTTP task type vocabulary to the
// action-based manifest/result protocol. An explicit action is accepted for
// compatible variants (for example remix.enhanced), but it may never switch
// a task into another Skill family.
func ResolveTaskAction(taskType string, requested domain.TaskAction) (domain.TaskAction, ActionResolution, error) {
	skill, err := SkillForTask(taskType)
	if err != nil {
		return "", ActionResolution{}, err
	}
	if requested == "" {
		var ok bool
		requested, ok = legacyTaskActions[taskType]
		if !ok {
			return "", ActionResolution{}, fmt.Errorf("no default action for codex task type %q", taskType)
		}
	}
	resolved, err := ResolveAction(requested)
	if err != nil {
		return "", ActionResolution{}, err
	}
	if resolved.Skill != skill {
		return "", ActionResolution{}, fmt.Errorf("action %q belongs to skill %q, not task type %q", requested, resolved.Skill, taskType)
	}
	return requested, resolved, nil
}

// TaskContext is the complete, project-scoped context supplied to one Codex run.
type TaskContext struct {
	ProjectID    string
	AccountName  string
	TaskType     string
	WorkspaceDir string
	ProjectDir   string
	AllowedDir   string
	// ManifestPath is the canonical, task-scoped manifest to use as the
	// authoritative prompt source. Empty retains the legacy compatibility
	// prompt for callers that have not migrated to manifest preparation yet.
	ManifestPath    string
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
	return buildManifestPrompt(manifest, manifestPath, true)
}

// BuildManifestAppServerPrompt is the App Server equivalent of
// BuildManifestPrompt. App Server turns cannot receive a task-specific process
// environment, so they use the already validated absolute manifest path rather
// than VIDEO_CONSOLE_TASK_MANIFEST.
func BuildManifestAppServerPrompt(manifest TaskManifest, manifestPath string) (string, error) {
	return buildManifestPrompt(manifest, manifestPath, false)
}

func buildManifestPrompt(manifest TaskManifest, manifestPath string, useEnvironmentPath bool) (string, error) {
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
	_, projectRoot, err := canonicalTaskOutput(manifest.OutputDir, manifest.TaskID, "")
	if err != nil {
		return "", err
	}
	canonical, err := resolvePath(manifestPath)
	if err != nil {
		return "", fmt.Errorf("canonicalize manifest path: %w", err)
	}
	expected, err := resolvePath(filepath.Join(projectRoot, "tasks", manifest.TaskID, "task_manifest.json"))
	if err != nil {
		return "", err
	}
	if !pathInside(projectRoot, expected) {
		return "", fmt.Errorf("manifest path escapes project root")
	}
	if !canonicalSamePath(canonical, expected) {
		return "", fmt.Errorf("manifest path must equal canonical task manifest path")
	}
	if useEnvironmentPath {
		return formatManifestPrompt(manifest.Skill, resolved.WireAction, taskManifestEnvironmentKey)
	}
	return formatManifestPrompt(manifest.Skill, resolved.WireAction, canonical)
}

func formatManifestPrompt(skill, wireAction, manifestPath string) (string, error) {
	if skill == "" || wireAction == "" {
		return "", fmt.Errorf("skill and wire action are required")
	}
	manifestInstruction := fmt.Sprintf("Execute action=%s using the task manifest at %s.", wireAction, manifestPath)
	if manifestPath == taskManifestEnvironmentKey {
		manifestInstruction = fmt.Sprintf("Execute action=%s using the task manifest path from environment variable %s. Read the variable at runtime; do not retype or reconstruct the absolute path.", wireAction, taskManifestEnvironmentKey)
	}
	extra := ""
	if skill == "jianying-montage-draft" && wireAction == "execute" {
		extra = `
Console montage.execute only:
1) Read console-contract.md only. No web/Grok search. Do not open run_montage_job.py source.
2) Run scripts/run_montage_job.py validate-inputs --manifest %VIDEO_CONSOLE_TASK_MANIFEST% via cmd.exe.
3) Write production_plan.json from narration duration + selective media-index queries (never dump whole indexes/scripts).
4) Run validate-plan then execute --plan. One thread; no subagents.`
	}
	prompt := fmt.Sprintf(`Use the $%s skill.
%s
Treat manifest inputs as authoritative and do not ask for paths already present.
Write declared artifacts only under output_dir.
Return exactly one JSON object matching the configured result schema.
On Windows, never pipe non-ASCII text or JSON through PowerShell into another process; use a UTF-8 file or the Skill's UTF-8 writer.
Do not open WeChat Channels.
Do not launch Jianying; this manifest protocol contains no UI authorization.%s`, skill, manifestInstruction, extra)
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
	if strings.TrimSpace(ctx.ManifestPath) != "" {
		manifest, err := normalizeInside("manifest path", ctx.ManifestPath, allowed)
		if err != nil {
			return TaskContext{}, err
		}
		ctx.ManifestPath = manifest
	}
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
	path = normalizeWindowsExtendedPath(path)
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

// normalizeWindowsExtendedPath converts Win32 extended-length paths emitted by
// some tools (for example, \\?\\C:\\work\\file) to the regular form used by
// filepath.Rel and filepath.EvalSymlinks. The prefix changes the spelling, not
// the filesystem location, so retaining it during containment checks would
// incorrectly report a path inside the output directory as external.
func normalizeWindowsExtendedPath(path string) string {
	if filepath.Separator != '\\' {
		return path
	}
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	if strings.HasPrefix(path, `\\?\`) {
		return strings.TrimPrefix(path, `\\?\`)
	}
	return path
}
