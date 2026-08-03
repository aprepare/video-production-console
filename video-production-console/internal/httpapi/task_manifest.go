package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/skillregistry"
	"video-production-console/internal/store"
)

// TaskManifestRequest contains the action-specific values that are not assets.
// Public settings are read from the settings service and are never accepted
// wholesale from the browser.
type TaskManifestRequest struct {
	SessionID           string
	CandidateID         string
	TopicCandidatesPath string
	TopicCardPath       string
	MachineProfilePath  string
	ApprovalMode        string
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
		projects: store.NewProjectRepository(db), assets: store.NewAssetRepository(db),
		settings: settings, skills: skills,
	}
}

func (p *taskManifestPreparer) Prepare(ctx context.Context, task domain.CodexTask, req TaskManifestRequest) error {
	if task.ProjectID == nil || strings.TrimSpace(*task.ProjectID) == "" {
		return fmt.Errorf("manifest preparation requires a project")
	}
	project, err := p.projects.GetProject(ctx, *task.ProjectID)
	if err != nil {
		return fmt.Errorf("read project for manifest: %w", err)
	}
	if project.AccountID != task.AccountID {
		return fmt.Errorf("task account does not match project account")
	}
	runtime, err := p.settings.Runtime(ctx)
	if err != nil {
		return fmt.Errorf("runtime settings unavailable: %w", err)
	}
	resolved, err := codex.ResolveAction(task.Action)
	if err != nil {
		return err
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

	versions, err := p.assets.CurrentByProject(ctx, project.ID)
	if err != nil {
		return fmt.Errorf("read current project assets: %w", err)
	}
	byType := make(map[domain.AssetType]domain.AssetVersion, len(versions))
	for _, version := range versions {
		byType[version.Type] = version
	}
	inputs, err := manifestInputs(task.Action, byType, p, ctx, project.ID)
	if err != nil {
		return err
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
		MachineProfilePath: strings.TrimSpace(req.MachineProfilePath),
	}
	manifest, err := codex.BuildManifest(codex.BuildManifestInput{
		Task: task, Project: &project, Inputs: inputs, Action: task.Action,
		OutputDir:    filepath.Join(runtime.DataRoot, "projects", project.ID, "tasks", task.ID, "output"),
		ApprovalMode: req.ApprovalMode, SkillSnapshot: snapshot, NonSecretSettings: settings,
	})
	if err != nil {
		return fmt.Errorf("build task manifest: %w", err)
	}
	_, err = codex.WriteManifest(manifest, codex.ManifestRoots{
		Project:       filepath.Join(runtime.DataRoot, "projects", project.ID),
		AccountAssets: filepath.Join(runtime.DataRoot, "accounts"),
		Obsidian:      runtime.ObsidianVault, TopicCards: runtime.TopicCardsDir,
		MachineProfiles: runtime.DataRoot,
	})
	if err != nil {
		return fmt.Errorf("write task manifest: %w", err)
	}
	return nil
}

func manifestInputs(action domain.TaskAction, byType map[domain.AssetType]domain.AssetVersion, repo *taskManifestPreparer, ctx context.Context, projectID string) ([]domain.AssetVersion, error) {
	types := map[domain.TaskAction][]domain.AssetType{
		domain.ActionRemixStandard: {domain.AssetSourceScript}, domain.ActionRemixEnhanced: {domain.AssetSourceScript},
		domain.ActionRemixFromTopic: {domain.AssetTopicCard}, domain.ActionSpokenFormat: {domain.AssetContinuousScript},
		domain.ActionRemixReview:    {domain.AssetContinuousScript},
		domain.ActionMontagePlan:    {domain.AssetSpokenScript, domain.AssetNarration, domain.AssetSubtitleSRT},
		domain.ActionMontageExecute: {domain.AssetSpokenScript, domain.AssetNarration, domain.AssetSubtitleSRT},
		domain.ActionTopicDeepen:    {domain.AssetTopicCard},
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
