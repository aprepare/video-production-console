package domain

import (
	"strings"
	"testing"
)

func TestCanMoveStageGates(t *testing.T) {
	if err := CanMove(StageTopic, StageScript, nil); err != nil {
		t.Fatalf("topic -> script: %v", err)
	}
	required := map[AssetType]bool{AssetContinuousScript: true, AssetAudio: true, AssetSubtitle: true, AssetAccountBackground: true}
	if err := CanMove(StageAssets, StageMixing, required); err != nil {
		t.Fatalf("assets -> mixing: %v", err)
	}
	delete(required, AssetSubtitle)
	if err := CanMove(StageAssets, StageMixing, required); err == nil || !strings.Contains(err.Error(), string(AssetSubtitle)) {
		t.Fatalf("missing subtitle error = %v", err)
	}
	for _, move := range []struct{ from, to ProjectStage }{{StageReview, StageReady}, {StageReady, StagePublished}} {
		if err := CanMove(move.from, move.to, nil); err == nil || !strings.Contains(err.Error(), string(AssetFinalVideo)) {
			t.Fatalf("%s -> %s error = %v", move.from, move.to, err)
		}
		if err := CanMove(move.from, move.to, map[AssetType]bool{AssetFinalVideo: true}); err != nil {
			t.Fatalf("with final video: %v", err)
		}
	}
}

func TestCanMoveAllowsRollbackButArchiveIsExplicit(t *testing.T) {
	if err := CanMove(StageReview, StageScript, nil); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := CanMove(StageTopic, StageArchived, nil); err != nil {
		t.Fatalf("explicit archive: %v", err)
	}
	if err := CanMove(StageArchived, StagePublished, nil); err == nil {
		t.Fatal("archived project moved without explicit restore rule")
	}
	if err := CanMove(StageTopic, StageMixing, nil); err == nil {
		t.Fatal("forward stage skipping succeeded")
	}
}
