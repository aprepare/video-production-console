package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type assetContentStore interface {
	GetAsset(rctx context.Context, id string) (domain.Asset, error)
}

type assetContentHandler struct {
	repository assetContentStore
	assets     *assets.Service
}

// NewAssetsHandler serves immutable, database-addressed asset bytes.
func NewAssetsHandler(db *sql.DB, service *assets.Service) http.Handler {
	return newAssetsHandler(store.NewProjectRepository(db), service)
}

func newAssetsHandler(repository assetContentStore, service *assets.Service) http.Handler {
	h := &assetContentHandler{repository: repository, assets: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/assets/{id}/content", h.content)
	return mux
}

func (h *assetContentHandler) content(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "asset_not_found", "The asset was not found.")
		return
	}
	asset, err := h.repository.GetAsset(r.Context(), id)
	if errors.Is(err, store.ErrAssetNotFound) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "asset_not_found", "The asset was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "asset_read_failed", "The asset could not be read.")
		return
	}
	if h.assets == nil {
		writeError(w, http.StatusInternalServerError, "asset_read_failed", "The asset could not be read.")
		return
	}
	file, info, err := h.assets.OpenAsset(asset)
	if errors.Is(err, assets.ErrAssetPathInvalid) || errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "asset_not_found", "The asset was not found.")
		return
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "asset_not_found", "The asset was not found.")
		} else {
			writeError(w, http.StatusInternalServerError, "asset_read_failed", "The asset could not be read.")
		}
		return
	}
	defer file.Close()
	if asset.MIMEType != "" {
		w.Header().Set("Content-Type", asset.MIMEType)
	}
	if asset.SHA256 != "" {
		w.Header().Set("ETag", `"`+asset.SHA256+`"`)
	}
	http.ServeContent(w, r, asset.Filename, info.ModTime(), file)
}
