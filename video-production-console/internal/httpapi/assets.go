package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type assetContentStore interface {
	GetAsset(rctx context.Context, id string) (domain.Asset, error)
}

type assetContentHandler struct {
	repository assetContentStore
	assets     *assets.Service
	db         *sql.DB
	runtime    AssetRuntimeProvider
	opener     assets.DesktopOpener
	exportJob  *videoExportJob
}

type AssetRuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

type AssetHandlerOptions struct {
	Database      *sql.DB
	Runtime       AssetRuntimeProvider
	DesktopOpener assets.DesktopOpener
}

// NewAssetsHandler serves immutable, database-addressed asset bytes.
func NewAssetsHandler(db *sql.DB, service *assets.Service, optionValues ...AssetHandlerOptions) http.Handler {
	options := AssetHandlerOptions{}
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	options.Database = db
	return newAssetsHandler(store.NewProjectRepository(db), service, options)
}

func newAssetsHandler(repository assetContentStore, service *assets.Service, optionValues ...AssetHandlerOptions) http.Handler {
	h := &assetContentHandler{repository: repository, assets: service}
	if len(optionValues) > 0 {
		options := optionValues[0]
		h.db, h.runtime, h.opener = options.Database, options.Runtime, options.DesktopOpener
	}
	h.exportJob = &videoExportJob{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/assets/{id}/content", h.content)
	mux.HandleFunc("GET /api/assets/{id}/directory-manifest", h.directoryManifest)
	mux.HandleFunc("POST /api/assets/{id}/open-directory", h.openDirectory)
	mux.HandleFunc("POST /api/assets/{id}/export-video", h.exportVideoStart)
	mux.HandleFunc("GET /api/assets/{id}/export-video", h.exportVideoStatus)
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
	if info.IsDir() {
		writeError(w, http.StatusConflict, "asset_is_directory", "Directory assets must be read through their manifest.")
		return
	}
	if asset.MIMEType != "" {
		w.Header().Set("Content-Type", asset.MIMEType)
	}
	if asset.SHA256 != "" {
		w.Header().Set("ETag", `"`+asset.SHA256+`"`)
	}
	http.ServeContent(w, r, asset.Filename, info.ModTime(), file)
}

type registeredDirectory struct{ ID, Path, Root, DisplayName string }

func (h *assetContentHandler) registeredDirectory(r *http.Request) (registeredDirectory, string, error) {
	id := strings.TrimSpace(r.PathValue("id"))
	if _, err := uuid.Parse(id); err != nil {
		return registeredDirectory{}, "asset_not_found", sql.ErrNoRows
	}
	if h.db == nil || h.runtime == nil {
		return registeredDirectory{}, "asset_directory_unavailable", errors.New("directory asset service is unavailable")
	}
	var item registeredDirectory
	var manifestPath sql.NullString
	err := h.db.QueryRowContext(r.Context(), `SELECT version.id,version.path,task.manifest_path
		FROM asset_versions version
		JOIN codex_tasks task ON task.id=version.source_task_id AND task.action='montage.execute'
		JOIN montage_registration_attempts attempt
		  ON attempt.task_id=version.source_task_id
		 AND attempt.state='succeeded' AND attempt.registered_path=version.path
		WHERE version.id=? AND version.state IN ('ready','stale') AND version.storage_kind='directory'
		  AND version.type='mix_draft'
		ORDER BY attempt.finished_at DESC,attempt.id DESC LIMIT 1`, id).Scan(&item.ID, &item.Path, &manifestPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return registeredDirectory{}, "asset_not_found", err
		}
		return registeredDirectory{}, "asset_directory_unavailable", err
	}
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil || strings.TrimSpace(runtime.JianyingRoot) == "" {
		return registeredDirectory{}, "asset_directory_unavailable", errors.New("trusted Jianying root is unavailable")
	}
	item.DisplayName = draftDisplayNameFromManifest(manifestPath.String)
	handle, canonical, err := assets.OpenVerifiedDirectory(runtime.JianyingRoot, item.Path)
	if err != nil && item.DisplayName != "" {
		// Jianying renames the registered UUID folder to the draft's display
		// name the first time the user opens the draft, which orphans the
		// path we recorded at registration; look for the renamed folder.
		handle, canonical, err = assets.OpenVerifiedDirectory(runtime.JianyingRoot, filepath.Join(runtime.JianyingRoot, item.DisplayName))
	}
	if err != nil {
		return registeredDirectory{}, "asset_directory_invalid", err
	}
	if err := handle.Close(); err != nil {
		return registeredDirectory{}, "asset_directory_unavailable", err
	}
	item.Path, item.Root = canonical, runtime.JianyingRoot
	return item, "", nil
}

func (h *assetContentHandler) directoryManifest(w http.ResponseWriter, r *http.Request) {
	item, code, err := h.registeredDirectory(r)
	if err != nil {
		h.writeDirectoryError(w, code, err)
		return
	}
	canonical, entries, err := assets.BuildDirectoryManifest(item.Root, item.Path)
	if err != nil {
		h.writeDirectoryError(w, "asset_directory_invalid", err)
		return
	}
	writeJSON(w, 200, map[string]any{"asset_id": item.ID, "registered_path": canonical, "entries": entries})
}

func (h *assetContentHandler) openDirectory(w http.ResponseWriter, r *http.Request) {
	if !localOpenRequest(r) {
		writeError(w, http.StatusForbidden, "local_same_origin_required", "Opening a desktop directory requires a loopback same-origin request.")
		return
	}
	item, code, err := h.registeredDirectory(r)
	if err != nil {
		h.writeDirectoryError(w, code, err)
		return
	}
	if h.opener == nil {
		writeError(w, 501, "desktop_open_unavailable", "Desktop directory opening is unavailable.")
		return
	}
	handle, canonical, err := assets.OpenVerifiedDirectory(item.Root, item.Path)
	if err != nil {
		h.writeDirectoryError(w, "asset_directory_invalid", err)
		return
	}
	defer handle.Close()
	if err := h.opener.OpenDirectory(r.Context(), canonical); err != nil {
		writeError(w, 500, "desktop_open_failed", "The registered directory could not be opened.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *assetContentHandler) writeDirectoryError(w http.ResponseWriter, code string, err error) {
	switch {
	case code == "asset_not_found" || errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, "asset_not_found", "The asset was not found.")
	case errors.Is(err, assets.ErrDirectoryTooLarge), errors.Is(err, assets.ErrDirectoryTooDeep):
		writeError(w, http.StatusRequestEntityTooLarge, "directory_manifest_too_large", "The directory exceeds manifest safety limits.")
	case errors.Is(err, assets.ErrDirectoryPathInvalid):
		writeError(w, 409, "asset_directory_invalid", "The registered directory is no longer trusted.")
	default:
		writeError(w, 503, "asset_directory_unavailable", "The registered directory is unavailable.")
	}
}

func loopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remote))
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func localOpenRequest(r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || local == nil || !loopbackRemote(local.String()) || !loopbackRemote(r.RemoteAddr) || !loopbackAuthority(r.Host) {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false
	}
	fetchMode := r.Header.Get("Sec-Fetch-Mode")
	if fetchMode != "cors" && fetchMode != "same-origin" {
		return false
	}
	parsed, err := url.Parse(origin)
	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	if err != nil || parsed.Scheme != expectedScheme || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && loopbackAuthority(parsed.Host)
}

func loopbackAuthority(authority string) bool {
	host := strings.TrimSpace(authority)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
