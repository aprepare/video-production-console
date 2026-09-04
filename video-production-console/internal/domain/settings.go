package domain

import (
	"strings"
	"time"
)

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
	RemixPromptStyle            string       `json:"remix_prompt_style"`
	CopyBaseURL                 string       `json:"copy_base_url"`
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
	KeywordsHidden  bool    `json:"keywords_hidden,omitempty"` // true: captions render plain, no keyword highlighting
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

	// 描边 / 底色 / 字体扩展：空值表示沿用 montage-style-policy 里验证过的默认，
	// 与上面「零值=默认」的约定一致。这批字段主要由「从剪映草稿导入样式」回填。
	CaptionBorderColor  string  `json:"caption_border_color,omitempty"`  // #RRGGBB
	CaptionBorderWidth  float64 `json:"caption_border_width,omitempty"`  // 剪映 0-100 口径
	CaptionBorderHidden bool    `json:"caption_border_hidden,omitempty"` // true: 字幕不描边
	CaptionBgColor      string  `json:"caption_bg_color,omitempty"`      // #RRGGBB；空=无底色
	CaptionBgAlpha      float64 `json:"caption_bg_alpha,omitempty"`      // 0..1；0 视为 1
	KeywordBorderColor  string  `json:"keyword_border_color,omitempty"`
	TitleFont           string  `json:"title_font,omitempty"`
	TitleBorderColor    string  `json:"title_border_color,omitempty"`
	TitleBgColor        string  `json:"title_bg_color,omitempty"`
	TitleBgAlpha        float64 `json:"title_bg_alpha,omitempty"`
	SubtitleFont        string  `json:"subtitle_font,omitempty"`
	SubtitleBorderColor string  `json:"subtitle_border_color,omitempty"`
	SubtitleBgColor     string  `json:"subtitle_bg_color,omitempty"`
	SubtitleBgAlpha     float64 `json:"subtitle_bg_alpha,omitempty"`
}

// AccountOverrides 账号级制作差异化配置：矩阵账号靠它拉开画面与声音指纹，
// 避免多号同模板被平台查重连坐。字段为空即回落全局设置。
type AccountOverrides struct {
	// MontageStyle 非 nil 时整体替换全局混剪样式；未填字段用内置默认，不再跟随全局。
	MontageStyle *MontageStyle  `json:"montage_style,omitempty"`
	Voice        *VoiceOverride `json:"voice,omitempty"`
}

// VoiceOverride 账号专属配音音色。只覆盖音色身份；API 密钥、语速等参数仍用全局设置。
type VoiceOverride struct {
	AuraSTDVoiceID      string `json:"aurastd_voice_id,omitempty"`
	VolcSpeechSpeakerID string `json:"volc_speech_speaker_id,omitempty"`
}

// IsZero reports whether the overrides carry no effective configuration.
func (o *AccountOverrides) IsZero() bool {
	if o == nil {
		return true
	}
	if o.MontageStyle != nil {
		return false
	}
	return o.Voice == nil || (o.Voice.AuraSTDVoiceID == "" && o.Voice.VolcSpeechSpeakerID == "")
}

// BuiltinBGMID selects the historical verified Jianying BGM track.
const BuiltinBGMID = "builtin"

// KnownMontageFonts are pyJianYingDraft FontType member names the UI offers
// and the draft builder is allowed to emit. Imported draft titles such as
// "WenYue" are not in this set and must not be written into a production plan.
var KnownMontageFonts = []string{
	"新青年体",
	"俪金黑",
	"大字报",
	"抖音美好体",
	"汉仪英雄体",
	"站酷酷黑体",
	"宋体",
	"圆体",
	"毛笔行楷",
	"台北黑体_Bold",
}

var knownMontageFontSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(KnownMontageFonts))
	for _, name := range KnownMontageFonts {
		out[name] = struct{}{}
	}
	return out
}()

// NormalizeMontageFont returns name when it is a FontType member the builder
// can emit, otherwise "". Unknown imported titles (e.g. "WenYue") are dropped
// so pyJianYingDraft does not abort the whole job.
func NormalizeMontageFont(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if _, ok := knownMontageFontSet[name]; ok {
		return name
	}
	return ""
}

// MontageCaptionFont is the caption/keyword font written into style_overrides.
// Unknown names fall back to 新青年体 so spoken captions keep a verified face.
func MontageCaptionFont(name string) string {
	if normalized := NormalizeMontageFont(name); normalized != "" {
		return normalized
	}
	return DefaultMontageStyle().CaptionFont
}

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
