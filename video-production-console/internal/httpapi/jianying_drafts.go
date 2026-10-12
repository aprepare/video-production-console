package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

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
	db      *sql.DB
}

// NewJianyingDraftsHandler serves draft listing, version health, backup prune, and storyboard preview.
func NewJianyingDraftsHandler(runtime jianyingRuntimeProvider, db *sql.DB) http.Handler {
	h := &jianyingDraftsHandler{runtime: runtime, db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/jianying-drafts", h.list)
	mux.HandleFunc("POST /api/jianying-drafts/extract-style", h.extract)
	mux.HandleFunc("POST /api/jianying-drafts/import-bgm", h.importBGM)
	mux.HandleFunc("GET /api/jianying-drafts/health", h.health)
	mux.HandleFunc("POST /api/jianying-drafts/backups/prune", h.pruneBackups)
	mux.HandleFunc("GET /api/jianying-drafts/storyboard", h.storyboard)
	mux.HandleFunc("GET /api/jianying-drafts/storyboard/frame", h.storyboardFrame)
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

type jianyingHealthView struct {
	JianyingRoot     string   `json:"jianying_root"`
	RootExists       bool     `json:"root_exists"`
	InstalledVersion string   `json:"installed_version"`
	VersionSource    string   `json:"version_source"`
	VersionNote      string   `json:"version_note"`
	VerifiedVersions []string `json:"verified_versions"`
	Compatible       bool     `json:"compatible"`
	BackupCount      int      `json:"backup_count"`
	BackupBytes      int64    `json:"backup_bytes"`
}

func (h *jianyingDraftsHandler) health(w http.ResponseWriter, r *http.Request) {
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return
	}
	root := strings.TrimSpace(runtime.JianyingRoot)
	if root == "" {
		writeJSON(w, http.StatusOK, jianyingHealthView{VerifiedVersions: []string{}})
		return
	}
	view := jianyingHealthView{
		JianyingRoot:     root,
		VerifiedVersions: append([]string{}, jianyingdraft.VerifiedJianyingVersions...),
	}
	info, statErr := os.Stat(root)
	view.RootExists = statErr == nil && info.IsDir()
	installed, err := jianyingdraft.DetectInstalledVersion(root)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "jianying_version_unreadable", "剪映版本读取失败："+err.Error())
		return
	}
	view.InstalledVersion = installed.Version
	view.VersionSource = installed.Source
	view.VersionNote = installed.Note
	view.Compatible = jianyingdraft.CompatibleWithVerified(installed.Version, jianyingdraft.VerifiedJianyingVersions)
	if view.RootExists {
		backups, err := jianyingdraft.ListRegistrationBackups(root)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "jianying_backup_list_failed", "剪映注册备份读取失败："+err.Error())
			return
		}
		view.BackupCount = len(backups)
		for _, backup := range backups {
			view.BackupBytes += backup.Size
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *jianyingDraftsHandler) pruneBackups(w http.ResponseWriter, r *http.Request) {
	var in struct {
		KeepLatest    *int  `json:"keep_latest"`
		OlderThanDays *int  `json:"older_than_days"`
		DryRun        *bool `json:"dry_run"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSmallJSONRequest))
	if err != nil {
		writeDecodeError(w, err, "invalid_backup_prune", "清理参数无法读取。")
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(body)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeDecodeError(w, err, "invalid_backup_prune", "清理参数不正确。")
			return
		}
		if err := ensureJSONEOF(dec); err != nil {
			writeDecodeError(w, err, "invalid_backup_prune", "清理参数不正确。")
			return
		}
	}
	keep := 10
	if in.KeepLatest != nil {
		keep = *in.KeepLatest
	}
	days := 7
	if in.OlderThanDays != nil {
		days = *in.OlderThanDays
	}
	dryRun := true
	if in.DryRun != nil {
		dryRun = *in.DryRun
	}
	if keep < 1 || keep > 200 {
		writeError(w, http.StatusBadRequest, "invalid_keep_latest", "keep_latest 要在 1 到 200 之间。")
		return
	}
	if days < 0 || days > 365 {
		writeError(w, http.StatusBadRequest, "invalid_older_than_days", "older_than_days 要在 0 到 365 之间。")
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
	result, err := jianyingdraft.PruneRegistrationBackups(root, keep, time.Duration(days)*24*time.Hour, dryRun)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "jianying_backup_prune_failed", "清理剪映注册备份失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// storyboardPlan mirrors ProductionPlanV2 / TimelineShotV2 fields the preview needs.
// v1 plans use a string transition and plan_version 1.0, so they are rejected below.
type storyboardPlan struct {
	PlanVersion      string  `json:"plan_version"`
	ProjectName      string  `json:"project_name"`
	ProjectDurationS float64 `json:"project_duration_s"`
	Timeline         []struct {
		ShotNo     int      `json:"shot_no"`
		StartS     float64  `json:"start_s"`
		EndS       float64  `json:"end_s"`
		MediaKind  string   `json:"media_kind"`
		SourcePath string   `json:"source_path"`
		SourceInS  *float64 `json:"source_in_s"`
		SourceOutS *float64 `json:"source_out_s"`
	} `json:"timeline"`
	Graphics struct {
		Title struct {
			Text string `json:"text"`
		} `json:"title"`
		Captions struct {
			Items []struct {
				Text   string  `json:"text"`
				StartS float64 `json:"start_s"`
				EndS   float64 `json:"end_s"`
			} `json:"items"`
		} `json:"captions"`
	} `json:"graphics"`
}

type storyboardShot struct {
	ShotNo     int      `json:"shot_no"`
	StartS     float64  `json:"start_s"`
	EndS       float64  `json:"end_s"`
	SourcePath string   `json:"source_path"`
	SourceInS  *float64 `json:"source_in_s,omitempty"`
	SourceOutS *float64 `json:"source_out_s,omitempty"`
	MediaKind  string   `json:"media_kind"`
	Caption    string   `json:"caption,omitempty"`
}

type storyboardView struct {
	Supported bool             `json:"supported"`
	Title     string           `json:"title"`
	DurationS float64          `json:"duration_s"`
	Shots     []storyboardShot `json:"shots"`
}

func (h *jianyingDraftsHandler) storyboard(w http.ResponseWriter, r *http.Request) {
	loaded, ok := h.loadStoryboard(w, r)
	if !ok {
		return
	}
	if loaded.unsupported || loaded.plan.PlanVersion != "2.0" {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}
	writeJSON(w, http.StatusOK, storyboardFromPlan(loaded.plan))
}

var storyboardFrameMu sync.Mutex

func (h *jianyingDraftsHandler) storyboardFrame(w http.ResponseWriter, r *http.Request) {
	loaded, ok := h.loadStoryboard(w, r)
	if !ok {
		return
	}
	if loaded.unsupported || loaded.plan.PlanVersion != "2.0" {
		writeError(w, http.StatusUnprocessableEntity, "storyboard_unsupported", "这份计划不是 v2，不能预览分镜。")
		return
	}
	plan := loaded.plan
	shotNo, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("shot")))
	if err != nil || shotNo < 1 {
		writeError(w, http.StatusBadRequest, "invalid_shot", "请提供镜头序号。")
		return
	}
	var shot *storyboardShot
	for i := range plan.Timeline {
		if plan.Timeline[i].ShotNo == shotNo {
			view := shotFromPlan(plan, i)
			shot = &view
			break
		}
	}
	if shot == nil {
		writeError(w, http.StatusNotFound, "storyboard_shot_not_found", "没有这个镜头。")
		return
	}
	source := strings.TrimSpace(shot.SourcePath)
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusUnprocessableEntity, "storyboard_source_missing", "镜头素材不在这台电脑上。")
		return
	}
	ffmpeg := strings.TrimSpace(loaded.runtime.FFmpegPath)
	if ffmpeg == "" {
		ffmpeg = effectiveFFmpeg(loaded.runtime)
	}
	if ffmpeg == "" {
		writeError(w, http.StatusServiceUnavailable, "ffmpeg_unavailable", "没有可用的 ffmpeg。")
		return
	}
	if stat, statErr := os.Stat(ffmpeg); statErr != nil || stat.IsDir() {
		if resolved, lookErr := exec.LookPath(ffmpeg); lookErr == nil {
			ffmpeg = resolved
		} else {
			writeError(w, http.StatusServiceUnavailable, "ffmpeg_unavailable", "没有可用的 ffmpeg。")
			return
		}
	}
	taskID := canonicalTaskID(r.URL.Query().Get("task_id"))
	dest := filepath.Join(loaded.runtime.DataRoot, "cache", "storyboard", taskID, strconv.Itoa(shotNo)+".jpg")
	if !pathUnderRoot(loaded.runtime.DataRoot, dest) {
		writeError(w, http.StatusForbidden, "plan_path_forbidden", "预览缓存不在数据目录内。")
		return
	}
	seek := 0.2
	if shot.SourceInS != nil {
		seek = *shot.SourceInS + 0.2
	}
	if seek < 0 {
		seek = 0
	}
	storyboardFrameMu.Lock()
	defer storyboardFrameMu.Unlock()
	if cached, statErr := os.Stat(dest); statErr == nil && cached.Mode().IsRegular() && cached.Size() > 0 {
		http.ServeFile(w, r, dest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "storyboard_cache_unwritable", "分镜缓存目录不可写："+err.Error())
		return
	}
	partial := dest + ".part"
	_ = os.Remove(partial)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(seek, 'f', 3, 64),
		"-i", source,
		"-frames:v", "1",
		"-vf", "scale=320:-1",
		"-q:v", "3",
		partial,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		_ = os.Remove(partial)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "storyboard_frame_timeout", "抽帧超时。")
			return
		}
		message := strings.TrimSpace(string(output))
		if len(message) > 300 {
			message = message[:300]
		}
		if message == "" {
			message = runErr.Error()
		}
		writeError(w, http.StatusUnprocessableEntity, "storyboard_frame_failed", "抽帧失败："+message)
		return
	}
	_ = os.Remove(dest)
	if err := os.Rename(partial, dest); err != nil {
		_ = os.Remove(partial)
		writeError(w, http.StatusUnprocessableEntity, "storyboard_frame_failed", "抽帧结果无法写入缓存。")
		return
	}
	http.ServeFile(w, r, dest)
}

type storyboardLoad struct {
	runtime     consoleSettings.Runtime
	plan        storyboardPlan
	unsupported bool
}

func (h *jianyingDraftsHandler) loadStoryboard(w http.ResponseWriter, r *http.Request) (storyboardLoad, bool) {
	taskID := canonicalTaskID(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "invalid_task_id", "任务 ID 必须是 UUID。")
		return storyboardLoad{}, false
	}
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "数据库不可用。")
		return storyboardLoad{}, false
	}
	runtime, err := h.runtime.Runtime(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not available.")
		return storyboardLoad{}, false
	}
	var manifestPath sql.NullString
	err = h.db.QueryRowContext(r.Context(), `SELECT manifest_path FROM codex_tasks WHERE id = ?`, taskID).Scan(&manifestPath)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "task_not_found", "混剪任务不存在。")
		return storyboardLoad{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "task_lookup_failed", "混剪任务读取失败。")
		return storyboardLoad{}, false
	}
	if !manifestPath.Valid || strings.TrimSpace(manifestPath.String) == "" {
		writeError(w, http.StatusNotFound, "task_manifest_missing", "任务还没有清单路径。")
		return storyboardLoad{}, false
	}
	planPath, err := productionPlanPath(manifestPath.String)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "task_manifest_invalid", "清单读取失败："+err.Error())
		return storyboardLoad{}, false
	}
	if !pathUnderRoot(runtime.DataRoot, planPath) {
		writeError(w, http.StatusForbidden, "plan_path_forbidden", "计划文件不在数据目录内。")
		return storyboardLoad{}, false
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "production_plan_missing", "找不到 production_plan.json。")
			return storyboardLoad{}, false
		}
		writeError(w, http.StatusUnprocessableEntity, "production_plan_unreadable", "计划文件读取失败："+err.Error())
		return storyboardLoad{}, false
	}
	var plan storyboardPlan
	if json.Unmarshal(raw, &plan) != nil {
		return storyboardLoad{unsupported: true}, true
	}
	return storyboardLoad{runtime: runtime, plan: plan}, true
}

func storyboardFromPlan(plan storyboardPlan) storyboardView {
	title := strings.TrimSpace(plan.Graphics.Title.Text)
	if title == "" {
		title = plan.ProjectName
	}
	shots := make([]storyboardShot, 0, len(plan.Timeline))
	for i := range plan.Timeline {
		shots = append(shots, shotFromPlan(plan, i))
	}
	return storyboardView{Supported: true, Title: title, DurationS: plan.ProjectDurationS, Shots: shots}
}

func shotFromPlan(plan storyboardPlan, index int) storyboardShot {
	item := plan.Timeline[index]
	shot := storyboardShot{
		ShotNo: item.ShotNo, StartS: item.StartS, EndS: item.EndS,
		SourcePath: item.SourcePath, SourceInS: item.SourceInS, SourceOutS: item.SourceOutS,
		MediaKind: item.MediaKind, Caption: captionForShot(plan, item.StartS, item.EndS),
	}
	return shot
}

func captionForShot(plan storyboardPlan, start, end float64) string {
	var parts []string
	for _, item := range plan.Graphics.Captions.Items {
		if item.EndS <= start || item.StartS >= end {
			continue
		}
		text := strings.TrimSpace(item.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// productionPlanPath matches montagescript.planPathFromManifest: <output_dir>/production_plan.json.
func productionPlanPath(manifestPath string) (string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	var manifest struct {
		OutputDir string `json:"output_dir"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	if strings.TrimSpace(manifest.OutputDir) == "" {
		return "", errors.New("manifest output_dir is required")
	}
	return filepath.Join(manifest.OutputDir, "production_plan.json"), nil
}

func canonicalTaskID(value string) string {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return id.String()
}

func pathUnderRoot(root, target string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}
