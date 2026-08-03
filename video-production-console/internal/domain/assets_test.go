package domain

import (
	"slices"
	"testing"
)

func TestInvalidatedAssetTypes(t *testing.T) {
	tests := []struct {
		changed AssetType
		want    []AssetType
	}{
		{AssetSourceScript, []AssetType{AssetContinuousScript, AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetTopicCard, []AssetType{AssetContinuousScript, AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetContinuousScript, []AssetType{AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetSpokenScript, []AssetType{AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
		{AssetNarration, []AssetType{AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}},
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
	want := []AssetType{AssetSpokenScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo}
	if next := InvalidatedAssetTypes(AssetContinuousScript); !slices.Equal(next, want) {
		t.Fatalf("invalidation graph was mutated: %v", next)
	}
}
