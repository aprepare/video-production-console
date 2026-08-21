package domain

import "time"

type PublicSettings struct {
	ListenAddr                  string       `json:"listen_addr"`
	DataRoot                    string       `json:"data_root"`
	MaxCodexConcurrency         int          `json:"max_codex_concurrency"`
	CodexDefaultModel           string       `json:"codex_default_model"`
	CodexDefaultReasoningEffort string       `json:"codex_default_reasoning_effort"`
	BaokuanBaseURL              string       `json:"baokuan_base_url"`
	BaokuanMCPExecutable        string       `json:"baokuan_mcp_executable"`
	ObsidianVault               string       `json:"obsidian_vault"`
	TopicCardsDir               string       `json:"topic_cards_dir"`
	GrokBaseURL                 string       `json:"grok_base_url"`
	GrokModel                   string       `json:"grok_model"`
	RemixBaseURL                string       `json:"remix_base_url"`
	RemixModel                  string       `json:"remix_model"`
	RemixReasoningEffort        string       `json:"remix_reasoning_effort"`
	RemixCheckModel             string       `json:"remix_check_model"`
	SpokenLinesModel            string       `json:"spoken_lines_model"`
	ModelOptions                string       `json:"model_options"`
	ImageBaseURL                string       `json:"image_base_url"`
	ImageModel                  string       `json:"image_model"`
	ImageTextBaseURL            string       `json:"image_text_base_url"`
	ImageTextModel              string       `json:"image_text_model"`
	ImageTextReasoningEffort    string       `json:"image_text_reasoning_effort"`
	ImageStream                 bool         `json:"image_stream"`
	MaxImageConcurrency         int          `json:"max_image_concurrency"`
	ImageGenerationAttempts     int          `json:"image_generation_attempts"`
	DefaultImageRatio           string       `json:"default_image_ratio"`
	DefaultImageStyle           string       `json:"default_image_style"`
	CodexBinaryPath             string       `json:"codex_binary_path"`
	MediaIndexPath              string       `json:"media_index_path"`
	MediaRoot                   string       `json:"media_root"`
	JianyingRoot                string       `json:"jianying_root"`
	MachineProfilePath          string       `json:"machine_profile_path"`
	AppServerEnabled            bool         `json:"app_server_enabled"`
	CodexWorkspaceRoots         []string     `json:"codex_workspace_roots"`
	CodexTaskProjectRoot        string       `json:"codex_task_project_root"`
	VolcSpeechSpeakerID         string       `json:"volc_speech_speaker_id"`
	VolcSpeechResourceID        string       `json:"volc_speech_resource_id"`
	TTSProvider                 string       `json:"tts_provider"`
	AuraSTDBaseURL              string       `json:"aurastd_base_url"`
	AuraSTDModel                string       `json:"aurastd_model"`
	AuraSTDVoiceID              string       `json:"aurastd_voice_id"`
	AuraSTDSpeed                float64      `json:"aurastd_speed"`
	AuraSTDVolume               float64      `json:"aurastd_volume"`
	AuraSTDPitch                int          `json:"aurastd_pitch"`
	AuraSTDEmotion              string       `json:"aurastd_emotion"`
	AuraSTDLanguageBoost        string       `json:"aurastd_language_boost"`
	AuraSTDModifyPitch          int          `json:"aurastd_modify_pitch"`
	AuraSTDModifyIntensity      int          `json:"aurastd_modify_intensity"`
	AuraSTDModifyTimbre         int          `json:"aurastd_modify_timbre"`
	AuraSTDSoundEffects         string       `json:"aurastd_sound_effects"`
	MediaCatalogPath            string       `json:"media_catalog_path"`
	FFmpegPath                  string       `json:"ffmpeg_path"`
	FFprobePath                 string       `json:"ffprobe_path"`
	VisionBaseURL               string       `json:"vision_base_url"`
	VisionModel                 string       `json:"vision_model"`
	EmbeddingBaseURL            string       `json:"embedding_base_url"`
	EmbeddingModel              string       `json:"embedding_model"`
	PexelsAPIBaseURL            string       `json:"pexels_api_base_url"`
	PixabayAPIBaseURL           string       `json:"pixabay_api_base_url"`
	MaxExternalResultsPerQuery  int          `json:"max_external_results_per_query"`
	BGMDir                      string       `json:"bgm_dir"`
	MontageStyle                MontageStyle `json:"montage_style"`
}

// MontageStyle configures the montage draft's caption/title typography and
// background music. Zero values mean "use the built-in default", so a blank
// configuration renders exactly like the historical hardcoded style.
type MontageStyle struct {
	CaptionSize     float64 `json:"caption_size,omitempty"`
	CaptionColor    string  `json:"caption_color,omitempty"`    // #RRGGBB
	CaptionPosition string  `json:"caption_position,omitempty"` // middle | bottom | custom
	CaptionY        float64 `json:"caption_y,omitempty"`        // used when position=custom
	CaptionFont     string  `json:"caption_font,omitempty"`
	PlainSize       float64 `json:"plain_size,omitempty"`   // non-keyword runs in keyword lines
	KeywordSize     float64 `json:"keyword_size,omitempty"` // enlarged keyword runs
	KeywordColor    string  `json:"keyword_color,omitempty"`
	TitleHidden     bool    `json:"title_hidden,omitempty"`
	TitleSize       float64 `json:"title_size,omitempty"`
	TitleColor      string  `json:"title_color,omitempty"`
	TitleY          float64 `json:"title_y,omitempty"`
	SubtitleHidden  bool    `json:"subtitle_hidden,omitempty"`
	SubtitleSize    float64 `json:"subtitle_size,omitempty"`
	SubtitleColor   string  `json:"subtitle_color,omitempty"`
	SubtitleY       float64 `json:"subtitle_y,omitempty"`
	BGMID           string  `json:"bgm_id,omitempty"` // "builtin" or a BGM library track ID
	BGMVolume       float64 `json:"bgm_volume,omitempty"`
}

// BuiltinBGMID selects the historical verified Jianying BGM track.
const BuiltinBGMID = "builtin"

// DefaultMontageStyle mirrors the values that used to be hardcoded across
// montage-style-policy.v2.json and run_montage_job.py.
func DefaultMontageStyle() MontageStyle {
	return MontageStyle{
		CaptionSize: 20, CaptionColor: "#F9F3C4", CaptionPosition: "middle", CaptionFont: "新青年体",
		PlainSize: 17, KeywordSize: 23, KeywordColor: "#FF1515",
		TitleSize: 16, TitleColor: "#FFDB1A", TitleY: 0.6,
		SubtitleSize: 9.2, SubtitleColor: "#FFFFFF", SubtitleY: 0.49,
		BGMID: BuiltinBGMID, BGMVolume: 0.2512,
	}
}

// Normalized fills every zero field from the defaults; hidden flags are kept
// as-is because false is the default.
func (s MontageStyle) Normalized() MontageStyle {
	defaults := DefaultMontageStyle()
	if s.CaptionSize == 0 {
		s.CaptionSize = defaults.CaptionSize
	}
	if s.CaptionColor == "" {
		s.CaptionColor = defaults.CaptionColor
	}
	if s.CaptionPosition == "" {
		s.CaptionPosition = defaults.CaptionPosition
	}
	if s.CaptionFont == "" {
		s.CaptionFont = defaults.CaptionFont
	}
	if s.PlainSize == 0 {
		s.PlainSize = defaults.PlainSize
	}
	if s.KeywordSize == 0 {
		s.KeywordSize = defaults.KeywordSize
	}
	if s.KeywordColor == "" {
		s.KeywordColor = defaults.KeywordColor
	}
	if s.TitleSize == 0 {
		s.TitleSize = defaults.TitleSize
	}
	if s.TitleColor == "" {
		s.TitleColor = defaults.TitleColor
	}
	if s.TitleY == 0 {
		s.TitleY = defaults.TitleY
	}
	if s.SubtitleSize == 0 {
		s.SubtitleSize = defaults.SubtitleSize
	}
	if s.SubtitleColor == "" {
		s.SubtitleColor = defaults.SubtitleColor
	}
	if s.SubtitleY == 0 {
		s.SubtitleY = defaults.SubtitleY
	}
	if s.BGMID == "" {
		s.BGMID = defaults.BGMID
	}
	if s.BGMVolume == 0 {
		s.BGMVolume = defaults.BGMVolume
	}
	return s
}

// CaptionTransformY resolves the caption position keyword to a Jianying
// transform_y. middle sits between the boundary lines; bottom matches the
// verified highlight position just above the lower red line.
func (s MontageStyle) CaptionTransformY() float64 {
	switch s.CaptionPosition {
	case "bottom":
		return -0.3
	case "custom":
		return s.CaptionY
	default:
		return 0
	}
}

type SecretStatus struct {
	Configured bool   `json:"configured"`
	Masked     string `json:"masked"`
}

type SkillFileSnapshot struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type SkillSnapshot struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Path       string              `json:"path"`
	SHA256     string              `json:"sha256"`
	Files      []SkillFileSnapshot `json:"files"`
	ModifiedAt time.Time           `json:"modified_at"`
	CreatedAt  time.Time           `json:"created_at"`
}
