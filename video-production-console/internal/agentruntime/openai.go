package agentruntime

import "video-production-console/internal/domain"

// OpenAIAdapter is the OpenAI-compatible chat/tool runtime for remix/topic.
type OpenAIAdapter struct{}

func (OpenAIAdapter) Name() RuntimeName { return RuntimeOpenAI }

func (OpenAIAdapter) Supports(action domain.TaskAction) bool {
	switch action {
	case domain.ActionTopicBrainstorm, domain.ActionTopicCommit, domain.ActionTopicDeepen,
		domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic, domain.ActionRemixReview:
		return true
	default:
		return false
	}
}
