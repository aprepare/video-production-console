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
	"time"
	"unicode/utf8"

	"video-production-console/internal/domain"
	"video-production-console/internal/spokenlines"
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

func parseRemixDraft(raw string) (remixDraft, error) {
	text := strings.TrimSpace(stripCodeFence(raw))
	if text == "" {
		return remixDraft{}, nil
	}
	if draft, ok := decodeRemixDraft(text); ok {
		return draft, nil
	}
	if recovered, ok := recoverRemixDraftFromBrokenJSON(text); ok {
		return recovered, nil
	}
	if repaired := repairUnescapedJSONQuotes(text); repaired != text {
		if draft, ok := decodeRemixDraft(repaired); ok {
			return draft, nil
		}
	}
	if extracted := extractJSONObject(text); extracted != "" && extracted != text {
		if draft, ok := decodeRemixDraft(extracted); ok {
			return draft, nil
		}
		if repaired := repairUnescapedJSONQuotes(extracted); repaired != extracted {
			if draft, ok := decodeRemixDraft(repaired); ok {
				return draft, nil
			}
		}
	}
	if strings.Contains(text, "{") {
		return remixDraft{}, fmt.Errorf("model returned malformed structured remix response")
	}
	return remixDraft{ContinuousScript: text}, nil
}

func decodeRemixDraft(text string) (remixDraft, bool) {
	candidate := strings.TrimSpace(text)
	const maxTrailingClosingBraces = 4
	for removed := 0; removed <= maxTrailingClosingBraces; removed++ {
		var draft remixDraft
		if err := json.Unmarshal([]byte(candidate), &draft); err == nil {
			draft.ContinuousScript = strings.TrimSpace(firstNonEmpty(
				draft.ContinuousScript,
				mapString(candidate, "continuous_script"),
				mapString(candidate, "continuousScript"),
				mapString(candidate, "script"),
			))
			if draft.ContinuousScript == "" {
				return remixDraft{}, false
			}
			return draft, true
		}
		if removed == maxTrailingClosingBraces || !strings.HasSuffix(candidate, "}") {
			break
		}
		candidate = strings.TrimSpace(strings.TrimSuffix(candidate, "}"))
	}
	return remixDraft{}, false
}

func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1])
			}
		}
	}
	return strings.TrimSpace(text[start:])
}

// repairUnescapedJSONQuotes 修模型把正文里的中文引号写成 "接财" 这种未转义双引号。
func repairUnescapedJSONQuotes(text string) string {
	start := strings.Index(text, "{")
	if start < 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	b.WriteString(text[:start])
	inString := false
	escape := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if !inString {
			b.WriteByte(ch)
			if ch == '"' {
				inString = true
				escape = false
			}
			continue
		}
		if escape {
			b.WriteByte(ch)
			escape = false
			continue
		}
		if ch == '\\' {
			b.WriteByte(ch)
			escape = true
			continue
		}
		if ch != '"' {
			b.WriteByte(ch)
			continue
		}
		if looksLikeJSONCloser(text, i+1) {
			b.WriteByte(ch)
			inString = false
			continue
		}
		if looksLikeLiteralQuote(text, i) {
			b.WriteString(`\"`)
			continue
		}
		b.WriteByte(ch)
		inString = false
	}
	return b.String()
}

func looksLikeJSONCloser(text string, i int) bool {
	for i < len(text) {
		switch text[i] {
		case ' ', '\n', '\r', '	':
			i++
		case ',', '}', ']':
			return true
		default:
			return false
		}
	}
	return true
}

func looksLikeLiteralQuote(text string, i int) bool {
	if i+1 >= len(text) {
		return false
	}
	next := text[i+1]
	if next == ' ' || next == '\n' || next == '\r' || next == '\t' || next == ',' || next == '}' || next == ']' || next == ':' {
		return false
	}
	closeAt := strings.IndexByte(text[i+1:], '"')
	if closeAt < 0 {
		return false
	}
	closeAt += i + 1
	inner := text[i+1 : closeAt]
	if inner == "" || strings.ContainsAny(inner, "\n{}[]") {
		return false
	}
	return !looksLikeJSONCloser(text, closeAt+1)
}

func recoverRemixDraftFromBrokenJSON(text string) (remixDraft, bool) {
	script, ok := extractBrokenJSONStringField(text, "continuous_script")
	if !ok {
		script, ok = extractBrokenJSONStringField(text, "continuousScript")
	}
	if !ok {
		script, ok = extractBrokenJSONStringField(text, "script")
	}
	script = strings.TrimSpace(script)
	if !ok || utf8.RuneCountInString(script) < 40 {
		return remixDraft{}, false
	}
	return remixDraft{
		ContinuousScript: script,
		Titles:           extractBrokenJSONStringArray(text, "titles"),
		ShortTitles:      extractBrokenJSONStringArray(text, "short_titles"),
		Descriptions:     extractBrokenJSONStringArray(text, "descriptions"),
		Topics:           extractBrokenJSONStringArray(text, "topics"),
	}, true
}

func extractBrokenJSONStringField(text, key string) (string, bool) {
	needle := `"` + key + `"`
	start := strings.Index(text, needle)
	if start < 0 {
		return "", false
	}
	rest := text[start+len(needle):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return "", false
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	body := rest[1:]
	end := -1
	for _, marker := range []string{
		"\",\n  \"titles\"",
		"\",\n  \"short_titles\"",
		"\",\n  \"descriptions\"",
		"\",\n  \"topics\"",
		"\",\n  \"cta\"",
		`","titles"`,
		`","short_titles"`,
		`","descriptions"`,
		`","topics"`,
		`","cta"`,
	} {
		if idx := strings.LastIndex(body, marker); idx >= 0 && (end < 0 || idx < end) {
			end = idx
		}
	}
	if end < 0 {
		// 流在 continuous_script 字符串中间被掐断：后面没有 titles 等字段，
		// 把已写出的正文捞出来，避免 HTTP 200 却整篇作废。
		script := strings.TrimSpace(unescapeJSONString(body))
		if utf8.RuneCountInString(script) < 40 {
			return "", false
		}
		return script, true
	}
	return unescapeJSONString(body[:end]), true
}

func extractBrokenJSONStringArray(text, key string) []string {
	needle := `"` + key + `"`
	start := strings.Index(text, needle)
	if start < 0 {
		return nil
	}
	rest := text[start+len(needle):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return nil
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if !strings.HasPrefix(rest, "[") {
		return nil
	}
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil
	}
	raw := "[" + rest[1:end] + "]"
	var values []string
	if json.Unmarshal([]byte(raw), &values) == nil {
		return values
	}
	return nil
}

func unescapeJSONString(value string) string {
	replacer := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `	`, "	", `\"`, `"`, `\\`, `\`)
	return replacer.Replace(value)
}

func mapString(raw, key string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func remixAbs(outputDir, name string) string {
	path, _ := filepath.Abs(filepath.Join(outputDir, name))
	return path
}

func remixDeliverableArtifacts(outputDir string, abs func(string) string) []map[string]string {
	if abs == nil {
		abs = func(name string) string { return remixAbs(outputDir, name) }
	}
	items := []struct{ name, typ, desc string }{
		{"viral_analysis.json", "viral_analysis", "Viral mechanism analysis"},
		{"structure_design.json", "structure_design", "Remix structure design"},
		{"publishing_package.json", "publishing_package", "Publishing titles, descriptions, topics, and CTA"},
		{"self_check.json", "self_check", "Editorial and contract self-check"},
		{"remix_run.json", "remix_run", "Remix model run log"},
		{"model_raw.txt", "model_raw", "Raw model remix response"},
	}
	out := make([]map[string]string, 0, len(items))
	for _, item := range items {
		if _, err := os.Stat(filepath.Join(outputDir, item.name)); err != nil {
			continue
		}
		out = append(out, map[string]string{"type": item.typ, "path": abs(item.name), "description": item.desc})
	}
	return out
}

func appendRemixRunLog(outputDir string, event map[string]any) {
	if strings.TrimSpace(outputDir) == "" || event == nil {
		return
	}
	_ = os.MkdirAll(outputDir, 0o755)
	path := filepath.Join(outputDir, "remix_run.json")
	var log struct {
		Events []map[string]any `json:"events"`
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &log)
	}
	if event["at"] == nil {
		event["at"] = time.Now().Format(time.RFC3339)
	}
	log.Events = append(log.Events, event)
	_ = writeJSONFile(path, log)
}

func captureRemixModelReply(outputDir, model, style, raw string) {
	if strings.TrimSpace(outputDir) == "" {
		return
	}
	_ = os.MkdirAll(outputDir, 0o755)
	_ = os.WriteFile(filepath.Join(outputDir, "model_raw.txt"), []byte(raw), 0o644)
	script := ""
	if draft, err := parseRemixDraft(raw); err == nil {
		script = strings.TrimSpace(draft.ContinuousScript)
	}
	if script != "" {
		_ = os.WriteFile(filepath.Join(outputDir, "continuous_script.txt"), []byte(script), 0o644)
	}
	appendRemixRunLog(outputDir, map[string]any{
		"event":        "model_reply",
		"model":        model,
		"prompt_style": style,
		"raw_bytes":    len(raw),
		"script_runes": utf8.RuneCountInString(script),
		"parse_ok":     script != "",
	})
}

func writeRemixDeliverable(outputDir, taskID, action, modelText string, warnings []string, checkNote string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	rawPath := filepath.Join(outputDir, "model_raw.txt")
	if _, err := os.Stat(rawPath); err != nil {
		_ = os.WriteFile(rawPath, []byte(modelText), 0o644)
	}
	draft, err := parseRemixDraft(modelText)
	if err != nil {
		return err
	}
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
	summary := "二创连续文案已生成。"
	if note := strings.TrimSpace(checkNote); note != "" {
		summary += note
	} else if len(warnings) > 0 {
		summary += fmt.Sprintf("自检发现 %d 处与原文重合未修复，见警告。", len(warnings))
	}
	warningList := make([]any, 0, len(warnings))
	for _, warning := range warnings {
		warningList = append(warningList, warning)
	}
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         action,
		"status":         "completed",
		"summary":        summary,
		"questions":      []any{},
		"warnings":       warningList,
		"artifacts": remixDeliverableArtifacts(outputDir, abs),
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

func writeSpokenDeliverable(outputDir, taskID, modelText string) error {
	formatted, err := spokenlines.Format(modelText)
	if err != nil {
		return err
	}
	if isAskModeRefusal(formatted) {
		return fmt.Errorf("%s", truncate(formatted, 240))
	}
	scriptPath := filepath.Join(outputDir, "spoken_script.txt")
	scriptBytes := []byte(formatted)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, scriptBytes, 0o644); err != nil {
		return err
	}
	abs, _ := filepath.Abs(scriptPath)
	sum := sha256.Sum256(scriptBytes)
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         string(domain.ActionRemixSpokenLines),
		"status":         "completed",
		"summary":        "口播稿已生成。",
		"questions":      []any{},
		"warnings":       []any{},
		"artifacts":      []any{},
		"asset_outputs": []map[string]any{
			{
				"type": "spoken_script", "path": abs, "storage_kind": "file",
				"filename": "spoken_script.txt", "mime": "text/plain; charset=utf-8",
				"size": int64(len(scriptBytes)), "sha256": hex.EncodeToString(sum[:]),
			},
		},
	}
	return writeJSONFile(filepath.Join(outputDir, "result.json"), envelope)
}

func writeKeywordsDeliverable(outputDir, taskID, modelText string, lines []string) error {
	if isAskModeRefusal(modelText) {
		return fmt.Errorf("%s", truncate(modelText, 240))
	}
	doc, err := spokenlines.BuildKeywordDoc(modelText, lines)
	if err != nil {
		return err
	}
	payload, err := spokenlines.MarshalKeywordDoc(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	keywordsPath := filepath.Join(outputDir, "caption_keywords.json")
	if err := os.WriteFile(keywordsPath, payload, 0o644); err != nil {
		return err
	}
	abs, _ := filepath.Abs(keywordsPath)
	sum := sha256.Sum256(payload)
	envelope := map[string]any{
		"schema_version": "2.0",
		"task_id":        taskID,
		"action":         string(domain.ActionCaptionKeywords),
		"status":         "completed",
		"summary":        "字幕关键词已标注。",
		"questions":      []any{},
		"warnings":       []any{},
		"artifacts":      []any{},
		"asset_outputs": []map[string]any{
			{
				"type": "caption_keywords", "path": abs, "storage_kind": "file",
				"filename": "caption_keywords.json", "mime": "application/json; charset=utf-8",
				"size": int64(len(payload)), "sha256": hex.EncodeToString(sum[:]),
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
		descriptions[i] = withHotTopics(clipDescriptionBody(description), topics)
	}
	cta := ""
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

// clipDescriptionBody keeps at most two sentences so the 视频描述 stays a
// short hook instead of a re-pasted script paragraph. Trailing hashtags the
// model already appended are dropped along with anything past the second
// sentence; withHotTopics re-appends the canonical topic run.
func clipDescriptionBody(text string) string {
	body := strings.TrimSpace(trailingHashtagRun.ReplaceAllString(strings.TrimSpace(text), ""))
	runes := []rune(body)
	sentences := 0
	for i, r := range runes {
		switch r {
		case '。', '！', '？', '!', '?', '；', ';':
			sentences++
			if sentences == 2 {
				return strings.TrimSpace(string(runes[:i+1]))
			}
		}
	}
	return body
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
