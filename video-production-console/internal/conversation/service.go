package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/codexapp"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

// ThreadRPC is deliberately separate from Broker RPC because a session may be
// created before it has a turn. The browser never supplies thread identifiers.
type ThreadRPC interface {
	Call(context.Context, string, any, any) error
}

type CreateSessionInput struct {
	Kind                                                    domain.ChatKind
	Title, WorkingDirectory, Model, ReasoningEffort, Source string
	SkillNames                                              []string
	ProjectID, IdeaSessionID                                *string
}

type Service struct {
	repo                    *store.ConversationRepository
	broker                  *Broker
	rpc                     ThreadRPC
	roots                   []string
	knownSkills             map[string]struct{}
	dataRoot                string
	desktopWorkingDirectory string
	ensureMu                sync.Mutex
}

type ServiceOptions struct {
	DataRoot                string
	DesktopWorkingDirectory string
}

func NewService(repo *store.ConversationRepository, broker *Broker, rpc ThreadRPC, roots []string, skillNames []string, optionValues ...ServiceOptions) *Service {
	known := make(map[string]struct{}, len(skillNames))
	for _, name := range skillNames {
		if name = strings.TrimSpace(name); name != "" {
			known[name] = struct{}{}
		}
	}
	options := ServiceOptions{}
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	service := &Service{repo: repo, broker: broker, rpc: rpc, roots: canonicalRoots(roots), knownSkills: known, dataRoot: strings.TrimSpace(options.DataRoot), desktopWorkingDirectory: strings.TrimSpace(options.DesktopWorkingDirectory)}
	if repo != nil && rpc != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		service.retryThreadCleanup(cleanupCtx)
		cancel()
	}
	return service
}

// EnsureProjectMainSession lazily creates the database-unique main project
// session and reuses it for every formal project task.
func (s *Service) EnsureProjectMainSession(ctx context.Context, projectID string) (domain.ChatSession, error) {
	if s == nil || s.repo == nil || s.rpc == nil {
		return domain.ChatSession{}, errors.New("conversation service is not configured")
	}
	if _, err := uuid.Parse(projectID); err != nil {
		return domain.ChatSession{}, errors.New("project id must be a UUID")
	}
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	s.retryThreadCleanup(ctx)
	workingDirectory, err := s.projectWorkingDirectory(projectID)
	if err != nil {
		return domain.ChatSession{}, err
	}
	if session, err := s.repo.ResolveProjectMainSession(ctx, projectID); err == nil {
		if session.CodexThreadID != nil && strings.TrimSpace(*session.CodexThreadID) != "" && samePath(session.WorkingDirectory, workingDirectory) {
			exists, verifyErr := s.threadExists(ctx, *session.CodexThreadID)
			if verifyErr != nil {
				return domain.ChatSession{}, fmt.Errorf("verify project main Codex thread: %w", verifyErr)
			}
			if exists {
				return session, nil
			}
		}
		if err := s.repo.DemoteProjectMainSession(ctx, session.ID); err != nil {
			return domain.ChatSession{}, fmt.Errorf("demote unusable project main session: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.ChatSession{}, err
	}
	session, err := s.createSession(ctx, CreateSessionInput{Kind: domain.ChatProject, Title: "Project " + projectID, ProjectID: &projectID, WorkingDirectory: workingDirectory})
	if err == nil {
		return session, nil
	}
	// Another process may have won the partial unique index after both had
	// started a thread. Formal tasks always bind the durable winner; the losing
	// thread identifier is never persisted or selected.
	winner, winnerErr := s.repo.ResolveProjectMainSession(ctx, projectID)
	if winnerErr == nil && winner.CodexThreadID != nil && strings.TrimSpace(*winner.CodexThreadID) != "" && samePath(winner.WorkingDirectory, workingDirectory) {
		return winner, nil
	}
	return domain.ChatSession{}, err
}

// ResolveProjectMainSession is the scheduler-facing name for the lazy ensure
// operation.
func (s *Service) ResolveProjectMainSession(ctx context.Context, projectID string) (domain.ChatSession, error) {
	return s.EnsureProjectMainSession(ctx, projectID)
}

func (s *Service) projectWorkingDirectory(projectID string) (string, error) {
	if s.dataRoot == "" || !filepath.IsAbs(s.dataRoot) {
		return "", errors.New("conversation data root is not configured")
	}
	directory := filepath.Clean(filepath.Join(s.dataRoot, "projects", projectID))
	allowed := false
	for _, root := range s.roots {
		if within(root, directory) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("project working directory is outside configured Codex workspace roots")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create project working directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || !samePath(directory, resolved) {
		return "", errors.New("project working directory is not canonical")
	}
	return directory, nil
}

// threadExists prevents a persisted console session from pointing at a thread
// that was removed by a Codex App Server restart or desktop-side cleanup.
func (s *Service) threadExists(ctx context.Context, threadID string) (bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, nil
	}
	var result struct{}
	err := s.rpc.Call(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, &result)
	if err == nil {
		return true, nil
	}
	if isMissingThread(err) {
		return false, nil
	}
	return false, err
}

func isMissingThread(err error) bool {
	var rpcErr *codexapp.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32600 {
		return strings.Contains(strings.ToLower(rpcErr.Message), "thread not found")
	}
	return strings.Contains(strings.ToLower(err.Error()), "thread not found")
}

func managedThreadStartParams(workingDirectory string) map[string]any {
	return map[string]any{
		"cwd":            workingDirectory,
		"sandbox":        "workspace-write",
		"approvalPolicy": "never",
	}
}

func managedTurnSandboxPolicy(workingDirectory string) map[string]any {
	policy := map[string]any{"type": "workspaceWrite"}
	if workingDirectory = strings.TrimSpace(workingDirectory); workingDirectory != "" {
		policy["writableRoots"] = []string{workingDirectory}
	}
	return policy
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func (s *Service) List(ctx context.Context) ([]domain.ChatSession, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("conversation service is not configured")
	}
	return s.repo.ListSessions(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (domain.ChatSession, []domain.ChatMessage, error) {
	if s == nil || s.repo == nil {
		return domain.ChatSession{}, nil, errors.New("conversation service is not configured")
	}
	session, err := s.repo.GetSession(ctx, id)
	if err != nil {
		return domain.ChatSession{}, nil, err
	}
	messages, err := s.repo.ListMessages(ctx, id)
	return session, messages, err
}

func (s *Service) Events(ctx context.Context, id string, after int64, limit int) ([]domain.SemanticEvent, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("conversation service is not configured")
	}
	if _, err := s.repo.GetSession(ctx, id); err != nil {
		return nil, err
	}
	return s.repo.SemanticForSession(ctx, id, after, limit)
}

func (s *Service) Create(ctx context.Context, in CreateSessionInput) (domain.ChatSession, error) {
	if in.Kind == domain.ChatProject {
		if in.ProjectID == nil {
			return domain.ChatSession{}, errors.New("project conversation requires a project id")
		}
		return s.EnsureProjectMainSession(ctx, *in.ProjectID)
	}
	return s.createSession(ctx, in)
}

func (s *Service) createSession(ctx context.Context, in CreateSessionInput) (domain.ChatSession, error) {
	if s == nil || s.repo == nil || s.rpc == nil {
		return domain.ChatSession{}, errors.New("conversation service is not configured")
	}
	s.retryThreadCleanup(ctx)
	if in.Kind == "" {
		in.Kind = domain.ChatGeneral
	}
	if !validChatKind(in.Kind) {
		return domain.ChatSession{}, fmt.Errorf("unsupported conversation kind %q", in.Kind)
	}
	var workingDirectory string
	var err error
	if in.Kind == domain.ChatProject {
		if in.ProjectID == nil {
			return domain.ChatSession{}, errors.New("project conversation requires a project id")
		}
		if _, parseErr := uuid.Parse(*in.ProjectID); parseErr != nil {
			return domain.ChatSession{}, errors.New("project id must be a UUID")
		}
		workingDirectory, err = s.projectWorkingDirectory(*in.ProjectID)
	} else {
		if in.Source == "desktop" && strings.TrimSpace(s.desktopWorkingDirectory) != "" {
			workingDirectory, err = s.resolveWorkingDirectory(in.Kind, s.desktopWorkingDirectory)
		} else {
			workingDirectory, err = s.resolveWorkingDirectory(in.Kind, in.WorkingDirectory)
		}
	}
	if err != nil {
		return domain.ChatSession{}, err
	}
	for _, skill := range in.SkillNames {
		if _, ok := s.knownSkills[skill]; !ok {
			return domain.ChatSession{}, fmt.Errorf("Skill %q is not registered", skill)
		}
	}
	if in.Title = strings.TrimSpace(in.Title); in.Title == "" {
		in.Title = "New Codex conversation"
	}
	result := struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}{}
	threadParams := managedThreadStartParams(workingDirectory)
	if source := strings.TrimSpace(in.Source); source == "desktop" {
		// Codex Desktop's default history view includes VS Code-originated
		// interactive threads. Marking this explicitly keeps the two entry
		// points interoperable across App Server versions.
		threadParams["threadSource"] = "vscode"
	}
	if err := s.rpc.Call(ctx, "thread/start", threadParams, &result); err != nil {
		return domain.ChatSession{}, fmt.Errorf("start Codex thread: %w", err)
	}
	threadID := strings.TrimSpace(result.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(result.Thread.ID)
	}
	if threadID == "" {
		return domain.ChatSession{}, errors.New("Codex did not return a thread ID")
	}
	now := time.Now().UTC()
	source := strings.TrimSpace(in.Source)
	if source != "desktop" {
		source = "console"
	}
	session := domain.ChatSession{ID: uuid.NewString(), Title: in.Title, Source: source, Kind: in.Kind, Status: domain.ChatIdle, ProjectID: in.ProjectID, IdeaSessionID: in.IdeaSessionID, CodexThreadID: &threadID, WorkingDirectory: workingDirectory, Model: strings.TrimSpace(in.Model), ReasoningEffort: strings.TrimSpace(in.ReasoningEffort), SkillNames: append([]string(nil), in.SkillNames...), CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		s.cleanupOrphanThread(ctx, threadID, "session_persist_failed")
		return domain.ChatSession{}, fmt.Errorf("persist conversation: %w", err)
	}
	return session, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if s == nil || s.repo == nil {
		return errors.New("conversation service is not configured")
	}
	return s.repo.DeleteMapping(ctx, id)
}

func (s *Service) Send(ctx context.Context, input SendInput) (SendReceipt, error) {
	if s == nil || s.broker == nil {
		return SendReceipt{}, errors.New("conversation delivery is not configured")
	}
	return s.broker.Send(ctx, input)
}

func (s *Service) Fork(ctx context.Context, id string) (domain.ChatSession, error) {
	if s == nil || s.repo == nil || s.rpc == nil {
		return domain.ChatSession{}, errors.New("conversation service is not configured")
	}
	s.retryThreadCleanup(ctx)
	source, err := s.repo.GetSession(ctx, id)
	if err != nil {
		return domain.ChatSession{}, err
	}
	if source.CodexThreadID == nil || *source.CodexThreadID == "" {
		return domain.ChatSession{}, errors.New("conversation has no Codex thread")
	}
	result := struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}{}
	if err := s.rpc.Call(ctx, "thread/fork", map[string]any{"threadId": *source.CodexThreadID}, &result); err != nil {
		return domain.ChatSession{}, fmt.Errorf("fork Codex thread: %w", err)
	}
	threadID := strings.TrimSpace(result.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(result.Thread.ID)
	}
	if threadID == "" {
		return domain.ChatSession{}, errors.New("Codex did not return a forked thread ID")
	}
	now := time.Now().UTC()
	fork := source
	fork.ID = uuid.NewString()
	fork.Title = source.Title + " (fork)"
	fork.Source = "console_fork"
	fork.CodexThreadID = &threadID
	fork.Status = domain.ChatIdle
	fork.CreatedAt = now
	fork.UpdatedAt = now
	if err := s.repo.CreateSession(ctx, fork); err != nil {
		s.cleanupOrphanThread(ctx, threadID, "fork_persist_failed")
		return domain.ChatSession{}, err
	}
	return fork, nil
}

func (s *Service) cleanupOrphanThread(ctx context.Context, threadID, reason string) {
	if owned, err := s.repo.OwnedThreadIDs(ctx); err == nil && owned[threadID] {
		return
	}
	err := s.rpc.Call(ctx, "thread/archive", map[string]any{"threadId": threadID}, &struct{}{})
	if err != nil {
		_ = s.repo.RecordThreadCleanup(context.WithoutCancel(ctx), threadID, reason, err)
	}
}

func (s *Service) retryThreadCleanup(ctx context.Context) {
	items, err := s.repo.PendingThreadCleanup(ctx, 20)
	if err != nil {
		return
	}
	owned, _ := s.repo.OwnedThreadIDs(ctx)
	for _, item := range items {
		if owned[item.ThreadID] {
			_ = s.repo.CompleteThreadCleanup(context.WithoutCancel(ctx), item.ThreadID)
			continue
		}
		err := s.rpc.Call(ctx, "thread/archive", map[string]any{"threadId": item.ThreadID}, &struct{}{})
		if err == nil {
			_ = s.repo.CompleteThreadCleanup(context.WithoutCancel(ctx), item.ThreadID)
			continue
		}
		_ = s.repo.RecordThreadCleanup(context.WithoutCancel(ctx), item.ThreadID, item.Reason, err)
	}
}

func (s *Service) resolveWorkingDirectory(kind domain.ChatKind, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && kind == domain.ChatGeneral && len(s.roots) > 0 {
		return s.roots[0], nil
	}
	if value == "" {
		return "", errors.New("working directory is required")
	}
	value = filepath.Clean(value)
	for _, root := range s.roots {
		if within(root, value) {
			return value, nil
		}
	}
	return "", errors.New("working directory is outside configured Codex workspace roots")
}

func canonicalRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if root = strings.TrimSpace(root); root != "" && filepath.IsAbs(root) {
			out = append(out, filepath.Clean(root))
		}
	}
	return out
}
func within(root, value string) bool {
	relative, err := filepath.Rel(root, value)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func validChatKind(kind domain.ChatKind) bool {
	return kind == domain.ChatGeneral || kind == domain.ChatIdea || kind == domain.ChatProject || kind == domain.ChatHistory
}
