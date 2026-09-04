package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/bgmlibrary"
	"video-production-console/internal/jianyingdraft"
	consoleSettings "video-production-console/internal/settings"
)

type jianyingRuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

// jianyingDraftsHandler exposes the operator's local Jianying drafts so an
// account can copy the typography of a draft styled by hand:
// list drafts → pick one → extracted MontageStyle preview → save as override.
type jianyingDraftsHandler struct {
	runtime jianyingRuntimeProvider
}

func NewJianyingDraftsHandler(runtime jianyingRuntimeProvider) http.Handler {
	h := &jianyingDraftsHandler{runtime: runtime}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/jianying-drafts", h.list)
	mux.HandleFunc("POST /api/jianying-drafts/extract-style", h.extract)
	mux.HandleFunc("POST /api/jianying-drafts/import-bgm", h.importBGM)
	return mux
}

// importBGM copies the draft's library music out of the Jianying cache into
// the console BGM directory, rescans, and returns the track ID to select.
func (h *jianyingDraftsHandler) importBGM(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_jianying_draft", "A draft name is required.")
		return
	}
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return
	}
	root := strings.TrimSpace(runtime.JianyingRoot)
	bgmDir := effectiveBGMDir(runtime)
	if root == "" {
		writeError(w, http.StatusConflict, "jianying_root_not_configured", "请先在设置里填写剪映草稿目录。")
		return
	}
	content, err := jianyingdraft.LoadContent(r.Context(), root, strings.TrimSpace(in.Name), jianyingdraft.FindDecryptTool(runtime.DataRoot))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "jianying_draft_unreadable", "草稿读取失败："+err.Error())
		return
	}
	name, src := jianyingdraft.DraftBGM(content)
	if src == "" {
		writeError(w, http.StatusNotFound, "bgm_cache_missing", "草稿的 BGM 缓存文件不在这台电脑上。")
		return
	}
	if err := os.MkdirAll(bgmDir, 0o755); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "bgm_dir_unwritable", "BGM 目录不可写："+err.Error())
		return
	}
	dst := filepath.Join(bgmDir, safeBGMFileName(name)+filepath.Ext(src))
	if _, statErr := os.Stat(dst); statErr != nil {
		if err := copyFile(src, dst); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "bgm_copy_failed", "复制 BGM 失败："+err.Error())
			return
		}
	}
	index, scanErr := bgmlibrary.Rescan(context.Background(), bgmDir, effectiveFFmpeg(runtime), bgmIndexPath(runtime.DataRoot))
	for _, track := range index.Tracks {
		if filepath.Clean(track.Path) == filepath.Clean(dst) {
			writeJSON(w, http.StatusOK, map[string]any{"bgm_id": track.ID, "name": track.Name, "path": dst})
			return
		}
	}
	msg := "曲子已复制到 BGM 目录，但分析没有成功。"
	if scanErr != nil {
		msg += " " + scanErr.Error()
	}
	writeError(w, http.StatusUnprocessableEntity, "bgm_analyze_failed", msg)
}

func safeBGMFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "jianying-bgm"
	}
	replacer := strings.NewReplacer(`\`, "_", "/", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

type jianyingDraftsView struct {
	Root       string                    `json:"root"`
	Drafts     []jianyingdraft.DraftInfo `json:"drafts"`
	DecryptOK  bool                      `json:"decrypt_tool_available"`
	DecryptTip string                    `json:"decrypt_tip,omitempty"`
}

func (h *jianyingDraftsHandler) list(w http.ResponseWriter, r *http.Request) {
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return
	}
	root := strings.TrimSpace(runtime.JianyingRoot)
	if root == "" {
		writeError(w, http.StatusConflict, "jianying_root_not_configured", "请先在设置里填写剪映草稿目录。")
		return
	}
	drafts, err := jianyingdraft.ListDrafts(root)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "jianying_root_unreadable", "剪映草稿目录读取失败："+err.Error())
		return
	}
	view := jianyingDraftsView{Root: root, Drafts: drafts}
	if jianyingdraft.FindDecryptTool(runtime.DataRoot) != "" {
		view.DecryptOK = true
	} else {
		view.DecryptTip = "未找到 jy-draftc.exe：把它放到数据目录 tools/ 下，或设置环境变量 JY_DRAFTC_PATH；加密草稿才需要它。"
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *jianyingDraftsHandler) extract(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, maxMessageJSONRequest, &in); err != nil {
		writeDecodeError(w, err, "invalid_jianying_draft", "A draft name is required.")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_jianying_draft", "A draft name is required.")
		return
	}
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return
	}
	root := strings.TrimSpace(runtime.JianyingRoot)
	if root == "" {
		writeError(w, http.StatusConflict, "jianying_root_not_configured", "请先在设置里填写剪映草稿目录。")
		return
	}
	content, err := jianyingdraft.LoadContent(r.Context(), root, name, jianyingdraft.FindDecryptTool(runtime.DataRoot))
	if err != nil {
		if errors.Is(err, jianyingdraft.ErrNoDecryptTool) {
			writeError(w, http.StatusConflict, "decrypt_tool_missing", "这个草稿是加密的，需要 jy-draftc.exe 才能读取。")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "jianying_draft_unreadable", "草稿读取失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jianyingdraft.ExtractStyle(content))
}
