package montageplan

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const mixPresetScenicSemantic = "scenic_semantic"

// ScenicPolicy is independent of the immutable machine profile. Each task
// freezes this policy and its history before selection so retries are stable.
type ScenicPolicy struct {
	Version        int     `json:"version"`
	ChinaOnly      bool    `json:"china_only"`
	Orientation    string  `json:"orientation"`
	PlaybackSpeed  float64 `json:"playback_speed"`
	OpeningMin     float64 `json:"opening_min_s"`
	OpeningMax     float64 `json:"opening_max_s"`
	BodyMin        float64 `json:"body_min_s"`
	BodyMax        float64 `json:"body_max_s"`
	DissolveRatio  float64 `json:"dissolve_ratio"`
	MaxSourceUses  int     `json:"max_source_uses"`
	ReuseGap       int     `json:"reuse_gap"`
	HistoryWindow  int     `json:"history_window"`
	ModelSelection bool    `json:"model_selection"`
	EditorVersion  int     `json:"editor_version"`
}

func defaultScenicPolicy() ScenicPolicy {
	return ScenicPolicy{1, true, "landscape", 1, 6, 8, 6, 9, .45, 1, 5, 12, true, 1}
}
func (p ScenicPolicy) validate() error {
	if p.Version != 1 || !p.ChinaOnly || p.Orientation != "landscape" || p.PlaybackSpeed != 1 || p.OpeningMin < 3 || p.OpeningMax < p.OpeningMin || p.OpeningMax > 9 || p.BodyMin < 3 || p.BodyMax < p.BodyMin || p.BodyMax > 12 || p.DissolveRatio < 0 || p.DissolveRatio > .5 || p.MaxSourceUses < 1 || p.MaxSourceUses > 2 || p.ReuseGap < 3 || p.HistoryWindow < 0 || p.HistoryWindow > 50 {
		return fmt.Errorf("invalid scenic policy: this board requires China-only landscape footage at normal speed")
	}
	return nil
}
func scenicMixPolicy() mixPolicy {
	return mixPolicy{Targets: map[mediaKind]quotaRange{mediaKindBroll: {Min: .85, Max: 1}, mediaKindMovie: {Min: 0, Max: 0}, mediaKindImage: {Min: 0, Max: .15}}, MaxSourceUses: 1, MinSegmentsBetweenReuse: 5}
}

type scenicTheme struct {
	name    string
	cues    []string
	visuals []string
}

// These are editorial metaphors, not claims that stock footage depicts an
// actual news event. Selection is metadata-based and explicitly reports that.
var scenicThemes = []scenicTheme{
	{"乡村与土地", []string{"农村", "回村", "乡村", "乡下", "老家", "宅基地", "土地", "种地", "粮食", "农业", "田地"}, []string{"rural", "village", "farm", "rice", "terrace", "countryside", "农村", "乡村", "土地", "田园"}},
	{"住宅与房产", []string{"房子", "盖房", "盖楼", "买房", "房价", "楼市", "房产", "住宅", "房贷", "首付", "月供", "烂尾", "租房"}, []string{"housing", "house", "residential", "apartment", "construction", "building", "住宅", "房子", "楼房", "建筑"}},
	{"储蓄与家庭", []string{"存款", "存钱", "养老钱", "积蓄", "银行", "利息", "储蓄", "现金", "钱袋", "五笔钱", "乱花", "花钱"}, []string{"business", "market", "community", "residential", "family", "金融", "商业", "家庭", "生活", "住宅"}},
	{"流动与机会", []string{"迁移", "搬家", "流向", "转移", "轨道", "机会", "翻身", "发财", "时代", "趋势", "财富", "发展"}, []string{"transport", "bridge", "railway", "train", "river", "city", "道路", "流动", "城市", "经济", "桥"}},
	{"劳动与生产", []string{"工人", "工厂", "老板", "工资", "工作", "失业", "就业", "产业", "制造", "机器人", "人工智能", "技术"}, []string{"factory", "worker", "industrial", "construction", "office", "traffic", "工人", "建筑", "建设", "车流", "城市"}},
	{"家庭与养老", []string{"老人", "养老", "退休", "父母", "孩子", "家庭", "普通人", "老百姓", "年轻人", "生活"}, []string{"community", "people", "park", "walking", "family", "market", "elderly", "tai chi", "生活", "家庭", "社区", "养老"}},
	{"时间与积累", []string{"五年", "十年", "以后", "未来", "过去", "当年", "几年", "变化", "积累", "熬"}, []string{"ancient", "historic", "traditional", "sunset", "sunrise", "river", "architecture", "历史", "传统建筑", "时间", "变化"}},
	{"风险与压力", []string{"风险", "危险", "后悔", "亏", "债务", "负债", "压力", "危机", "下跌", "别急", "小心", "陷阱"}, []string{"rain", "cloud", "empty", "night", "traffic", "construction", "雨", "车流", "建筑", "变化"}},
}
var chinaTitlePattern = regexp.MustCompile(`(?i)\b(china|shanghai|beijing|shenzhen|guangzhou|chongqing|chengdu|hangzhou|suzhou|guilin|yangshuo|yunnan|fujian|hong kong|macau|nanjing|wuhan|pudong|wuzhen|hongcun)\b|中国|上海|北京|深圳|广州|重庆|成都|杭州|苏州|桂林|阳朔|云南|福建|香港|南京|武汉|贵州|安徽|江西|湖南|河南|山东|陕西|山西|广东|浙江|广西|四川`)
var foreignTitlePattern = regexp.MustCompile(`(?i)\b(japan|japanese|tokyo|kyoto|korea|seoul|vietnam|thailand|bangkok|bali|singapore|germany|german|america|london|paris|italy|switzerland|australia|chinatown)\b|德国|美国|英国|法国|日本|韩国|越南|泰国|欧洲|澳洲|澳大利亚|瑞士|意大利|新加坡|印度|俄罗斯|土耳其|挪威|冰岛|加拿大`)

func scenicMetadata(item mediaItem) string {
	return strings.ToLower(strings.Join([]string{item.Title, item.Keywords, item.Summary, item.Category, strings.Join(item.Tags, " ")}, " "))
}
func chinaScenicItem(item mediaItem) bool {
	if item.Kind == mediaKindMovie {
		return false
	}
	text := scenicMetadata(item)
	if foreignTitlePattern.MatchString(text) {
		return false
	}
	return item.Geography == "china_metadata" || item.Geography == "china_verified" || chinaTitlePattern.MatchString(text)
}
func scenicSourceKey(item mediaItem) string {
	if item.SHA256 != "" {
		return strings.ToLower(item.SHA256)
	}
	return strings.ToLower(item.sourceKey())
}
func readScenicIndex(path, root string) ([]mediaItem, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []mediaItem
	if err = json.Unmarshal(stripBOM(raw), &items); err != nil {
		return nil, fmt.Errorf("read scenic index: %w", err)
	}
	out := make([]mediaItem, 0, len(items))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if normalizeMediaItem(&item) != nil || item.ID == "" || !chinaScenicItem(item) {
			continue
		}
		if item.Kind != mediaKindImage && item.availableSeconds() < 6 {
			continue
		}
		if item.Width > 0 && item.Height > item.Width {
			continue
		} // The current board has a horizontal window.
		abs, e := resolveMediaPath(root, item.RelativePath)
		if e != nil {
			continue
		}
		resolved, e := filepath.EvalSymlinks(abs)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(canonicalRoot, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, e := os.Stat(resolved)
		if e != nil || !info.Mode().IsRegular() {
			continue
		}
		item.AbsPath = resolved
		out = append(out, item)
	}
	return out, nil
}
func scenicPool(ctx *planContext) ([]mediaItem, error) {
	seen := map[string]bool{}
	var out []mediaItem
	// Curated metadata wins over a less-informative legacy entry for the same file.
	for _, path := range []string{filepath.Join(filepath.Dir(ctx.mediaIndex), "china_scenic_index.json"), ctx.mediaIndex} {
		items, err := readScenicIndex(path, ctx.mediaRoot)
		if os.IsNotExist(err) && path != ctx.mediaIndex {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := scenicSourceKey(item)
			if !seen[key] {
				seen[key] = true
				out = append(out, item)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("china_scenic_pool_empty: import geographically attributed China footage before starting semantic montage")
	}
	return out, nil
}

type scenicSnapshot struct {
	Policy  ScenicPolicy   `json:"policy"`
	History map[string]int `json:"history"`
}
type ScenicSelectionQC struct {
	Version            int      `json:"version"`
	TaskID             string   `json:"task_id"`
	AccountID          string   `json:"account_id"`
	Basis              string   `json:"basis"`
	ModelStatus        string   `json:"model_status,omitempty"`
	RequestedModel     string   `json:"requested_model,omitempty"`
	ModelSelectedShots int      `json:"model_selected_shots,omitempty"`
	PoolSize           int      `json:"pool_size"`
	ShotCount          int      `json:"shot_count"`
	UniqueSources      int      `json:"unique_sources"`
	RepeatCount        int      `json:"repeat_count"`
	NearDuplicateCount int      `json:"near_duplicate_count"`
	MetadataMatchRatio float64  `json:"metadata_match_ratio"`
	DissolveRatio      float64  `json:"dissolve_ratio"`
	MeanShotSeconds    float64  `json:"mean_shot_seconds"`
	PlaybackSpeed      float64  `json:"playback_speed"`
	SourceKeys         []string `json:"source_keys"`
	SourceURLs         []string `json:"source_urls"`
	Warnings           []string `json:"warnings"`
}

func writeScenicJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".scenic-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func loadScenicSnapshot(ctx *planContext, planPath string) (scenicSnapshot, error) {
	snapshotPath := filepath.Join(filepath.Dir(planPath), "scenic_policy_snapshot.json")
	var snap scenicSnapshot
	if raw, err := os.ReadFile(snapshotPath); err == nil {
		if err = json.Unmarshal(raw, &snap); err != nil {
			return snap, err
		}
		return snap, snap.Policy.validate()
	} else if !os.IsNotExist(err) {
		return snap, err
	}
	snap = scenicSnapshot{Policy: defaultScenicPolicy(), History: map[string]int{}}
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(ctx.mediaIndex), "scenic_policy.json")); err == nil {
		if err = json.Unmarshal(raw, &snap.Policy); err != nil {
			return snap, err
		}
	} else if !os.IsNotExist(err) {
		return snap, err
	}
	if err := snap.Policy.validate(); err != nil {
		return snap, err
	}
	root := ctx.manifest.NonSecretSettings.DataRoot
	if root == "" {
		for p := filepath.Dir(ctx.manifest.OutputDir); p != filepath.Dir(p); p = filepath.Dir(p) {
			if filepath.Base(p) == "projects" {
				root = filepath.Dir(p)
				break
			}
		}
	}
	if root != "" && snap.Policy.HistoryWindow > 0 {
		paths, _ := filepath.Glob(filepath.Join(root, "projects", "*", "tasks", "*", "output", "scenic_selection_qc.json"))
		type prior struct {
			path     string
			modified int64
		}
		var recent []prior
		for _, p := range paths {
			if filepath.Dir(p) == filepath.Dir(planPath) {
				continue
			}
			if info, e := os.Stat(p); e == nil {
				recent = append(recent, prior{p, info.ModTime().UnixNano()})
			}
		}
		sort.Slice(recent, func(i, j int) bool {
			if recent[i].modified != recent[j].modified {
				return recent[i].modified > recent[j].modified
			}
			return recent[i].path < recent[j].path
		})
		accepted := 0
		for _, p := range recent {
			raw, e := os.ReadFile(p.path)
			if e != nil {
				continue
			}
			var qc ScenicSelectionQC
			if json.Unmarshal(raw, &qc) != nil {
				continue
			}
			for _, key := range qc.SourceKeys {
				snap.History[key]++
			}
			accepted++
			if accepted >= snap.Policy.HistoryWindow {
				break
			}
		}
	}
	return snap, writeScenicJSON(snapshotPath, snap)
}
func scenicThemesFor(text string) []int {
	var out []int
	for i, t := range scenicThemes {
		for _, cue := range t.cues {
			if strings.Contains(text, cue) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// Match English visual terms as words: "presidential" is not "residential".
func containsScenicVisual(hay, cue string) bool {
	for _, r := range cue {
		if r > 127 {
			return strings.Contains(hay, cue)
		}
	}
	letter := func(b byte) bool { return b >= 'a' && b <= 'z' }
	for offset := 0; offset < len(hay); {
		n := strings.Index(hay[offset:], cue)
		if n < 0 {
			return false
		}
		start := offset + n
		end := start + len(cue)
		left := start == 0 || !letter(hay[start-1])
		right := end == len(hay) || !letter(hay[end])
		if !right && hay[end] == 's' && (end+1 == len(hay) || !letter(hay[end+1])) {
			right = true
		}
		if left && right {
			return true
		}
		offset = start + 1
	}
	return false
}

func scenicGroupKey(item mediaItem) string {
	if strings.TrimSpace(item.Creator) == "" || strings.TrimSpace(item.Summary) == "" {
		return scenicSourceKey(item)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(item.Creator) + "|" + strings.Join(strings.Fields(item.Summary), " "))))
	return fmt.Sprintf("scene:%x", sum)
}

func scenicFit(text string, item mediaItem) (float64, string, string) {
	ids := scenicThemesFor(text)
	hay := scenicMetadata(item)
	var names []string
	for _, i := range ids {
		for _, v := range scenicThemes[i].visuals {
			if containsScenicVisual(hay, strings.ToLower(v)) {
				names = append(names, scenicThemes[i].name)
				break
			}
		}
	}
	if len(names) == 0 {
		return .15, "neutral", "中国场景过渡：当前语句没有足够的元数据匹配"
	}
	level := "metaphor"
	for _, name := range names {
		if name == "乡村与土地" || name == "住宅与房产" || name == "家庭与养老" {
			level = "direct"
			break
		}
	}
	return math.Min(.9, .58+.08*float64(len(names))), level, "元数据意象匹配：" + strings.Join(names, "、") + "；画面描述：" + item.Summary
}
func scenicTextAt(sentences []TimedSentence, at, duration float64) string {
	var text []string
	for _, s := range sentences {
		if float64(s.EndMS)/1000 > at-2 && float64(s.StartMS)/1000 < at+duration+2 {
			text = append(text, s.Text)
		}
	}
	return strings.Join(text, "")
}
func scenicHash(seed string, item mediaItem, ordinal int) float64 {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", seed, scenicSourceKey(item), ordinal)))
	return float64(binary.BigEndian.Uint32(hash[:4])) / float64(math.MaxUint32)
}
func nearSame(a, b mediaItem) bool {
	if len(a.FrameHashes) < 3 || len(b.FrameHashes) < 3 {
		return false
	}
	hits := 0
	for i := 0; i < 3; i++ {
		x, e := strconv.ParseUint(a.FrameHashes[i], 16, 64)
		y, f := strconv.ParseUint(b.FrameHashes[i], 16, 64)
		if e == nil && f == nil && bits.OnesCount64(x^y) <= 4 {
			hits++
		}
	}
	return hits >= 2
}
func buildScenicSemanticPlan(opts Options, ctx *planContext) ([]TimelineShotV2, []string, error) {
	runCtx := opts.Context
	if runCtx == nil {
		runCtx = context.Background()
	}
	pool, err := scenicPool(ctx)
	if err != nil {
		return nil, nil, err
	}
	snap, err := loadScenicSnapshot(ctx, opts.PlanPath)
	if err != nil {
		return nil, nil, err
	}
	sentences, err := loadPlanSentences(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(sentences) == 0 {
		return nil, nil, fmt.Errorf("scenic_semantic requires timed narration sentences")
	}
	p := snap.Policy
	used := map[string]int{}
	usedGroups := map[string]bool{}
	last := map[string]int{}
	creators := map[string]int{}
	var selected []mediaItem
	var shots []TimelineShotV2
	qc := ScenicSelectionQC{Version: 1, TaskID: ctx.manifest.TaskID, Basis: "metadata_rules_not_visual_verification", PoolSize: len(pool), PlaybackSpeed: 1, Warnings: []string{}}
	if ctx.manifest.Project != nil {
		qc.AccountID = ctx.manifest.Project.AccountID
	}
	cursor := 0.0
	matched := 0.0
	previousCategory := ""
	for cursor < ctx.duration-.001 {
		if err := runCtx.Err(); err != nil {
			return nil, nil, err
		}
		ordinal := len(shots)
		lo, hi := p.BodyMin, p.BodyMax
		if cursor < 30 {
			lo, hi = p.OpeningMin, p.OpeningMax
		}
		text := scenicTextAt(sentences, cursor, (lo+hi)/2)
		best := -1
		bestScore := math.Inf(-1)
		bestFit := 0.0
		bestLevel, bestReason := "", ""
		// Prefer visually distinct sources. A second pass permits a flagged perceptual
		// collision only when every other usable source is exhausted.
		for pass := 0; pass < 2 && best < 0; pass++ {
			for i, item := range pool {
				key := scenicSourceKey(item)
				avail := item.availableSeconds()
				if item.Kind == mediaKindImage {
					avail = math.Inf(1)
				}
				if avail < math.Min(lo, ctx.duration-cursor) || used[key] >= p.MaxSourceUses || usedGroups[scenicGroupKey(item)] {
					continue
				}
				if n, ok := last[key]; ok && ordinal-n <= p.ReuseGap {
					continue
				}
				collision := false
				for _, old := range selected {
					if nearSame(old, item) {
						collision = true
						break
					}
				}
				if collision && pass == 0 {
					continue
				}
				fit, level, reason := scenicFit(text, item)
				score := fit + scenicHash(ctx.manifest.TaskID, item, ordinal)*.13
				score -= math.Min(.30, float64(snap.History[key])*.12)
				if item.Category == previousCategory {
					score -= .075
				}
				if item.Creator != "" {
					score -= math.Min(.12, float64(creators[item.Creator])*.015)
				}
				if item.Kind == mediaKindImage {
					score -= .18
				}
				if collision {
					score -= .3
				}
				if score > bestScore {
					best, bestScore, bestFit, bestLevel, bestReason = i, score, fit, level, reason
				}
			}
		}
		if best < 0 {
			return nil, nil, fmt.Errorf("scenic_pool_exhausted at %.2fs: add China landscape clips rather than silently repeating a source", cursor)
		}
		item := pool[best]
		length := math.Round((lo+scenicHash(ctx.manifest.TaskID, item, ordinal+1000)*(hi-lo))*10) / 10
		if item.Kind != mediaKindImage {
			length = math.Min(length, item.availableSeconds())
		}
		length = math.Min(length, ctx.duration-cursor)
		// Snap to a nearby narration boundary without turning each subtitle line into a cut.
		for _, sentence := range sentences {
			end := float64(sentence.EndMS) / 1000
			gap := end - cursor
			if gap >= lo && gap <= hi && math.Abs(gap-length) < .65 && (item.Kind == mediaKindImage || gap <= item.availableSeconds()) {
				length = gap
				break
			}
		}
		if tail := ctx.duration - cursor - length; tail > 0 && tail < 1.5 && (item.Kind == mediaKindImage || length+tail <= item.availableSeconds()) {
			length += tail
		}
		length = math.Min(length, ctx.duration-cursor)
		end := roundSFXStart(cursor + length)
		if math.Abs(end-ctx.duration) < .001 {
			end = ctx.duration
		}
		shot := TimelineShotV2{ShotNo: ordinal + 1, StartS: cursor, EndS: end, MediaKind: string(item.Kind), SourceID: item.ID, ShotID: item.ShotID, SourcePath: item.AbsPath, Match: MatchEvidence{Level: bestLevel, Score: bestFit, IntentID: fmt.Sprintf("scenic-%03d", ordinal+1), Reason: bestReason}, Motion: MotionPlan{Preset: "steady", ScaleFrom: 1.20, ScaleTo: 1.20}, Opacity: 1, SourceAudioMuted: true, Look: "none"}
		if item.Kind == mediaKindImage {
			shot.Motion = imageMotionPlan(ordinal)
		} else {
			base := 0.0
			limit := item.DurationSeconds
			if item.hasShotRange() {
				base = item.SourceInSeconds
				limit = item.SourceOutSeconds
			}
			in := roundSFXStart(base + math.Max(0, limit-base-(end-cursor))*scenicHash(ctx.manifest.TaskID, item, ordinal+2000))
			out := roundSFXStart(in + end - cursor)
			if out > limit {
				out = limit
				in = out - (end - cursor)
			}
			shot.SourceInS = &in
			shot.SourceOutS = &out
		}
		key := scenicSourceKey(item)
		used[key]++
		usedGroups[scenicGroupKey(item)] = true
		last[key] = ordinal
		creators[item.Creator]++
		previousCategory = item.Category
		if bestLevel != "neutral" {
			matched += end - cursor
		}
		for _, old := range selected {
			if nearSame(old, item) {
				qc.NearDuplicateCount++
				break
			}
		}
		qc.SourceKeys = append(qc.SourceKeys, key)
		qc.SourceURLs = append(qc.SourceURLs, item.SourceURL)
		selected = append(selected, item)
		shots = append(shots, shot)
		cursor = end
	}
	// Refine eligible local choices with the configured model. Rebuild source QC
	// after reranking so the report describes the actual final timeline.
	if p.ModelSelection {
		var audit scenicModelAudit
		shots, selected, audit, err = rerankScenicShots(opts, ctx, sentences, shots, selected, pool)
		if err != nil {
			return nil, nil, err
		}
		qc.ModelStatus = audit.Status
		qc.RequestedModel = audit.RequestedModel
		qc.ModelSelectedShots = audit.Applied
		qc.Warnings = append(qc.Warnings, audit.Warnings...)
		if audit.Applied > 0 {
			qc.Basis = "model_shortlist_on_metadata_with_deterministic_guards"
		}
	}
	used = map[string]int{}
	matched = 0
	qc.SourceKeys = nil
	qc.SourceURLs = nil
	qc.NearDuplicateCount = 0
	for i, it := range selected {
		key := scenicSourceKey(it)
		used[key]++
		qc.SourceKeys = append(qc.SourceKeys, key)
		qc.SourceURLs = append(qc.SourceURLs, it.SourceURL)
		if shots[i].Match.Level != "neutral" {
			matched += shots[i].EndS - shots[i].StartS
		}
		for j := 0; j < i; j++ {
			if nearSame(selected[j], it) {
				qc.NearDuplicateCount++
				break
			}
		}
		// A gentle push/pull is independent of playback speed. Do not turn every
		// subtitle into a zoom or add strobing/flashes for an older audience.
		if p.EditorVersion > 0 && it.Kind != mediaKindImage && i%3 != 2 {
			if i%2 == 0 {
				shots[i].Motion = MotionPlan{Preset: "push", ScaleFrom: 1.20, ScaleTo: 1.24}
			} else {
				shots[i].Motion = MotionPlan{Preset: "pull", ScaleFrom: 1.24, ScaleTo: 1.20}
			}
		}
	}
	// Null is a real hard cut. Prefer dissolves at changes of visual theme, never
	// on the first shot or consecutive boundaries. The Python executor honors this.
	type boundary struct {
		i     int
		score float64
	}
	var boundaries []boundary
	firstBoundary := 2
	if p.EditorVersion > 0 {
		firstBoundary = 1
	}
	for i := firstBoundary; i < len(shots); i++ {
		score := scenicHash(ctx.manifest.TaskID, selected[i], i+3000)
		if p.EditorVersion > 0 && i == 1 {
			score += 100
		}
		if selected[i].Category != selected[i-1].Category {
			score += 1
		}
		boundaries = append(boundaries, boundary{i, score})
	}
	sort.Slice(boundaries, func(i, j int) bool {
		if boundaries[i].score != boundaries[j].score {
			return boundaries[i].score > boundaries[j].score
		}
		return boundaries[i].i < boundaries[j].i
	})
	wanted := int(math.Round(float64(len(shots)-1) * p.DissolveRatio))
	applied := map[int]bool{}
	for _, b := range boundaries {
		if len(applied) >= wanted {
			break
		}
		if applied[b.i-1] || applied[b.i+1] {
			continue
		}
		shots[b.i].Transition = &TransitionV2{Preset: v2TransitionPreset, DurationS: v2TransitionDurationS}
		applied[b.i] = true
	}
	qc.ShotCount = len(shots)
	qc.UniqueSources = len(used)
	qc.RepeatCount = len(shots) - len(used)
	qc.MetadataMatchRatio = matched / ctx.duration
	qc.MeanShotSeconds = ctx.duration / float64(len(shots))
	if len(shots) > 1 {
		qc.DissolveRatio = float64(len(applied)) / float64(len(shots)-1)
	}
	if qc.MetadataMatchRatio < .5 {
		qc.Warnings = append(qc.Warnings, "low_metadata_match: more than half the duration uses neutral China scenery")
	}
	if qc.NearDuplicateCount > 0 {
		qc.Warnings = append(qc.Warnings, "near_duplicate_fallback: perceptually similar clips were necessary")
	}
	if err = writeScenicJSON(filepath.Join(filepath.Dir(opts.PlanPath), "scenic_selection_qc.json"), qc); err != nil {
		return nil, nil, err
	}
	notes := []string{fmt.Sprintf("scenic_semantic: China-only metadata/imagery selector; pool=%d speed=1.0", len(pool)), fmt.Sprintf("scenic_qc: metadata_match=%.3f unique_sources=%d repeats=%d near_duplicates=%d hard_cut=%.3f", qc.MetadataMatchRatio, qc.UniqueSources, qc.RepeatCount, qc.NearDuplicateCount, 1-qc.DissolveRatio), "scenic_selection_qc.json contains provenance; stock footage is illustrative, not event evidence"}
	return shots, append(notes, qc.Warnings...), nil
}
