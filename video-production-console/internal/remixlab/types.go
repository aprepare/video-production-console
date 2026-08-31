package remixlab

import (
	"context"
	"io"
	"time"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
)

// Runner executes one openaicompat remix run. Unused in Task 3 (CreateExperiment
// only validates, resolves keys, and persists queued rows).
type Runner func(ctx context.Context, opts openaicompat.Options) error

type AssetAdopter interface {
	SaveProjectAsset(projectID string, assetType domain.AssetType, filename string, reader io.Reader) (assets.SavedAsset, error)
}

type ProjectBinder interface {
	GetProject(context.Context, string) (domain.Project, error)
	AddAsset(context.Context, *domain.Asset) (store.CommitState, error)
}

type RuntimeView struct {
	RemixBaseURL, RemixModel, RemixReasoningEffort, RemixAPIKey, DataRoot string
	// Grok 搜索通道：多agent管线里事实核查agent用它联网核实与补充数据。
	GrokBaseURL, GrokModel, GrokAPIKey string
}

type RuntimeProvider interface {
	Runtime(context.Context) (RuntimeView, error)
}

type SlotInput struct {
	BaseURL, Model, APIKey, ReasoningEffort string
	// Pipeline：""/"single" 单模型写手；"multi_agent" 三路情报agent+写手。
	Pipeline    string
	RunCount    int
	PresetIndex *int
}

type DefaultsView struct {
	RemixBaseURL          string           `json:"remix_base_url"`
	RemixModel            string           `json:"remix_model"`
	RemixReasoningEffort  string           `json:"remix_reasoning_effort"`
	RemixAPIKeyConfigured bool             `json:"remix_api_key_configured"`
	Presets               []PresetSlotView `json:"presets"`
}

type PresetSlotView struct {
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoning_effort"`
	Pipeline         string `json:"pipeline"`
	RunCount         int    `json:"run_count"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	PresetIndex      int    `json:"preset_index"`
}

type Experiment struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	SourceText  string    `json:"source_text"`
	PromptStamp string    `json:"prompt_stamp"`
	Status      string    `json:"status"`
	// Workflow 为 true 表示按工作流快照执行的实验。
	Workflow  bool       `json:"workflow"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Slots     []SlotView `json:"slots"`
	Runs      []RunView  `json:"runs"`
}

type SlotView struct {
	ID               string `json:"id"`
	ExperimentID     string `json:"experiment_id"`
	SortIndex        int    `json:"sort_index"`
	Label            string `json:"label"`
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoning_effort"`
	Pipeline         string `json:"pipeline"`
	RunCount         int    `json:"run_count"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}

type RunView struct {
	ID               string     `json:"id"`
	ExperimentID     string     `json:"experiment_id"`
	SlotID           string     `json:"slot_id"`
	RunIndex         int        `json:"run_index"`
	Status           string     `json:"status"`
	PromptID         string     `json:"prompt_id"`
	PromptStamp      string     `json:"prompt_stamp"`
	PromptName       string     `json:"prompt_name"`
	ContinuousScript string     `json:"continuous_script"`
	TitlesJSON       string     `json:"titles_json"`
	PackageJSON      string     `json:"package_json"`
	DraftV1JSON      string     `json:"draft_v1_json"`
	ReviewJSON       string     `json:"review_json"`
	// Production 生产段状态（确认闸门/建项目/口播/配音/混剪）；无则未触发。
	Production   *ProductionView `json:"production,omitempty"`
	ErrorMessage string          `json:"error_message"`
	Comment          string     `json:"comment"`
	OutputDir        string     `json:"output_dir"`
	AdoptedProjectID string     `json:"adopted_project_id"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

// Service validates remix-lab experiments, resolves slot keys, and drives runners.
type Service struct {
	repo      *store.RemixLabRepository
	runtime   RuntimeProvider
	protector security.Protector
	dataRoot  string
	runner    Runner
	adopter   AssetAdopter
	binder    ProjectBinder
	now       func() time.Time
	// producer 生产驱动器（确认闸门放行后串起混剪链路）；nil 时生产功能不可用。
	producer ProductionLauncher
	// putPresetJSON optionally overrides preset persistence (tests / seam).
	putPresetJSON func(ctx context.Context, raw string) error
	agentChat     func(baseURL, apiKey string, req openaicompat.ChatRequest) (openaicompat.ChatResponse, error)
}

func (s *Service) DataRoot() string {
	if s == nil {
		return ""
	}
	return s.dataRoot
}
