package agentruntime

import "video-production-console/internal/domain"

// PiAdapter is the opt-in Pi coding-agent runtime for remix/topic.
type PiAdapter struct{}

func (PiAdapter) Name() RuntimeName { return RuntimePi }

func (PiAdapter) Supports(action domain.TaskAction) bool {
	switch action {
	case domain.ActionTopicBrainstorm, domain.ActionTopicCommit, domain.ActionTopicDeepen,
		domain.ActionRemixStandard, domain.ActionRemixEnhanced, domain.ActionRemixFromTopic, domain.ActionRemixReview:
		return true
	default:
		return false
	}
}
