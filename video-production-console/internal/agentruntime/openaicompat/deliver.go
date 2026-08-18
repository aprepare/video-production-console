package openaicompat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 视频号描述只用这组热门话题，落盘时也会把模型自造的标签换掉。
var hotPublishingTopics = []string{
	"#经济", "#思维认知", "#认知", "#宏观趋势", "#思维", "#干货分享", "#认知觉醒",
}

var trailingHashtagRun = regexp.MustCompile(`(?:\s*#[^\s#]+)+\s*$`)

type remixDraft struct {
	ContinuousScript string   `json:"continuous_script"`
	Titles           []string `json:"titles"`
	ShortTitles      []string `json:"short_titles"`
	Descriptions     []string `json:"descriptions"`
	Topics           []string `json:"topics"`
	CTA              string   `json:"cta"`
}

func parseRemixDraft(raw string) remixDraft {
	text := strings.TrimSpace(stripCodeFence(raw))
	if text == "" {
		return remixDraft{}
	}
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			var draft remixDraft
			if json.Unmarshal([]byte(text[start:end+1]), &draft) == nil && strings.TrimSpace(draft.ContinuousScript) != "" {
				draft.ContinuousScript = strings.TrimSpace(draft.ContinuousScript)
				return draft
			}
		}
	}
	return remixDraft{ContinuousScript: text}
}

func stripCodeFence(raw string) string {
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if nl := strings.Index(text, "\n"); nl >= 0 {
		text = text[nl+1:]
	}
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text)
}

func isAskModeRefusal(text string) bool {
	return strings.Contains(text, "Ask 模式") && (strings.Contains(text, "不能") || strings.Contains(text, "无法"))
}

func writeRemixDeliverable(outputDir, taskID, action, modelText string) error {
	draft := parseRemixDraft(modelText)
	script := strings.TrimSpace(draft.ContinuousScript)
	if script == "" || utf8.RuneCountInString(script) < 40 {
		return fmt.Errorf("model returned no usable remix script")
	}
	if isAskModeRefusal(script) {
		return fmt.Errorf("%s", truncate(script, 240))
	}
	pkg := publishingPackageFromDraft(draft, script)
	if err := writeJSONFile(filepath.Join(outputDir, "viral_analysis.json"), map[string]any{
		"action": action, "source_role": "primary_source", "research_used": false,
		"cta": map[string]string{"course": "《财富觉醒方法论》", "entry": "主页橱窗", "energy": pkg["cta"].(string)},
	}); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(outputDir, "structure_design.json"), map[string]any{
		"locked_topic": "同一条爆款机器换说法，不换题",
		"kept":         []string{"开场钩子", "历史证明", "故意不说完的答案", "上车催促"},
		"changed":      []string{"换说法", "可加料"},
		"topic_drift":  false,
	}); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(outputDir, "publishing_package.json"), pkg); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(outputDir, "self_check.json"), map[string]any{
		"action": action, "wire_action": "standard", "input_roles": []string{"primary_source"},
		"generated": []string{"continuous_script.txt", "viral_analysis.json", "structure_design.json", "publishing_package.json", "self_check.json"},
		"checks":    []string{"console wrote files; model only supplied copy"},
	}); err != nil {
		return err
	}
	scriptPath := filepath.Join(outputDir, "continuous_script.txt")
	scriptBytes := []byte(script)
	if err := os.WriteFile(scriptPath, scriptBytes, 0o644); err != nil {
		return err
	}
	abs := func(name string) string {
		path, _ := filepath.Abs(filepath.Join(outputDir, name))
		return path
	}
	sum := sha256.Sum256(scriptBytes)
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         action,
		"status":         "completed",
		"summary":        "二创连续文案已生成。",
		"questions":      []any{},
		"warnings":       []any{},
		"artifacts": []map[string]string{
			{"type": "viral_analysis", "path": abs("viral_analysis.json"), "description": "Viral mechanism analysis"},
			{"type": "structure_design", "path": abs("structure_design.json"), "description": "Remix structure design"},
			{"type": "publishing_package", "path": abs("publishing_package.json"), "description": "Publishing titles, descriptions, topics, and CTA"},
			{"type": "self_check", "path": abs("self_check.json"), "description": "Editorial and contract self-check"},
		},
		"asset_outputs": []map[string]any{
			{
				"type": "continuous_script", "path": abs("continuous_script.txt"), "storage_kind": "file",
				"filename": "continuous_script.txt", "mime": "text/plain; charset=utf-8",
				"size": int64(len(scriptBytes)), "sha256": hex.EncodeToString(sum[:]),
			},
		},
	}
	return writeJSONFile(filepath.Join(outputDir, "result.json"), envelope)
}

func publishingPackageFromDraft(draft remixDraft, script string) map[string]any {
	titles := uniqueFilled(draft.Titles, titleFallbacks(script), 8, 12)
	if len(titles) > 12 {
		titles = titles[:12]
	}
	short := uniqueFilled(draft.ShortTitles, shortTitleFallbacks(script), 5, 5)
	if len(short) > 5 {
		short = short[:5]
	}
	descriptions := uniqueFilled(draft.Descriptions, descriptionFallbacks(script), 3, 3)
	if len(descriptions) > 3 {
		descriptions = descriptions[:3]
	}
	topics := pickHotTopics(draft.Topics, script)
	for i, description := range descriptions {
		descriptions[i] = withHotTopics(description, topics)
	}
	cta := strings.TrimSpace(draft.CTA)
	if cta == "" {
		cta = "关掉干扰，现在就去主页橱窗看《财富觉醒方法论》。"
	}
	top := []map[string]any{}
	for i := 0; i < 3 && i < len(titles); i++ {
		top = append(top, map[string]any{"rank": i + 1, "title": titles[i], "reason": "保留原稿钩子与未解问题。"})
	}
	return map[string]any{
		"titles": titles, "top_titles": top, "short_titles": short,
		"descriptions": descriptions, "description": descriptions[0],
		"topics": topics, "cta": cta,
	}
}

func titleFallbacks(script string) []string {
	seed := firstSentence(script)
	if utf8.RuneCountInString(seed) < 8 {
		seed = clipRunes(strings.TrimSpace(script), 8, 24)
	}
	if utf8.RuneCountInString(seed) < 8 {
		seed = "窗口不会等人，先看懂再上车"
	}
	return []string{
		seed,
		seed + "窗口不会等人",
		"看懂的人先上车，观望的人后知道",
		"答案先留着，窗口不会一直开着",
		"真正拉开差距的是先看懂方向",
		"别只盯工资，先看钱往哪走",
		"新一轮机会开始，普通人还有没有窗口",
		"现在补判断力，比事后后悔便宜",
	}
}

func shortTitleFallbacks(script string) []string {
	seed := clipRunes(firstSentence(script), 6, 16)
	out := []string{seed, "窗口不会等人", "钱会流向哪里", "下一批赢家是谁", "现在就上车吧"}
	return uniqueFilled(nil, out, 5, 5)
}

func descriptionFallbacks(script string) []string {
	lead := firstSentence(script)
	if lead == "" {
		lead = "看懂方向的人先拿位置，观望的人最后才知道规则变了。"
	}
	return []string{
		lead,
		"看懂资金上游的人先拿位置，观望的人最后才知道规则变了。",
		"答案先留着，窗口不会一直开着，现在就去补齐判断力。",
	}
}

func normalizeHashtag(raw string) string {
	tag := strings.TrimSpace(raw)
	if tag == "" {
		return ""
	}
	if !strings.HasPrefix(tag, "#") {
		tag = "#" + tag
	}
	return strings.ReplaceAll(tag, " ", "")
}

func pickHotTopics(given []string, seed string) []string {
	allow := make(map[string]bool, len(hotPublishingTopics))
	for _, tag := range hotPublishingTopics {
		allow[tag] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	add := func(tag string) {
		tag = normalizeHashtag(tag)
		if !allow[tag] || seen[tag] || len(out) >= 4 {
			return
		}
		seen[tag] = true
		out = append(out, tag)
	}
	for _, tag := range given {
		add(tag)
	}
	if len(out) >= 3 {
		return out
	}
	start := 0
	if seed != "" {
		sum := sha256.Sum256([]byte(seed))
		start = int(sum[0]) % len(hotPublishingTopics)
	}
	for i := 0; len(out) < 3; i++ {
		add(hotPublishingTopics[(start+i)%len(hotPublishingTopics)])
	}
	return out
}

func withHotTopics(text string, topics []string) string {
	body := strings.TrimSpace(trailingHashtagRun.ReplaceAllString(strings.TrimSpace(text), ""))
	if body == "" {
		return strings.Join(topics, " ")
	}
	return body + " " + strings.Join(topics, " ")
}

func firstSentence(script string) string {
	text := strings.TrimSpace(strings.ReplaceAll(script, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	cut := strings.IndexAny(text, "。！？\n")
	if cut <= 0 {
		return clipRunes(text, 8, 32)
	}
	return strings.TrimSpace(text[:cut])
}

func clipRunes(text string, min, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > max {
		runes = runes[:max]
	}
	for len(runes) < min {
		runes = append(runes, []rune("窗口来了")...)
	}
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

func uniqueFilled(given, fallback []string, min, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, max)
	add := func(items []string) {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] || len(out) >= max {
				continue
			}
			seen[item] = true
			out = append(out, item)
		}
	}
	add(given)
	add(fallback)
	for i := 0; len(out) < min; i++ {
		item := fmt.Sprintf("二创标题%d窗口", i+1)
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func writeJSONFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
