package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
	"video-production-console/internal/taskmodel"
	"video-production-console/internal/workflow"
)

type workflowTaskLauncher struct {
	db        *sql.DB
	scheduler codex.Scheduler
	preparer  TaskManifestPreparer
	models    TaskModelResolver
	mu        sync.Mutex
}

func NewWorkflowTaskLauncher(db *sql.DB, scheduler codex.Scheduler, preparer TaskManifestPreparer, models TaskModelResolver) workflow.TaskLauncher {
	return &workflowTaskLauncher{db: db, scheduler: scheduler, preparer: preparer, models: models}
}

func (l *workflowTaskLauncher) LaunchTopicCommit(ctx context.Context, in workflow.LaunchTask) (domain.CodexTask, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.validate(in); err != nil {
		return domain.CodexTask{}, err
	}
	selection, err := findProjectTopicSelection(ctx, l.db, in.Project)
	if err != nil {
		return domain.CodexTask{}, err
	}
	return enqueueTopicCommit(ctx, l.db, l.scheduler, l.preparer, l.models, in.Project, selection, topicCommitLaunch{model: taskmodel.Selection{Model: in.ModelName, ReasoningEffort: in.ReasoningEffort}, now: in.Now, taskID: workflowStepTaskID(in.WorkflowID, "topic"), publish: l.scheduler.Enqueue})
}

func (l *workflowTaskLauncher) LaunchRemixFromTopicCard(ctx context.Context, in workflow.LaunchTask) (domain.CodexTask, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.validate(in); err != nil {
		return domain.CodexTask{}, err
	}
	if in.TopicCard == nil || in.TopicCard.Type != domain.AssetTopicCard || in.TopicCard.State != domain.AssetReady || in.TopicCard.ProjectID == nil || *in.TopicCard.ProjectID != in.Project.ID || in.TopicCard.AccountID != in.Project.AccountID {
		return domain.CodexTask{}, errors.New("ready project topic card is required")
	}
	sourceFeedIDs, err := topicCardSourceFeedIDs(in.TopicCard.Path)
	if err != nil {
		return domain.CodexTask{}, fmt.Errorf("read topic card sources: %w", err)
	}
	tasks := store.NewTaskRepository(l.db)
	prepareStartedAt := in.Now
	if prepareStartedAt.IsZero() {
		prepareStartedAt = time.Now().UTC()
	}
	model, err := resolveTaskModel(ctx, l.models, taskmodel.Selection{Model: in.ModelName, ReasoningEffort: in.ReasoningEffort})
	if err != nil {
		return domain.CodexTask{}, err
	}
	taskID := workflowStepTaskID(in.WorkflowID, "remix")
	if existing, readErr := tasks.Get(ctx, taskID); readErr == nil {
		if existing.Action != domain.ActionRemixFromTopic || existing.ProjectID == nil || *existing.ProjectID != in.Project.ID || existing.AccountID != in.Project.AccountID || existing.ModelName != model.Model || existing.ReasoningEffort != model.ReasoningEffort {
			return domain.CodexTask{}, errors.New("workflow remix task identity conflict")
		}
		if existing.Status != domain.TaskQueued {
			return existing, nil
		}
		if _, _, manifestErr := tasks.PreparedManifest(ctx, taskID); manifestErr == nil {
			return publishQueuedTask(ctx, l.db, existing, l.scheduler.Enqueue)
		} else if !errors.Is(manifestErr, sql.ErrNoRows) {
			return domain.CodexTask{}, manifestErr
		}
		return prepareAndPublishTask(ctx, l.db, l.preparer, existing, TaskManifestRequest{TopicCardPath: in.TopicCard.Path, SourceFeedIDs: sourceFeedIDs}, prepareStartedAt, l.scheduler.Enqueue, nil)
	} else if !errors.Is(readErr, sql.ErrNoRows) {
		return domain.CodexTask{}, readErr
	}
	now := prepareStartedAt
	projectID := in.Project.ID
	task := domain.CodexTask{
		ID: taskID, ProjectID: &projectID, AccountID: in.Project.AccountID,
		Type: "remix", SkillName: "finance-viral-remix", Action: domain.ActionRemixFromTopic,
		Status: domain.TaskQueued, PromptSnapshot: "Create a remix from the approved topic card.",
		ModelName: model.Model, ReasoningEffort: model.ReasoningEffort, CreatedAt: now,
	}
	return prepareAndPublishTask(ctx, l.db, l.preparer, task, TaskManifestRequest{TopicCardPath: in.TopicCard.Path, SourceFeedIDs: sourceFeedIDs}, prepareStartedAt, l.scheduler.Enqueue, nil)
}

func topicCardSourceFeedIDs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if !utf8.Valid(data) || !strings.HasPrefix(strings.TrimSpace(text), "---") {
		return nil, errors.New("topic card must be UTF-8 Markdown with frontmatter")
	}
	end := strings.Index(text[3:], "---")
	if end < 0 {
		return nil, errors.New("topic card frontmatter is incomplete")
	}
	frontmatter := text[3 : 3+end]
	var raw string
	for _, line := range strings.Split(frontmatter, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) == "source_refs" {
			raw = strings.TrimSpace(value)
			break
		}
	}
	if raw == "" {
		return nil, errors.New("topic card source_refs are required")
	}
	var refs []string
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, errors.New("topic card source_refs must be a JSON string array")
	}
	out := make([]string, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, err := strconv.ParseUint(ref, 10, 64); err != nil {
			continue
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil, errors.New("topic card has no usable baokuan feed IDs")
	}
	return out, nil
}

func workflowStepTaskID(workflowID, step string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("video-production-console/workflow/"+workflowID+"/"+step)).String()
}

func (l *workflowTaskLauncher) validate(in workflow.LaunchTask) error {
	if l == nil || l.db == nil || l.scheduler == nil || l.preparer == nil {
		return errors.New("workflow task launcher is unavailable")
	}
	if strings.TrimSpace(in.WorkflowID) == "" || strings.TrimSpace(in.Project.ID) == "" || strings.TrimSpace(in.Project.AccountID) == "" || strings.TrimSpace(in.ModelName) == "" || strings.TrimSpace(in.ReasoningEffort) == "" {
		return fmt.Errorf("workflow launch identity and model are required")
	}
	return nil
}
