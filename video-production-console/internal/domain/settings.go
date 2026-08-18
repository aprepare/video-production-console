package domain

import "time"

type PublicSettings struct {
	ListenAddr                  string   `json:"listen_addr"`
	DataRoot                    string   `json:"data_root"`
	MaxCodexConcurrency         int      `json:"max_codex_concurrency"`
	CodexDefaultModel           string   `json:"codex_default_model"`
	CodexDefaultReasoningEffort string   `json:"codex_default_reasoning_effort"`
	BaokuanBaseURL              string   `json:"baokuan_base_url"`
	BaokuanMCPExecutable        string   `json:"baokuan_mcp_executable"`
	ObsidianVault               string   `json:"obsidian_vault"`
	TopicCardsDir               string   `json:"topic_cards_dir"`
	GrokBaseURL                 string   `json:"grok_base_url"`
	GrokModel                   string   `json:"grok_model"`
	RemixBaseURL                string   `json:"remix_base_url"`
	RemixModel                  string   `json:"remix_model"`
	RemixReasoningEffort        string   `json:"remix_reasoning_effort"`
	ImageBaseURL                string   `json:"image_base_url"`
	ImageModel                  string   `json:"image_model"`
	ImageTextBaseURL            string   `json:"image_text_base_url"`
	ImageTextModel              string   `json:"image_text_model"`
	ImageTextReasoningEffort    string   `json:"image_text_reasoning_effort"`
	ImageStream                 bool     `json:"image_stream"`
	MaxImageConcurrency         int      `json:"max_image_concurrency"`
	ImageGenerationAttempts     int      `json:"image_generation_attempts"`
	DefaultImageRatio           string   `json:"default_image_ratio"`
	DefaultImageStyle           string   `json:"default_image_style"`
	CodexBinaryPath             string   `json:"codex_binary_path"`
	MediaIndexPath              string   `json:"media_index_path"`
	MediaRoot                   string   `json:"media_root"`
	JianyingRoot                string   `json:"jianying_root"`
	MachineProfilePath          string   `json:"machine_profile_path"`
	AppServerEnabled            bool     `json:"app_server_enabled"`
	CodexWorkspaceRoots         []string `json:"codex_workspace_roots"`
	CodexTaskProjectRoot        string   `json:"codex_task_project_root"`
	VolcSpeechSpeakerID         string   `json:"volc_speech_speaker_id"`
	VolcSpeechResourceID        string   `json:"volc_speech_resource_id"`
	TTSProvider                 string   `json:"tts_provider"`
	AuraSTDBaseURL              string   `json:"aurastd_base_url"`
	AuraSTDModel                string   `json:"aurastd_model"`
	AuraSTDVoiceID              string   `json:"aurastd_voice_id"`
	AuraSTDSpeed                float64  `json:"aurastd_speed"`
	AuraSTDVolume               float64  `json:"aurastd_volume"`
	AuraSTDPitch                int      `json:"aurastd_pitch"`
	AuraSTDEmotion              string   `json:"aurastd_emotion"`
	AuraSTDLanguageBoost        string   `json:"aurastd_language_boost"`
	AuraSTDModifyPitch          int      `json:"aurastd_modify_pitch"`
	AuraSTDModifyIntensity      int      `json:"aurastd_modify_intensity"`
	AuraSTDModifyTimbre         int      `json:"aurastd_modify_timbre"`
	AuraSTDSoundEffects         string   `json:"aurastd_sound_effects"`
	MediaCatalogPath            string   `json:"media_catalog_path"`
	FFmpegPath                  string   `json:"ffmpeg_path"`
	FFprobePath                 string   `json:"ffprobe_path"`
	VisionBaseURL               string   `json:"vision_base_url"`
	VisionModel                 string   `json:"vision_model"`
	EmbeddingBaseURL            string   `json:"embedding_base_url"`
	EmbeddingModel              string   `json:"embedding_model"`
	PexelsAPIBaseURL            string   `json:"pexels_api_base_url"`
	PixabayAPIBaseURL           string   `json:"pixabay_api_base_url"`
	MaxExternalResultsPerQuery  int      `json:"max_external_results_per_query"`
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
