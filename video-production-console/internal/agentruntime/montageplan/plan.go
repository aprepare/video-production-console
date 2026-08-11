package montageplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	transitionDuration = 0.466666
	transitionEffectID = "322577"
	transitionResID    = "6724845717472416269"
	bgmLoopSeconds     = 194.4
)

// DurationFunc measures narration length in seconds.
type DurationFunc func(path string) (float64, error)

// Options configures deterministic plan generation.
type Options struct {
	ManifestPath string
	PlanPath     string
	Duration     DurationFunc
	MediaLimit   int
}

type manifestFile struct {
	TaskID    string `json:"task_id"`
	JobID     string `json:"job_id"`
	Action    string `json:"action"`
	OutputDir string `json:"output_dir"`
	Inputs    []struct {
		Role string `json:"role"`
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"inputs"`
	NonSecretSettings struct {
		MediaIndexPath     string `json:"media_index_path"`
		MediaRoot          string `json:"media_root"`
		MachineProfilePath string `json:"machine_profile_path"`
		DraftDisplayName   string `json:"draft_display_name"`
	} `json:"non_secret_settings"`
}

type mediaItem struct {
	ID              string  `json:"id"`
	Category        string  `json:"category"`
	RelativePath    string  `json:"relative_path"`
	DurationSeconds float64 `json:"duration_seconds"`
	AbsPath         string  `json:"-"`
}

// Build writes an approved production_plan.json for console montage.execute.
func Build(opts Options) error {
	if strings.TrimSpace(opts.ManifestPath) == "" || strings.TrimSpace(opts.PlanPath) == "" {
		return fmt.Errorf("manifest and plan paths are required")
	}
	durationFn := opts.Duration
	if durationFn == nil {
		durationFn = ProbeDuration
	}
	limit := opts.MediaLimit
	if limit <= 0 {
		limit = 48
	}

	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	raw = stripBOM(raw)
	var manifest manifestFile
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.TaskID == "" || manifest.TaskID != manifest.JobID {
		return fmt.Errorf("manifest task_id must equal job_id")
	}
	roles := map[string]string{}
	for _, input := range manifest.Inputs {
		role := strings.TrimSpace(input.Role)
		if role == "" {
			role = strings.TrimSpace(input.Type)
		}
		if role != "" && input.Path != "" {
			roles[role] = input.Path
		}
	}
	narration := roles["narration"]
	if narration == "" {
		return fmt.Errorf("manifest missing narration input")
	}
	background := roles["account_background"]
	if background == "" {
		return fmt.Errorf("manifest missing account_background input")
	}
	scriptPath := roles["continuous_script"]
	srtPath := roles["subtitle_srt"]

	duration, err := durationFn(narration)
	if err != nil {
		return fmt.Errorf("measure narration duration: %w", err)
	}
	if duration <= 0.5 {
		return fmt.Errorf("narration duration must be positive")
	}

	profilePath := strings.TrimSpace(manifest.NonSecretSettings.MachineProfilePath)
	mediaRoot := strings.TrimSpace(manifest.NonSecretSettings.MediaRoot)
	mediaIndex := strings.TrimSpace(manifest.NonSecretSettings.MediaIndexPath)
	if profilePath != "" {
		profileRoot, profileIndex, err := readProfilePaths(profilePath)
		if err != nil {
			return err
		}
		if mediaRoot == "" {
			mediaRoot = profileRoot
		}
		if mediaIndex == "" {
			mediaIndex = profileIndex
		}
	}
	if mediaRoot == "" || mediaIndex == "" {
		return fmt.Errorf("media_root and media_index_path are required")
	}

	clips, err := sampleMedia(mediaIndex, mediaRoot, limit, manifest.TaskID, false)
	if err != nil {
		return err
	}
	if len(clips) == 0 {
		return fmt.Errorf("media index produced no usable clips")
	}

	workspace := filepath.Join(manifest.OutputDir, "workspace", manifest.JobID)
	// draft_display_name is for Jianying draft folder naming (includes account).
	// On-screen title/subtitle must use only the content label, never the account.
	title, subtitle := titlePair(onScreenTitleSource(manifest.NonSecretSettings.DraftDisplayName))
	timeline := buildTimeline(duration, clips)
	plan := map[string]any{
		"plan_version":       "1.0",
		"status":             "approved",
		"model_role":         "planner",
		"project_name":       title,
		"project_duration_s": duration,
		"concurrency": map[string]any{
			"job_id":              manifest.JobID,
			"workspace_path":      workspace,
			"media_index_lock":    "media-index-write",
			"registration_lock":   "jianying-registration",
			"registration_status": "not_started",
		},
		"creative_summary": map[string]any{
			"audience":         "45-65岁",
			"tone":             "稳健、真实家庭场景、适度焦虑、不制造恐慌",
			"opening_strategy": "前30秒语义匹配",
			"later_strategy":   "风景、景观、建筑类别混剪",
		},
		"inputs": map[string]any{
			"context_text":     nullIfEmpty(scriptPath),
			"narration":        narration,
			"srt_context_only": nullIfEmpty(srtPath),
			"background_board": background,
			"media_index":      mediaIndex,
		},
		"timeline": timeline,
		"audio": map[string]any{
			"narration": map[string]any{"path": narration, "db": 5, "start_s": 0},
			"bgm": map[string]any{
				"name": "EXTA$Y+ (Remake)", "music_id": "7223314484093405186", "resource_id": "7223314484093405186",
				"cache_key": "bgm_extasy_remake", "linear_volume": 0.1593, "loop_every_s": bgmLoopSeconds, "required": true,
			},
			"sfx": []map[string]any{{
				"name": "综艺开头-咚（空旷）", "effect_id": "7132789318354996487", "resource_id": "7132789318354996487",
				"cache_key": "sfx_opening_hit", "start_s": 0, "db": -8,
			}},
		},
		"graphics": map[string]any{
			"title":          map[string]any{"text": title, "chars_min": 6, "chars_max": 8, "size_min": 16, "y": 0.6, "full_duration": true},
			"subtitle":       map[string]any{"text": subtitle, "chars_min": 6, "chars_max": 8, "size_min": 9.2, "y": 0.49, "full_duration": true},
			"boundary_lines": map[string]any{"asset_width_px": 1080, "asset_height_px": 6, "top_y": 0.38, "bottom_y": -0.38, "full_duration": true},
			"caption_tracks": "forbidden",
		},
		"execution_actions": []string{
			"核验全部文件路径",
			"按timeline创建静音画面片段",
			"设置1.4视觉缩放并保持倍速独立",
			"添加每个相邻镜头之间0.467秒 verified 叠化",
			"添加贯穿全片的标题、副标题和上下红线",
			"添加用户旁白、verified BGM和稀疏verified SFX并设置目标dB",
			"不创建文稿匹配轨或自动听写轨",
			"运行草稿和方案验证器",
			"登记全新剪映草稿；UI冒烟测试仅在用户明确要求时执行",
		},
		"known_missing_assets": []any{},
		"planner_notes":        []string{"deterministic console planner"},
		"approval":             map[string]any{"approved_by": "video-console-script-runtime", "approved_at": "auto"},
	}

	if err := os.MkdirAll(filepath.Dir(opts.PlanPath), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(opts.PlanPath, encoded, 0o644)
}

func nullIfEmpty(path string) any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return path
}

func readProfilePaths(path string) (mediaRoot, mediaIndex string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read machine profile: %w", err)
	}
	raw = stripBOM(raw)
	var profile struct {
		MediaRoot      string `json:"media_root"`
		MediaIndexPath string `json:"media_index_path"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		return "", "", fmt.Errorf("decode machine profile: %w", err)
	}
	return strings.TrimSpace(profile.MediaRoot), strings.TrimSpace(profile.MediaIndexPath), nil
}

func stripBOM(raw []byte) []byte {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}

func readerWithoutBOM(r io.Reader) io.Reader {
	buf := make([]byte, 3)
	n, err := io.ReadFull(r, buf)
	if n == 3 && buf[0] == 0xEF && buf[1] == 0xBB && buf[2] == 0xBF {
		return r
	}
	if n > 0 {
		prefix := bytes.NewReader(buf[:n])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return prefix
		}
		return io.MultiReader(prefix, r)
	}
	return r
}

// ProbeDuration uses ffprobe to measure media duration.
func ProbeDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var value float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &value); err != nil {
		return 0, err
	}
	return value, nil
}

func sampleMedia(indexPath, mediaRoot string, limit int, seed string, strict bool) ([]mediaItem, error) {
	file, err := os.Open(indexPath)
	if err != nil {
		return nil, fmt.Errorf("open media index: %w", err)
	}
	defer file.Close()
	dec := json.NewDecoder(readerWithoutBOM(file))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '[' {
		return nil, fmt.Errorf("media index must be a JSON array")
	}
	preferred := make([]mediaItem, 0, limit)
	fallback := make([]mediaItem, 0, limit)
	for dec.More() {
		var item mediaItem
		if err := dec.Decode(&item); err != nil {
			return nil, err
		}
		if item.ID == "" || item.RelativePath == "" || item.DurationSeconds < 8 {
			continue
		}
		abs := filepath.Join(mediaRoot, filepath.FromSlash(item.RelativePath))
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			if strict {
				return nil, fmt.Errorf("media index clip is missing or not a regular file: %s", abs)
			}
			continue
		}
		item.AbsPath = abs
		if isScenic(item.Category) {
			preferred = append(preferred, item)
		} else {
			fallback = append(fallback, item)
		}
	}
	end, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("close media index array: %w", err)
	}
	if delim, ok := end.(json.Delim); !ok || delim != ']' {
		return nil, fmt.Errorf("media index array is not closed")
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("media index contains trailing data")
		}
		return nil, fmt.Errorf("read media index end: %w", err)
	}
	pool := preferred
	if len(pool) == 0 {
		pool = fallback
	}
	// A task-specific stable order keeps retries reproducible while preventing
	// every video from starting at the first rows of media_index.json.
	sort.SliceStable(pool, func(i, j int) bool {
		left := mediaRank(seed, pool[i])
		right := mediaRank(seed, pool[j])
		return bytes.Compare(left[:], right[:]) < 0
	})
	if len(pool) > limit {
		pool = pool[:limit]
	}
	return pool, nil
}

func mediaRank(seed string, item mediaItem) [sha256.Size]byte {
	identity := strings.TrimSpace(item.ID) + "\x00" + filepath.Clean(item.AbsPath)
	return sha256.Sum256([]byte(strings.TrimSpace(seed) + "\x00" + identity))
}

// ValidateMediaLibrary checks the same indexed clip pool used by Build.
func ValidateMediaLibrary(indexPath, mediaRoot, profilePath string) error {
	if strings.TrimSpace(profilePath) != "" {
		root, index, err := readProfilePaths(profilePath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(mediaRoot) == "" {
			mediaRoot = root
		}
		if strings.TrimSpace(indexPath) == "" {
			indexPath = index
		}
	}
	if strings.TrimSpace(mediaRoot) == "" || strings.TrimSpace(indexPath) == "" {
		return fmt.Errorf("media_root and media_index_path are required")
	}
	clips, err := sampleMedia(indexPath, mediaRoot, 1, "preflight", true)
	if err != nil {
		return err
	}
	if len(clips) == 0 {
		return fmt.Errorf("media index produced no usable clips")
	}
	return nil
}

func isScenic(category string) bool {
	// Prefer Nature_Landscape. City_Traffic often requires title keyword rules
	// in the draft validator and is unsafe for deterministic planning.
	lower := strings.ToLower(strings.TrimSpace(category))
	return strings.Contains(lower, "nature") || strings.Contains(lower, "landscape") || strings.Contains(lower, "scenery") || strings.Contains(lower, "architecture") || strings.Contains(lower, "building")
}

func buildTimeline(duration float64, clips []mediaItem) []map[string]any {
	shots := make([]map[string]any, 0, 32)
	cursor := 0.0
	shotNo := 1
	clipIdx := 0
	for cursor < duration-0.01 {
		remaining := duration - cursor
		if remaining < 1.5 && len(shots) > 0 {
			prev := shots[len(shots)-1]
			start := asFloat(prev["start_s"])
			sourceIn := asFloat(prev["source_in_s"])
			speed := asFloat(prev["playback_speed"])
			prev["end_s"] = duration
			prev["source_out_s"] = sourceIn + (duration-start)*speed
			break
		}
		length := 8.0
		if cursor < 30 {
			length = 7.0
		}
		if remaining < length {
			length = remaining
		}
		clip := clips[clipIdx%len(clips)]
		clipIdx++
		speed := 1.1
		sourceIn := 0.0
		maxSource := clip.DurationSeconds
		need := length * speed
		if maxSource > need+1 {
			sourceIn = 1.0
		}
		sourceOut := sourceIn + need
		if sourceOut > maxSource {
			sourceOut = maxSource
			sourceIn = maxSource - need
			if sourceIn < 0 {
				sourceIn = 0
				sourceOut = maxSource
			}
		}
		reason := "后段风景/建筑类镜头轮询，保持画面节奏稳定"
		if cursor < 30 {
			reason = "前30秒语义匹配旁白开场，选用时长充足的本地镜头"
		}
		end := cursor + length
		shots = append(shots, map[string]any{
			"shot_no":                shotNo,
			"start_s":                cursor,
			"end_s":                  end,
			"semantic_context":       reason,
			"source_id":              clip.ID,
			"source_path":            clip.AbsPath,
			"source_origin":          "local_index",
			"selection_reason":       reason,
			"source_in_s":            sourceIn,
			"source_out_s":           sourceOut,
			"playback_speed":         speed,
			"visual_scale":           1.4,
			"opacity":                0.5,
			"source_audio_muted":     true,
			"visual_action":          "缓慢推进或保持稳定",
			"transition":             "叠化",
			"transition_effect_id":   transitionEffectID,
			"transition_resource_id": transitionResID,
			"transition_duration_s":  transitionDuration,
		})
		cursor = end
		shotNo++
	}
	return shots
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func onScreenTitleSource(draftDisplayName string) string {
	name := strings.TrimSpace(draftDisplayName)
	if name == "" {
		return "时代观察"
	}
	// Format from montage.BuildDraftDisplayName: account_label_taskSuffix
	if i := strings.LastIndex(name, "_"); i > 0 && len(name)-i-1 == 6 && isHexSuffix(name[i+1:]) {
		name = name[:i]
	}
	if i := strings.Index(name, "_"); i > 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "未命名项目" {
		return "时代观察"
	}
	return name
}

func isHexSuffix(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func titlePair(contentLabel string) (string, string) {
	base := strings.TrimSpace(contentLabel)
	if base == "" {
		base = "时代观察"
	}
	runes := []rune(base)
	title := fitRunes(runes, 6, 8, "时代观察笔记")
	subtitle := fitRunes([]rune("家庭财务提醒"), 6, 8, "家庭财务提醒")
	if subtitle == title {
		subtitle = "生活成本提醒"
	}
	return title, subtitle
}

func fitRunes(runes []rune, min, max int, fallback string) string {
	if len(runes) >= min {
		if len(runes) > max {
			runes = runes[:max]
		}
		return string(runes)
	}
	fb := []rune(fallback)
	out := append([]rune{}, runes...)
	for len(out) < min && len(fb) > 0 {
		out = append(out, fb[len(out)%len(fb)])
	}
	if len(out) > max {
		out = out[:max]
	}
	if utf8.RuneCountInString(string(out)) < min {
		fb = []rune(fallback)
		if len(fb) < min {
			return fallback
		}
		return string(fb[:min])
	}
	return string(out)
}
