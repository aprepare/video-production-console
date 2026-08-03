package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

const ProtocolSchemaVersion = "2.0"

type ActionResolution struct {
	Skill      string
	WireAction string
}

var actionResolutions = map[domain.TaskAction]ActionResolution{
	domain.ActionTopicBrainstorm: {Skill: "finance-topic-selector", WireAction: "brainstorm"},
	domain.ActionTopicCommit:     {Skill: "finance-topic-selector", WireAction: "commit_topic"},
	domain.ActionTopicDeepen:     {Skill: "finance-topic-selector", WireAction: "deepen"},
	domain.ActionRemixStandard:   {Skill: "finance-viral-remix", WireAction: "standard"},
	domain.ActionRemixEnhanced:   {Skill: "finance-viral-remix", WireAction: "enhanced"},
	domain.ActionRemixFromTopic:  {Skill: "finance-viral-remix", WireAction: "from_topic_card"},
	domain.ActionSpokenFormat:    {Skill: "finance-viral-remix", WireAction: "spoken_format"},
	domain.ActionRemixReview:     {Skill: "finance-viral-remix", WireAction: "review"},
	domain.ActionMontagePlan:     {Skill: "jianying-montage-draft", WireAction: "plan"},
	domain.ActionMontageExecute:  {Skill: "jianying-montage-draft", WireAction: "execute"},
}

func ResolveAction(action domain.TaskAction) (ActionResolution, error) {
	resolved, ok := actionResolutions[action]
	if !ok {
		return ActionResolution{}, fmt.Errorf("unsupported domain action %q", action)
	}
	return resolved, nil
}

type TaskManifest struct {
	SchemaVersion     string             `json:"schema_version"`
	TaskID            string             `json:"task_id"`
	JobID             string             `json:"job_id"`
	Skill             string             `json:"skill"`
	Action            domain.TaskAction  `json:"action"`
	Project           *ManifestProject   `json:"project,omitempty"`
	Inputs            []ManifestInput    `json:"inputs"`
	EngineeringInputs []EngineeringInput `json:"engineering_inputs"`
	OutputDir         string             `json:"output_dir"`
	ExpectedOutputs   []ExpectedOutput   `json:"expected_outputs"`
	ApprovalMode      string             `json:"approval_mode"`
	SkillSnapshotID   string             `json:"skill_snapshot_id"`
	NonSecretSettings ManifestSettings   `json:"non_secret_settings"`
}

type ManifestProject struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
}

type ManifestInput struct {
	AssetID     string             `json:"asset_id"`
	VersionID   string             `json:"version_id"`
	Version     int                `json:"version"`
	Type        domain.AssetType   `json:"type"`
	Role        string             `json:"role"`
	Path        string             `json:"path"`
	StorageKind domain.StorageKind `json:"storage_kind"`
	MIME        string             `json:"mime"`
	Size        int64              `json:"size"`
	SHA256      string             `json:"sha256"`
	Required    bool               `json:"required"`
}

type EngineeringInput struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type ExpectedOutput struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// ManifestSettings is the flat allowlist of public console configuration that
// a Skill may consume. Credentials have no representable field in this type.
type ManifestSettings struct {
	ListenAddr           string `json:"listen_addr,omitempty"`
	DataRoot             string `json:"data_root,omitempty"`
	MaxCodexConcurrency  int    `json:"max_codex_concurrency,omitempty"`
	BaokuanBaseURL       string `json:"baokuan_base_url,omitempty"`
	BaokuanMCPExecutable string `json:"baokuan_mcp_executable,omitempty"`
	ObsidianVault        string `json:"obsidian_vault,omitempty"`
	TopicCardsDir        string `json:"topic_cards_dir,omitempty"`
	SessionID            string `json:"session_id,omitempty"`
	CandidateID          string `json:"candidate_id,omitempty"`
	TopicCandidatesPath  string `json:"topic_candidates_path,omitempty"`
	GrokBaseURL          string `json:"grok_base_url,omitempty"`
	GrokModel            string `json:"grok_model,omitempty"`
	CodexBinaryPath      string `json:"codex_binary_path,omitempty"`
	MediaIndexPath       string `json:"media_index_path,omitempty"`
	MediaRoot            string `json:"media_root,omitempty"`
	JianyingRoot         string `json:"jianying_root,omitempty"`
	MachineProfilePath   string `json:"machine_profile_path,omitempty"`
}

type BuildManifestInput struct {
	Task              domain.CodexTask
	Project           *domain.Project
	Inputs            []domain.AssetVersion
	Action            domain.TaskAction
	OutputDir         string
	ExpectedOutputs   []ExpectedOutput
	ApprovalMode      string
	SkillSnapshot     domain.SkillSnapshot
	NonSecretSettings ManifestSettings
}

func BuildManifest(in BuildManifestInput) (TaskManifest, error) {
	resolved, err := ResolveAction(in.Action)
	if err != nil {
		return TaskManifest{}, err
	}
	if in.Task.ID == "" {
		return TaskManifest{}, fmt.Errorf("task ID is required")
	}
	if in.SkillSnapshot.Name != "" && in.SkillSnapshot.Name != resolved.Skill {
		return TaskManifest{}, fmt.Errorf("skill snapshot %q does not match action skill %q", in.SkillSnapshot.Name, resolved.Skill)
	}
	outputDir, projectRoot, err := canonicalTaskOutput(in.OutputDir, in.Task.ID, "")
	if err != nil {
		return TaskManifest{}, err
	}
	manifestPath, err := resolvePath(filepath.Join(projectRoot, "tasks", in.Task.ID, "task_manifest.json"))
	if err != nil {
		return TaskManifest{}, fmt.Errorf("canonicalize task manifest path: %w", err)
	}
	inputs := make([]ManifestInput, 0, len(in.Inputs))
	roles := make(map[string]bool, len(in.Inputs))
	for _, version := range in.Inputs {
		if version.State != domain.AssetReady {
			return TaskManifest{}, fmt.Errorf("input version %q is not ready", version.ID)
		}
		if err := validateInputIdentity(in.Project, version); err != nil {
			return TaskManifest{}, err
		}
		inputPath, err := resolvePath(version.Path)
		if err != nil {
			return TaskManifest{}, fmt.Errorf("canonicalize input version %q: %w", version.ID, err)
		}
		if pathsOverlap(outputDir, inputPath) {
			return TaskManifest{}, fmt.Errorf("input version %q overlaps output_dir", version.ID)
		}
		if pathsOverlap(manifestPath, inputPath) {
			return TaskManifest{}, fmt.Errorf("input version %q overlaps task manifest path", version.ID)
		}
		role := manifestRole(version.Type)
		roles[role] = true
		inputs = append(inputs, ManifestInput{
			AssetID: version.AssetID, VersionID: version.ID, Version: version.Version,
			Type: version.Type, Role: role, Path: inputPath, StorageKind: version.StorageKind, MIME: version.MIMEType,
			Size: version.Size, SHA256: version.SHA256, Required: true,
		})
	}
	if required := requiredInputRoles[in.Action]; required != nil {
		for _, role := range required {
			if !roles[role] {
				return TaskManifest{}, fmt.Errorf("action %q input %q is required", in.Action, role)
			}
		}
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Role == inputs[j].Role {
			return inputs[i].VersionID < inputs[j].VersionID
		}
		return inputs[i].Role < inputs[j].Role
	})
	engineeringInputs := []EngineeringInput{}
	if strings.TrimSpace(in.NonSecretSettings.TopicCandidatesPath) != "" {
		path, err := resolvePath(in.NonSecretSettings.TopicCandidatesPath)
		if err != nil {
			return TaskManifest{}, fmt.Errorf("canonicalize topic candidates path: %w", err)
		}
		in.NonSecretSettings.TopicCandidatesPath = path
		engineeringInputs = append(engineeringInputs, EngineeringInput{Type: "topic_candidates", Path: path})
	}
	if strings.TrimSpace(in.NonSecretSettings.MachineProfilePath) != "" {
		path, err := resolvePath(in.NonSecretSettings.MachineProfilePath)
		if err != nil {
			return TaskManifest{}, fmt.Errorf("canonicalize machine profile path: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return TaskManifest{}, fmt.Errorf("machine_profile_path must name a regular file")
		}
		in.NonSecretSettings.MachineProfilePath = path
	}
	approval := in.ApprovalMode
	if approval == "" {
		approval = "plan_then_wait"
	}
	if approval != "plan_then_wait" && approval != "plan_only" && approval != "auto_after_valid_plan" {
		return TaskManifest{}, fmt.Errorf("unsupported approval mode %q", approval)
	}
	manifest := TaskManifest{
		SchemaVersion: ProtocolSchemaVersion, TaskID: in.Task.ID, JobID: in.Task.ID,
		Skill: resolved.Skill, Action: in.Action, Inputs: inputs, OutputDir: outputDir,
		EngineeringInputs: engineeringInputs,
		ExpectedOutputs:   append([]ExpectedOutput(nil), in.ExpectedOutputs...), ApprovalMode: approval,
		SkillSnapshotID: in.SkillSnapshot.ID, NonSecretSettings: in.NonSecretSettings,
	}
	if manifest.ExpectedOutputs == nil {
		manifest.ExpectedOutputs = []ExpectedOutput{}
	}
	if in.Project != nil {
		manifest.Project = &ManifestProject{ID: in.Project.ID, AccountID: in.Project.AccountID}
	}
	if err := validateManifestSchemaValues(manifest); err != nil {
		return TaskManifest{}, err
	}
	return manifest, nil
}

type ManifestRoots struct {
	Project       string
	AccountAssets string
	Obsidian      string
}

type manifestFileOperations struct {
	chmod func(*os.File, os.FileMode) error
	write func(*os.File, []byte) (int, error)
	sync  func(*os.File) error
	close func(*os.File) error
	link  func(string, string) error
}

var manifestFileOps = manifestFileOperations{
	chmod: func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) },
	write: func(file *os.File, data []byte) (int, error) { return file.Write(data) },
	sync:  func(file *os.File) error { return file.Sync() },
	close: func(file *os.File) error { return file.Close() },
	link:  os.Link,
}

func WriteManifest(manifest TaskManifest, roots ManifestRoots) (string, error) {
	if err := validateManifestSchemaValues(manifest); err != nil {
		return "", err
	}
	resolved, err := ResolveAction(manifest.Action)
	if err != nil {
		return "", err
	}
	if manifest.SchemaVersion != ProtocolSchemaVersion || manifest.Skill != resolved.Skill || manifest.JobID != manifest.TaskID {
		return "", fmt.Errorf("manifest protocol identity is inconsistent")
	}
	projectRoot, err := requiredDirectoryRoot("project", roots.Project)
	if err != nil {
		return "", fmt.Errorf("canonicalize project root: %w", err)
	}
	accountRoot, err := optionalResolvedRoot(roots.AccountAssets)
	if err != nil {
		return "", fmt.Errorf("canonicalize account-assets root: %w", err)
	}
	obsidianRoot, err := optionalResolvedRoot(roots.Obsidian)
	if err != nil {
		return "", fmt.Errorf("canonicalize Obsidian root: %w", err)
	}
	output, _, err := canonicalTaskOutput(manifest.OutputDir, manifest.TaskID, projectRoot)
	if err != nil {
		return "", err
	}
	manifest.OutputDir = output
	if err := os.MkdirAll(output, 0o700); err != nil {
		return "", fmt.Errorf("create output_dir: %w", err)
	}
	for i := range manifest.Inputs {
		input := &manifest.Inputs[i]
		var allowed []string
		switch input.Type {
		case domain.AssetAccountBackground:
			allowed = []string{accountRoot}
		case domain.AssetTopicCard:
			allowed = []string{projectRoot, obsidianRoot}
		default:
			allowed = []string{projectRoot}
		}
		path, err := canonicalInAnyRoot("input path", input.Path, allowed...)
		if err != nil {
			return "", fmt.Errorf("input %q: %w", input.Role, err)
		}
		input.Path = path
		if pathsOverlap(output, path) {
			return "", fmt.Errorf("input %q overlaps output_dir", input.Role)
		}
	}
	for i := range manifest.EngineeringInputs {
		input := &manifest.EngineeringInputs[i]
		path, err := canonicalInAnyRoot("engineering input path", input.Path, projectRoot)
		if err != nil {
			return "", fmt.Errorf("engineering input %q: %w", input.Type, err)
		}
		input.Path = path
		if pathsOverlap(output, path) {
			return "", fmt.Errorf("engineering input %q overlaps output_dir", input.Type)
		}
	}
	taskDir, err := canonicalContained("task directory", filepath.Join(projectRoot, "tasks", manifest.TaskID), projectRoot)
	if err != nil {
		return "", err
	}
	finalPath := filepath.Join(taskDir, "task_manifest.json")
	if pathsOverlap(output, finalPath) {
		return "", fmt.Errorf("output_dir overlaps task manifest path")
	}
	for _, input := range manifest.Inputs {
		if pathsOverlap(finalPath, input.Path) {
			return "", fmt.Errorf("input %q overlaps task manifest path", input.Role)
		}
	}
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return "", fmt.Errorf("create task directory: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')
	if old, readErr := os.ReadFile(finalPath); readErr == nil {
		if bytes.Equal(old, data) {
			return finalPath, nil
		}
		return "", fmt.Errorf("manifest already exists with different content")
	} else if !os.IsNotExist(readErr) {
		return "", fmt.Errorf("inspect existing manifest: %w", readErr)
	}
	tmp, err := os.CreateTemp(taskDir, ".task_manifest-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create manifest temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	ops := manifestFileOps
	writeErr := ops.chmod(tmp, 0o600)
	if writeErr == nil {
		var written int
		written, writeErr = ops.write(tmp, data)
		if writeErr == nil && written != len(data) {
			writeErr = fmt.Errorf("short manifest write: wrote %d of %d bytes", written, len(data))
		}
	}
	if writeErr == nil {
		writeErr = ops.sync(tmp)
	}
	closeErr := ops.close(tmp)
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return "", fmt.Errorf("write manifest: %w", writeErr)
	}
	if _, err := os.Lstat(finalPath); err == nil {
		return "", fmt.Errorf("manifest already exists")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Linking a fully flushed temporary file publishes it atomically while
	// preserving O_EXCL-style no-overwrite semantics on every supported OS.
	if err := ops.link(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("publish manifest atomically: %w", err)
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", fmt.Errorf("remove manifest temporary link: %w", err)
	}
	return finalPath, nil
}

func canonicalTaskOutput(path, taskID, projectRoot string) (string, string, error) {
	output, err := resolvePath(path)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize output_dir: %w", err)
	}
	root := projectRoot
	if root == "" {
		taskDir := filepath.Dir(output)
		if filepath.Base(output) != "output" || filepath.Base(taskDir) != taskID || filepath.Base(filepath.Dir(taskDir)) != "tasks" {
			return "", "", fmt.Errorf("output_dir must be projectRoot/tasks/%s/output", taskID)
		}
		root = filepath.Dir(filepath.Dir(taskDir))
	}
	root, err = resolvePath(root)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize project root: %w", err)
	}
	expected, err := resolvePath(filepath.Join(root, "tasks", taskID, "output"))
	if err != nil {
		return "", "", fmt.Errorf("canonicalize expected output_dir: %w", err)
	}
	if !pathInside(root, expected) {
		return "", "", fmt.Errorf("output_dir escapes project root")
	}
	if !canonicalSamePath(output, expected) {
		return "", "", fmt.Errorf("output_dir must equal %q", expected)
	}
	return expected, root, nil
}

func manifestRole(typ domain.AssetType) string {
	if typ == domain.AssetSourceScript {
		return "primary_source"
	}
	return string(typ)
}

func validateInputIdentity(project *domain.Project, version domain.AssetVersion) error {
	if project == nil {
		if version.ProjectID != nil && version.Type != domain.AssetTopicCard {
			return fmt.Errorf("project input %q cannot be attached to a project-less task", version.ID)
		}
		return nil
	}
	if version.AccountID != project.AccountID {
		return fmt.Errorf("input %q account does not match project", version.ID)
	}
	if version.Type == domain.AssetAccountBackground || version.Type == domain.AssetTopicCard {
		return nil
	}
	if version.ProjectID == nil || *version.ProjectID != project.ID {
		return fmt.Errorf("input %q project does not match manifest project", version.ID)
	}
	return nil
}

func validateManifestIDs(manifest TaskManifest) error {
	for label, value := range map[string]string{"task_id": manifest.TaskID, "job_id": manifest.JobID, "skill_snapshot_id": manifest.SkillSnapshotID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%s must be a UUID", label)
		}
	}
	if manifest.Project != nil {
		for label, value := range map[string]string{"project.id": manifest.Project.ID, "project.account_id": manifest.Project.AccountID} {
			if _, err := uuid.Parse(value); err != nil {
				return fmt.Errorf("%s must be a UUID", label)
			}
		}
	}
	for _, input := range manifest.Inputs {
		for label, value := range map[string]string{"asset_id": input.AssetID, "version_id": input.VersionID} {
			if _, err := uuid.Parse(value); err != nil {
				return fmt.Errorf("input %s must be a UUID", label)
			}
		}
	}
	return nil
}

func validateManifestSchemaValues(manifest TaskManifest) error {
	if err := validateManifestIDs(manifest); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.OutputDir) == "" {
		return fmt.Errorf("output_dir is required")
	}
	if manifest.Inputs == nil || manifest.EngineeringInputs == nil || manifest.ExpectedOutputs == nil {
		return fmt.Errorf("manifest array fields must be present")
	}
	switch manifest.ApprovalMode {
	case "plan_then_wait", "plan_only", "auto_after_valid_plan":
	default:
		return fmt.Errorf("unsupported approval_mode %q", manifest.ApprovalMode)
	}
	for i, input := range manifest.Inputs {
		if err := validateManifestInput(input); err != nil {
			return fmt.Errorf("input %d: %w", i, err)
		}
	}
	for i, input := range manifest.EngineeringInputs {
		if input.Type != "topic_candidates" || strings.TrimSpace(input.Path) == "" {
			return fmt.Errorf("engineering input %d is invalid", i)
		}
	}
	if err := validateActionManifestContract(manifest); err != nil {
		return err
	}
	for i, output := range manifest.ExpectedOutputs {
		if strings.TrimSpace(output.Type) == "" {
			return fmt.Errorf("expected output %d type is required", i)
		}
	}
	if manifest.NonSecretSettings.MaxCodexConcurrency < 0 {
		return fmt.Errorf("max_codex_concurrency cannot be negative")
	}
	return nil
}

func validateManifestInput(input ManifestInput) error {
	if !formalAssetTypes[input.Type] {
		return fmt.Errorf("asset type %q is not formal", input.Type)
	}
	if input.Role != manifestRole(input.Type) {
		return fmt.Errorf("role %q does not match asset type %q", input.Role, input.Type)
	}
	if input.Version < 1 {
		return fmt.Errorf("version must be at least 1")
	}
	if strings.TrimSpace(input.Path) == "" {
		return fmt.Errorf("path is required")
	}
	if input.StorageKind != domain.StorageFile {
		return fmt.Errorf("storage_kind must be file")
	}
	info, err := os.Stat(input.Path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("path must name a regular file")
	}
	if input.Size != info.Size() {
		return fmt.Errorf("size does not match file")
	}
	mediaType, _, err := mime.ParseMediaType(input.MIME)
	if err != nil || !strings.Contains(mediaType, "/") {
		return fmt.Errorf("MIME %q is invalid", input.MIME)
	}
	if input.Size < 0 {
		return fmt.Errorf("size cannot be negative")
	}
	if !sha256Pattern.MatchString(input.SHA256) {
		return fmt.Errorf("sha256 must be 64 hexadecimal characters")
	}
	actual, err := hashResultFile(input.Path)
	if err != nil || !strings.EqualFold(actual, input.SHA256) {
		return fmt.Errorf("sha256 does not match file content")
	}
	return nil
}

func requiredDirectoryRoot(label, root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%s root is required", label)
	}
	resolved, err := resolvePath(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s root must be a directory", label)
	}
	return resolved, nil
}

var expectedOutputAllowlist = map[domain.TaskAction]map[string]bool{
	domain.ActionTopicBrainstorm: {"topic_candidates": true},
	domain.ActionTopicCommit:     {"topic_card": true}, domain.ActionTopicDeepen: {"topic_card": true},
	domain.ActionRemixStandard:  {"continuous_script": true, "spoken_script": true},
	domain.ActionRemixEnhanced:  {"continuous_script": true, "spoken_script": true},
	domain.ActionRemixFromTopic: {"continuous_script": true, "spoken_script": true},
	domain.ActionSpokenFormat:   {"spoken_script": true}, domain.ActionRemixReview: {},
	domain.ActionMontagePlan:    {"production_plan": true},
	domain.ActionMontageExecute: {"production_plan": true, "mix_draft": true},
}

var requiredInputRoles = map[domain.TaskAction][]string{
	domain.ActionRemixStandard: {"primary_source"}, domain.ActionRemixEnhanced: {"primary_source"},
	domain.ActionRemixFromTopic: {string(domain.AssetTopicCard)}, domain.ActionSpokenFormat: {string(domain.AssetContinuousScript)},
	domain.ActionMontagePlan:    {string(domain.AssetSpokenScript), string(domain.AssetNarration), string(domain.AssetSubtitleSRT), string(domain.AssetAccountBackground)},
	domain.ActionMontageExecute: {string(domain.AssetSpokenScript), string(domain.AssetNarration), string(domain.AssetSubtitleSRT), string(domain.AssetAccountBackground)},
}

func validateActionManifestContract(manifest TaskManifest) error {
	allowed, ok := expectedOutputAllowlist[manifest.Action]
	if !ok {
		return fmt.Errorf("unsupported manifest action %q", manifest.Action)
	}
	for _, output := range manifest.ExpectedOutputs {
		if !allowed[output.Type] {
			return fmt.Errorf("expected output %q is not allowed for action %q", output.Type, manifest.Action)
		}
	}
	s := manifest.NonSecretSettings
	switch manifest.Action {
	case domain.ActionTopicBrainstorm:
		if strings.TrimSpace(s.SessionID) == "" {
			return fmt.Errorf("session_id is required")
		}
	case domain.ActionTopicCommit:
		if strings.TrimSpace(s.SessionID) == "" || strings.TrimSpace(s.CandidateID) == "" || strings.TrimSpace(s.ObsidianVault) == "" || strings.TrimSpace(s.TopicCardsDir) == "" || len(manifest.EngineeringInputs) != 1 {
			return fmt.Errorf("topic commit requires session_id, candidate_id, and topic candidates input")
		}
	case domain.ActionTopicDeepen:
		if strings.TrimSpace(s.SessionID) == "" || len(manifest.Inputs) != 1 || manifest.Inputs[0].Type != domain.AssetTopicCard {
			return fmt.Errorf("topic deepen requires session_id and exactly one topic_card input")
		}
	case domain.ActionMontagePlan, domain.ActionMontageExecute:
		if strings.TrimSpace(s.MachineProfilePath) == "" {
			return fmt.Errorf("machine_profile_path is required")
		}
	}
	return nil
}

func optionalResolvedRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	resolved, err := resolvePath(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("managed root must be a directory")
	}
	return resolved, nil
}

func canonicalInAnyRoot(label, path string, roots ...string) (string, error) {
	var last error
	for _, root := range roots {
		if root == "" {
			continue
		}
		if got, err := canonicalContained(label, path, root); err == nil {
			return got, nil
		} else {
			last = err
		}
	}
	if last == nil {
		last = fmt.Errorf("no managed root configured")
	}
	return "", last
}

func canonicalContained(label, path, root string) (string, error) {
	got, err := resolvePath(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", label, err)
	}
	if !pathInside(root, got) {
		return "", fmt.Errorf("%s %q is outside managed root %q", label, path, root)
	}
	return got, nil
}

func pathInside(root, path string) bool {
	if filepath.Separator == '\\' {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

var secretValue = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|bearer\s+[a-z0-9._~+/-]{3,}|(?:api[_-]?key|token|password|authorization)\s*[=:?%][^\s&]+)`)
