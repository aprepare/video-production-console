package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
)

type persistedTopicCandidates struct {
	SessionID  string `json:"session_id"`
	Candidates []struct {
		ID             string   `json:"id"`
		Topic          string   `json:"topic"`
		MotherTheme    string   `json:"mother_theme"`
		FamilyConflict string   `json:"family_conflict"`
		AnomalyFraming string   `json:"anomaly_framing"`
		NarrativeEntry string   `json:"narrative_entry"`
		SourceRefs     []string `json:"source_refs"`
		FragmentRefs   []string `json:"fragment_refs"`
	} `json:"candidates"`
}

type projectTopicSelection struct {
	SessionID           string
	Candidate           domain.IdeaCandidate
	TopicCandidatesPath string
}

func hydrateIdeaCandidates(ctx context.Context, db *sql.DB, candidates []domain.IdeaCandidate) []domain.IdeaCandidate {
	if db == nil || len(candidates) == 0 {
		return candidates
	}
	cache := map[string]persistedTopicCandidates{}
	for i := range candidates {
		if candidates[i].TaskID == nil || strings.TrimSpace(*candidates[i].TaskID) == "" {
			continue
		}
		taskID := *candidates[i].TaskID
		artifact, ok := cache[taskID]
		if !ok {
			path, err := topicCandidatesArtifactPath(ctx, db, taskID)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(data, &artifact) != nil {
				continue
			}
			cache[taskID] = artifact
		}
		for _, item := range artifact.Candidates {
			if item.ID != candidates[i].ID {
				continue
			}
			candidates[i].MotherTheme = item.MotherTheme
			candidates[i].FamilyConflict = item.FamilyConflict
			candidates[i].AnomalyFraming = item.AnomalyFraming
			candidates[i].NarrativeEntry = item.NarrativeEntry
			candidates[i].SourceRefs = append([]string(nil), item.SourceRefs...)
			candidates[i].FragmentRefs = append([]string(nil), item.FragmentRefs...)
			if candidates[i].Summary == "" {
				candidates[i].Summary = item.NarrativeEntry
			}
			break
		}
	}
	return candidates
}

func topicCandidatesArtifactPath(ctx context.Context, db *sql.DB, taskID string) (string, error) {
	artifacts, err := store.NewTaskRepository(db).Artifacts(ctx, taskID)
	if err != nil {
		return "", err
	}
	for _, artifact := range artifacts {
		if artifact.Kind == "topic_candidates" && strings.TrimSpace(artifact.Path) != "" {
			return artifact.Path, nil
		}
	}
	return "", fmt.Errorf("topic candidates artifact is missing")
}

func findProjectTopicSelection(ctx context.Context, db *sql.DB, project domain.Project) (projectTopicSelection, error) {
	if db == nil {
		return projectTopicSelection{}, sql.ErrNoRows
	}
	var candidate domain.IdeaCandidate
	var task sql.NullString
	var selected int
	row := db.QueryRowContext(ctx, `
		SELECT s.id,c.id,c.session_id,c.task_id,c.position,c.title,c.summary,c.score,c.source,c.selected,c.created_at
		FROM idea_sessions s
		JOIN idea_candidates c ON c.session_id=s.id
		WHERE (s.project_id=? AND c.id=s.selected_id)
		   OR (c.title=? AND (s.account_id=? OR s.account_id IS NULL))
		ORDER BY CASE WHEN s.project_id=? THEN 0 ELSE 1 END,c.created_at DESC
		LIMIT 1`, project.ID, project.Title, project.AccountID, project.ID)
	var sessionID string
	if err := row.Scan(&sessionID, &candidate.ID, &candidate.SessionID, &task, &candidate.Position, &candidate.Title, &candidate.Summary, &candidate.Score, &candidate.Source, &selected, &candidate.CreatedAt); err != nil {
		return projectTopicSelection{}, err
	}
	if task.Valid {
		candidate.TaskID = &task.String
	}
	candidate.Selected = selected != 0
	candidate = hydrateIdeaCandidates(ctx, db, []domain.IdeaCandidate{candidate})[0]
	if candidate.TaskID == nil {
		return projectTopicSelection{}, fmt.Errorf("candidate source task is missing")
	}
	path, err := topicCandidatesArtifactPath(ctx, db, *candidate.TaskID)
	if err != nil {
		return projectTopicSelection{}, err
	}
	return projectTopicSelection{SessionID: sessionID, Candidate: candidate, TopicCandidatesPath: path}, nil
}

type topicCommitLaunch struct {
	model   taskmodel.Selection
	now     time.Time
	taskID  string
	publish func(context.Context, domain.CodexTask) error
}

func enqueueTopicCommit(ctx context.Context, db *sql.DB, scheduler codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver, project domain.Project, selection projectTopicSelection, options ...topicCommitLaunch) (domain.CodexTask, error) {
	if scheduler == nil || preparer == nil {
		return domain.CodexTask{}, errors.New("topic card task service is unavailable")
	}
	requested := taskmodel.Selection{}
	now := time.Now().UTC()
	if len(options) > 0 {
		requested = options[0].model
		if !options[0].now.IsZero() {
			now = options[0].now
		}
	}
	prepareStartedAt := now
	model, err := resolveTaskModel(ctx, models, requested)
	if err != nil {
		return domain.CodexTask{}, err
	}
	tasks := store.NewTaskRepository(db)
	if len(options) > 0 && options[0].taskID != "" {
		if existing, readErr := tasks.Get(ctx, options[0].taskID); readErr == nil {
			if existing.Action != domain.ActionTopicCommit || existing.ProjectID == nil || *existing.ProjectID != project.ID || existing.AccountID != project.AccountID || existing.ModelName != model.Model || existing.ReasoningEffort != model.ReasoningEffort {
				return domain.CodexTask{}, errors.New("workflow topic task identity conflict")
			}
			if existing.Status != domain.TaskQueued {
				return existing, nil
			}
			publish := scheduler.Enqueue
			if options[0].publish != nil {
				publish = options[0].publish
			}
			if _, _, manifestErr := tasks.PreparedManifest(ctx, existing.ID); manifestErr == nil {
				return publishQueuedTask(ctx, db, existing, publish)
			} else if !errors.Is(manifestErr, sql.ErrNoRows) {
				return domain.CodexTask{}, manifestErr
			}
			prepared, err := prepareAndPublishTask(ctx, db, preparer, existing, TaskManifestRequest{SessionID: selection.SessionID, CandidateID: selection.Candidate.ID, TopicCandidatesPath: selection.TopicCandidatesPath}, prepareStartedAt, publish, nil)
			if err != nil {
				return domain.CodexTask{}, err
			}
			_ = store.NewIdeaRepository(db).LinkProject(ctx, selection.SessionID, project.ID, project.AccountID)
			return prepared, nil
		} else if !errors.Is(readErr, sql.ErrNoRows) {
			return domain.CodexTask{}, readErr
		}
	}
	existing, err := tasks.List(ctx, project.ID, "")
	if err != nil {
		return domain.CodexTask{}, err
	}
	for _, task := range existing {
		if len(options) > 0 && options[0].taskID != "" {
			break
		}
		modelMatches := requested.Model == "" || (task.ModelName == model.Model && task.ReasoningEffort == model.ReasoningEffort)
		if task.Action == domain.ActionTopicCommit && modelMatches && (task.Status == domain.TaskQueued || task.Status == domain.TaskRunning || task.Status == domain.TaskResuming || task.Status == domain.TaskAwaitingInput || task.Status == domain.TaskWaitingInput) {
			if task.Status != domain.TaskQueued {
				return task, nil
			}
			if _, _, manifestErr := tasks.PreparedManifest(ctx, task.ID); manifestErr == nil {
				return publishQueuedTask(ctx, db, task, scheduler.Enqueue)
			} else if !errors.Is(manifestErr, sql.ErrNoRows) {
				return domain.CodexTask{}, manifestErr
			}
			return prepareAndPublishTask(ctx, db, preparer, task, TaskManifestRequest{SessionID: selection.SessionID, CandidateID: selection.Candidate.ID, TopicCandidatesPath: selection.TopicCandidatesPath}, prepareStartedAt, scheduler.Enqueue, nil)
		}
	}
	taskID := uuid.NewString()
	if len(options) > 0 && options[0].taskID != "" {
		taskID = options[0].taskID
	}
	projectID := project.ID
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: project.AccountID,
		Type: "topic_commit", SkillName: "finance-topic-selector", Action: domain.ActionTopicCommit,
		Status: domain.TaskQueued, PromptSnapshot: "将已确认候选写入 Obsidian 正式选题卡。",
		ModelName: model.Model, ReasoningEffort: model.ReasoningEffort, CreatedAt: now,
	}
	publish := scheduler.Enqueue
	if len(options) > 0 && options[0].publish != nil {
		publish = options[0].publish
	}
	prepared, err := prepareAndPublishTask(ctx, db, preparer, task, TaskManifestRequest{SessionID: selection.SessionID, CandidateID: selection.Candidate.ID, TopicCandidatesPath: selection.TopicCandidatesPath}, prepareStartedAt, publish, nil)
	if err != nil {
		return domain.CodexTask{}, err
	}
	_ = store.NewIdeaRepository(db).LinkProject(ctx, selection.SessionID, project.ID, project.AccountID)
	message := domain.IdeaMessage{
		ID: uuid.NewString(), SessionID: selection.SessionID, TaskID: &taskID,
		Role: "user", Content: "已确认候选并创建项目：" + selection.Candidate.Title, CreatedAt: now,
	}
	_ = store.NewIdeaRepository(db).AddMessage(ctx, message)
	return prepared, nil
}
