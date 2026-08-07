package history

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type ThreadSource interface {
	List(context.Context, int) ([]ThreadSummary, error)
	Read(context.Context, string) (ThreadDetail, error)
	Resume(context.Context, string) error
	Fork(context.Context, string) (string, error)
}

type ThreadSummary struct {
	ID, Title, Preview, Source, Model, ReasoningEffort string
	Archived, Active                                   bool
	Recency                                            time.Time
}
type ThreadDetail struct {
	ThreadSummary
	Messages []domain.ChatMessage
}

type Service struct {
	source ThreadSource
	repo   *store.ConversationRepository
	now    func() time.Time
}

func NewService(source ThreadSource, repo *store.ConversationRepository) *Service {
	return &Service{source: source, repo: repo, now: time.Now}
}

func FilterRecent(threads []ThreadSummary, owned map[string]bool, limit int) []ThreadSummary {
	if limit < 1 {
		return []ThreadSummary{}
	}
	seen := map[string]bool{}
	out := make([]ThreadSummary, 0, limit)
	for _, item := range threads {
		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.TrimSpace(item.Title)
		if item.ID == "" || item.Title == "" || seen[item.ID] || owned[item.ID] || item.Archived || strings.EqualFold(item.Source, "subagent") {
			continue
		}
		switch strings.ToLower(item.Source) {
		case "vscode":
			item.Source = "desktop"
		case "cli":
			item.Source = "cli"
		case "exec":
			item.Source = "task"
		}
		seen[item.ID] = true
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Recency.After(out[j].Recency) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Service) List(ctx context.Context, limit int, source string) ([]ThreadSummary, error) {
	if s == nil || s.source == nil || s.repo == nil {
		return nil, errors.New("history service is not configured")
	}
	if limit < 5 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}
	owned, err := s.repo.OwnedThreadIDs(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.source.List(ctx, limit*4)
	if err != nil {
		return nil, err
	}
	items = FilterRecent(items, owned, limit)
	if source == "" {
		return items, nil
	}
	out := items[:0]
	for _, item := range items {
		if item.Source == source {
			out = append(out, item)
		}
	}
	return out, nil
}
func (s *Service) Read(ctx context.Context, id string) (ThreadDetail, error) {
	if s == nil || s.source == nil {
		return ThreadDetail{}, errors.New("history service is not configured")
	}
	return s.source.Read(ctx, id)
}
func (s *Service) Resume(ctx context.Context, id string) (domain.ChatSession, error) {
	return s.mapThread(ctx, id, false)
}
func (s *Service) Fork(ctx context.Context, id string) (domain.ChatSession, error) {
	if s == nil || s.source == nil {
		return domain.ChatSession{}, errors.New("history service is not configured")
	}
	fork, err := s.source.Fork(ctx, id)
	if err != nil {
		return domain.ChatSession{}, err
	}
	return s.mapThread(ctx, fork, true)
}
func (s *Service) mapThread(ctx context.Context, id string, fork bool) (domain.ChatSession, error) {
	if s == nil || s.source == nil || s.repo == nil {
		return domain.ChatSession{}, errors.New("history service is not configured")
	}
	detail, err := s.source.Read(ctx, id)
	if err != nil {
		return domain.ChatSession{}, err
	}
	if detail.Active && !fork {
		return domain.ChatSession{}, errors.New("thread_active_elsewhere")
	}
	if !fork {
		if err := s.source.Resume(ctx, id); err != nil {
			return domain.ChatSession{}, err
		}
	}
	now := s.now().UTC()
	session := domain.ChatSession{ID: uuid.NewString(), Title: detail.Title, Source: "history", Kind: domain.ChatHistory, Status: domain.ChatIdle, CodexThreadID: &id, Model: detail.Model, ReasoningEffort: detail.ReasoningEffort, SkillNames: []string{}, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return domain.ChatSession{}, err
	}
	return session, nil
}
