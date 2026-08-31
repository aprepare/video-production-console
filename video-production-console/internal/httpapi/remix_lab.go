package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/remixlab"
	"video-production-console/internal/store"
)

// scenicProjectLookup is reserved for adopt (Task 6); Task 5 only accepts it for wiring.
type scenicProjectLookup interface {
	GetProject(context.Context, string) (domain.Project, error)
}

type remixLabHandler struct {
	svc      *remixlab.Service
	projects scenicProjectLookup
	prompts  remixlab.Store
}

func NewRemixLabHandler(svc *remixlab.Service, projects scenicProjectLookup) http.Handler {
	h := &remixLabHandler{svc: svc, projects: projects, prompts: remixlab.Store{DataRoot: svc.DataRoot()}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/remix-lab/defaults", h.defaults)
	mux.HandleFunc("PUT /api/remix-lab/presets", h.savePresets)
	mux.HandleFunc("GET /api/remix-lab/experiments", h.list)
	mux.HandleFunc("POST /api/remix-lab/experiments", h.create)
	mux.HandleFunc("GET /api/remix-lab/experiments/{id}", h.get)
	mux.HandleFunc("DELETE /api/remix-lab/experiments/{id}", h.delete)
	mux.HandleFunc("PATCH /api/remix-lab/runs/{id}", h.patchComment)
	mux.HandleFunc("POST /api/remix-lab/runs/{id}/adopt", h.adopt)
	mux.HandleFunc("PUT /api/remix-lab/runs/{id}/package", h.putPackage)
	mux.HandleFunc("POST /api/remix-lab/runs/{id}/rework", h.rework)
	mux.HandleFunc("POST /api/remix-lab/runs/{id}/retry", h.retryRun)
	mux.HandleFunc("POST /api/remix-lab/runs/{id}/produce", h.produceRun)
	mux.HandleFunc("GET /api/remix-lab/productions/by-project/{projectID}", h.productionByProject)
	mux.HandleFunc("GET /api/remix-lab/runs/{id}/stages", h.runStages)
	mux.HandleFunc("GET /api/remix-lab/prompts", h.listPrompts)
	mux.HandleFunc("POST /api/remix-lab/prompts", h.upsertPrompt)
	mux.HandleFunc("PUT /api/remix-lab/prompts/{id}", h.updatePrompt)
	mux.HandleFunc("DELETE /api/remix-lab/prompts/{id}", h.deletePrompt)
	mux.HandleFunc("GET /api/remix-lab/active-prompt", h.getActive)
	mux.HandleFunc("PUT /api/remix-lab/active-prompt", h.putActive)
	mux.HandleFunc("GET /api/remix-lab/agent-settings", h.getAgentSettings)
	mux.HandleFunc("PUT /api/remix-lab/agent-settings", h.putAgentSettings)
	mux.HandleFunc("GET /api/remix-lab/agent-prompts", h.getAgentPrompts)
	mux.HandleFunc("PUT /api/remix-lab/agent-prompts", h.putAgentPrompts)
	mux.HandleFunc("GET /api/remix-lab/workflow", h.getWorkflow)
	mux.HandleFunc("PUT /api/remix-lab/workflow", h.putWorkflow)
	mux.HandleFunc("POST /api/remix-lab/workflow/run", h.runWorkflow)
	mux.HandleFunc("POST /api/remix-lab/agent/chat", h.agentChat)
	mux.HandleFunc("GET /api/remix-lab/agent/last", h.getAgentLast)
	mux.HandleFunc("GET /api/remix-lab/agent/history", h.getAgentHistory)
	mux.HandleFunc("DELETE /api/remix-lab/agent/history", h.clearAgentHistory)
	mux.HandleFunc("POST /api/remix-lab/agent/confirm", h.agentConfirm)
	return mux
}

func (h *remixLabHandler) defaults(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.Defaults(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "remix_lab_defaults_failed", "Remix lab defaults could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListExperiments(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "remix_lab_list_failed", "Remix lab experiments could not be listed.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type remixLabSlotPOST struct {
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	APIKey          string `json:"api_key"`
	ReasoningEffort string `json:"reasoning_effort"`
	Pipeline        string `json:"pipeline"`
	RunCount        int    `json:"run_count"`
	PresetIndex     *int   `json:"preset_index"`
}

type remixLabCreatePOST struct {
	Source    string             `json:"source"`
	Slots     []remixLabSlotPOST `json:"slots"`
	PromptIDs []string           `json:"prompt_ids"`
}

// savePresets 把模型配置弹窗的槽位草稿存成预设，「完成」即保存，防止刷新丢配置。
func (h *remixLabHandler) savePresets(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slots []remixLabSlotPOST `json:"slots"`
	}
	if err := decodeJSON(w, r, maxNormalJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid slots payload is required.")
		return
	}
	slots := make([]remixlab.SlotInput, 0, len(in.Slots))
	for _, slot := range in.Slots {
		slots = append(slots, remixlab.SlotInput{
			BaseURL:         slot.BaseURL,
			Model:           slot.Model,
			APIKey:          slot.APIKey,
			ReasoningEffort: slot.ReasoningEffort,
			Pipeline:        slot.Pipeline,
			RunCount:        slot.RunCount,
			PresetIndex:     slot.PresetIndex,
		})
	}
	view, err := h.svc.SavePresets(r.Context(), slots)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) create(w http.ResponseWriter, r *http.Request) {
	var in remixLabCreatePOST
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid remix lab experiment is required.")
		return
	}
	slots := make([]remixlab.SlotInput, 0, len(in.Slots))
	for _, slot := range in.Slots {
		slots = append(slots, remixlab.SlotInput{
			BaseURL:         slot.BaseURL,
			Model:           slot.Model,
			APIKey:          slot.APIKey,
			ReasoningEffort: slot.ReasoningEffort,
			Pipeline:        slot.Pipeline,
			RunCount:        slot.RunCount,
			PresetIndex:     slot.PresetIndex,
		})
	}
	exp, err := h.svc.CreateExperimentWithPrompts(r.Context(), in.Source, slots, in.PromptIDs)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, exp)
}

func (h *remixLabHandler) get(w http.ResponseWriter, r *http.Request) {
	exp, err := h.svc.GetExperiment(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exp)
}

func (h *remixLabHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteExperiment(r.Context(), r.PathValue("id")); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) patchComment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Comment string `json:"comment"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid comment payload is required.")
		return
	}
	if err := h.svc.PatchComment(r.Context(), r.PathValue("id"), in.Comment); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) adopt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid adopt payload is required.")
		return
	}
	if err := h.svc.Adopt(r.Context(), r.PathValue("id"), in.ProjectID); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// getWorkflow 返回当前工作流定义（画布编辑用）。
func (h *remixLabHandler) getWorkflow(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.WorkflowForAccount(strings.TrimSpace(r.URL.Query().Get("account_id")))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// putWorkflow 保存画布编辑后的工作流（含拓扑校验）。带 account_id 时写入该账号最新一版。
func (h *remixLabHandler) putWorkflow(w http.ResponseWriter, r *http.Request) {
	var in remixlab.Workflow
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid workflow payload is required.")
		return
	}
	saved, err := h.svc.SaveWorkflowDefinitionForAccount(strings.TrimSpace(r.URL.Query().Get("account_id")), in)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// runWorkflow 用当前工作流开跑一个实验（可带生产账号与全自动开关）。
func (h *remixLabHandler) runWorkflow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source    string `json:"source"`
		RunCount  int    `json:"run_count"`
		AccountID string `json:"account_id"`
		Auto      bool   `json:"auto"`
	}
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid run payload is required.")
		return
	}
	exp, err := h.svc.CreateWorkflowExperiment(r.Context(), in.Source, in.RunCount, in.AccountID, in.Auto)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, exp)
}

// produceRun 确认闸门放行 / 生产失败续跑：定稿进混剪链路（建项目→口播→配音→混剪）。
func (h *remixLabHandler) produceRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid produce payload is required.")
		return
	}
	if err := h.svc.StartProduction(r.Context(), r.PathValue("id"), in.AccountID); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) productionByProject(w http.ResponseWriter, r *http.Request) {
	link, err := h.svc.ProductionByProject(r.Context(), r.PathValue("projectID"))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// retryRun 断点重试：失败运行整体重试（已有agent产物复用），或指定节点
// 作废重跑，写手链路重做后自动续走后面的环节。
func (h *remixLabHandler) retryRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NodeID string `json:"node_id"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid retry payload is required.")
		return
	}
	if err := h.svc.RetryRun(r.Context(), r.PathValue("id"), in.NodeID); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

// runStages 返回一次运行的工作流分解（节点+连线+各阶段实际输入输出）。
func (h *remixLabHandler) runStages(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.RunStages(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// getAgentPrompts 返回四路agent（钩子/事实/弹药/审稿）系统提示词编辑视图。
func (h *remixLabHandler) getAgentPrompts(w http.ResponseWriter, _ *http.Request) {
	view, err := h.svc.AgentPromptsView()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "remix_lab_failed", "Agent提示词读取失败。")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// putAgentPrompts 保存agent提示词：与默认一致的字段自动回落为跟随默认。
func (h *remixLabHandler) putAgentPrompts(w http.ResponseWriter, r *http.Request) {
	var in remixlab.AgentPrompts
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid agent prompts payload is required.")
		return
	}
	view, err := h.svc.SaveAgentPrompts(in)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// putPackage 保存操作员在创作台编辑后的定稿（正文+发布包字段）。
func (h *remixLabHandler) putPackage(w http.ResponseWriter, r *http.Request) {
	var in remixlab.PackageInput
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid package payload is required.")
		return
	}
	if err := h.svc.UpdateRunPackage(r.Context(), r.PathValue("id"), in); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// rework 把操作员批注交给审稿agent返工，异步执行，前端靠实验轮询拿结果。
func (h *remixLabHandler) rework(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Annotations string `json:"annotations"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid rework payload is required.")
		return
	}
	if err := h.svc.Rework(r.Context(), r.PathValue("id"), in.Annotations); err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}

func writeRemixLabError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrRemixLabNotFound):
		writeError(w, http.StatusNotFound, "remix_lab_not_found", "Remix lab record was not found.")
	case errors.Is(err, store.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "The project was not found.")
	case errors.Is(err, remixlab.ErrMissingAPIKey):
		writeError(w, http.StatusBadRequest, "remix_key_missing", err.Error())
	case errors.Is(err, remixlab.ErrRunNotAdoptable):
		writeError(w, http.StatusBadRequest, "run_not_adoptable", err.Error())
	case errors.Is(err, remixlab.ErrRunNotReworkable):
		writeError(w, http.StatusConflict, "run_not_reworkable", "只有已完成的运行才能编辑或打回重做。")
	case errors.Is(err, remixlab.ErrRunNotRetryable):
		writeError(w, http.StatusConflict, "run_not_retryable", "失败的运行可整体重试；已完成的运行要指定重跑哪个agent节点。")
	case errors.Is(err, remixlab.ErrInvalidAnnotations):
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", "打回必须附批注，且不超过2000字。")
	case errors.Is(err, remixlab.ErrInvalidPackage):
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", "正文不能为空（至少40字），且第1条短标题（板标题）必填。")
	case errors.Is(err, remixlab.ErrInvalidAgentPrompts):
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", "单条Agent提示词不能超过2万字。")
	case errors.Is(err, remixlab.ErrInvalidWorkflow):
		writeError(w, http.StatusBadRequest, "invalid_workflow", err.Error())
	case errors.Is(err, remixlab.ErrProductionAccountRequired):
		writeError(w, http.StatusBadRequest, "produce_account_required", "先选择要进哪个账号的混剪。")
	case errors.Is(err, remixlab.ErrInvalidAutoProduce):
		writeError(w, http.StatusBadRequest, "invalid_auto_produce", "全自动需要选好账号且运行次数为1。")
	case errors.Is(err, remixlab.ErrProductionActive):
		writeError(w, http.StatusConflict, "production_active", "生产已在进行中。")
	case errors.Is(err, remixlab.ErrProductionDone):
		writeError(w, http.StatusConflict, "production_done", "这条运行已经生产完成，去项目里看草稿。")
	case errors.Is(err, remixlab.ErrRunNotProducible):
		writeError(w, http.StatusConflict, "run_not_producible", "只有已完成且有定稿的运行才能进混剪。")
	case errors.Is(err, remixlab.ErrProductionUnavailable):
		writeError(w, http.StatusServiceUnavailable, "production_unavailable", "生产驱动器未启用。")
	case errors.Is(err, remixlab.ErrAdoptUnavailable):
		writeError(w, http.StatusInternalServerError, "adopt_unavailable", err.Error())
	case errors.Is(err, remixlab.ErrInvalidSource),
		errors.Is(err, remixlab.ErrInvalidSlots),
		errors.Is(err, remixlab.ErrInvalidPrompts),
		errors.Is(err, remixlab.ErrInvalidRunCount),
		errors.Is(err, remixlab.ErrMissingModel),
		errors.Is(err, remixlab.ErrInvalidComment),
		errors.Is(err, remixlab.ErrAgentModel),
		errors.Is(err, remixlab.ErrUnknownProposal):
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", err.Error())
	case errors.Is(err, remixlab.ErrEmptyAgentReply):
		writeError(w, http.StatusBadGateway, "agent_empty_reply", "智能体没有返回可用正文。请再发一次。")
	case errors.Is(err, remixlab.ErrAgentUpstream):
		if isAgentGatewayTimeout(err) {
			writeError(w, http.StatusBadGateway, "agent_upstream", "上游网关 504：后台那次其实可能已经跑完，只是中转没等到完整结果。请再发一次。")
			return
		}
		detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), remixlab.ErrAgentUpstream.Error()+": "))
		if detail == "" {
			detail = "上游没有返回可用结果。"
		}
		writeError(w, http.StatusBadGateway, "agent_upstream", "智能体请求失败："+detail)
	case errors.Is(err, remixlab.ErrProposalNotFound):
		writeError(w, http.StatusNotFound, "remix_lab_not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "remix_lab_failed", "Remix lab request could not be completed.")
	}
}

func isAgentGatewayTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "504") || strings.Contains(strings.ToLower(msg), "gateway timeout")
}

const maxRemixLabRequestSize = 512 << 10

func (h *remixLabHandler) listPrompts(w http.ResponseWriter, _ *http.Request) {
	out, err := h.prompts.ListLibrary()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "prompt_library_failed", "提示词库读取失败。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": out})
}

func (h *remixLabHandler) upsertPrompt(w http.ResponseWriter, r *http.Request) {
	var in remixlab.PromptTemplate
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "请求格式无效。")
		return
	}
	saved, err := h.prompts.UpsertPrompt(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *remixLabHandler) updatePrompt(w http.ResponseWriter, r *http.Request) {
	var in remixlab.PromptTemplate
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "请求格式无效。")
		return
	}
	in.ID = r.PathValue("id")
	saved, err := h.prompts.UpsertPrompt(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *remixLabHandler) deletePrompt(w http.ResponseWriter, r *http.Request) {
	if err := h.prompts.DeletePrompt(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_remix_lab", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) getActive(w http.ResponseWriter, _ *http.Request) {
	active, ok, err := h.prompts.GetActive()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "active_prompt_read_failed", "当前采用的提示词读取失败。")
		return
	}
	if !ok {
		base := remixlab.ResolvePrompt("elder_stable", "", "")
		writeJSON(w, http.StatusOK, map[string]any{
			"active": false,
			"prompt": remixlab.ActivePrompt{
				ID: base.ID, Name: base.Name, Stamp: base.Stamp, Style: base.Style,
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "prompt": active})
}

func (h *remixLabHandler) putActive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		System string `json:"system"`
		User   string `json:"user"`
		Clear  bool   `json:"clear"`
	}
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "请求格式无效。")
		return
	}
	if body.Clear || (strings.TrimSpace(body.ID) == "" && strings.TrimSpace(body.System) == "" && strings.TrimSpace(body.User) == "") {
		if err := h.prompts.SetActive(remixlab.ActivePrompt{}); err != nil {
			writeError(w, http.StatusInternalServerError, "active_prompt_clear_failed", "恢复默认提示词失败。")
			return
		}
		base := remixlab.ResolvePrompt("elder_stable", "", "")
		writeJSON(w, http.StatusOK, map[string]any{
			"active": false,
			"prompt": remixlab.ActivePrompt{
				ID: base.ID, Name: base.Name, Stamp: base.Stamp, Style: base.Style,
			},
		})
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		id = "elder_stable"
	}
	var active remixlab.ActivePrompt
	var err error
	if strings.TrimSpace(body.System) != "" || strings.TrimSpace(body.User) != "" {
		active, err = h.prompts.AdoptFromTemplate(id, body.System, body.User)
	} else {
		active, err = h.prompts.AdoptPrompt(id)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "active_prompt_save_failed", "保存系统提示词失败。")
		return
	}
	hasCustom := strings.TrimSpace(active.System) != "" || strings.TrimSpace(active.User) != ""
	writeJSON(w, http.StatusOK, map[string]any{"active": hasCustom, "prompt": active})
}

func (h *remixLabHandler) getAgentSettings(w http.ResponseWriter, _ *http.Request) {
	view, err := h.svc.GetAgentSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agent_settings_failed", "智能体设置读取失败。")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) putAgentSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model           string `json:"model"`
		BaseURL         string `json:"base_url"`
		ReasoningEffort string `json:"reasoning_effort"`
		APIKey          string `json:"api_key"`
		ClearAPIKey     bool   `json:"clear_api_key"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid agent settings payload is required.")
		return
	}
	view, err := h.svc.PutAgentSettings(remixlab.AgentSettingsInput{
		Model:           in.Model,
		BaseURL:         in.BaseURL,
		ReasoningEffort: in.ReasoningEffort,
		APIKey:          in.APIKey,
		ClearAPIKey:     in.ClearAPIKey,
	})
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) agentChat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message      string `json:"message"`
		ExperimentID string `json:"experiment_id"`
	}
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid agent message is required.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), remixlab.AgentChatTimeout)
	defer cancel()
	out, err := h.svc.AgentChat(ctx, in.Message, in.ExperimentID)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *remixLabHandler) getAgentHistory(w http.ResponseWriter, _ *http.Request) {
	turns, err := h.svc.GetAgentHistory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agent_history_failed", "智能体对话历史读取失败。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}

func (h *remixLabHandler) clearAgentHistory(w http.ResponseWriter, _ *http.Request) {
	if err := h.svc.ClearAgentHistory(); err != nil {
		writeError(w, http.StatusInternalServerError, "agent_history_failed", "智能体对话历史清空失败。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) getAgentLast(w http.ResponseWriter, _ *http.Request) {
	view, ok, err := h.svc.GetAgentLast()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agent_last_failed", "智能体上一轮结果读取失败。")
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": true, "last": view})
}

func (h *remixLabHandler) agentConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProposalID string `json:"proposal_id"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "A valid confirm payload is required.")
		return
	}
	out, err := h.svc.ConfirmProposal(r.Context(), in.ProposalID)
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
