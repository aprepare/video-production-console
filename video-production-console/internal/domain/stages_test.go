package domain

import (
	"strings"
	"testing"
)

func TestCanMoveUsesFiveProductionStages(t *testing.T) {
	for _, move := range []struct{ from, to ProjectStage }{
		{StageScript, StageAssets},
		{StageMixing, StageReview},
		{StageReview, StageReview},
		{StageScript, StageArchived},
	} {
		if err := CanMove(move.from, move.to, nil); err != nil {
			t.Fatalf("CanMove(%q, %q): %v", move.from, move.to, err)
		}
	}
	for _, move := range []struct{ from, to ProjectStage }{
		{StageTopic, StageScript},
		{StageReady, StagePublished},
		{StageScript, StageReady},
		{StageScript, StageMixing},
		{StageArchived, StagePublished},
	} {
		if err := CanMove(move.from, move.to, nil); err == nil {
			t.Fatalf("CanMove(%q, %q) succeeded", move.from, move.to)
		}
	}
}

func TestCanMoveAssetsToMixingUsesCanonicalAssets(t *testing.T) {
	required := map[AssetType]bool{
		AssetContinuousScript:  true,
		AssetNarration:         true,
		AssetSubtitleSRT:       true,
		AssetAccountBackground: true,
	}
	if err := CanMove(StageAssets, StageMixing, required); err != nil {
		t.Fatalf("assets -> mixing: %v", err)
	}
	delete(required, AssetSubtitleSRT)
	required[AssetSubtitle] = true
	if err := CanMove(StageAssets, StageMixing, required); err == nil || !strings.Contains(err.Error(), string(AssetSubtitleSRT)) {
		t.Fatalf("deprecated subtitle satisfied gate: %v", err)
	}
}

func TestReviewPublishesOnlyWithFinalVideo(t *testing.T) {
	if err := CanMove(StageReview, StagePublished, nil); err == nil || !strings.Contains(err.Error(), string(AssetFinalVideo)) {
		t.Fatalf("review -> published error = %v", err)
	}
	if err := CanMove(StageReview, StagePublished, map[AssetType]bool{AssetFinalVideo: true}); err != nil {
		t.Fatalf("review -> published with final video: %v", err)
	}
}

func TestCanMoveRejectsUnknownStagesBeforeSpecialCases(t *testing.T) {
	for _, move := range []struct{ from, to ProjectStage }{{"invalid", "invalid"}, {"invalid", StageArchived}, {StageScript, "invalid"}} {
		if err := CanMove(move.from, move.to, nil); err == nil {
			t.Fatalf("CanMove(%q,%q) succeeded", move.from, move.to)
		}
	}
}

func TestCanMovePublicationStatus(t *testing.T) {
	for _, move := range []struct{ from, to ProjectStatus }{
		{ProjectDraft, ProjectProducing},
		{ProjectProducing, ProjectReadyToPublish},
		{ProjectReadyToPublish, ProjectPublished},
		{ProjectProducing, ProjectProducing},
		{ProjectDraft, ProjectArchived},
		{ProjectPublished, ProjectArchived},
	} {
		if err := CanMovePublicationStatus(move.from, move.to); err != nil {
			t.Fatalf("CanMovePublicationStatus(%q, %q): %v", move.from, move.to, err)
		}
	}
}

func TestCanMovePublicationStatusRejectsInvalidMovement(t *testing.T) {
	for _, move := range []struct{ from, to ProjectStatus }{
		{ProjectDraft, ProjectReadyToPublish},
		{ProjectPublished, ProjectDraft},
		{ProjectArchived, ProjectPublished},
		{"invalid", ProjectArchived},
		{ProjectDraft, "invalid"},
	} {
		if err := CanMovePublicationStatus(move.from, move.to); err == nil {
			t.Fatalf("CanMovePublicationStatus(%q, %q) succeeded", move.from, move.to)
		}
	}
}
