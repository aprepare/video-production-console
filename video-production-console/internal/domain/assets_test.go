package domain

import (
	"slices"
	"testing"
)

func TestSpokenScriptIsInvalidatedWithNarration(t *testing.T) {
	tests := []struct {
		changed AssetType
		want    []AssetType
	}{
		{AssetSourceScript, []AssetType{AssetContinuousScript, AssetNarration, AssetSpokenScript, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetTopicCard, []AssetType{AssetContinuousScript, AssetNarration, AssetSpokenScript, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetContinuousScript, []AssetType{AssetNarration, AssetSpokenScript, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetSpokenScript, nil},
		{AssetNarration, []AssetType{AssetSpokenScript, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetSubtitleSRT, []AssetType{AssetMixDraft, AssetFinalVideo}},
		{AssetAccountBackground, []AssetType{AssetMixDraft, AssetFinalVideo}},
		{AssetMixDraft, []AssetType{AssetFinalVideo}},
		{AssetFinalVideo, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.changed), func(t *testing.T) {
			if got := InvalidatedAssetTypes(tt.changed); !slices.Equal(got, tt.want) {
				t.Fatalf("InvalidatedAssetTypes(%q) = %v, want %v", tt.changed, got, tt.want)
			}
		})
	}
}

func TestInvalidatedAssetTypesReturnsCopy(t *testing.T) {
	got := InvalidatedAssetTypes(AssetContinuousScript)
	got[0] = AssetFinalVideo
	want := []AssetType{AssetNarration, AssetSpokenScript, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}
	if next := InvalidatedAssetTypes(AssetContinuousScript); !slices.Equal(next, want) {
		t.Fatalf("invalidation graph was mutated: %v", next)
	}
}
