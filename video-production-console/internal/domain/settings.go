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
