package domain

import (
	"fmt"
	"strings"
)

type MissingAssetsError struct{ Missing []AssetType }

type ProjectStatus string

const (
	ProjectDraft          ProjectStatus = "draft"
	ProjectProducing      ProjectStatus = "producing"
	ProjectReadyToPublish ProjectStatus = "ready_to_publish"
	ProjectPublished      ProjectStatus = "published"
	ProjectArchived       ProjectStatus = "archived"
)

func (e *MissingAssetsError) Error() string {
	parts := make([]string, len(e.Missing))
	for i, assetType := range e.Missing {
		parts[i] = string(assetType)
	}
	return "missing required assets: " + strings.Join(parts, ", ")
}

func CanMove(from, to ProjectStage, available map[AssetType]bool) error {
	order := map[ProjectStage]int{StageTopic: 0, StageScript: 1, StageAssets: 2, StageMixing: 3, StageReview: 4, StageReady: 5, StagePublished: 6}
	known := func(stage ProjectStage) bool { _, ok := order[stage]; return ok || stage == StageArchived }
	if !known(from) || !known(to) {
		return fmt.Errorf("invalid stage move from %s to %s", from, to)
	}
	fromOrder, fromOK := order[from]
	toOrder, toOK := order[to]
	if from == StageArchived && to != StageArchived {
		return fmt.Errorf("archived project cannot be moved")
	}
	if to == StageArchived || from == to {
		return nil
	}
	if !fromOK || !toOK {
		return fmt.Errorf("invalid stage move from %s to %s", from, to)
	}
	if toOrder > fromOrder+1 {
		return fmt.Errorf("cannot skip stages from %s to %s", from, to)
	}
	var required []AssetType
	if from == StageAssets && to == StageMixing {
		required = []AssetType{AssetContinuousScript, AssetAudio, AssetSubtitle, AssetAccountBackground}
	}
	if (from == StageReview && to == StageReady) || (from == StageReady && to == StagePublished) {
		required = []AssetType{AssetFinalVideo}
	}
	missing := make([]AssetType, 0)
	for _, assetType := range required {
		if !available[assetType] {
			missing = append(missing, assetType)
		}
	}
	if len(missing) > 0 {
		return &MissingAssetsError{Missing: missing}
	}
	return nil
}

func CanMovePublicationStatus(from, to ProjectStatus) error {
	order := map[ProjectStatus]int{
		ProjectDraft:          0,
		ProjectProducing:      1,
		ProjectReadyToPublish: 2,
		ProjectPublished:      3,
	}
	known := func(status ProjectStatus) bool {
		_, ok := order[status]
		return ok || status == ProjectArchived
	}
	if !known(from) || !known(to) {
		return fmt.Errorf("invalid publication status move from %s to %s", from, to)
	}
	if from == ProjectArchived && to != ProjectArchived {
		return fmt.Errorf("archived project cannot be moved")
	}
	if from == to || to == ProjectArchived {
		return nil
	}
	fromOrder, fromOK := order[from]
	toOrder, toOK := order[to]
	if !fromOK || !toOK || toOrder != fromOrder+1 {
		return fmt.Errorf("invalid publication status move from %s to %s", from, to)
	}
	return nil
}
