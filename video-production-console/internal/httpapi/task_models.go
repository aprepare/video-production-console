package httpapi

import (
	"context"

	"video-production-console/internal/domain"
	"video-production-console/internal/taskmodel"
)

func modelKindForAction(action domain.TaskAction) string {
	switch action {
	// 字幕关键词跟口播稿走同一个（更轻的）模型：都是机械整理，不需要二创级推理。
	case domain.ActionRemixSpokenLines, domain.ActionCaptionKeywords:
		return taskmodel.KindSpokenLines
	case domain.ActionTopicBrainstorm, domain.ActionTopicCommit, domain.ActionTopicDeepen,
		domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic, domain.ActionRemixReview:
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
