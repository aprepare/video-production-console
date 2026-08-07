package domain

import "time"

type AssetType string

const (
	AssetSourceScript     AssetType = "source_script"
	AssetTopicCard        AssetType = "topic_card"
	AssetContinuousScript AssetType = "continuous_script"
	// Deprecated: retained so historical asset and audit data can be decoded.
	AssetSpokenScript      AssetType = "spoken_script"
	AssetNarration         AssetType = "narration"
	AssetSubtitleSRT       AssetType = "subtitle_srt"
	AssetAccountBackground AssetType = "account_background"
	AssetMixDraft          AssetType = "mix_draft"
	AssetFinalVideo        AssetType = "final_video"

	// Deprecated: retained while legacy asset persistence is migrated.
	AssetAudio AssetType = "audio"
	// Deprecated: retained while legacy asset persistence is migrated.
	AssetSubtitle AssetType = "subtitle"
)

type AssetState string

const (
	AssetMissing    AssetState = "missing"
	AssetReady      AssetState = "ready"
	AssetStale      AssetState = "stale"
	AssetGenerating AssetState = "generating"
	AssetFailed     AssetState = "failed"
)

type StorageKind string

const (
	StorageFile      StorageKind = "file"
	StorageDirectory StorageKind = "directory"
)

// LogicalAsset identifies a versioned asset independently of its stored versions.
type LogicalAsset struct {
	ID               string
	ProjectID        *string
	AccountID        string
	Type             AssetType
	CurrentVersionID *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AssetItem is the persistence-facing name for a logical asset.
type AssetItem = LogicalAsset

type AssetVersion struct {
	ID              string
	AssetID         string
	ProjectID       *string
	AccountID       string
	Type            AssetType
	Version         int
	StorageKind     StorageKind
	Path            string
	Filename        string
	MIMEType        string
	Size            int64
	SHA256          string
	ParentVersionID *string
	SourceTaskID    *string
	State           AssetState
	StaleReason     *string
	CreatedAt       time.Time
}

type AssetDependency struct {
	AssetVersionID     string
	DependsOnVersionID string
}

var invalidates = map[AssetType][]AssetType{
	AssetSourceScript:      {AssetContinuousScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetTopicCard:         {AssetContinuousScript, AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetContinuousScript:  {AssetNarration, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetNarration:         {AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetSubtitleSRT:       {AssetMixDraft, AssetFinalVideo},
	AssetAccountBackground: {AssetMixDraft, AssetFinalVideo},
	AssetMixDraft:          {AssetFinalVideo},
}

func InvalidatedAssetTypes(changed AssetType) []AssetType {
	return append([]AssetType(nil), invalidates[changed]...)
}
