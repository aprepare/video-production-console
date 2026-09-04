package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"video-production-console/internal/bgmlibrary"
	consoleSettings "video-production-console/internal/settings"
)

type bgmRuntimeProvider interface {
	Runtime(context.Context) (consoleSettings.Runtime, error)
}

type bgmLibraryHandler struct {
	runtime bgmRuntimeProvider
	// One rescan at a time: ffmpeg analysis of a whole directory is heavy
	// and concurrent runs would race on the index file.
	rescanMu sync.Mutex
}

// NewBGMLibraryHandler serves the local BGM library used by montage styling.
func NewBGMLibraryHandler(runtime bgmRuntimeProvider) http.Handler {
	h := &bgmLibraryHandler{runtime: runtime}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/bgm-library", h.list)
	mux.HandleFunc("POST /api/bgm-library/rescan", h.rescan)
	return mux
}

type bgmTrackView struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	DurationS       float64 `json:"duration_s"`
	UsableHeadS     float64 `json:"usable_head_s"`
	ClimaxStartS    float64 `json:"climax_start_s"`
	ClimaxDurationS float64 `json:"climax_duration_s"`
}

type bgmLibraryView struct {
	Dir    string         `json:"dir"`
	Tracks []bgmTrackView `json:"tracks"`
	Note   string         `json:"note,omitempty"`
}

func bgmIndexPath(dataRoot string) string {
	return filepath.Join(dataRoot, bgmlibrary.IndexFileName)
}

// effectiveFFmpeg is the configured ffmpeg, else the copy bundled under
// <data root>/tools/ffmpeg, else whatever "ffmpeg" resolves to on PATH.
func effectiveFFmpeg(runtime consoleSettings.Runtime) string {
	if path := strings.TrimSpace(runtime.FFmpegPath); path != "" {
		return path
	}
	bundled := filepath.Join(runtime.DataRoot, "tools", "ffmpeg", "ffmpeg.exe")
	if stat, err := os.Stat(bundled); err == nil && !stat.IsDir() {
		return bundled
	}
	return ""
}

// effectiveBGMDir is the configured BGM directory, or <data root>/bgm when
// the operator never set one, so BGM import works without a settings trip.
func effectiveBGMDir(runtime consoleSettings.Runtime) string {
	if dir := strings.TrimSpace(runtime.BGMDir); dir != "" {
		return dir
	}
	return filepath.Join(runtime.DataRoot, "bgm")
}

func bgmView(dir string, index bgmlibrary.Index, note string) bgmLibraryView {
	view := bgmLibraryView{Dir: dir, Tracks: make([]bgmTrackView, 0, len(index.Tracks)), Note: note}
	for _, track := range index.Tracks {
		view.Tracks = append(view.Tracks, bgmTrackView{
			ID: track.ID, Name: track.Name, DurationS: track.DurationS,
			UsableHeadS: track.UsableHeadS, ClimaxStartS: track.ClimaxStartS, ClimaxDurationS: track.ClimaxDurationS,
		})
	}
	return view
}

func (h *bgmLibraryHandler) list(w http.ResponseWriter, r *http.Request) {
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return
	}
	index := bgmlibrary.LoadIndex(bgmIndexPath(runtime.DataRoot))
	writeJSON(w, http.StatusOK, bgmView(effectiveBGMDir(runtime), index, ""))
}

func (h *bgmLibraryHandler) rescan(w http.ResponseWriter, r *http.Request) {
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return
	}
	bgmDir := effectiveBGMDir(runtime)
	if err := os.MkdirAll(bgmDir, 0o755); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "bgm_dir_unwritable", "BGM 目录不可用："+err.Error())
		return
	}
	if !h.rescanMu.TryLock() {
		writeError(w, http.StatusConflict, "bgm_rescan_busy", "BGM 扫描正在进行中。")
		return
	}
	defer h.rescanMu.Unlock()
	// The scan may run for minutes on a large directory; it must not die
	// with the HTTP request context, so it gets its own lifetime.
	index, err := bgmlibrary.Rescan(context.Background(), bgmDir, effectiveFFmpeg(runtime), bgmIndexPath(runtime.DataRoot))
	note := ""
	if err != nil {
		if len(index.Tracks) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "bgm_rescan_failed", err.Error())
			return
		}
		note = err.Error()
	}
	writeJSON(w, http.StatusOK, bgmView(bgmDir, index, note))
}
