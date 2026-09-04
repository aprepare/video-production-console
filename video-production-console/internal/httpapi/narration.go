package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/logging"
	"video-production-console/internal/narration"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/spokenlines"
	"video-production-console/internal/store"
)

// narrationTimeout bounds one synthesis call. The vendor streams audio for as
// long as the script is, and a stalled stream must not hold the request open.
const narrationTimeout = 5 * time.Minute

// maxNarrationScriptBytes bounds the continuous script read from disk before it
// is measured in characters, so a corrupt asset cannot exhaust memory.
const maxNarrationScriptBytes = 1 << 20

type narrationStore interface {
	GetProject(context.Context, string) (domain.Project, error)
	ListAssets(context.Context, string) ([]domain.Asset, error)
	AddAsset(context.Context, *domain.Asset) (store.CommitState, error)
}

// narrationAccountStore reads the project's account so per-account voice
// overrides can replace the global speaker. Nil disables the override path.
type narrationAccountStore interface {
	Get(context.Context, string) (domain.Account, error)
}

// NarrationHandlerOptions supplies the credentials source and, for tests, a
// replacement for the vendor call.
type NarrationHandlerOptions struct {
	Runtime AssetRuntimeProvider
	Produce func(context.Context, narration.ProduceRequest) (narration.Delivery, error)
	// Accounts enables per-account voice overrides; nil keeps the global voice.
	Accounts narrationAccountStore
}

type narrationHandler struct {
	repository narrationStore
	assets     *assets.Service
	runtime    AssetRuntimeProvider
	accounts   narrationAccountStore
	produce    func(context.Context, narration.ProduceRequest) (narration.Delivery, error)
	inFlight   sync.Map
}

// NewNarrationHandler serves narration and subtitle generation for one project.
// Synthesis is a deterministic vendor call rather than agent work, so it runs in
// this process and registers its output through the same asset path an operator
// upload takes.
func NewNarrationHandler(db *sql.DB, service *assets.Service, options NarrationHandlerOptions) http.Handler {
	if options.Accounts == nil {
		options.Accounts = store.NewAccountRepository(db)
	}
	return newNarrationHandler(store.NewProjectRepository(db), service, options)
}

func newNarrationHandler(repository narrationStore, service *assets.Service, options NarrationHandlerOptions) http.Handler {
	h := &narrationHandler{repository: repository, assets: service, runtime: options.Runtime, accounts: options.Accounts, produce: options.Produce}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/narration", h.create)
	return mux
}

type narrationView struct {
	Narration        assetView `json:"narration"`
	SubtitleSRT      assetView `json:"subtitle_srt"`
	SpokenScript     assetView `json:"spoken_script"`
	WordTiming       assetView `json:"word_timing"`
	Captions         int       `json:"captions"`
	DurationSeconds  float64   `json:"duration_seconds"`
	BilledCharacters int       `json:"billed_characters"`
	Warnings         []string  `json:"warnings,omitempty"`
}

func (h *narrationHandler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if h.assets == nil {
		writeError(w, http.StatusInternalServerError, "asset_save_failed", "The asset service is unavailable.")
		return
	}
	project, err := h.repository.GetProject(r.Context(), id)
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed", "Project could not be read.")
		return
	}
	// One narration job per project at a time: a second run would spend the
	// character quota on a version that immediately supersedes the first.
	if _, busy := h.inFlight.LoadOrStore(id, struct{}{}); busy {
		writeError(w, http.StatusConflict, "narration_in_progress", "配音正在生成中，请等待本次生成结束。")
		return
	}
	defer h.inFlight.Delete(id)

	scriptText, code, err := h.readContinuousScript(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusConflict, code, err.Error())
		return
	}
	spokenRaw, spokenAsset, code, err := h.readSpokenScript(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusConflict, code, err.Error())
		return
	}
	formatted, err := spokenlines.Format(spokenRaw)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "spoken_script_invalid", "口播稿无法按一句一行整理，请重做口播稿。")
		return
	}
	// TTS reads the continuous script (natural punctuation, no artificial line
	// breaks); the 口播稿 lines only cut the subtitles against the word timings.
	speech := spokenlines.SpeechFromScript(scriptText)
	lines := spokenlines.Lines(formatted)
	request, code, err := h.produceRequest(r.Context(), speech, h.accountVoice(r.Context(), project.AccountID))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, code, err.Error())
		return
	}
	applyNarrationOverrides(&request, readNarrationOverrides(r))
	request.SpokenLines = lines
	ctx, cancel := context.WithTimeout(r.Context(), narrationTimeout)
	defer cancel()
	delivery, err := h.synthesize(ctx, request)
	if err != nil {
		var gate *narration.QualityGateError
		if errors.As(err, &gate) {
			writeError(w, http.StatusUnprocessableEntity, "narration_quality_gate",
				"字幕未通过交付门禁，请修改文案后重试："+strings.Join(gate.Report.Failures, "；"))
			return
		}
		logging.LoggerFrom(r.Context()).Error("synthesize narration", "project_id", id, "error", err)
		writeError(w, http.StatusBadGateway, "narration_synthesis_failed", "配音合成失败："+err.Error())
		return
	}
	rebuiltTiming, timingErr := narration.NewWordTimingDocument(delivery.Script, request.Provider, delivery.TimingDocument.Hash, delivery.Words)
	validTiming := timingErr == nil && delivery.TimingDocument.SchemaVersion == rebuiltTiming.SchemaVersion &&
		delivery.TimingDocument.Script == rebuiltTiming.Script && delivery.TimingDocument.Provider == rebuiltTiming.Provider &&
		delivery.TimingDocument.Hash == rebuiltTiming.Hash && delivery.TimingDocument.ScriptHash == rebuiltTiming.ScriptHash &&
		delivery.TimingDocument.Duration == rebuiltTiming.Duration && len(delivery.TimingDocument.Words) == len(rebuiltTiming.Words)
	if validTiming {
		for i := range rebuiltTiming.Words {
			if delivery.TimingDocument.Words[i] != rebuiltTiming.Words[i] {
				validTiming = false
				break
			}
		}
	}
	if !validTiming {
		writeError(w, http.StatusBadGateway, "narration_timing_invalid", "閰嶉煶鏃犳硶鐢熸垚鏈夋晥鐨勫瓧鏃堕棿鏂囨。")
		return
	}

	audioName := "narration." + delivery.AudioFormat
	audio, status, code, err := h.registerAsset(r.Context(), id, domain.AssetNarration, audioName, func() (assets.SavedAsset, error) {
		return h.assets.SaveProjectAsset(id, domain.AssetNarration, audioName, bytes.NewReader(delivery.Audio))
	})
	if err != nil {
		writeError(w, status, code, "配音文件无法登记："+err.Error())
		return
	}
	// The narration is already registered at this point. A subtitle failure
	// leaves it in place: the audio is what the synthesis was billed for, and
	// the subtitle can be rebuilt from the same script.
	subtitle, status, code, err := h.registerAsset(r.Context(), id, domain.AssetSubtitleSRT, "narration.srt", func() (assets.SavedAsset, error) {
		return h.assets.SaveTextVersion(id, domain.AssetSubtitleSRT, "narration.srt", delivery.SRT)
	})
	if err != nil {
		writeError(w, status, code, "字幕文件无法登记："+err.Error())
		return
	}
	timingBytes, marshalErr := json.Marshal(delivery.TimingDocument)
	if marshalErr != nil {
		writeError(w, http.StatusInternalServerError, "asset_save_failed", marshalErr.Error())
		return
	}
	timing, status, code, err := h.registerAsset(r.Context(), id, domain.AssetWordTiming, "narration.word_timing.json", func() (assets.SavedAsset, error) {
		return h.assets.SaveProjectAsset(id, domain.AssetWordTiming, "narration.word_timing.json", bytes.NewReader(timingBytes))
	})
	if err != nil {
		writeError(w, status, code, err.Error())
		return
	}
	if syncer, ok := h.repository.(interface {
		SyncStageFromAssets(context.Context, string, time.Time) (domain.Project, error)
	}); ok {
		if _, syncErr := syncer.SyncStageFromAssets(r.Context(), id, time.Now().UTC()); syncErr != nil {
			logging.LoggerFrom(r.Context()).Error("sync project stage after narration", "project_id", id, "error", syncErr)
		}
	}
	writeJSON(w, http.StatusCreated, narrationView{
		Narration: toAssetView(audio), SubtitleSRT: toAssetView(subtitle),
		SpokenScript: toAssetView(spokenAsset),
		WordTiming:   toAssetView(timing),
		Captions:     len(delivery.Captions), DurationSeconds: delivery.Duration,
		BilledCharacters: delivery.BilledWords, Warnings: delivery.Report.Warnings,
	})
}

func (h *narrationHandler) synthesize(ctx context.Context, request narration.ProduceRequest) (narration.Delivery, error) {
	if h.produce != nil {
		return h.produce(ctx, request)
	}
	return narration.Delivery{}, errors.New("narration synthesis is not wired")
}

// readContinuousScript returns the text of the current continuous script,
// which is what the TTS engine reads. Word timings come back from synthesis,
// and the 口播稿 lines are aligned against them for subtitles.
func (h *narrationHandler) readContinuousScript(ctx context.Context, projectID string) (string, string, error) {
	all, err := h.repository.ListAssets(ctx, projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "project_assets_failed", errors.New("项目素材无法读取。")
	}
	asset, ok := latestAssetsByType(all)[string(domain.AssetContinuousScript)]
	if !ok || asset.Status != string(domain.AssetReady) {
		return "", "continuous_script_missing", errors.New("请先完成连续文案，再生成配音与字幕。")
	}
	file, info, err := h.assets.OpenAsset(asset)
	if err != nil {
		return "", "continuous_script_missing", errors.New("连续文案文件缺失，请重新上传或重做文案。")
	}
	defer file.Close()
	if info.IsDir() {
		return "", "continuous_script_missing", errors.New("连续文案文件缺失，请重新上传或重做文案。")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNarrationScriptBytes))
	if err != nil {
		return "", "continuous_script_missing", errors.New("连续文案无法读取。")
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", "continuous_script_missing", errors.New("连续文案是空的。")
	}
	return string(data), "", nil
}

func (h *narrationHandler) readSpokenScript(ctx context.Context, projectID string) (string, domain.Asset, string, error) {
	all, err := h.repository.ListAssets(ctx, projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", domain.Asset{}, "project_assets_failed", errors.New("项目素材无法读取。")
	}
	asset, ok := latestAssetsByType(all)[string(domain.AssetSpokenScript)]
	if !ok || asset.Status != string(domain.AssetReady) {
		return "", domain.Asset{}, "spoken_script_missing", errors.New("请先生成口播稿，再生成配音与字幕。")
	}
	file, info, err := h.assets.OpenAsset(asset)
	if err != nil {
		return "", domain.Asset{}, "spoken_script_missing", errors.New("口播稿文件缺失，请重新生成口播稿。")
	}
	defer file.Close()
	if info.IsDir() {
		return "", domain.Asset{}, "spoken_script_missing", errors.New("口播稿文件缺失，请重新生成口播稿。")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNarrationScriptBytes))
	if err != nil {
		return "", domain.Asset{}, "spoken_script_missing", errors.New("口播稿无法读取。")
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", domain.Asset{}, "spoken_script_missing", errors.New("口播稿是空的。")
	}
	return string(data), asset, "", nil
}

// accountVoice reads the project account's voice override. A missing account
// or read error just keeps the global voice — narration must not fail on it.
func (h *narrationHandler) accountVoice(ctx context.Context, accountID string) *domain.VoiceOverride {
	if h.accounts == nil || strings.TrimSpace(accountID) == "" {
		return nil
	}
	account, err := h.accounts.Get(ctx, accountID)
	if err != nil {
		logging.LoggerFrom(ctx).Warn("account voice override unavailable, using global voice",
			"account_id", accountID, "error", err)
		return nil
	}
	if account.Overrides == nil {
		return nil
	}
	return account.Overrides.Voice
}

// produceRequest assembles the vendor call from stored settings. The API key
// stays inside the returned client and never reaches a response or a snapshot.
// A non-nil voice override replaces the global speaker for this project's account.
func (h *narrationHandler) produceRequest(ctx context.Context, script string, voice *domain.VoiceOverride) (narration.ProduceRequest, string, error) {
	if h.runtime == nil {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("配音服务未配置，请在设置的配音页填写 Aura Studio 凭据。")
	}
	runtime, err := h.runtime.Runtime(ctx)
	if err != nil {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("配音服务配置无法读取。")
	}
	if strings.TrimSpace(runtime.VolcSpeechAPIKey) == "" && strings.TrimSpace(runtime.AuraSTDTTsAPIKey) == "" {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置的配音页填写 Aura Studio API Key。")
	}
	if provider := chosenTTSProvider(runtime); provider == "aurastd" {
		if strings.TrimSpace(runtime.AuraSTDTTsAPIKey) == "" {
			return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置的配音页填写 Aura Studio API Key。")
		}
		voiceID := strings.TrimSpace(runtime.AuraSTDVoiceID)
		if voice != nil && strings.TrimSpace(voice.AuraSTDVoiceID) != "" {
			voiceID = strings.TrimSpace(voice.AuraSTDVoiceID)
		}
		if voiceID == "" {
			return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置的配音页填写克隆音色 ID。")
		}
		return narration.ProduceRequest{
			Script:          script,
			SpeakerID:       voiceID,
			Provider:        "aurastd",
			Model:           strings.TrimSpace(runtime.AuraSTDModel),
			Speed:           runtime.AuraSTDSpeed,
			Volume:          runtime.AuraSTDVolume,
			Pitch:           runtime.AuraSTDPitch,
			Emotion:         strings.TrimSpace(runtime.AuraSTDEmotion),
			LanguageBoost:   strings.TrimSpace(runtime.AuraSTDLanguageBoost),
			ModifyPitch:     runtime.AuraSTDModifyPitch,
			ModifyIntensity: runtime.AuraSTDModifyIntensity,
			ModifyTimbre:    runtime.AuraSTDModifyTimbre,
			SoundEffects:    strings.TrimSpace(runtime.AuraSTDSoundEffects),
		}, "", nil
	}
	if strings.TrimSpace(runtime.VolcSpeechAPIKey) == "" {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置中填写火山语音 API Key。")
	}
	speakerID := strings.TrimSpace(runtime.VolcSpeechSpeakerID)
	if voice != nil && strings.TrimSpace(voice.VolcSpeechSpeakerID) != "" {
		speakerID = strings.TrimSpace(voice.VolcSpeechSpeakerID)
	}
	if speakerID == "" {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置中填写火山音色 ID。")
	}
	return narration.ProduceRequest{Script: script, SpeakerID: speakerID, Provider: "volc"}, "", nil
}

type narrationOverrides struct {
	VoiceID   string   `json:"voice_id"`
	SpeakerID string   `json:"speaker_id"`
	Model     string   `json:"model"`
	Emotion   string   `json:"emotion"`
	Speed     *float64 `json:"speed"`
	Volume    *float64 `json:"volume"`
	Pitch     *int     `json:"pitch"`
}

func readNarrationOverrides(r *http.Request) narrationOverrides {
	var ov narrationOverrides
	if r.Body == nil {
		return ov
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&ov)
	return ov
}

func applyNarrationOverrides(req *narration.ProduceRequest, ov narrationOverrides) {
	if id := strings.TrimSpace(ov.VoiceID); id != "" {
		req.SpeakerID = id
	}
	if id := strings.TrimSpace(ov.SpeakerID); id != "" {
		req.SpeakerID = id
	}
	if model := strings.TrimSpace(ov.Model); model != "" {
		req.Model = model
	}
	if emotion := strings.TrimSpace(ov.Emotion); emotion != "" {
		req.Emotion = emotion
	}
	if ov.Speed != nil {
		req.Speed = *ov.Speed
	}
	if ov.Volume != nil {
		req.Volume = *ov.Volume
	}
	if ov.Pitch != nil {
		req.Pitch = *ov.Pitch
	}
}

func chosenTTSProvider(runtime consoleSettings.Runtime) string {
	provider := strings.TrimSpace(runtime.TTSProvider)
	if provider == "" {
		provider = "aurastd"
	}
	if provider == "aurastd" && strings.TrimSpace(runtime.AuraSTDTTsAPIKey) == "" &&
		strings.TrimSpace(runtime.VolcSpeechAPIKey) != "" && strings.TrimSpace(runtime.VolcSpeechSpeakerID) != "" {
		return "volc"
	}
	return provider
}

// registerAsset writes the generated bytes through the managed asset service and
// records the version. It mirrors the upload path, including removing a file
// whose registration is known not to have committed.
func (h *narrationHandler) registerAsset(ctx context.Context, projectID string, typ domain.AssetType, filename string, save func() (assets.SavedAsset, error)) (domain.Asset, int, string, error) {
	saved, err := save()
	if err != nil {
		return domain.Asset{}, http.StatusInternalServerError, "asset_save_failed", err
	}
	id := projectID
	asset := domain.Asset{
		ID: uuid.NewString(), ProjectID: &id, Type: typ, Path: saved.Path,
		Filename: safeFilename(filename), MIMEType: saved.MIMEType, Size: saved.Size,
		SHA256: saved.SHA256, Status: "active", CreatedAt: time.Now().UTC(),
	}
	state, err := h.repository.AddAsset(ctx, &asset)
	if err == nil {
		return asset, 0, "", nil
	}
	if state == store.CommitNotCommitted {
		if removeErr := os.Remove(saved.Path); removeErr != nil {
			logging.LoggerFrom(ctx).Error("remove uncommitted narration asset", "project_id", projectID, "asset_type", string(typ), "error", removeErr)
		}
	}
	if state == store.CommitUnknown {
		return domain.Asset{}, http.StatusServiceUnavailable, "asset_commit_unknown", errors.New("素材可能已登记，请刷新后再重试。")
	}
	return domain.Asset{}, http.StatusInternalServerError, "asset_store_failed", err
}

// NewNarrationProducer returns the production synthesis entry point. It reads
// the credentials fresh on every call so a settings change takes effect without
// a restart. Aura Studio is the default provider; Volcengine remains a fallback
// when only those credentials are configured.
// NewNarrationRequestBuilder 暴露按当前设置组配音请求的能力（音色、语速、账号覆盖），
// 给 AI 短片这类不经过 HTTP 层直接调配音的调用方用。
func NewNarrationRequestBuilder(runtime AssetRuntimeProvider) func(ctx context.Context, script string, voice *domain.VoiceOverride) (narration.ProduceRequest, error) {
	handler := &narrationHandler{runtime: runtime}
	return func(ctx context.Context, script string, voice *domain.VoiceOverride) (narration.ProduceRequest, error) {
		req, _, err := handler.produceRequest(ctx, script, voice)
		return req, err
	}
}

func NewNarrationProducer(runtime AssetRuntimeProvider) func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
	return func(ctx context.Context, request narration.ProduceRequest) (narration.Delivery, error) {
		if runtime == nil {
			return narration.Delivery{}, narration.ErrNotConfigured
		}
		settings, err := runtime.Runtime(ctx)
		if err != nil {
			return narration.Delivery{}, err
		}
		if chosenTTSProvider(settings) == "volc" {
			client := &narration.Client{
				APIKey:     strings.TrimSpace(settings.VolcSpeechAPIKey),
				ResourceID: strings.TrimSpace(settings.VolcSpeechResourceID),
			}
			return narration.Produce(ctx, client, request)
		}
		client := &narration.AuraSTDClient{
			BaseURL: strings.TrimSpace(settings.AuraSTDBaseURL),
			APIKey:  strings.TrimSpace(settings.AuraSTDTTsAPIKey),
		}
		return narration.Produce(ctx, client, request)
	}
}

// NewVolcengineProducer keeps the historical name for existing wiring.
func NewVolcengineProducer(runtime AssetRuntimeProvider) func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
	return NewNarrationProducer(runtime)
}
