package domain

type TaskAction string

const (
	ActionTopicBrainstorm TaskAction = "topic.brainstorm"
	ActionTopicCommit     TaskAction = "topic.commit"
	ActionTopicDeepen     TaskAction = "topic.deepen"
	ActionRemixStandard   TaskAction = "remix.standard"
	ActionRemixEnhanced   TaskAction = "remix.enhanced"
	ActionRemixFromTopic  TaskAction = "remix.from_topic_card"
	// Deprecated: retained so historical task and audit data can be decoded.
	ActionSpokenFormat   TaskAction = "remix.spoken_format"
	ActionRemixReview    TaskAction = "remix.review"
	ActionMontagePlan    TaskAction = "montage.plan"
	ActionMontageExecute TaskAction = "montage.execute"
)

type DependencyHealth struct {
	Baokuan            bool
	ObsidianRead       bool
	ObsidianWrite      bool
	TopicConfig        *bool
	ValidCandidate     *bool
	UniqueWritableCard *bool
	Montage            bool
}

type ActionReadiness struct {
	Ready         bool
	MissingInputs []string
}

type prerequisite struct {
	name     string
	healthy  bool
	assessed bool
}

var actionAssets = map[TaskAction][]AssetType{
	ActionRemixStandard:  {AssetSourceScript},
	ActionRemixEnhanced:  {AssetSourceScript},
	ActionRemixFromTopic: {AssetTopicCard},
	ActionRemixReview:    {AssetContinuousScript},
	ActionMontagePlan:    {AssetContinuousScript, AssetNarration, AssetSubtitleSRT, AssetAccountBackground},
	ActionMontageExecute: {AssetContinuousScript, AssetNarration, AssetSubtitleSRT, AssetAccountBackground},
}

func EvaluateAction(action TaskAction, assets map[AssetType]AssetState, deps DependencyHealth) ActionReadiness {
	missing := make([]string, 0)
	requiredAssets, assetAction := actionAssets[action]
	for _, assetType := range requiredAssets {
		if assets[assetType] != AssetReady {
			missing = append(missing, string(assetType))
		}
	}

	known := assetAction
	switch action {
	case ActionTopicBrainstorm:
		known = true
		missing = append(missing, missingPrerequisites(
			knownPrerequisite("baokuan_mcp", deps.Baokuan),
			knownPrerequisite("obsidian_read", deps.ObsidianRead),
			optionalPrerequisite("topic_config", deps.TopicConfig),
		)...)
	case ActionTopicCommit:
		known = true
		missing = append(missing, missingPrerequisites(
			optionalPrerequisite("valid_candidate", deps.ValidCandidate),
			knownPrerequisite("obsidian_write", deps.ObsidianWrite),
		)...)
	case ActionTopicDeepen:
		known = true
		missing = append(missing, missingPrerequisites(
			optionalPrerequisite("unique_writable_card", deps.UniqueWritableCard),
			knownPrerequisite("baokuan_mcp", deps.Baokuan),
		)...)
	case ActionMontagePlan, ActionMontageExecute:
		if !deps.Montage {
			missing = append(missing, "montage")
		}
	}
	if !known {
		missing = append(missing, "unknown_action")
	}
	return ActionReadiness{Ready: len(missing) == 0, MissingInputs: missing}
}

func knownPrerequisite(name string, healthy bool) prerequisite {
	return prerequisite{name: name, healthy: healthy, assessed: true}
}

func optionalPrerequisite(name string, healthy *bool) prerequisite {
	return prerequisite{name: name, healthy: healthy != nil && *healthy, assessed: healthy != nil}
}

func missingPrerequisites(checks ...prerequisite) []string {
	explicit := make([]string, 0, len(checks))
	firstUnassessed := ""
	for _, check := range checks {
		if !check.assessed {
			if firstUnassessed == "" {
				firstUnassessed = check.name
			}
			continue
		}
		if !check.healthy {
			explicit = append(explicit, check.name)
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	// An unassessed prerequisite blocks readiness, but only the next one is
	// exposed after all currently known failures have been resolved.
	if firstUnassessed != "" {
		return []string{firstUnassessed}
	}
	return nil
}
