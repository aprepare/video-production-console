package domain

import (
	"slices"
	"testing"
)

func TestEvaluateActionRequirements(t *testing.T) {
	ready := func(types ...AssetType) map[AssetType]AssetState {
		out := map[AssetType]AssetState{}
		for _, typ := range types {
			out[typ] = AssetReady
		}
		return out
	}
	tests := []struct {
		name        string
		action      TaskAction
		assets      map[AssetType]AssetState
		deps        DependencyHealth
		wantReady   bool
		wantMissing []string
	}{
		{"empty remix", ActionRemixStandard, ready(), DependencyHealth{}, false, []string{"source_script"}},
		{"direct remix", ActionRemixStandard, ready(AssetSourceScript), DependencyHealth{}, true, nil},
		{"spoken", ActionSpokenFormat, ready(AssetContinuousScript), DependencyHealth{}, true, nil},
		{"montage missing", ActionMontagePlan, ready(AssetContinuousScript), DependencyHealth{Montage: true}, false, []string{"narration", "subtitle_srt", "account_background"}},
		{"montage ready", ActionMontagePlan, ready(AssetContinuousScript, AssetNarration, AssetSubtitleSRT, AssetAccountBackground), DependencyHealth{Montage: true}, true, nil},
		{"topic deps", ActionTopicBrainstorm, ready(), DependencyHealth{Baokuan: false, ObsidianRead: true}, false, []string{"baokuan_mcp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAction(tt.action, tt.assets, tt.deps)
			if got.Ready != tt.wantReady || !slices.Equal(got.MissingInputs, tt.wantMissing) {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestEvaluateActionTreatsOnlyReadyAssetsAsAvailable(t *testing.T) {
	for _, state := range []AssetState{AssetMissing, AssetStale, AssetGenerating, AssetFailed} {
		t.Run(string(state), func(t *testing.T) {
			got := EvaluateAction(ActionRemixStandard, map[AssetType]AssetState{AssetSourceScript: state}, DependencyHealth{})
			if got.Ready || !slices.Equal(got.MissingInputs, []string{"source_script"}) {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestEvaluateActionDependencyRequirements(t *testing.T) {
	unhealthy := false
	tests := []struct {
		name        string
		action      TaskAction
		deps        DependencyHealth
		wantMissing []string
	}{
		{"brainstorm", ActionTopicBrainstorm, DependencyHealth{TopicConfig: &unhealthy}, []string{"baokuan_mcp", "obsidian_read", "topic_config"}},
		{"commit", ActionTopicCommit, DependencyHealth{ValidCandidate: &unhealthy}, []string{"valid_candidate", "obsidian_write"}},
		{"deepen", ActionTopicDeepen, DependencyHealth{UniqueWritableCard: &unhealthy}, []string{"unique_writable_card", "baokuan_mcp"}},
		{"unknown", TaskAction("unknown"), DependencyHealth{}, []string{"unknown_action"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAction(tt.action, nil, tt.deps)
			if got.Ready || !slices.Equal(got.MissingInputs, tt.wantMissing) {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestEvaluateActionBlocksUnassessedTopicPrerequisites(t *testing.T) {
	tests := []struct {
		name        string
		action      TaskAction
		deps        DependencyHealth
		wantMissing []string
	}{
		{"brainstorm topic config", ActionTopicBrainstorm, DependencyHealth{Baokuan: true, ObsidianRead: true}, []string{"topic_config"}},
		{"commit candidate", ActionTopicCommit, DependencyHealth{ObsidianWrite: true}, []string{"valid_candidate"}},
		{"deepen card", ActionTopicDeepen, DependencyHealth{Baokuan: true}, []string{"unique_writable_card"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAction(tt.action, nil, tt.deps)
			if got.Ready || !slices.Equal(got.MissingInputs, tt.wantMissing) {
				t.Fatalf("got %#v", got)
			}
		})
	}
}
