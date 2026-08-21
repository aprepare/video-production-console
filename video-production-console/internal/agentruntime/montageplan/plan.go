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

	"video-production-console/internal/domain"
	"video-production-console/internal/narration"
	"video-production-console/internal/publishing"
	"video-production-console/internal/spokenlines"
)

// manifestBGM mirrors codex.ManifestBGM: an analyzed local music file that
// replaces the built-in verified BGM.
type manifestBGM struct {
	Name            string  `json:"name"`
	FilePath        string  `json:"file_path"`
	DurationS       float64 `json:"duration_s"`
	UsableHeadS     float64 `json:"usable_head_s"`
	ClimaxStartS    float64 `json:"climax_start_s"`
	ClimaxDurationS float64 `json:"climax_duration_s"`
	Volume          float64 `json:"volume"`
}

const (
	sfxMinGapSeconds   = 12.0
	sfxLongFormSeconds = 240.0
)

type verifiedSFX struct {
	Name       string
	EffectID   string
	ResourceID string
	CacheKey   string
}

// DurationFunc measures narration length in seconds.
type DurationFunc func(path string) (float64, error)

// Options configures deterministic plan generation.
// CaptionMode and MixPreset only affect BuildV2; Build (v1) ignores them.
type Options struct {
	ManifestPath string
	PlanPath     string
	Duration     DurationFunc
	MediaLimit   int
	CaptionMode  CaptionMode
	MixPreset    string
	FFprobePath  string
	CatalogPath  string
	Catalog      CatalogReader
	Analyzer     IntentAnalyzer
	Embedder     Embedder
	ShotSelector ShotSelector
	// LineBreaker supplies word-safe caption lines. A nil breaker keeps the
	// deterministic splitter. Production montage leaves this nil while
	// CaptionLLMLineBreakerEnabled is false.
	LineBreaker LineBreaker
	// SelectMode is landscape (method one, default) or movie_catalog (method three).
	SelectMode string
}

type manifestFile struct {
	TaskID    string `json:"task_id"`
	JobID     string `json:"job_id"`
	Skill     string `json:"skill"`
	Action    string `json:"action"`
	OutputDir string `json:"output_dir"`
	Inputs    []struct {
		Role string `json:"role"`
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"inputs"`
	NonSecretSettings struct {
		MediaIndexPath     string               `json:"media_index_path"`
		MediaRoot          string               `json:"media_root"`
		MachineProfilePath string               `json:"machine_profile_path"`
		DraftDisplayName   string               `json:"draft_display_name"`
		BoardTitle         string               `json:"board_title"`
		BoardSubtitle      string               `json:"board_subtitle"`
		MediaCatalogPath   string               `json:"media_catalog_path"`
		FFprobePath        string               `json:"ffprobe_path"`
		DataRoot           string               `json:"data_root"`
		MontageStyle       *domain.MontageStyle `json:"montage_style"`
		MontageBGM         *manifestBGM         `json:"montage_bgm"`
	} `json:"non_secret_settings"`
	Project *struct {
		ID        string `json:"id"`
		AccountID string `json:"account_id"`
	} `json:"project"`
}

// planContext carries everything both plan builders derive from Options and
// the task manifest before they diverge into v1 or v2 output.
type planContext struct {
	manifest     manifestFile
	narration    string
	background   string
	scriptPath   string
	srtPath      string
	duration     float64
	mediaRoot    string
	mediaIndex   string
	resources    montageResources
	limit        int
	timingPath   string
	timing       *narration.WordTimingDocument
	keywords     *spokenlines.KeywordDoc
	keywordsNote string
}

// loadPlanContext validates the options, decodes the manifest, measures the
// narration and resolves media paths plus verified resources. It is shared by
// Build (v1) and BuildV2 and must keep v1 behaviour exactly.
func loadPlanContext(opts Options) (*planContext, error) {
	if strings.TrimSpace(opts.ManifestPath) == "" || strings.TrimSpace(opts.PlanPath) == "" {
		return nil, fmt.Errorf("manifest and plan paths are required")
	}
	limit := opts.MediaLimit
	if limit <= 0 {
		// Scenic index used to cap at 48, which forced reuse on long
		// narrations. 256 leaves room for Weather_Water_Fire plus later adds.
		limit = 256
	}

	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	raw = stripBOM(raw)
	var manifest manifestFile
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.TaskID == "" || manifest.TaskID != manifest.JobID {
		return nil, fmt.Errorf("manifest task_id must equal job_id")
	}
	durationFn := opts.Duration
	if durationFn == nil {
		probe := strings.TrimSpace(opts.FFprobePath)
		if probe == "" {
			probe = strings.TrimSpace(manifest.NonSecretSettings.FFprobePath)
		}
		if probe != "" {
			durationFn = func(path string) (float64, error) { return probeDurationWith(probe, path) }
		} else {
			durationFn = ProbeDuration
		}
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
	narrationPath := roles["narration"]
	if narrationPath == "" {
		return nil, fmt.Errorf("manifest missing narration input")
	}
	background := roles["account_background"]
	if background == "" {
		return nil, fmt.Errorf("manifest missing account_background input")
	}

	duration, err := durationFn(narrationPath)
	if err != nil {
		return nil, fmt.Errorf("measure narration duration: %w", err)
	}
	if duration <= 0.5 {
		return nil, fmt.Errorf("narration duration must be positive")
	}
	timingPath := roles["word_timing"]
	var timing *narration.WordTimingDocument
	if timingPath != "" {
		doc, err := decodeWordTiming(timingPath)
		if err != nil {
			return nil, err
		}
		timing = &doc
	}

	profilePath := strings.TrimSpace(manifest.NonSecretSettings.MachineProfilePath)
	mediaRoot := strings.TrimSpace(manifest.NonSecretSettings.MediaRoot)
	mediaIndex := strings.TrimSpace(manifest.NonSecretSettings.MediaIndexPath)
	if profilePath != "" {
		profileRoot, profileIndex, err := readProfilePaths(profilePath)
		if err != nil {
			return nil, err
		}
		if mediaRoot == "" {
			mediaRoot = profileRoot
		}
		if mediaIndex == "" {
			mediaIndex = profileIndex
		}
	}
	if mediaRoot == "" || mediaIndex == "" {
		return nil, fmt.Errorf("media_root and media_index_path are required")
	}
	resources, err := loadMontageResources(profilePath)
	if err != nil {
		return nil, err
	}
	ctx := &planContext{
		manifest:   manifest,
		narration:  narrationPath,
		background: background,
		scriptPath: roles["continuous_script"],
		srtPath:    roles["subtitle_srt"],
		duration:   duration,
		mediaRoot:  mediaRoot,
		mediaIndex: mediaIndex,
		resources:  resources,
		limit:      limit,
		timingPath: timingPath,
		timing:     timing,
	}
	// Keyword annotations are optional and never block a montage: a missing
	// or malformed file just falls back to the local keyword list.
	if keywordsPath := roles["caption_keywords"]; keywordsPath != "" {
		if raw, readErr := os.ReadFile(keywordsPath); readErr != nil {
			ctx.keywordsNote = "caption_keywords_unreadable: " + readErr.Error()
		} else if doc, parseErr := spokenlines.ParseKeywordDoc(stripBOM(raw)); parseErr != nil {
			ctx.keywordsNote = "caption_keywords_invalid: " + parseErr.Error()
		} else {
			ctx.keywords = &doc
		}
	}
	return ctx, nil
}

func decodeWordTiming(path string) (narration.WordTimingDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return narration.WordTimingDocument{}, fmt.Errorf("read word timing: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(stripBOM(raw))))
	dec.DisallowUnknownFields()
	var in narration.WordTimingDocument
	if err := dec.Decode(&in); err != nil {
		return narration.WordTimingDocument{}, fmt.Errorf("decode word timing: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return narration.WordTimingDocument{}, fmt.Errorf("word timing must contain one JSON value")
	}
	if in.SchemaVersion != 1 {
		return narration.WordTimingDocument{}, fmt.Errorf("unsupported word timing schema_version")
	}
	doc, err := narration.NewWordTimingDocument(in.Script, in.Provider, in.Hash, in.Words)
	if err != nil {
		return narration.WordTimingDocument{}, err
	}
	if strings.TrimSpace(in.ScriptHash) == "" || in.ScriptHash != doc.ScriptHash {
		return narration.WordTimingDocument{}, fmt.Errorf("word timing script_hash mismatch")
	}
	doc.Hash, doc.Provider = in.Hash, in.Provider
	return doc, nil
}

// Build writes an approved production_plan.json for console montage.execute.
func Build(opts Options) error {
	ctx, err := loadPlanContext(opts)
	if err != nil {
		return err
	}
	manifest := ctx.manifest
	narration := ctx.narration
	background := ctx.background
	scriptPath := ctx.scriptPath
	srtPath := ctx.srtPath
	duration := ctx.duration
	mediaRoot := ctx.mediaRoot
	mediaIndex := ctx.mediaIndex
	resources := ctx.resources

	clips, err := sampleMedia(mediaIndex, mediaRoot, ctx.limit, manifest.TaskID, false)
	if err != nil {
		return err
	}
	if len(clips) == 0 {
		return fmt.Errorf("media index produced no usable clips")
	}
	candidates := make([]rankedCandidate, 0, len(clips))
	for _, clip := range clips {
		candidates = append(candidates, rankedCandidate{Item: clip})
	}
	// Quota warnings stay out of the v1 plan JSON by contract; callers that
	// need them observe selectTimeline directly.
	selection, _, err := selectTimeline(candidates, duration, manifest.TaskID, movieMixPolicy())
	if err != nil {
		return err
	}
	ordered := make([]mediaItem, 0, len(selection))
	for _, planned := range selection {
		ordered = append(ordered, planned.Item)
	}

	workspace := filepath.Join(manifest.OutputDir, "workspace", manifest.JobID)
	// draft_display_name is for Jianying draft folder naming (includes account).
	// On-screen title/subtitle must use only the content label, never the account.
	title, subtitle := boardTitles(ctx, "", "")
	timeline := buildTimeline(duration, ordered, resources.Transition)
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
			"bgm":       bgmPlacement(resources.BGM),
			"sfx":       buildSFXPlacements(duration, resources.SFX),
		},
		"graphics": map[string]any{
			"title":          map[string]any{"text": title, "chars_min": boardTitleMinRunes, "chars_max": boardTitleMaxRunes, "size_min": 16, "y": 0.6, "full_duration": true},
			"subtitle":       map[string]any{"text": subtitle, "chars_min": boardTitleMinRunes, "chars_max": boardTitleMaxRunes, "size_min": 9.2, "y": 0.49, "full_duration": true},
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

func bgmPlacement(bgm bgmResource) map[string]any {
	return map[string]any{
		"name": bgm.Name, "music_id": bgm.MusicID, "resource_id": bgm.ResourceID,
		"cache_key": bgm.CacheKey, "linear_volume": bgm.LinearVolume,
		"loop_every_s": bgm.LoopEveryS, "usable_head_s": bgm.UsableHeadS,
		"climax_start_s": bgm.ClimaxStartS, "climax_duration_s": bgm.ClimaxDurationS,
		"required": bgm.Required,
	}
}

// buildSFXPlacements emits verified SFX that satisfy jianying-montage-draft validate-plan:
// opening hit at 0s, optional mid/end hits for >=240s videos (3-5 total, >=12s apart).
// library[0] is the opening hit; a shorter library is reused cyclically so the
// long-form count stays inside 3-5.
func buildSFXPlacements(duration float64, library []verifiedSFX) []map[string]any {
	if len(library) == 0 {
		library = defaultMontageResources().SFX
	}
	count := 1
	if duration >= sfxLongFormSeconds {
		count = 4
		if duration < sfxLongFormSeconds+2*sfxMinGapSeconds {
			count = 3
		}
		if maxByGap := int(duration/sfxMinGapSeconds) + 1; maxByGap < count {
			count = maxByGap
		}
		if count < 3 {
			count = 3
		}
		if count > 5 {
			count = 5
		}
		if count > len(library) {
			count = len(library)
		}
	}

	starts := make([]float64, 0, count)
	starts = append(starts, 0)
	if count > 1 {
		span := duration - sfxMinGapSeconds
		if span < sfxMinGapSeconds {
			span = sfxMinGapSeconds
		}
		for i := 1; i < count; i++ {
			frac := float64(i) / float64(count)
			start := roundSFXStart(span * frac)
			prev := starts[len(starts)-1]
			if start-prev < sfxMinGapSeconds {
				start = prev + sfxMinGapSeconds
			}
			if start >= duration {
				start = duration - 0.001
			}
			if start <= prev {
				continue
			}
			starts = append(starts, start)
		}
	}
	// Long-form validation requires 3-5 placements; keep filling from the end if rounding collapsed any.
	for duration >= sfxLongFormSeconds && len(starts) < 3 {
		next := starts[len(starts)-1] + sfxMinGapSeconds
		if next >= duration {
			next = duration - 0.001
		}
		if next <= starts[len(starts)-1] {
			break
		}
		starts = append(starts, next)
	}

	out := make([]map[string]any, 0, len(starts))
	for i, start := range starts {
		// starts[0] is 0s, so the first placement is always library[0].
		item := library[i%len(library)]
		out = append(out, map[string]any{
			"name":        item.Name,
			"effect_id":   item.EffectID,
			"resource_id": item.ResourceID,
			"cache_key":   item.CacheKey,
			"start_s":     start,
			"db":          -8,
		})
	}
	return out
}

func timingHighlightCaptions(doc *narration.WordTimingDocument, durations ...float64) []CaptionItem {
	if doc == nil {
		return []CaptionItem{}
	}
	duration := doc.Duration
	if len(durations) > 0 && durations[0] > 0 {
		duration = durations[0]
	}
	keys := []string{"但是", "然而", "不过", "所以", "因此", "结论", "总结", "关键", "最后"}
	var out []CaptionItem
	for i := 0; i < len(doc.Words); i++ {
		text := doc.Words[i].Text
		j := i
		for j+1 < len(doc.Words) && j-i < 4 {
			text += doc.Words[j+1].Text
			j++
		}
		hit := false
		for _, k := range keys {
			if strings.Contains(text, k) {
				hit = true
			}
		}
		if !hit && !strings.Contains(text, "%") {
			continue
		}
		j = i
		for j+1 < len(doc.Words) && j-i < 4 {
			n := doc.Words[j+1].Text
			if strings.Contains(n, "但是") || strings.Contains(n, "然而") || strings.Contains(n, "不过") || strings.Contains(n, "所以") || strings.Contains(n, "因此") || strings.Contains(n, "结论") || strings.Contains(n, "总结") || strings.Contains(n, "关键") || strings.Contains(n, "最后") || strings.Contains(n, "%") || len([]rune(n)) <= 2 {
				j++
			} else {
				break
			}
		}
		start := doc.Words[i].StartTime
		end := doc.Words[j].EndTime
		if end-start < 2 {
			end = start + 2
		}
		if end-start > 4 {
			end = start + 4
		}
		if end > duration {
			end = duration
		}
		if end <= start {
			continue
		}
		if len(out) > 0 && start < out[len(out)-1].EndS {
			continue
		}
		txt := ""
		for k := i; k <= j; k++ {
			txt += doc.Words[k].Text
		}
		kind := CaptionConclusion
		if strings.Contains(txt, "%") || strings.ContainsAny(txt, "0123456789") {
			kind = CaptionNumber
		} else if strings.Contains(txt, "但是") || strings.Contains(txt, "然而") || strings.Contains(txt, "不过") {
			kind = CaptionTurningPoint
		}
		if len(out) > 0 {
			covered := 0.0
			for _, x := range out {
				covered += x.EndS - x.StartS
			}
			if covered+end-start > duration*0.25 {
				break
			}
		}
		out = append(out, CaptionItem{Text: txt, StartS: start, EndS: end, Kind: kind, Style: "highlight_v1", Intro: "none"})
	}
	return out
}

// sfxBudget scales the cue count with runtime — about one hit per 45 seconds,
// capped at the plan validator's 12 — so a six-minute script keeps landing
// hits at its turning points all the way through instead of spending the whole
// allowance inside the number-dense opening minute.
func sfxBudget(duration float64) int {
	budget := int(duration/45) + 1
	if budget < 1 {
		budget = 1
	}
	if duration >= sfxLongFormSeconds && budget < 3 {
		budget = 3
	}
	if budget > 12 {
		budget = 12
	}
	return budget
}

func buildSemanticSFXPlacements(duration float64, library []verifiedSFX, doc *narration.WordTimingDocument) []map[string]any {
	if doc == nil {
		return buildSFXPlacements(duration, library)
	}
	base := buildSFXPlacements(duration, library)
	if len(library) == 0 {
		return base
	}
	type sfxCue struct {
		start float64
		kind  string
		turn  bool
	}
	var candidates []sfxCue
	for i := range doc.Words {
		// The window starts at the current word (including i == 0) and may
		// consume at most three following words when adjacent in time.
		semantic := doc.Words[i].Text
		for k := i + 1; k < len(doc.Words) && k <= i+3; k++ {
			if doc.Words[k].StartTime-doc.Words[k-1].EndTime > 1 {
				break
			}
			semantic += doc.Words[k].Text
		}
		kind := semanticSFXKind(semantic, doc.Words[i].Text)
		if kind == "" {
			continue
		}
		start := roundSFXStart(doc.Words[i].StartTime)
		if start < sfxMinGapSeconds || start >= duration {
			continue
		}
		candidates = append(candidates, sfxCue{start: start, kind: kind, turn: kind != "sfx_water_drop"})
	}
	budget := sfxBudget(duration)
	selected := []sfxCue{{start: 0, kind: "sfx_opening_hit", turn: true}}
	fits := func(start float64) bool {
		for _, s := range selected {
			delta := start - s.start
			if delta < 0 {
				delta = -delta
			}
			if delta < sfxMinGapSeconds {
				return false
			}
		}
		return true
	}
	// One cue per time bucket: slicing the runtime into budget-sized windows
	// forces the hits to follow the script all the way to the end instead of
	// spending every slot inside the number-dense opening minute. Inside a
	// bucket the first turning point beats any number cue.
	if budget > 1 && len(candidates) > 0 {
		bucketLen := duration / float64(budget)
		for b := 0; b < budget && len(selected) < budget; b++ {
			lo := float64(b) * bucketLen
			hi := lo + bucketLen
			var pick *sfxCue
			for idx := range candidates {
				c := &candidates[idx]
				if c.start < lo || c.start >= hi || !fits(c.start) {
					continue
				}
				if pick == nil || (c.turn && !pick.turn) {
					pick = c
				}
				if pick.turn {
					break
				}
			}
			if pick != nil {
				selected = append(selected, *pick)
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].start < selected[j].start })
	if duration >= sfxLongFormSeconds {
		for len(selected) < 3 {
			n := selected[len(selected)-1].start + sfxMinGapSeconds
			if n >= duration {
				break
			}
			selected = append(selected, sfxCue{start: n, kind: "sfx_whoosh", turn: true})
		}
		if len(selected) < 3 {
			return base
		}
	}
	byKey := make(map[string]verifiedSFX, len(library))
	for _, item := range library {
		byKey[item.CacheKey] = item
	}
	out := make([]map[string]any, 0, len(selected))
	for _, c := range selected {
		x, ok := byKey[c.kind]
		if !ok {
			// Never substitute a different semantic type when a resource is
			// absent; the cue is omitted and long-form fallback below can apply.
			continue
		}
		out = append(out, map[string]any{"name": x.Name, "effect_id": x.EffectID, "resource_id": x.ResourceID, "cache_key": x.CacheKey, "start_s": c.start, "db": -8})
	}
	if duration >= sfxLongFormSeconds && len(out) < 3 {
		return base
	}
	return out
}

func semanticSFXKind(window, token string) string {
	// Turns and conclusions outrank numbers: a clause like 但是到2026年 is a
	// narrative beat first and a statistic second, and 爆款口播稿 packs digits
	// into nearly every sentence, so number-first classification drowns the
	// real beats.
	for _, k := range []string{"记住", "说白了", "最惨", "\u7ed3\u8bba", "\u603b\u7ed3", "\u5173\u952e", "\u6700\u540e", "缁撹", "鎬荤粨", "鍏抽敭", "鏈€鍚?"} {
		if strings.Contains(window, k) {
			return "sfx_conclusion_hit"
		}
	}
	// 爆款口播稿 rarely writes 但是/然而 in full — its turns are colloquial
	// (但、结果、你以为、真相), so those count too.
	for _, k := range []string{"\u4f46", "\u7136\u800c", "\u4e0d\u8fc7", "\u6240\u4ee5", "\u56e0\u6b64", "结果", "你以为", "真相", "反过来", "也就是说", "换句话说", "问题来了", "注意", "别急", "浣嗘槸", "鐒惰€?)", "涓嶈繃", "鎵€浠?)", "鍥犳"} {
		if strings.Contains(window, k) {
			return "sfx_whoosh"
		}
	}
	// A few timing providers split the mojibake form of 然而 into these
	// single-rune tokens; require the pair in the same adjacent window.
	if strings.ContainsRune(window, '\u5a34') && strings.ContainsRune(window, '\u95ba') {
		return "sfx_whoosh"
	}
	if strings.ContainsRune(window, '\u6d63') && strings.ContainsRune(window, '\u93c4') {
		return "sfx_whoosh"
	}
	if strings.ContainsAny(window, "0123456789%") {
		return "sfx_water_drop"
	}
	return ""
}

func roundSFXStart(value float64) float64 {
	return float64(int(value*1000+0.5)) / 1000
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
	return probeDurationWith("ffprobe", path)
}

func probeDurationWith(binary, path string) (float64, error) {
	if strings.TrimSpace(binary) == "" {
		binary = "ffprobe"
	}
	cmd := exec.Command(binary, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
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
	return sampleMediaMatching(indexPath, mediaRoot, limit, seed, strict, nil)
}

func sampleMediaMatching(indexPath, mediaRoot string, limit int, seed string, strict bool, keep func(mediaItem) bool) ([]mediaItem, error) {
	scan, err := scanMediaIndex(indexPath, mediaRoot, strict)
	if err != nil {
		return nil, err
	}
	// Every usable row joins one shared pool: broll, movie shots and images
	// mix immediately, and the quota selector decides the final balance.
	pool := make([]mediaItem, 0, len(scan.rows))
	for _, row := range scan.rows {
		if keep != nil && !keep(row.item) {
			continue
		}
		if !row.usable {
			// An unusable row always precedes the error that ended the scan,
			// so strict keeps reporting it first.
			if strict {
				return nil, fmt.Errorf("media index clip is missing or not a regular file: %s", row.item.AbsPath)
			}
			continue
		}
		pool = append(pool, row.item)
	}
	if scan.err != nil {
		return nil, scan.err
	}
	// A task-specific stable order keeps retries reproducible while preventing
	// every video from starting at the first rows of media_index.json.
	sort.SliceStable(pool, func(i, j int) bool {
		left := mediaRank(seed, pool[i])
		right := mediaRank(seed, pool[j])
		return bytes.Compare(left[:], right[:]) < 0
	})
	// Rotate categories before truncating so the kept prefix is spread out too.
	pool = interleaveByCategory(pool)
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

// ValidateMovieCatalog checks that the method-three catalog file exists.
func ValidateMovieCatalog(catalogPath string) error {
	catalogPath = strings.TrimSpace(catalogPath)
	if catalogPath == "" {
		return fmt.Errorf("media_catalog_path is required for movie montage")
	}
	info, err := os.Stat(catalogPath)
	if err != nil {
		return fmt.Errorf("movie catalog missing: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("media_catalog_path must be a regular file")
	}
	return nil
}

func buildTimeline(duration float64, clips []mediaItem, transition transitionResource) []map[string]any {
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
			sourceOut := sourceIn + (duration-start)*speed
			maxSource := asFloat(prev["source_duration_s"])
			if maxSource <= 0 {
				maxSource = sourceOut
			}
			if sourceOut > maxSource+0.0001 {
				// Keep the last shot inside the real clip; slow it to fill remaining narration.
				sourceOut = maxSource
				if sourceOut <= sourceIn {
					sourceIn = 0
					sourceOut = maxSource
				}
				avail := sourceOut - sourceIn
				if avail > 0 {
					speed = avail / (duration - start)
				} else {
					speed = 1.0
				}
				prev["playback_speed"] = roundSFXStart(speed)
				prev["source_in_s"] = sourceIn
			}
			prev["end_s"] = duration
			prev["source_out_s"] = sourceOut
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
		var sourceIn, sourceOut, speed float64
		sourceDuration := clip.DurationSeconds
		switch {
		case clip.Kind == mediaKindImage:
			// A still image has no intrinsic duration: it fills the slot at
			// 1.0x and its indexed duration (usually 0) is reported as-is so
			// the tail-absorb clamp treats it as unbounded.
			sourceIn, sourceOut, speed = 0, roundSFXStart(length), 1.0
		case clip.hasShotRange():
			// Movie shots consume only their [in, out] window; offsets keep
			// source_in/out in full-source coordinates and the clamp boundary
			// at the shot's end.
			if avail := clip.availableSeconds(); avail+1e-9 < length {
				// The whole shot window is shorter than the slot. Shrinking
				// the slot would drift away from the quota selector's plan
				// and wrap around the selection, so slow the shot to fill
				// the slot instead.
				sourceIn = roundSFXStart(clip.SourceInSeconds)
				sourceOut = roundSFXStart(clip.SourceOutSeconds)
				speed = roundSFXStart(avail / length)
			} else {
				in, out, sp, fitted := fitShotToClip(length, avail)
				length = fitted
				sourceIn = roundSFXStart(in + clip.SourceInSeconds)
				sourceOut = roundSFXStart(out + clip.SourceInSeconds)
				speed = sp
			}
			sourceDuration = clip.SourceOutSeconds
		default:
			sourceIn, sourceOut, speed, length = fitShotToClip(length, clip.DurationSeconds)
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
			"source_duration_s":      sourceDuration,
			"source_in_s":            sourceIn,
			"source_out_s":           sourceOut,
			"playback_speed":         speed,
			"visual_scale":           1.4,
			"opacity":                0.5,
			"source_audio_muted":     true,
			"visual_action":          "缓慢推进或保持稳定",
			"transition":             transition.Name,
			"transition_effect_id":   transition.EffectID,
			"transition_resource_id": transition.ResourceID,
			"transition_duration_s":  transition.DurationS,
		})
		cursor = end
		shotNo++
	}
	return shots
}

// fitShotToClip chooses source_in/out and speed so source usage never exceeds indexed clip duration.
func fitShotToClip(length, maxSource float64) (sourceIn, sourceOut, speed, timelineLength float64) {
	if length < 0.5 {
		length = 0.5
	}
	if maxSource < 0.5 {
		maxSource = 0.5
	}
	try := func(candidateSpeed float64) (float64, float64, float64, bool) {
		need := length * candidateSpeed
		if maxSource >= need+1 {
			return 1.0, 1.0 + need, candidateSpeed, true
		}
		if maxSource >= need {
			return 0, need, candidateSpeed, true
		}
		return 0, 0, 0, false
	}
	if in, out, sp, ok := try(1.1); ok {
		return roundSFXStart(in), roundSFXStart(out), sp, length
	}
	if in, out, sp, ok := try(1.0); ok {
		return roundSFXStart(in), roundSFXStart(out), sp, length
	}
	// Indexed clip is shorter than the preferred shot: use the full clip at 1.0x.
	sourceIn = 0
	if maxSource > 1.5 {
		sourceIn = 1.0
	}
	sourceOut = maxSource
	speed = 1.0
	timelineLength = sourceOut - sourceIn
	if timelineLength < 0.5 {
		sourceIn = 0
		timelineLength = maxSource
		sourceOut = maxSource
	}
	return roundSFXStart(sourceIn), roundSFXStart(sourceOut), speed, timelineLength
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
	// Format from montage.BuildDraftDisplayName: account_label_MMDD-HHMM
	// (older drafts carried a 6-hex task suffix instead).
	if i := strings.LastIndex(name, "_"); i > 0 {
		suffix := name[i+1:]
		if (len(suffix) == 6 && isHexSuffix(suffix)) || isClockSuffix(suffix) {
			name = name[:i]
		}
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

// isClockSuffix matches the MMDD-HHMM datetime suffix on draft display names.
func isClockSuffix(value string) bool {
	if len(value) != 9 || value[4] != '-' {
		return false
	}
	for i, r := range value {
		if i == 4 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func boardTitles(ctx *planContext, aiTitle, aiSubtitle string) (string, string) {
	titleSrc := strings.TrimSpace(ctx.manifest.NonSecretSettings.BoardTitle)
	subtitleSrc := strings.TrimSpace(ctx.manifest.NonSecretSettings.BoardSubtitle)
	if titleSrc != "" {
		return FitBoardTitlePair(titleSrc, subtitleSrc)
	}
	if pkg := loadProjectPublishingPackage(ctx); pkg != nil && len(pkg.ShortTitles) > 0 {
		titleSrc = strings.TrimSpace(pkg.ShortTitles[0])
		if len(pkg.ShortTitles) > 1 {
			subtitleSrc = strings.TrimSpace(pkg.ShortTitles[1])
		}
		return FitBoardTitlePair(titleSrc, subtitleSrc)
	}
	if fitModelBoardTitle(aiTitle) != "" {
		return FitBoardTitlePair(aiTitle, aiSubtitle)
	}
	return FitBoardTitlePair(onScreenTitleSource(ctx.manifest.NonSecretSettings.DraftDisplayName), subtitleSrc)
}

// FitBoardTitlePair keeps non-empty on-screen board copy up to 15 characters.
func FitBoardTitlePair(titleSrc, subtitleSrc string) (string, string) {
	return titlePair(titleSrc, subtitleSrc)
}

func loadProjectPublishingPackage(ctx *planContext) *publishing.Package {
	if ctx == nil || ctx.manifest.Project == nil {
		return nil
	}
	root := strings.TrimSpace(ctx.manifest.NonSecretSettings.DataRoot)
	projectID := strings.TrimSpace(ctx.manifest.Project.ID)
	if root == "" || projectID == "" {
		return nil
	}
	pkg, err := (publishing.Reader{}).Read(filepath.Join(root, "projects", projectID, "publishing_package.json"), "")
	if err != nil {
		return nil
	}
	return &pkg
}

func titlePair(titleSrc, subtitleSrc string) (string, string) {
	title := fitBoardTitle(titleSrc, "时代观察笔记")
	subtitle := fitBoardTitle(subtitleSrc, "家庭财务提醒")
	if subtitle == title {
		subtitle = "生活成本提醒"
	}
	return title, subtitle
}

// fitBoardTitle keeps publishing copy intact up to the board limit. Empty copy
// uses the fallback, and copy inside the limit is never truncated: the board
// shrinks its font instead (see boardTextSize).
func fitBoardTitle(src, fallback string) string {
	runes := []rune(strings.TrimSpace(src))
	if len(runes) < boardTitleMinRunes {
		return fallback
	}
	if len(runes) > boardTitleMaxRunes {
		runes = runes[:boardTitleMaxRunes]
	}
	return string(runes)
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
