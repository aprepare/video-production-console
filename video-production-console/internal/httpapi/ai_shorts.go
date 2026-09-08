package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"video-production-console/internal/aishorts"
	"video-production-console/internal/domain"
	"video-production-console/internal/narration"
	consoleSettings "video-production-console/internal/settings"
)

type aiShortsHandler struct {
	svc     *aishorts.Service
	runtime aishorts.RuntimeProvider
}

// NewAIShortsHandler 建「AI 短片」路由，并把设置/配音/账号音色接进服务。
func NewAIShortsHandler(dataRoot string, runtime AssetRuntimeProvider, accounts accountStore) http.Handler {
	provider := func(ctx context.Context) (aishorts.Runtime, error) {
		rt, err := runtime.Runtime(ctx)
		if err != nil {
			return aishorts.Runtime{}, err
		}
		models := aishorts.DefaultModels()
		if m := strings.TrimSpace(os.Getenv("AI_SHORTS_IMAGE_MODEL")); m != "" {
			models.Image = m
		}
		if m := strings.TrimSpace(os.Getenv("AI_SHORTS_VIDEO_MODEL")); m != "" {
			models.Video = m
		}
		if m := strings.TrimSpace(os.Getenv("AI_SHORTS_TEXT_MODEL")); m != "" {
			models.Text = m
		} else if strings.TrimSpace(rt.GrokModel) != "" {
			models.Text = strings.TrimSpace(rt.GrokModel)
		}
		return aishorts.Runtime{
			BaseURL: rt.RemixBaseURL, APIKey: rt.RemixAPIKey, Models: models, JianyingRoot: rt.JianyingRoot,
			FFprobePath: rt.FFprobePath,
		}, nil
	}
	buildRequest := NewNarrationRequestBuilder(runtime)
	assembler := &aishorts.DraftAssembler{
		Produce: NewNarrationProducer(runtime),
		BuildRequest: func(ctx context.Context, script, accountID string) (narration.ProduceRequest, error) {
			var voice *domain.VoiceOverride
			if accounts != nil && strings.TrimSpace(accountID) != "" {
				if account, err := accounts.Get(ctx, accountID); err == nil && account.Overrides != nil {
					voice = account.Overrides.Voice
				}
			}
			return buildRequest(ctx, script, voice)
		},
		ScriptPath: aiShortsScriptPath(),
		Python:     "python",
		Brand:      NewAIShortsBrandResolver(dataRoot, runtime, accounts),
		AccountName: func(accountID string) string {
			if accounts == nil || strings.TrimSpace(accountID) == "" {
				return ""
			}
			account, err := accounts.Get(context.Background(), accountID)
			if err != nil {
				return ""
			}
			return account.Name
		},
	}
	h := &aiShortsHandler{svc: aishorts.NewService(dataRoot, provider, assembler), runtime: provider}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ai-shorts", h.list)
	mux.HandleFunc("GET /api/ai-shorts/styles", h.styles)
	mux.HandleFunc("GET /api/ai-shorts/style-preview-prompts", h.stylePreviewPrompts)
	mux.HandleFunc("GET /api/ai-shorts/media-settings", h.mediaSettings)
	mux.HandleFunc("PUT /api/ai-shorts/media-settings", h.saveMediaSettings)
	mux.HandleFunc("POST /api/ai-shorts", h.create)
	mux.HandleFunc("GET /api/ai-shorts/{id}", h.get)
	mux.HandleFunc("PATCH /api/ai-shorts/{id}", h.update)
	mux.HandleFunc("DELETE /api/ai-shorts/{id}", h.remove)
	mux.HandleFunc("POST /api/ai-shorts/{id}/storyboard", h.storyboard)
	mux.HandleFunc("POST /api/ai-shorts/{id}/generate", h.generate)
	mux.HandleFunc("PATCH /api/ai-shorts/{id}/shots/{index}", h.updateShot)
	mux.HandleFunc("POST /api/ai-shorts/{id}/shots/{index}/regenerate", h.regenerateShot)
	mux.HandleFunc("POST /api/ai-shorts/{id}/characters/{index}/regenerate", h.regenerateCharacter)
	mux.HandleFunc("POST /api/ai-shorts/{id}/assemble", h.assemble)
	mux.HandleFunc("POST /api/ai-shorts/{id}/cover", h.cover)
	mux.HandleFunc("GET /api/ai-shorts/{id}/asset", h.asset)
	return mux
}

// aiShortsScriptPath 找草稿构建脚本：优先可执行文件旁的 scripts/，其次工作目录。
func aiShortsScriptPath() string {
	rel := filepath.Join("scripts", "ai-shorts", "build_ai_short_draft.py")
	if exe, err := os.Executable(); err == nil {
		for _, base := range []string{filepath.Dir(exe), filepath.Dir(filepath.Dir(exe))} {
			if p := filepath.Join(base, rel); fileExists(p) {
				return p
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if p := filepath.Join(wd, rel); fileExists(p) {
			return p
		}
	}
	return rel
}

func fileExists(p string) bool {
	stat, err := os.Stat(p)
	return err == nil && !stat.IsDir()
}

func (h *aiShortsHandler) writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, aishorts.ErrNotFound):
		writeError(w, http.StatusNotFound, "ai_short_not_found", "短片不存在。")
	case errors.Is(err, aishorts.ErrBusy):
		writeError(w, http.StatusConflict, "ai_short_busy", err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, "ai_short_failed", err.Error())
	}
}

// styles 返回画风预设及文本/生图默认模型，前端显示真实的回退值。
func (h *aiShortsHandler) styles(w http.ResponseWriter, r *http.Request) {
	models := aishorts.DefaultModels()
	if rt, err := h.runtime(r.Context()); err == nil {
		if strings.TrimSpace(rt.Models.Text) != "" {
			models.Text = rt.Models.Text
		}
		if strings.TrimSpace(rt.Models.Image) != "" {
			models.Image = rt.Models.Image
		}
		if strings.TrimSpace(rt.Models.Video) != "" {
			models.Video = rt.Models.Video
		}
	}
	items := make([]aishorts.StylePreset, len(aishorts.ExplainerStyles))
	for i, s := range aishorts.ExplainerStyles {
		s.Preview = aishorts.StylePreviewPath(s.Key)
		items[i] = s
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "default_text_model": models.Text, "default_image_model": models.Image, "default_video_model": models.Video})
}

// stylePreviewPrompts 给 scripts/ai-shorts/render_style_previews.py 用：同一测试场景下每套画风的完整生图提示词，
// 走生产同一条链路（explainerImagePrompt），保证参考图和真实出图一致。?scene= 可换场景。
func (h *aiShortsHandler) stylePreviewPrompts(w http.ResponseWriter, r *http.Request) {
	scene := strings.TrimSpace(r.URL.Query().Get("scene"))
	if scene == "" {
		scene = "一位六十多岁的普通中国老人坐在家里的餐桌旁，面前摊着几张存折和银行卡，手边一副老花镜和一杯茶，老伴在旁边站着倒水，窗外是老小区的居民楼"
	}
	out := make([]map[string]string, 0, len(aishorts.ExplainerStyles))
	for _, s := range aishorts.ExplainerStyles {
		out = append(out, map[string]string{"key": s.Key, "name": s.Name, "prompt": aishorts.StylePreviewPrompt(s.Key, scene)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"scene": scene, "items": out})
}

func (h *aiShortsHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List()
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *aiShortsHandler) mediaSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.MediaSettings()
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
func (h *aiShortsHandler) saveMediaSettings(w http.ResponseWriter, r *http.Request) {
	var in aishorts.MediaSettingsInput
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_media_settings", "接口配置格式无效")
		return
	}
	settings, err := h.svc.UpdateMediaSettings(in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type aiShortTextInput struct {
	TextReasoningEffort *string `json:"text_reasoning_effort"`
	AccountID           string  `json:"account_id"`
	Mode                string  `json:"mode"`
	Title               string  `json:"title"`
	Story               string  `json:"story"`
	// Headline 顶部大标题。PATCH 时不传表示保留现值，传空串表示清空——
	// 之前是普通 string，只改画面设置的请求会把标题悄悄清掉（09-07 实测）。
	Headline *string `json:"headline"`
	Style    string  `json:"style"`
	// TextModel 拆分镜模型、SegmentModel 分大段模型；PATCH 时不传表示不改，传空串表示改回默认。
	TextModel      *string                  `json:"text_model"`
	SegmentModel   *string                  `json:"segment_model"`
	ImageModel     *string                  `json:"image_model"`
	VisualSettings *aishorts.VisualSettings `json:"visual_settings"`
}

func (h *aiShortsHandler) create(w http.ResponseWriter, r *http.Request) {
	var in aiShortTextInput
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_ai_short", "A valid short payload is required.")
		return
	}
	textModel, segmentModel, imageModel := "", "", ""
	if in.TextModel != nil {
		textModel = *in.TextModel
	}
	if in.SegmentModel != nil {
		segmentModel = *in.SegmentModel
	}
	if in.ImageModel != nil {
		imageModel = *in.ImageModel
	}
	effort := ""
	if in.TextReasoningEffort != nil {
		effort = *in.TextReasoningEffort
	}
	headline := ""
	if in.Headline != nil {
		headline = *in.Headline
	}
	short, err := h.svc.CreateWithReasoning(in.AccountID, in.Mode, in.Title, in.Story, headline, in.Style, textModel, segmentModel, imageModel, effort, in.VisualSettings)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, short)
}

func (h *aiShortsHandler) get(w http.ResponseWriter, r *http.Request) {
	short, err := h.svc.Get(r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, short)
}

func (h *aiShortsHandler) update(w http.ResponseWriter, r *http.Request) {
	var in aiShortTextInput
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_ai_short", "A valid short payload is required.")
		return
	}
	id := r.PathValue("id")
	headline := ""
	if in.Headline != nil {
		headline = *in.Headline
	} else if cur, err := h.svc.Get(id); err == nil {
		headline = cur.Headline
	}
	short, err := h.svc.UpdateTextWithReasoning(id, in.Title, in.Story, headline, in.Style, in.TextModel, in.SegmentModel, in.ImageModel, in.TextReasoningEffort, in.VisualSettings)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, short)
}

func (h *aiShortsHandler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.PathValue("id")); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *aiShortsHandler) storyboard(w http.ResponseWriter, r *http.Request) {
	short, err := h.svc.Storyboard(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, short)
}

func (h *aiShortsHandler) generate(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.GenerateAll(r.PathValue("id")); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

func (h *aiShortsHandler) updateShot(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_ai_short", "bad shot index")
		return
	}
	var in struct {
		Scene     string `json:"scene"`
		Motion    string `json:"motion"`
		Narration string `json:"narration"`
		Speaker   string `json:"speaker"`
		Seconds   int    `json:"seconds"`
		// 解说模式
		StyleKey     string                  `json:"style_key"`
		Subject      string                  `json:"subject"`
		Hero         *bool                   `json:"hero"`
		VisualIntent *string                 `json:"visual_intent"`
		SubjectType  *string                 `json:"subject_type"`
		CameraMove   *string                 `json:"camera_move"`
		Annotation   *string                 `json:"annotation"`
		Keywords     *[]aishorts.ShotKeyword `json:"keywords"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_ai_short", "A valid shot payload is required.")
		return
	}
	patch := aishorts.ShotPatch{
		Scene: in.Scene, Motion: in.Motion, Narration: in.Narration, Speaker: in.Speaker, Seconds: in.Seconds,
		StyleKey: in.StyleKey, Subject: in.Subject, Hero: in.Hero,
		VisualIntent: in.VisualIntent, SubjectType: in.SubjectType, CameraMove: in.CameraMove, Annotation: in.Annotation, Keywords: in.Keywords,
	}
	short, err := h.svc.UpdateShot(r.PathValue("id"), index, patch)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, short)
}

func (h *aiShortsHandler) regenerateShot(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_ai_short", "bad shot index")
		return
	}
	var in struct {
		Stage string `json:"stage"` // image | video
	}
	_ = decodeJSON(w, r, maxMessageJSONRequest, &in)
	stage := in.Stage
	if stage != "video" {
		stage = "image"
	}
	if err := h.svc.RegenerateShot(r.PathValue("id"), index, stage); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

func (h *aiShortsHandler) regenerateCharacter(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_ai_short", "bad character index")
		return
	}
	if err := h.svc.RegenerateCharacter(r.PathValue("id"), index); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

func (h *aiShortsHandler) assemble(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.AssembleAsync(r.PathValue("id")); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

func (h *aiShortsHandler) cover(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.GenerateCover(r.PathValue("id")); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

// asset 回传短片目录下的一个文件（角色图 / 分镜图 / 视频 / 配音），只允许目录内文件名。
func (h *aiShortsHandler) asset(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "" || name == "." || strings.HasPrefix(name, "..") {
		writeError(w, http.StatusBadRequest, "invalid_ai_short", "bad asset name")
		return
	}
	short, err := h.svc.Get(r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	path := filepath.Join(h.svc.Store().AssetDir(short.ID), name)
	if !fileExists(path) {
		writeError(w, http.StatusNotFound, "ai_short_asset_missing", "文件不存在。")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

var _ = consoleSettings.Runtime{}
