package catalogbuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/mediacatalog"
)

const defaultListen = "127.0.0.1:2031"

type Server struct {
	configPath string
	toolsDir   string
	logFile    string

	mu              sync.Mutex
	logMu           sync.Mutex
	cfg             Config
	phase           string
	lastError       string
	lastErrorDetail string
	packReady       bool
	lastMerge       *MergeSummary
	cancel          context.CancelFunc
	building        bool
}

func NewServer(configPath string) (*Server, error) {
	if strings.TrimSpace(configPath) == "" || !filepath.IsAbs(configPath) {
		return nil, errInvalidConfig
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	toolsDir := ""
	if exe, exeErr := os.Executable(); exeErr == nil {
		toolsDir = filepath.Dir(exe)
	}
	server := &Server{
		configPath: configPath,
		toolsDir:   toolsDir,
		logFile:    filepath.Join(filepath.Dir(configPath), "catalog-builder.log"),
		cfg:        cfg,
		phase:      "idle",
	}
	server.appendLog("server start config=%s tools_dir=%s", configPath, toolsDir)
	return server, nil
}

func ValidateListen(addr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || strings.TrimSpace(port) == "" {
		return ErrListenNotLocal
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return ErrListenNotLocal
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handlePage)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	mux.HandleFunc("POST /api/build", s.handleBuild)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/pack", s.handlePack)
	mux.HandleFunc("GET /api/log", s.handleLog)
	mux.HandleFunc("POST /api/merge", s.handleMerge)
	return mux
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(pageHTML)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.suggested(s.cfg).Public()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var incoming Config
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&incoming); err != nil {
		s.fail(w, errInvalidConfig)
		return
	}
	s.mu.Lock()
	merged := keepExistingSecrets(s.cfg, incoming)
	prepared, err := merged.Prepare(s.searchDirs()...)
	if err != nil {
		s.mu.Unlock()
		s.fail(w, err)
		return
	}
	if err := SaveConfig(s.configPath, prepared); err != nil {
		s.mu.Unlock()
		s.fail(w, err)
		return
	}
	s.cfg = applyEnvOverrides(prepared)
	public := s.cfg.Public()
	s.appendLog("saved config media_root=%s ffmpeg=%s ffprobe=%s vision_model=%s embedding_model=%s analysis_concurrency=%d",
		s.cfg.MediaRoot, s.cfg.FFmpegPath, s.cfg.FFprobePath, s.cfg.VisionModel, s.cfg.EmbeddingModel, mediacatalog.ClampAnalysisConcurrency(s.cfg.AnalysisConcurrency))
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, public)
}

func keepExistingSecrets(existing, incoming Config) Config {
	if incoming.VisionAPIKey == "" || strings.HasPrefix(incoming.VisionAPIKey, "****") {
		incoming.VisionAPIKey = existing.VisionAPIKey
	}
	if incoming.EmbeddingAPIKey == "" || strings.HasPrefix(incoming.EmbeddingAPIKey, "****") {
		incoming.EmbeddingAPIKey = existing.EmbeddingAPIKey
	}
	return incoming
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.building {
		s.mu.Unlock()
		s.fail(w, errJobActive)
		return
	}
	cfg, err := s.cfg.Prepare(s.searchDirs()...)
	if err != nil {
		s.mu.Unlock()
		s.fail(w, err)
		return
	}
	s.cfg = cfg
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	s.cancel = cancel
	s.building = true
	s.phase = "ingest"
	s.lastError = ""
	s.lastErrorDetail = ""
	s.packReady = false
	s.appendLog("build start media_root=%s movie_dir=%s ffmpeg=%s ffprobe=%s vision=%s embedding=%s analysis_concurrency=%d",
		cfg.MediaRoot, movieDir(cfg.MediaRoot), cfg.FFmpegPath, cfg.FFprobePath, cfg.VisionModel, cfg.EmbeddingModel, mediacatalog.ClampAnalysisConcurrency(cfg.AnalysisConcurrency))
	if strings.Contains(cfg.EmbeddingModel, "VL-Embedding") {
		s.appendLog("warning embedding model %s looks wrong; SiliconFlow text embedding is Qwen/Qwen3-Embedding-8B", cfg.EmbeddingModel)
	}
	s.mu.Unlock()
	go s.runBuild(ctx, cfg)
	writeJSON(w, http.StatusAccepted, map[string]string{"state": "running", "phase": "ingest"})
}

func (s *Server) runBuild(ctx context.Context, cfg Config) {
	summary, err := Build(ctx, cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.building = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if err != nil {
		s.phase = "failed"
		s.lastError = publicCode(err)
		s.lastErrorDetail = err.Error()
		s.packReady = false
		s.appendLog("build failed phase=%s: %s", summary.Phase, err)
		return
	}
	s.phase = summary.Phase
	s.lastError = ""
	s.lastErrorDetail = ""
	s.packReady = true
	s.appendLog("build ready discovered=%d new_sources=%d probe_sources=%d shots=%d analyzed=%d",
		summary.Index.DiscoveredFiles, summary.Index.NewSources, summary.Pipeline.ProcessedSources, summary.Pipeline.Shots, summary.Analysis.AnalyzedShots)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.cfg
	phase := s.phase
	building := s.building
	lastError := s.lastError
	lastErrorDetail := s.lastErrorDetail
	packReady := s.packReady && !building
	merge := s.lastMerge
	logPath := s.logFile
	s.mu.Unlock()
	status := map[string]any{
		"state":             statusState(building, phase, lastError),
		"phase":             phase,
		"pack_ready":        packReady,
		"last_error":        lastError,
		"last_error_detail": lastErrorDetail,
		"log_path":          logPath,
		"movie_dir":         movieDir(cfg.MediaRoot),
		"counts":            map[string]int{"sources": 0, "shots": 0, "ready_shots": 0, "failed_shots": 0},
	}
	if lastError != "" {
		status["hint"] = "把 last_error_detail 和 catalog-builder.log 发给开发者"
	}
	if merge != nil {
		status["last_merge"] = merge
	}
	if cfg.Validate() == nil {
		if counts, err := catalogCounts(r.Context(), cfg.MediaRoot); err == nil {
			status["counts"] = counts
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func statusState(building bool, phase, lastError string) string {
	switch {
	case building:
		return "running"
	case lastError != "":
		return "failed"
	case phase == "ready":
		return "ready"
	default:
		return "idle"
	}
}

func catalogCounts(ctx context.Context, mediaRoot string) (map[string]int, error) {
	repo, err := mediacatalog.Open(mediaRoot)
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	sources, err := repo.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"sources": len(sources), "shots": 0, "ready_shots": 0, "failed_shots": 0}
	for _, source := range sources {
		shots, err := repo.ShotsBySource(ctx, source.ID)
		if err != nil {
			return nil, err
		}
		counts["shots"] += len(shots)
		for _, shot := range shots {
			switch shot.AnalysisStatus {
			case mediacatalog.AnalysisCompleted:
				counts["ready_shots"]++
			case mediacatalog.AnalysisFailed:
				counts["failed_shots"]++
			}
		}
	}
	return counts, nil
}

func (s *Server) handlePack(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.building {
		s.mu.Unlock()
		s.fail(w, errJobActive)
		return
	}
	cfg := s.cfg
	s.mu.Unlock()
	if err := cfg.Validate(); err != nil {
		s.fail(w, err)
		return
	}
	zipPath := filepath.Join(os.TempDir(), "catalog-pack-"+filepath.Base(s.configPath)+".zip")
	_ = os.Remove(zipPath)
	if err := ExportPack(r.Context(), cfg.MediaRoot, zipPath, cfg.VisionModel); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="catalog-pack.zip"`)
	http.ServeFile(w, r, zipPath)
}

func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.building {
		s.mu.Unlock()
		s.fail(w, errJobActive)
		return
	}
	s.mu.Unlock()
	if err := r.ParseMultipartForm(64 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		if err := r.ParseForm(); err != nil {
			s.fail(w, errInvalidPack)
			return
		}
	}
	mediaRoot := strings.TrimSpace(r.FormValue("media_root"))
	packPath := strings.TrimSpace(r.FormValue("pack_path"))
	if mediaRoot == "" {
		s.mu.Lock()
		mediaRoot = s.cfg.MediaRoot
		s.mu.Unlock()
	}
	if file, _, err := r.FormFile("pack"); err == nil {
		defer file.Close()
		temp, createErr := os.CreateTemp("", "catalog-pack-*.zip")
		if createErr != nil {
			s.fail(w, errInvalidPack)
			return
		}
		defer os.Remove(temp.Name())
		if _, copyErr := io.Copy(temp, file); copyErr != nil {
			_ = temp.Close()
			s.fail(w, errInvalidPack)
			return
		}
		if err := temp.Close(); err != nil {
			s.fail(w, errInvalidPack)
			return
		}
		packPath = temp.Name()
	}
	summary, err := MergePack(r.Context(), mediaRoot, packPath)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.mu.Lock()
	s.lastMerge = &summary
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.logFile)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("还没有日志。\n"))
			return
		}
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="catalog-builder.log"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) suggested(cfg Config) Config {
	if strings.TrimSpace(cfg.MediaRoot) == "" {
		cfg.MediaRoot = defaultMediaRoot
	}
	if resolved, err := ResolveToolPath(cfg.FFmpegPath, toolFileName("ffmpeg"), s.searchDirs()...); err == nil {
		cfg.FFmpegPath = resolved
	}
	if resolved, err := ResolveToolPath(cfg.FFprobePath, toolFileName("ffprobe"), s.searchDirs()...); err == nil {
		cfg.FFprobePath = resolved
	}
	cfg.AnalysisConcurrency = mediacatalog.ClampAnalysisConcurrency(cfg.AnalysisConcurrency)
	return cfg
}

func (s *Server) searchDirs() []string {
	return []string{filepath.Dir(s.configPath), s.toolsDir}
}

func (s *Server) appendLog(format string, args ...any) {
	if s.logFile == "" {
		return
	}
	line := time.Now().Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, args...) + "\n"
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.logFile), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(s.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.appendLog("request error: %s", err)
	code := publicCode(err)
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errJobActive), errors.Is(err, errPackNotReady), errors.Is(err, errModelMismatch):
		status = http.StatusConflict
	case errors.Is(err, errBuildFailed), errors.Is(err, errMergeFailed), errors.Is(err, errNoMedia):
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]string{
		"error":    code,
		"detail":   err.Error(),
		"log_path": s.logFile,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func publicCode(err error) string {
	switch {
	case errors.Is(err, errInvalidConfig):
		return errInvalidConfig.Error()
	case errors.Is(err, errInvalidPack):
		return errInvalidPack.Error()
	case errors.Is(err, errModelMismatch):
		return errModelMismatch.Error()
	case errors.Is(err, errJobActive):
		return errJobActive.Error()
	case errors.Is(err, errPackNotReady):
		return errPackNotReady.Error()
	case errors.Is(err, ErrListenNotLocal):
		return ErrListenNotLocal.Error()
	case errors.Is(err, errVisionRequired):
		return errVisionRequired.Error()
	case errors.Is(err, errFFmpegRequired):
		return errFFmpegRequired.Error()
	case errors.Is(err, errNoMedia):
		return errNoMedia.Error()
	case errors.Is(err, errBuildFailed):
		return errBuildFailed.Error()
	case errors.Is(err, errMergeFailed):
		return errMergeFailed.Error()
	default:
		return "catalog_builder_internal"
	}
}
