package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/codex"
)

// videoExportJob drives tools/jianying-export/export_draft.py, which controls
// the local Jianying desktop app to render a registered draft to MP4. There
// is one desktop and one Jianying, so at most one export runs at a time.
type videoExportJob struct {
	mu       sync.Mutex
	running  bool
	assetID  string
	status   string // idle | running | done | failed
	message  string
	output   string
	started  time.Time
	finished time.Time
}

func (j *videoExportJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	status := j.status
	if status == "" {
		status = "idle"
	}
	view := map[string]any{"asset_id": j.assetID, "status": status, "message": j.message, "output_path": j.output}
	if !j.started.IsZero() {
		view["started_at"] = j.started.UTC().Format(time.RFC3339)
	}
	if !j.finished.IsZero() {
		view["finished_at"] = j.finished.UTC().Format(time.RFC3339)
	}
	return view
}

// exportScriptPath finds the exporter next to the console executable first,
// then in the working directory (the dev layout).
func exportScriptPath() string {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "tools", "jianying-export", "export_draft.py"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "tools", "jianying-export", "export_draft.py"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (h *assetContentHandler) exportVideoStart(w http.ResponseWriter, r *http.Request) {
	if !localOpenRequest(r) {
		writeError(w, http.StatusForbidden, "local_same_origin_required", "Exporting a video drives the desktop and requires a loopback same-origin request.")
		return
	}
	item, code, err := h.registeredDirectory(r)
	if err != nil {
		h.writeDirectoryError(w, code, err)
		return
	}
	script := exportScriptPath()
	if script == "" {
		writeError(w, http.StatusNotImplemented, "video_export_unavailable", "The Jianying export script is not installed on this console.")
		return
	}
	// The exporter finds the draft through Jianying's home-screen search,
	// which matches the display name, not the on-disk folder (a UUID until
	// Jianying renames it on first open).
	draftName := item.DisplayName
	if draftName == "" {
		draftName = filepath.Base(item.Path)
	}
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "video_export_unavailable", "Runtime settings are unavailable.")
		return
	}
	// The exporter needs OpenCV/mss/psutil/uiautomation; those ship in the
	// embedded Python named by the machine profile, not necessarily in the
	// interpreter found on PATH.
	python := exportPythonBinary(runtime.MachineProfilePath)
	// Exports land under the data root so the result is discoverable from
	// the console instead of a machine-specific folder baked into the script.
	exportDir := filepath.Join(runtime.DataRoot, "exports")
	if mkErr := os.MkdirAll(exportDir, 0o755); mkErr != nil {
		writeError(w, http.StatusInternalServerError, "video_export_unavailable", "The export directory could not be created.")
		return
	}
	j := h.exportJob
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		writeError(w, http.StatusConflict, "video_export_busy", "A video export is already running.")
		return
	}
	j.running, j.assetID = true, item.ID
	j.status, j.message, j.output = "running", "正在控制剪映导出，期间请不要操作鼠标键盘。", ""
	j.started, j.finished = time.Now(), time.Time{}
	j.mu.Unlock()
	go j.run(python, script, draftName, exportDir)
	writeJSON(w, http.StatusAccepted, j.snapshot())
}

// exportPythonBinary prefers the machine profile's python_binary (the
// embedded runtime with the exporter's dependencies) and falls back to the
// PATH interpreter only when the profile is missing or unreadable.
func exportPythonBinary(machineProfilePath string) string {
	machineProfilePath = strings.TrimSpace(machineProfilePath)
	if machineProfilePath == "" {
		return "python"
	}
	raw, err := os.ReadFile(machineProfilePath)
	if err != nil {
		return "python"
	}
	var profile struct {
		PythonBinary string `json:"python_binary"`
	}
	if json.Unmarshal(raw, &profile) != nil {
		return "python"
	}
	candidate := strings.TrimSpace(profile.PythonBinary)
	if candidate == "" || !filepath.IsAbs(candidate) {
		return "python"
	}
	if info, statErr := os.Stat(candidate); statErr != nil || !info.Mode().IsRegular() {
		return "python"
	}
	return candidate
}

func (h *assetContentHandler) exportVideoStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.exportJob.snapshot())
}

func (j *videoExportJob) run(python, script, draftName, exportDir string) {
	// Renders can legitimately take tens of minutes for long timelines.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-u", script, draftName, "--export-dir", exportDir)
	cmd.Dir = filepath.Dir(script)
	// Piped Python stdout defaults to the ANSI code page on Windows, which
	// would mangle the Chinese file paths the script prints.
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := cmd.CombinedOutput()
	done, failed := parseExportOutcome(string(out))
	j.mu.Lock()
	defer j.mu.Unlock()
	j.running, j.finished = false, time.Now()
	switch {
	case done != "":
		j.status, j.output = "done", done
		j.message = "视频已导出。"
	case failed != "":
		j.status, j.message = "failed", failed
	case ctx.Err() != nil:
		j.status, j.message = "failed", "导出超时，已停止等待。"
	case err != nil:
		j.status, j.message = "failed", "导出脚本运行失败："+err.Error()
	default:
		j.status, j.message = "failed", "导出脚本结束但未报告结果。"
	}
}

// draftDisplayNameFromManifest reads the montage task manifest to recover the
// draft name shown in Jianying's home screen.
func draftDisplayNameFromManifest(manifestPath string) string {
	manifestPath = strings.TrimSpace(manifestPath)
	if manifestPath == "" {
		return ""
	}
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return ""
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	_ = file.Close()
	if readErr != nil || len(data) > 1024*1024 {
		return ""
	}
	var manifest codex.TaskManifest
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.NonSecretSettings.DraftDisplayName)
}

// parseExportOutcome extracts the script's final verdict. export_draft.py
// prints exactly one of:
//
//	DONE: <path> (<size> MB)
//	FAILED: <reason>
func parseExportOutcome(output string) (done, failed string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "DONE: "); ok {
			if i := strings.LastIndex(rest, " ("); i > 0 {
				rest = rest[:i]
			}
			done = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "FAILED: "); ok {
			failed = strings.TrimSpace(rest)
		}
	}
	return done, failed
}
