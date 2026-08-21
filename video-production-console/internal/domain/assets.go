package domain

import "time"

type AssetType string

const (
	AssetSourceScript     AssetType = "source_script"
	AssetTopicCard        AssetType = "topic_card"
	AssetContinuousScript AssetType = "continuous_script"
	// AssetSpokenScript is the one-line 口播稿 used for TTS and SRT cuts.
	// remix.spoken_lines writes it; narration times those lines and does not
	// replace a ready copy.
	AssetSpokenScript AssetType = "spoken_script"
	// AssetCaptionKeywords marks the emphasis terms per 口播稿 line
	// (warning terms red, numbers gold). remix.caption_keywords writes it;
	// the montage plan reads it and falls back to a local list without it.
	AssetCaptionKeywords AssetType = "caption_keywords"
	AssetNarration         AssetType = "narration"
	AssetWordTiming        AssetType = "word_timing"
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
	AssetSourceScript:      {AssetContinuousScript, AssetNarration, AssetWordTiming, AssetSpokenScript, AssetCaptionKeywords, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetTopicCard:         {AssetContinuousScript, AssetNarration, AssetWordTiming, AssetSpokenScript, AssetCaptionKeywords, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetContinuousScript:  {AssetSpokenScript, AssetCaptionKeywords, AssetNarration, AssetWordTiming, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetSpokenScript:      {AssetCaptionKeywords, AssetNarration, AssetWordTiming, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetCaptionKeywords:   {AssetMixDraft, AssetFinalVideo},
	AssetNarration:         {AssetWordTiming, AssetSubtitleSRT, AssetMixDraft, AssetFinalVideo},
	AssetWordTiming:        {AssetMixDraft, AssetFinalVideo},
	AssetSubtitleSRT:       {AssetMixDraft, AssetFinalVideo},
	AssetAccountBackground: {AssetMixDraft, AssetFinalVideo},
	AssetMixDraft:          {AssetFinalVideo},
}

func InvalidatedAssetTypes(changed AssetType) []AssetType {
	return append([]AssetType(nil), invalidates[changed]...)
}
