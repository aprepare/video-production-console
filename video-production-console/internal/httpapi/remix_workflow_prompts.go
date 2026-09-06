package httpapi

import (
	"net/http"
	"video-production-console/internal/remixlab"
)

func (h *remixLabHandler) getWorkflowPrompts(w http.ResponseWriter, r *http.Request) {
	view, err := h.prompts.WorkflowPromptsForAccount(r.URL.Query().Get("account_id"))
	if err != nil {
		writeRemixLabError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) putWorkflowPrompts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Workflow remixlab.Workflow `json:"workflow"`
	}
	if err := decodeJSON(w, r, maxRemixLabRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_remix_lab", "提示词格式无效")
		return
	}
	accountID := r.URL.Query().Get("account_id")
	if _, err := h.svc.SaveWorkflowDefinitionForAccount(accountID, in.Workflow); err != nil {
		writeRemixLabError(w, err)
		return
	}
	h.getWorkflowPrompts(w, r)
}
