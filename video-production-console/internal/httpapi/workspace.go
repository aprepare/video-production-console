package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"video-production-console/internal/workspace"
)

type workspaceHandler struct {
	store workspace.Store
}

func NewWorkspaceHandler(store workspace.Store) http.Handler {
	h := &workspaceHandler{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspace/tree", h.tree)
	mux.HandleFunc("GET /api/workspace/file", h.getFile)
	mux.HandleFunc("PUT /api/workspace/file", h.putFile)
	return mux
}

func (h *workspaceHandler) ready() (workspace.Store, error) {
	if strings.TrimSpace(h.store.Root) != "" {
		return h.store, nil
	}
	root, err := workspace.FindRoot("", workspace.DefaultStarts())
	if err != nil {
		return workspace.Store{}, err
	}
	return workspace.Store{Root: root}, nil
}

func (h *workspaceHandler) tree(w http.ResponseWriter, _ *http.Request) {
	store, err := h.ready()
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	tree, err := store.Tree()
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (h *workspaceHandler) getFile(w http.ResponseWriter, r *http.Request) {
	store, err := h.ready()
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	file, err := store.Read(r.URL.Query().Get("path"))
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (h *workspaceHandler) putFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path             string `json:"path"`
		Content          string `json:"content"`
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, 16<<20, &in); err != nil {
		writeDecodeError(w, err, "invalid_json", "请求格式不对。")
		return
	}
	if strings.TrimSpace(in.ExpectedRevision) == "" {
		writeError(w, http.StatusBadRequest, "workspace_revision_required", "请先重新读取文件，再保存修改。")
		return
	}
	store, err := h.ready()
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	if err := store.Write(in.Path, in.Content, in.ExpectedRevision); err != nil {
		writeWorkspaceError(w, err)
		return
	}
	file, err := store.Read(in.Path)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func writeWorkspaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspace.ErrConflict):
		writeError(w, http.StatusConflict, "workspace_conflict", "文件已被其他窗口或程序修改，当前草稿已保留。请先复制草稿，再重新读取文件并合并修改。")
	case errors.Is(err, workspace.ErrOutside), errors.Is(err, workspace.ErrKind):
		writeError(w, http.StatusBadRequest, "workspace_path_invalid", "只能读写二创工作区里的 markdown 或文本文件。")
	case errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "workspace_missing", "找不到二创工作区或这个文件。从项目根目录启动控制台。")
	case errors.Is(err, workspace.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "workspace_too_large", "文件太大。")
	default:
		writeError(w, http.StatusInternalServerError, "workspace_failed", "工作区读取失败。")
	}
}
