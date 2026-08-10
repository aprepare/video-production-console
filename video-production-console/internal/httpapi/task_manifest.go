package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/montage"
	"video-production-console/internal/publishing"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
)

// TaskManifestRequest contains the action-specific values that are not assets.
// Public settings are read from the settings service and are never accepted
// wholesale from the browser.
type TaskManifestRequest struct {
	SessionID            string    `json:"session_id,omitempty"`
	CandidateID          string    `json:"candidate_id,omitempty"`
	SourceVersionID      string    `json:"source_version_id,omitempty"`
	TopicCandidatesPath  string    `json:"topic_candidates_path,omitempty"`
	TopicCardPath        string    `json:"topic_card_path,omitempty"`
	MachineProfilePath   string    `json:"machine_profile_path,omitempty"`
	ApprovalMode         string    `json:"approval_mode,omitempty"`
	PreparationStartedAt time.Time `json:"-"`
}

// TaskManifestPreparer is called before a task is handed to the scheduler.
// Implementations must leave the task unqueued when preparation fails.
type TaskManifestPreparer interface {
	Prepare(context.Context, domain.CodexTask, TaskManifestRequest) error
}

type runtimeSettingsProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

type skillSnapshotProvider interface {
	Latest(context.Context, string) (domain.SkillSnapshot, error)
}

type taskManifestPreparer struct {
	db       *sql.DB
	projects *store.ProjectRepository
	assets   *store.AssetRepository
	settings runtimeSettingsProvider
	skills   skillSnapshotProvider
}

// NewTaskManifestPreparer wires the database-backed V2 manifest preparation
// used by the HTTP task endpoint. A nil preparer preserves legacy callers of
// NewTasksHandler, while the production app supplies all three dependencies.
func NewTaskManifestPreparer(db *sql.DB, settings runtimeSettingsProvider, skills skillSnapshotProvider) TaskManifestPreparer {
	if db == nil || settings == nil || skills == nil {
		return nil
	}
	return &taskManifestPreparer{
		db: db, projects: store.NewProjectRepository(db), assets: store.NewAssetRepository(db),
		settings: settings, skills: skills,
	}
}

func (p *taskManifestPreparer) Prepare(ctx context.Context, task domain.CodexTask, req TaskManifestRequest) error {
	runtime, err := p.settings.Runtime(ctx)
	if err != nil {
		return fmt.Errorf("runtime settings unavailable: %w", err)
	}
	resolved, err := codex.ResolveAction(task.Action)
	if err != nil {
		return err
	}
	var project domain.Project
	var projectPtr *domain.Project
	if task.ProjectID != nil && strings.TrimSpace(*task.ProjectID) != "" {
		project, err = p.projects.GetProject(ctx, *task.ProjectID)
		if err != nil {
			return fmt.Errorf("read project for manifest: %w", err)
		}
		if project.AccountID != task.AccountID {
			return fmt.Errorf("task account does not match project account")
		}
		projectPtr = &project
	} else if task.Action != domain.ActionTopicBrainstorm {
		return fmt.Errorf("manifest preparation requires a project")
	}
	snapshot, err := p.skills.Latest(ctx, resolved.Skill)
	if err != nil {
		if errors.Is(err, skillregistry.ErrSkillNotFound) || errors.Is(err, store.ErrSkillSnapshotNotFound) {
			return fmt.Errorf("skill snapshot %q is not configured: %w", resolved.Skill, err)
		}
		return fmt.Errorf("read skill snapshot %q: %w", resolved.Skill, err)
	}
	if strings.TrimSpace(snapshot.ID) == "" || snapshot.Name != resolved.Skill {
		return fmt.Errorf("skill snapshot %q is invalid", resolved.Skill)
	}

	var inputs []domain.AssetVersion
	if projectPtr != nil {
		versions, readErr := p.assets.CurrentByProject(ctx, project.ID)
		if readErr != nil {
			return fmt.Errorf("read current project assets: %w", readErr)
		}
		byType := make(map[domain.AssetType]domain.AssetVersion, len(versions))
		for _, version := range versions {
			version.Path = resolveStoredAssetPath(runtime.DataRoot, version.Path)
			byType[version.Type] = version
		}
		inputs, err = manifestInputs(task.Action, byType, p, ctx, project.ID, req.SourceVersionID)
		if err != nil {
			return err
		}
		// Account backgrounds are loaded through the account repository rather
		// than CurrentByProject, so they must pass through the same legacy-path
		// normalization as project assets. Older versions stored paths relative
		// to the data-root parent (for example, "video-console-data\\accounts\\...").
		for i := range inputs {
			inputs[i].Path = resolveStoredAssetPath(runtime.DataRoot, inputs[i].Path)
			if inputs[i].Type == domain.AssetAccountBackground {
				info, statErr := os.Stat(inputs[i].Path)
				if statErr != nil || !info.Mode().IsRegular() {
					return fmt.Errorf("required asset %q is missing or not a regular file; re-upload the account background image", domain.AssetAccountBackground)
				}
			}
		}
	}
	machineProfilePath := strings.TrimSpace(req.MachineProfilePath)
	if machineProfilePath == "" && (task.Action == domain.ActionMontagePlan || task.Action == domain.ActionMontageExecute) {
		machineProfilePath = strings.TrimSpace(runtime.MachineProfilePath)
	}
	settings := codex.ManifestSettings{
		ListenAddr: runtime.ListenAddr, DataRoot: runtime.DataRoot,
		MaxCodexConcurrency: runtime.MaxCodexConcurrency,
		BaokuanBaseURL:      runtime.BaokuanBaseURL, BaokuanMCPExecutable: runtime.BaokuanMCPExecutable,
		ObsidianVault: runtime.ObsidianVault, TopicCardsDir: runtime.TopicCardsDir,
		GrokBaseURL: runtime.GrokBaseURL, GrokModel: runtime.GrokModel,
		CodexBinaryPath: runtime.CodexBinaryPath, MediaIndexPath: runtime.MediaIndexPath,
		MediaRoot: runtime.MediaRoot, JianyingRoot: runtime.JianyingRoot,
		SessionID: strings.TrimSpace(req.SessionID), CandidateID: strings.TrimSpace(req.CandidateID),
		TopicCandidatesPath: strings.TrimSpace(req.TopicCandidatesPath), TopicCardPath: strings.TrimSpace(req.TopicCardPath),
		MachineProfilePath: machineProfilePath,
	}
	if task.Action == domain.ActionMontageExecute {
		settings.DraftDisplayName, err = p.resolveDraftDisplayName(ctx, task, project)
		if err != nil {
			return fmt.Errorf("resolve draft display name: %w", err)
		}
	}
	// Project-less planning tasks use their task ID as the managed root; this
	// matches the scheduler's projectIDForTask fallback and keeps the manifest
	// discoverable by the Codex command factory.
	projectRoot := filepath.Join(runtime.DataRoot, "projects", task.ID)
	if projectPtr != nil {
		projectRoot = filepath.Join(runtime.DataRoot, "projects", project.ID)
	}
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		return fmt.Errorf("create manifest project root: %w", err)
	}
	if task.Action == domain.ActionTopicCommit {
		settings.TopicCandidatesPath, err = snapshotTopicCandidatesInput(runtime.DataRoot, projectRoot, task.ID, req.TopicCandidatesPath)
		if err != nil {
			return fmt.Errorf("snapshot topic candidates input: %w", err)
		}
	}
	accountAssetsRoot := filepath.Join(runtime.DataRoot, "accounts")
	if err := os.MkdirAll(accountAssetsRoot, 0o700); err != nil {
		return fmt.Errorf("create account-assets root: %w", err)
	}
	manifest, err := codex.BuildManifest(codex.BuildManifestInput{
		Task: task, Project: projectPtr, Inputs: inputs, Action: task.Action,
		OutputDir:    filepath.Join(projectRoot, "tasks", task.ID, "output"),
		ApprovalMode: req.ApprovalMode, SkillSnapshot: snapshot, NonSecretSettings: settings,
	})
	if err != nil {
		return fmt.Errorf("build task manifest: %w", err)
	}
	manifestPath, err := codex.WriteManifest(manifest, codex.ManifestRoots{
		Project:       projectRoot,
		AccountAssets: accountAssetsRoot,
		Obsidian:      runtime.ObsidianVault, TopicCards: runtime.TopicCardsDir,
		MachineProfiles: runtime.DataRoot,
	})
	if err != nil {
		return fmt.Errorf("write task manifest: %w", err)
	}
	// Formal Skills require the manifest path from a task-specific environment
	// variable. Keep their persisted prompt aligned with the CLI runner rather
	// than embedding a path that the Skill contract deliberately rejects.
	task.PromptSnapshot, err = codex.BuildManifestPrompt(manifest, manifestPath)
	if err != nil {
		return fmt.Errorf("build task prompt: %w", err)
	}
	// The HTTP task endpoints prepare the manifest before calling Scheduler.Enqueue.
	// Persist the task only after all validation and file writes have succeeded;
	// Scheduler.Enqueue then finds this row and only signals the worker. This
	// avoids both the old "no rows" callback failure and a worker racing an
	// incomplete manifest.
	if p.db != nil {
		tasks := store.NewTaskRepository(p.db)
		task.ChatSessionID, task.CodexThreadID, task.CodexTurnID = nil, nil, nil
		task.Transport, task.CompletionPhase = codex.TransportLegacyExec, ""
		preparationStartedAt := req.PreparationStartedAt
		if preparationStartedAt.IsZero() {
			preparationStartedAt = task.CreatedAt
		}
		if preparationStartedAt.IsZero() {
			preparationStartedAt = time.Now().UTC()
		}
		if _, err := tasks.EnsurePreparedTaskAt(ctx, task, snapshot.ID, manifestPath, preparationStartedAt); err != nil {
			return fmt.Errorf("persist prepared task: %w", err)
		}
	}
	return nil
}

func (p *taskManifestPreparer) resolveDraftDisplayName(ctx context.Context, task domain.CodexTask, project domain.Project) (string, error) {
	if p.db == nil {
		return "", fmt.Errorf("task database is unavailable")
	}
	account, err := store.NewAccountRepository(p.db).Get(ctx, project.AccountID)
	if err != nil {
		return "", fmt.Errorf("read account: %w", err)
	}
	shortTitle := ""
	tasks := store.NewTaskRepository(p.db)
	completed, err := tasks.CompletedByProject(ctx, project.ID)
	if err != nil {
		return "", fmt.Errorf("list completed project tasks: %w", err)
	}
	for _, completedTask := range completed {
		if !isRemixAction(completedTask.Action) {
			continue
		}
		artifacts, readErr := tasks.Artifacts(ctx, completedTask.ID)
		if readErr != nil {
			return "", fmt.Errorf("read publishing artifacts: %w", readErr)
		}
		for _, artifact := range artifacts {
			if artifact.Kind != "publishing_package" {
				continue
			}
			packageView, packageErr := (publishing.Reader{}).Read(artifact.Path, artifact.SHA256)
			if packageErr == nil && len(packageView.ShortTitles) > 0 {
				shortTitle = strings.TrimSpace(packageView.ShortTitles[0])
			}
			break
		}
		break
	}
	return montage.BuildDraftDisplayName(account.Name, project.Title, shortTitle, task.ID), nil
}

// resolveStoredAssetPath keeps assets created by older console versions usable.
// Those versions stored paths such as "video-console-data\\accounts\\..."
// relative to the process working directory. Resolve them against the configured
// data root's parent so task creation no longer depends on where the executable
// happened to be started.
func resolveStoredAssetPath(dataRoot, storedPath string) string {
	storedPath = strings.TrimSpace(storedPath)
	if storedPath == "" || filepath.IsAbs(storedPath) {
		return storedPath
	}
	dataRoot = filepath.Clean(dataRoot)
	storedPath = filepath.Clean(storedPath)
	parts := strings.Split(storedPath, string(filepath.Separator))
	if len(parts) > 0 && strings.EqualFold(parts[0], filepath.Base(dataRoot)) {
		return filepath.Join(filepath.Dir(dataRoot), storedPath)
	}
	return filepath.Join(dataRoot, storedPath)
}

const maxTopicCandidatesInputSize int64 = 8 << 20

func snapshotTopicCandidatesInput(dataRoot, projectRoot, taskID, sourcePath string) (string, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return "", fmt.Errorf("topic candidates path is required")
	}
	managedProjectsRoot, err := filepath.Abs(filepath.Join(dataRoot, "projects"))
	if err != nil {
		return "", fmt.Errorf("resolve managed projects root: %w", err)
	}
	managedProjectsRoot, err = filepath.EvalSymlinks(managedProjectsRoot)
	if err != nil {
		return "", fmt.Errorf("canonicalize managed projects root: %w", err)
	}
	resolvedSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve topic candidates source: %w", err)
	}
	resolvedSource, err = filepath.EvalSymlinks(resolvedSource)
	if err != nil {
		return "", fmt.Errorf("canonicalize topic candidates source: %w", err)
	}
	relative, err := filepath.Rel(managedProjectsRoot, resolvedSource)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("topic candidates source is outside managed projects root")
	}
	source, err := os.Open(resolvedSource)
	if err != nil {
		return "", fmt.Errorf("open topic candidates source: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect topic candidates source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("topic candidates source must be a regular file")
	}
	if info.Size() > maxTopicCandidatesInputSize {
		return "", fmt.Errorf("topic candidates source exceeds %d bytes", maxTopicCandidatesInputSize)
	}

	inputDir := filepath.Join(projectRoot, "tasks", taskID, "input")
	if err := os.MkdirAll(inputDir, 0o700); err != nil {
		return "", fmt.Errorf("create topic candidates input directory: %w", err)
	}
	temporary, err := os.CreateTemp(inputDir, ".topic-candidates-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create topic candidates snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure topic candidates snapshot: %w", err)
	}
	written, err := io.Copy(temporary, io.LimitReader(source, maxTopicCandidatesInputSize+1))
	if err != nil {
		return "", fmt.Errorf("copy topic candidates snapshot: %w", err)
	}
	if written > maxTopicCandidatesInputSize {
		return "", fmt.Errorf("topic candidates source exceeds %d bytes", maxTopicCandidatesInputSize)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync topic candidates snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close topic candidates snapshot: %w", err)
	}
	destination := filepath.Join(inputDir, "topic_candidates.json")
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("replace topic candidates snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", fmt.Errorf("publish topic candidates snapshot: %w", err)
	}
	keepTemporary = false
	return destination, nil
}

func manifestInputs(action domain.TaskAction, byType map[domain.AssetType]domain.AssetVersion, repo *taskManifestPreparer, ctx context.Context, projectID, sourceVersionID string) ([]domain.AssetVersion, error) {
	types := map[domain.TaskAction][]domain.AssetType{
		domain.ActionRemixStandard: {domain.AssetSourceScript}, domain.ActionRemixEnhanced: {domain.AssetSourceScript},
		domain.ActionRemixFromTopic: {domain.AssetTopicCard},
		domain.ActionRemixReview:    {domain.AssetContinuousScript},
		domain.ActionMontagePlan:    {domain.AssetContinuousScript, domain.AssetNarration, domain.AssetSubtitleSRT},
		domain.ActionMontageExecute: {domain.AssetContinuousScript, domain.AssetNarration, domain.AssetSubtitleSRT},
		domain.ActionTopicDeepen:    {domain.AssetTopicCard},
	}
	if (action == domain.ActionRemixStandard || action == domain.ActionRemixEnhanced) && strings.TrimSpace(sourceVersionID) != "" {
		version, err := repo.assets.Version(ctx, strings.TrimSpace(sourceVersionID))
		if err != nil {
			return nil, fmt.Errorf("requested source version is unavailable: %w", err)
		}
		if version.ProjectID == nil || *version.ProjectID != projectID {
			return nil, fmt.Errorf("requested source version does not belong to this project")
		}
		if version.Type != domain.AssetSourceScript {
			return nil, fmt.Errorf("requested source version is not a source script")
		}
		if version.State != domain.AssetReady {
			return nil, fmt.Errorf("requested source version is not ready")
		}
		current, ok := byType[domain.AssetSourceScript]
		if !ok || current.ID != version.ID {
			return nil, fmt.Errorf("requested source version is no longer current")
		}
		byType[domain.AssetSourceScript] = version
	}
	want := types[action]
	inputs := make([]domain.AssetVersion, 0, len(want)+1)
	for _, typ := range want {
		version, ok := byType[typ]
		// Review permits either a source script or an approved continuous script.
		if action == domain.ActionRemixReview && typ == domain.AssetContinuousScript && !ok {
			version, ok = byType[domain.AssetSourceScript]
		}
		if !ok || version.State != domain.AssetReady {
			return nil, fmt.Errorf("required asset %q is missing or not ready", typ)
		}
		inputs = append(inputs, version)
	}
	if action == domain.ActionMontagePlan || action == domain.ActionMontageExecute {
		background, err := repo.assets.CurrentBackgroundForProject(ctx, projectID)
		if err != nil {
			return nil, fmt.Errorf("required asset %q is missing or not ready: %w", domain.AssetAccountBackground, err)
		}
		inputs = append(inputs, background)
	}
	return inputs, nil
}
