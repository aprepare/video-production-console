package httpapi

import (
	"context"

	"video-production-console/internal/domain"
	"video-production-console/internal/taskmodel"
)

func modelKindForAction(action domain.TaskAction) string {
	switch action {
	case domain.ActionRemixSpokenLines:
		return taskmodel.KindSpokenLines
	case domain.ActionTopicBrainstorm, domain.ActionTopicCommit, domain.ActionTopicDeepen,
		domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic, domain.ActionCaptionKeywords, domain.ActionRemixReview:
		return taskmodel.KindRemix
	default:
		return taskmodel.KindCodex
	}
}

type TaskModelResolver interface {
	ResolveTaskModel(context.Context, taskmodel.Selection) (taskmodel.Selection, error)
}

type taskModelRequest struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

type defaultTaskModelResolver struct{}

func (defaultTaskModelResolver) ResolveTaskModel(_ context.Context, override taskmodel.Selection) (taskmodel.Selection, error) {
	return taskmodel.Resolve(taskmodel.Selection{Model: taskmodel.DefaultModel, ReasoningEffort: taskmodel.DefaultReasoningEffort}, override)
}

func resolveTaskModel(ctx context.Context, resolver TaskModelResolver, override taskmodel.Selection) (taskmodel.Selection, error) {
	if resolver == nil {
		resolver = defaultTaskModelResolver{}
	}
	return resolver.ResolveTaskModel(ctx, override)
}
