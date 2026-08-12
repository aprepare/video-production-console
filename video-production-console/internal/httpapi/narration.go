package httpapi

import (
	"bytes"
	"context"
	"database/sql"
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

// NarrationHandlerOptions supplies the credentials source and, for tests, a
// replacement for the vendor call.
type NarrationHandlerOptions struct {
	Runtime AssetRuntimeProvider
	Produce func(context.Context, narration.ProduceRequest) (narration.Delivery, error)
}

type narrationHandler struct {
	repository narrationStore
	assets     *assets.Service
	runtime    AssetRuntimeProvider
	produce    func(context.Context, narration.ProduceRequest) (narration.Delivery, error)
	inFlight   sync.Map
}

// NewNarrationHandler serves narration and subtitle generation for one project.
// Synthesis is a deterministic vendor call rather than agent work, so it runs in
// this process and registers its output through the same asset path an operator
// upload takes.
func NewNarrationHandler(db *sql.DB, service *assets.Service, options NarrationHandlerOptions) http.Handler {
	return newNarrationHandler(store.NewProjectRepository(db), service, options)
}

func newNarrationHandler(repository narrationStore, service *assets.Service, options NarrationHandlerOptions) http.Handler {
	h := &narrationHandler{repository: repository, assets: service, runtime: options.Runtime, produce: options.Produce}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects/{id}/narration", h.create)
	return mux
}

type narrationView struct {
	Narration        assetView `json:"narration"`
	SubtitleSRT      assetView `json:"subtitle_srt"`
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
	if _, err := h.repository.GetProject(r.Context(), id); errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, sql.ErrNoRows) {
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

	script, code, err := h.readContinuousScript(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusConflict, code, err.Error())
		return
	}
	request, code, err := h.produceRequest(r.Context(), script)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, code, err.Error())
		return
	}
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
	if syncer, ok := h.repository.(interface {
		SyncStageFromAssets(context.Context, string, time.Time) (domain.Project, error)
	}); ok {
		if _, syncErr := syncer.SyncStageFromAssets(r.Context(), id, time.Now().UTC()); syncErr != nil {
			logging.LoggerFrom(r.Context()).Error("sync project stage after narration", "project_id", id, "error", syncErr)
		}
	}
	writeJSON(w, http.StatusCreated, narrationView{
		Narration: toAssetView(audio), SubtitleSRT: toAssetView(subtitle),
		Captions: len(delivery.Captions), DurationSeconds: delivery.Duration,
		BilledCharacters: delivery.BilledWords, Warnings: delivery.Report.Warnings,
	})
}

func (h *narrationHandler) synthesize(ctx context.Context, request narration.ProduceRequest) (narration.Delivery, error) {
	if h.produce != nil {
		return h.produce(ctx, request)
	}
	return narration.Delivery{}, errors.New("narration synthesis is not wired")
}

// readContinuousScript returns the text of the current continuous script. The
// script is the only input: word timings come back from synthesis, so no
// separate transcription step can disagree with it.
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

// produceRequest assembles the vendor call from stored settings. The API key
// stays inside the returned client and never reaches a response or a snapshot.
func (h *narrationHandler) produceRequest(ctx context.Context, script string) (narration.ProduceRequest, string, error) {
	if h.runtime == nil {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("配音服务未配置，请在设置中填写火山语音凭据。")
	}
	runtime, err := h.runtime.Runtime(ctx)
	if err != nil {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("配音服务配置无法读取。")
	}
	if strings.TrimSpace(runtime.VolcSpeechAPIKey) == "" {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置中填写火山语音 API Key。")
	}
	if strings.TrimSpace(runtime.VolcSpeechSpeakerID) == "" {
		return narration.ProduceRequest{}, "narration_not_configured", errors.New("请先在设置中填写火山音色 ID。")
	}
	return narration.ProduceRequest{Script: script, SpeakerID: strings.TrimSpace(runtime.VolcSpeechSpeakerID)}, "", nil
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

// NewVolcengineProducer returns the production synthesis entry point. It reads
// the credentials fresh on every call so a settings change takes effect without
// a restart.
func NewVolcengineProducer(runtime AssetRuntimeProvider) func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
	return func(ctx context.Context, request narration.ProduceRequest) (narration.Delivery, error) {
		if runtime == nil {
			return narration.Delivery{}, narration.ErrNotConfigured
		}
		settings, err := runtime.Runtime(ctx)
		if err != nil {
			return narration.Delivery{}, err
		}
		client := &narration.Client{
			APIKey:     strings.TrimSpace(settings.VolcSpeechAPIKey),
			ResourceID: strings.TrimSpace(settings.VolcSpeechResourceID),
		}
		return narration.Produce(ctx, client, request)
	}
}
