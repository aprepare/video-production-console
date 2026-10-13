package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"video-production-console/internal/remixlab"
)

const maxRemixStyleRequestSize int64 = 4 << 20

func (h *remixLabHandler) listStyles(w http.ResponseWriter, _ *http.Request) {
	styles, err := h.prompts.ListStyles()
	if err != nil {
		writeRemixStyleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"styles": styles})
}

func (h *remixLabHandler) upsertStyle(w http.ResponseWriter, r *http.Request) {
	var in remixlab.StyleTemplate
	if err := decodeJSON(w, r, maxRemixStyleRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_style", "风格模板格式无效。")
		return
	}
	saved, err := h.prompts.UpsertStyle(in)
	if err != nil {
		writeRemixStyleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *remixLabHandler) deleteStyle(w http.ResponseWriter, r *http.Request) {
	if err := h.prompts.DeleteStyle(r.PathValue("id")); err != nil {
		writeRemixStyleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *remixLabHandler) applyWorkflowStyle(w http.ResponseWriter, r *http.Request) {
	accountID, ok := remixWorkflowAccountID(w, r)
	if !ok {
		return
	}
	var in struct {
		StyleID string          `json:"style_id"`
		Brief   *remixlab.Brief `json:"brief"`
	}
	if err := decodeJSON(w, r, maxRemixStyleRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_style", "风格应用请求格式无效。")
		return
	}
	in.StyleID = strings.TrimSpace(in.StyleID)
	if in.StyleID == "" {
		writeError(w, http.StatusBadRequest, "invalid_style", "style_id 不能为空。")
		return
	}
	if in.Brief != nil {
		normalized, err := remixlab.NormalizeBrief(*in.Brief)
		if err != nil {
			writeRemixStyleError(w, err)
			return
		}
		in.Brief = &normalized
	}
	view, err := h.prompts.ApplyStyleForAccount(accountID, in.StyleID, in.Brief)
	if err != nil {
		writeRemixStyleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *remixLabHandler) updateWorkflowBrief(w http.ResponseWriter, r *http.Request) {
	accountID, ok := remixWorkflowAccountID(w, r)
	if !ok {
		return
	}
	var in remixlab.Brief
	if err := decodeJSON(w, r, maxRemixStyleRequestSize, &in); err != nil {
		writeDecodeError(w, err, "invalid_brief", "内容简报格式无效。")
		return
	}
	normalized, err := remixlab.NormalizeBrief(in)
	if err != nil {
		writeRemixStyleError(w, err)
		return
	}
	view, err := h.prompts.UpdateBriefForAccount(accountID, normalized)
	if err != nil {
		writeRemixStyleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func remixWorkflowAccountID(w http.ResponseWriter, r *http.Request) (string, bool) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "invalid_account_id", "account_id 不能为空。")
		return "", false
	}
	parsed, err := uuid.Parse(accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_account_id", "Account ID must be a UUID.")
		return "", false
	}
	return parsed.String(), true
}

func writeRemixStyleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, remixlab.ErrInvalidStyle):
		writeError(w, http.StatusBadRequest, "invalid_style", err.Error())
	case errors.Is(err, remixlab.ErrInvalidBrief):
		writeError(w, http.StatusBadRequest, "invalid_brief", err.Error())
	case errors.Is(err, remixlab.ErrStyleNotFound):
		writeError(w, http.StatusNotFound, "style_not_found", "风格模板不存在。")
	case errors.Is(err, remixlab.ErrBuiltinStyle):
		writeError(w, http.StatusConflict, "builtin_style", "内置风格不能删除。")
	default:
		writeRemixLabError(w, err)
	}
}
