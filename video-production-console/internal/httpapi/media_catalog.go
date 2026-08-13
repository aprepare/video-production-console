package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// The media catalog handler only sees the narrow CatalogService surface below.
// The real implementation lives behind internal/app so this package never
// imports internal/mediacatalog while that package is still growing.

// Sentinel errors a CatalogService implementation returns to drive the fixed
// HTTP error contract. Wrapping them is allowed; the response body always uses
// a fixed message so absolute paths and credentials can never leak through.
var (
	ErrCatalogNotConfigured  = errors.New("media catalog is not configured")
	ErrCatalogJobActive      = errors.New("a media catalog job is already active")
	ErrCatalogSourceNotFound = errors.New("media catalog source not found")
	ErrProviderNotConfigured = errors.New("provider_not_configured")
	ErrCatalogImportInvalid  = errors.New("catalog_import_invalid")
	ErrCatalogSearchInvalid  = errors.New("catalog_search_invalid")
)

// CatalogCounts aggregates the library-wide totals shown in the panel header.
type CatalogCounts struct {
	Sources     int `json:"sources"`
	Shots       int `json:"shots"`
	ReadyShots  int `json:"ready_shots"`
	FailedShots int `json:"failed_shots"`
}

// CatalogActiveJob describes the single global indexing run, when one exists.
type CatalogActiveJob struct {
	ID             string `json:"id"`
	Phase          string `json:"phase"`
	CompletedUnits int    `json:"completed_units"`
	TotalUnits     int    `json:"total_units"`
}

// CatalogWarning groups non-fatal library issues by error code.
type CatalogWarning struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// CatalogStatus is the GET /api/media-catalog/status response.
type CatalogStatus struct {
	State     string            `json:"state"`
	Counts    CatalogCounts     `json:"counts"`
	ActiveJob *CatalogActiveJob `json:"active_job,omitempty"`
	Warnings  []CatalogWarning  `json:"warnings"`
}

// CatalogSource is one library entry. Only the media-root-relative path is
// exposed; the absolute location never leaves the server.
type CatalogSource struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Subtype      string `json:"subtype"`
	Origin       string `json:"origin"`
	RelativePath string `json:"relative_path"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code"`
	SizeBytes    int64  `json:"size_bytes"`
	DurationMS   int64  `json:"duration_ms"`
	UpdatedAt    string `json:"updated_at"`
}

// CatalogSourceFilter carries the validated sources query parameters.
type CatalogSourceFilter struct {
	Kind   string
	Status string
	Cursor string
}

// CatalogSourcesPage is the GET /api/media-catalog/sources response.
type CatalogSourcesPage struct {
	Sources    []CatalogSource `json:"sources"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type CatalogProviderStatus struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}

type CatalogProviders struct {
	Providers []CatalogProviderStatus `json:"providers"`
}

type CatalogSearchQuery struct {
	Provider string
	Query    string
	Limit    int
}

type CatalogRemoteAsset struct {
	Provider    string `json:"provider"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DownloadURL string `json:"download_url"`
	PageURL     string `json:"page_url"`
	Creator     string `json:"creator"`
	LicenseCode string `json:"license_code"`
	LicenseURL  string `json:"license_url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type CatalogSearchPage struct {
	Assets []CatalogRemoteAsset `json:"assets"`
}

type CatalogImportRequest struct {
	Source        string
	Provider      string
	Kind          string
	SuggestedName string
	Bytes         []byte
	Remote        CatalogRemoteAsset
}

type CatalogImportResult struct {
	Source         CatalogSource `json:"source"`
	Created        bool          `json:"created"`
	Publishability string        `json:"publishability"`
}

// CatalogService is the narrow surface the handler depends on.
type CatalogService interface {
	Status(context.Context) (CatalogStatus, error)
	Sources(context.Context, CatalogSourceFilter) (CatalogSourcesPage, error)
	StartIndex(context.Context) (CatalogStatus, error)
	RetrySource(context.Context, string) (CatalogStatus, error)
	Providers(context.Context) (CatalogProviders, error)
	Search(context.Context, CatalogSearchQuery) (CatalogSearchPage, error)
	Import(context.Context, CatalogImportRequest) (CatalogImportResult, error)
}

type mediaCatalogHandler struct {
	service CatalogService
}

// NewMediaCatalogHandler serves the media catalog build/status API. GET routes
// rely on the shared session middleware; POST routes additionally pass the
// shared CSRF check applied in front of the whole /api mux.
func NewMediaCatalogHandler(service CatalogService) http.Handler {
	h := &mediaCatalogHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/media-catalog/status", h.status)
	mux.HandleFunc("GET /api/media-catalog/sources", h.sources)
	mux.HandleFunc("GET /api/media-catalog/providers", h.providers)
	mux.HandleFunc("GET /api/media-catalog/search", h.search)
	mux.HandleFunc("POST /api/media-catalog/index", h.index)
	mux.HandleFunc("POST /api/media-catalog/import", h.importMedia)
	mux.HandleFunc("POST /api/media-catalog/sources/{id}/retry", h.retry)
	return mux
}

func (h *mediaCatalogHandler) status(w http.ResponseWriter, r *http.Request) {
	status, err := h.service.Status(r.Context())
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, normalizedCatalogStatus(status))
}

var catalogSourceKinds = map[string]bool{"": true, "movie": true, "broll": true, "image": true}
var catalogSourceStatuses = map[string]bool{"": true, "pending_probe": true, "ready": true, "failed": true}

func (h *mediaCatalogHandler) sources(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := CatalogSourceFilter{
		Kind:   strings.TrimSpace(query.Get("kind")),
		Status: strings.TrimSpace(query.Get("status")),
		Cursor: strings.TrimSpace(query.Get("cursor")),
	}
	if !catalogSourceKinds[filter.Kind] || !catalogSourceStatuses[filter.Status] {
		writeError(w, http.StatusBadRequest, "catalog_filter_invalid", "筛选条件无效：kind 只支持 movie/broll/image，status 只支持 pending_probe/ready/failed。")
		return
	}
	page, err := h.service.Sources(r.Context(), filter)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if page.Sources == nil {
		page.Sources = []CatalogSource{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *mediaCatalogHandler) index(w http.ResponseWriter, r *http.Request) {
	status, err := h.service.StartIndex(r.Context())
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, normalizedCatalogStatus(status))
}

func (h *mediaCatalogHandler) retry(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusNotFound, "catalog_source_not_found", "素材条目不存在。")
		return
	}
	status, err := h.service.RetrySource(r.Context(), id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, normalizedCatalogStatus(status))
}

func normalizedCatalogStatus(status CatalogStatus) CatalogStatus {
	if status.Warnings == nil {
		status.Warnings = []CatalogWarning{}
	}
	return status
}

func (h *mediaCatalogHandler) providers(w http.ResponseWriter, r *http.Request) {
	payload, err := h.service.Providers(r.Context())
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if payload.Providers == nil {
		payload.Providers = []CatalogProviderStatus{}
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *mediaCatalogHandler) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 0
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "catalog_search_invalid", "limit 必须是非负整数。")
			return
		}
		limit = parsed
	}
	page, err := h.service.Search(r.Context(), CatalogSearchQuery{
		Provider: strings.ToLower(strings.TrimSpace(query.Get("provider"))),
		Query:    strings.TrimSpace(query.Get("q")),
		Limit:    limit,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if page.Assets == nil {
		page.Assets = []CatalogRemoteAsset{}
	}
	writeJSON(w, http.StatusOK, page)
}

type catalogImportJSON struct {
	Source        string `json:"source"`
	Provider      string `json:"provider"`
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	DownloadURL   string `json:"download_url"`
	PageURL       string `json:"page_url"`
	Creator       string `json:"creator"`
	LicenseCode   string `json:"license_code"`
	LicenseURL    string `json:"license_url"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	SuggestedName string `json:"suggested_name"`
}

func (h *mediaCatalogHandler) importMedia(w http.ResponseWriter, r *http.Request) {
	request, err := decodeCatalogImport(w, r)
	if err != nil {
		return
	}
	result, err := h.service.Import(r.Context(), request)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func decodeCatalogImport(w http.ResponseWriter, r *http.Request) (CatalogImportRequest, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "catalog_import_invalid", "无法读取上传文件。")
			return CatalogImportRequest{}, err
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "catalog_import_invalid", "请选择要导入的本地文件。")
			return CatalogImportRequest{}, err
		}
		defer file.Close()
		payload, err := io.ReadAll(io.LimitReader(file, 200<<20+1))
		if err != nil || len(payload) == 0 || len(payload) > 200<<20 {
			writeError(w, http.StatusBadRequest, "catalog_import_invalid", "上传文件无效或超过 200MB。")
			return CatalogImportRequest{}, err
		}
		name := ""
		if header != nil {
			name = header.Filename
		}
		return CatalogImportRequest{
			Source:        "local",
			Kind:          strings.TrimSpace(r.FormValue("kind")),
			SuggestedName: name,
			Bytes:         payload,
		}, nil
	}
	var body catalogImportJSON
	if err := decodeJSON(w, r, 1<<20, &body); err != nil {
		writeDecodeError(w, err, "catalog_import_invalid", "导入请求格式无效。")
		return CatalogImportRequest{}, err
	}
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = "remote"
	}
	return CatalogImportRequest{
		Source:        source,
		Provider:      strings.TrimSpace(body.Provider),
		Kind:          strings.TrimSpace(body.Kind),
		SuggestedName: strings.TrimSpace(body.SuggestedName),
		Remote: CatalogRemoteAsset{
			Provider:    firstNonEmpty(body.Provider),
			ID:          strings.TrimSpace(body.ID),
			Kind:        firstNonEmpty(body.Kind, "image"),
			DownloadURL: strings.TrimSpace(body.DownloadURL),
			PageURL:     strings.TrimSpace(body.PageURL),
			Creator:     strings.TrimSpace(body.Creator),
			LicenseCode: strings.TrimSpace(body.LicenseCode),
			LicenseURL:  strings.TrimSpace(body.LicenseURL),
			Width:       body.Width,
			Height:      body.Height,
		},
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// writeCatalogError maps service failures onto the fixed error contract. The
// messages are deliberately constant: repository errors may quote absolute
// media paths, and those must never reach a response body.
func writeCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCatalogNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "catalog_not_configured", "素材库未配置，请在设置中填写素材库目录与媒体素材目录。")
	case errors.Is(err, ErrCatalogJobActive):
		writeError(w, http.StatusConflict, "catalog_job_active", "已有建库任务正在进行，请等待其完成后再试。")
	case errors.Is(err, ErrCatalogSourceNotFound):
		writeError(w, http.StatusNotFound, "catalog_source_not_found", "素材条目不存在。")
	case errors.Is(err, ErrProviderNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "provider_not_configured", "外部图库未配置，请在设置中填写 Pexels 或 Pixabay 密钥。")
	case errors.Is(err, ErrCatalogSearchInvalid):
		writeError(w, http.StatusBadRequest, "catalog_search_invalid", "搜索参数无效：provider 只支持 pexels/pixabay，且必须提供关键词。")
	case errors.Is(err, ErrCatalogImportInvalid):
		writeError(w, http.StatusBadRequest, "catalog_import_invalid", "导入请求无效。")
	default:
		writeError(w, http.StatusInternalServerError, "catalog_internal_error", "素材库操作失败，请稍后重试。")
	}
}
