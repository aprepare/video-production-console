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

// hotPublishingTopics 是模型没给够话题时的补位池，只放财经大池标签；
// 「#认知 #干货分享」这类空泛词不再补，宁缺毋滥。
var hotPublishingTopics = []string{
	"#财经", "#经济", "#理财", "#财富",
}

var trailingHashtagRun = regexp.MustCompile(`(?:\s*#[^\s#]+)+\s*$`)

// brokenJSONFieldBoundary 匹配烂 JSON 抢救时正文字段的结束边界：
// 闭引号、逗号、任意空白（可跨空行）、下一个已知字段名。
var brokenJSONFieldBoundary = regexp.MustCompile(`"\s*,\s*"(?:titles|short_titles|descriptions|topics|cta)"\s*:`)

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
	// 引号修复放在烂 JSON 抢救之前：修好未转义引号就能完整解析出全部字段，
	// 抢救路径是有损的最后手段（曾把 JSON 尾巴整段当正文交付）。
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
	if recovered, ok := recoverRemixDraftFromBrokenJSON(text); ok {
		return recovered, nil
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

// escapeControlCharsInJSONStrings 把 JSON 字符串字面量里的裸换行/回车/制表
// 符转成合法转义。模型输出长正文时最常见的解析死法就是 continuous_script
// 里带真实换行，json.Unmarshal 会直接拒绝控制字符。
func escapeControlCharsInJSONStrings(text string) string {
	start := strings.Index(text, "{")
	if start < 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + 64)
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
		switch ch {
		case '\\':
			b.WriteByte(ch)
			escape = true
		case '"':
			b.WriteByte(ch)
			inString = false
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

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
	if !ok || script == "" {
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
	// 字段边界容忍任意空白（含空行）：模型常在字段之间空一行。
	end := -1
	if loc := brokenJSONFieldBoundary.FindStringIndex(body); loc != nil {
		end = loc[0]
	}
	if end < 0 {
		script := strings.TrimSpace(unescapeJSONString(body))
		if script == "" {
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
	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(value)
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
	return strings.TrimSpace(strings.TrimSuffix(text, "```"))
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

func captureRemixModelReply(outputDir, model, style, stamp, raw string) {
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
	if strings.TrimSpace(stamp) == "" {
		stamp = promptStamp(style)
	}
	appendRemixRunLog(outputDir, map[string]any{
		"event": "model_reply", "model": model, "prompt_style": style, "prompt_stamp": stamp,
		"raw_bytes": len(raw), "script_runes": utf8.RuneCountInString(script), "parse_ok": script != "",
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
	if script == "" {
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
		"editorial_mode": "human_review", "policy_version": EditorialPolicyVersion,
		"note": "成稿与发布字段保留模型输出，内容效果由用户评阅。",
	}); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(outputDir, "publishing_package.json"), pkg); err != nil {
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
		"schema_version": "2.0", "task_id": taskID, "action": action, "status": "completed",
		"summary": summary, "questions": []any{}, "warnings": warningList,
		"artifacts": remixDeliverableArtifacts(outputDir, abs),
		"asset_outputs": []map[string]any{{
			"type": "continuous_script", "path": abs("continuous_script.txt"), "storage_kind": "file",
			"filename": "continuous_script.txt", "mime": "text/plain; charset=utf-8",
			"size": int64(len(scriptBytes)), "sha256": hex.EncodeToString(sum[:]),
		}},
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
		"schema_version": "2.0", "task_id": taskID, "action": string(domain.ActionRemixSpokenLines),
		"status": "completed", "summary": "口播稿已生成。", "questions": []any{}, "warnings": []any{}, "artifacts": []any{},
		"asset_outputs": []map[string]any{{
			"type": "spoken_script", "path": abs, "storage_kind": "file",
			"filename": "spoken_script.txt", "mime": "text/plain; charset=utf-8",
			"size": int64(len(scriptBytes)), "sha256": hex.EncodeToString(sum[:]),
		}},
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
		"schema_version": "2.0", "task_id": taskID, "action": string(domain.ActionCaptionKeywords),
		"status": "completed", "summary": "字幕关键词已标注。", "questions": []any{}, "warnings": []any{}, "artifacts": []any{},
		"asset_outputs": []map[string]any{{
			"type": "caption_keywords", "path": abs, "storage_kind": "file",
			"filename": "caption_keywords.json", "mime": "application/json; charset=utf-8",
			"size": int64(len(payload)), "sha256": hex.EncodeToString(sum[:]),
		}},
	}
	return writeJSONFile(filepath.Join(outputDir, "result.json"), envelope)
}

// publishingPackageFromDraft 只封装模型字段，不截短、补写、替换话题或凑数。
func publishingPackageFromDraft(draft remixDraft, script string) map[string]any {
	list := func(items []string) []string {
		if items == nil {
			return []string{}
		}
		return items
	}
	titles, short := list(draft.Titles), list(draft.ShortTitles)
	descriptions, topics := list(draft.Descriptions), list(draft.Topics)
	description := ""
	if len(descriptions) > 0 {
		description = descriptions[0]
	}
	top := []map[string]any{}
	for i, title := range titles {
		top = append(top, map[string]any{"rank": i + 1, "title": title})
	}
	return map[string]any{
		"titles": titles, "top_titles": top, "short_titles": short,
		"descriptions": descriptions, "description": description,
		"topics": topics, "cta": draft.CTA,
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
	return []string{seed, seed + "窗口不会等人", "看懂的人先上车，观望的人后知道", "答案先留着，窗口不会一直开着",
		"真正拉开差距的是先看懂方向", "别只盯工资，先看钱往哪走", "新一轮机会开始，普通人还有没有窗口", "现在补判断力，比事后后悔便宜"}
}

// shortTitleFallbacks 只从正文里取：首句的第一个分句、正文里第一个反问句。
// 不再塞「窗口不会等人」这类通用口号——宁可少一条，也不要发出去一条废话。
func shortTitleFallbacks(script string) []string {
	out := make([]string, 0, 3)
	if clause := firstClause(firstSentence(script), 15); utf8.RuneCountInString(clause) >= 6 {
		out = append(out, clause)
	}
	if q := firstQuestion(script); q != "" {
		if clause := firstClause(q, 15); utf8.RuneCountInString(clause) >= 6 && !containsString(out, clause) {
			out = append(out, clause)
		}
	}
	return out
}

// descriptionFallbacks 同样只从正文取：开头一句、第一个反问句。
func descriptionFallbacks(script string) []string {
	out := make([]string, 0, 2)
	if lead := strings.TrimSpace(firstSentence(script)); lead != "" {
		out = append(out, lead)
	}
	if q := firstQuestion(script); q != "" && !containsString(out, q) {
		out = append(out, q)
	}
	return out
}

// firstClause 取到第一个逗号/顿号/分号为止，超过 max 个字就在最后一个标点处截。
func firstClause(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	end := len(runes)
	for i, r := range runes {
		if r == '，' || r == ',' || r == '、' || r == '；' || r == '：' {
			end = i
			break
		}
	}
	if end > max {
		end = max
		for i := max; i > 6; i-- {
			if runes[i-1] == '，' || runes[i-1] == '、' || runes[i-1] == '。' {
				end = i - 1
				break
			}
		}
	}
	return strings.TrimRight(strings.TrimSpace(string(runes[:end])), "，,、；：。！？")
}

// firstQuestion 返回正文里第一个以问号结尾的句子（开头的反问最像标题）。
func firstQuestion(script string) string {
	text := strings.ReplaceAll(strings.TrimSpace(script), "\r\n", "\n")
	start := 0
	for i, r := range text {
		switch r {
		case '。', '！', '\n':
			start = i + utf8.RuneLen(r)
		case '？', '?':
			q := strings.TrimSpace(text[start : i+utf8.RuneLen(r)])
			if n := utf8.RuneCountInString(q); n >= 8 && n <= 60 {
				return q
			}
			start = i + utf8.RuneLen(r)
		}
	}
	return ""
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

// pickHotTopics 收模型给的话题（不卡白名单，垂直标签如 #楼市 #房贷 更利于
// 精准流量池），不足 3 个时从热门池补齐，最多 4 个——话题堆太多反而稀释。
func pickHotTopics(given []string, seed string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	add := func(tag string) {
		tag = normalizeHashtag(tag)
		if tag == "" || tag == "#" || seen[tag] || len(out) >= 4 {
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

// descriptionMaxRunes 是描述正文（不含话题）的目标上限，提示词按 40 字要求；
// descriptionHardRunes 是兜底截断线——超过才截，且只在标点处截，绝不切半句。
const (
	descriptionMaxRunes  = 40
	descriptionHardRunes = 60
)

func clipDescriptionBody(text string) string {
	body := strings.TrimSpace(trailingHashtagRun.ReplaceAllString(strings.TrimSpace(text), ""))
	runes := []rune(body)
	// 先按句号切：最多留两句，且第一句之后一旦超过目标长度就停在第一句。
	sentences := 0
	for i, r := range runes {
		switch r {
		case '。', '！', '？', '!', '?', '；', ';':
			sentences++
			if sentences == 2 || i+1 >= descriptionMaxRunes {
				runes = runes[:i+1]
				return strings.TrimSpace(string(runes))
			}
		}
	}
	if len(runes) <= descriptionHardRunes {
		return body
	}
	// 一句话超过硬线：退到硬线之前最后一个逗号/顿号，补句号收口。
	cut := descriptionHardRunes
	for i := descriptionHardRunes; i > descriptionMaxRunes/2; i-- {
		if runes[i-1] == '，' || runes[i-1] == '、' || runes[i-1] == ',' {
			cut = i - 1
			break
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + "。"
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

// clipRunes 截到 max 个字；不足 min 时原样返回（不再用「窗口来了」补字）。
func clipRunes(text string, min, max int) string {
	_ = min
	runes := []rune(strings.TrimSpace(text))
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
	// 凑不够 min 就少给：发布字段宁缺毋滥，不再生成「二创标题N窗口」这类占位。
	_ = min
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
