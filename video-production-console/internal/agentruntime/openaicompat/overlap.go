package openaicompat

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 交付前自检：新稿与同行原文的原句重合检测。
//
// 比对在「内容字」上做（去掉空白和标点），所以只改标点的照搬照样会被抓住。
// 数字必须原词保留是写作规则，但带数字的句子同样必须换说法，所以数字不打断
// 匹配；只有整段几乎全是数字时（letterCount 不足）才放过。
const (
	// overlapSeedRunes 是判定「照搬」的最短公共片段长度（内容字）。
	// 口头禅和固定表达通常在 10 字以内；12 字以上还一字不差基本就是抄。
	overlapSeedRunes = 12
	// overlapMinLetters 要求片段里至少有这么多非数字的字，纯数据串不算抄。
	overlapMinLetters = 8
	// overlapMaxFragments 限制报告条数，避免整篇照搬时刷屏。
	overlapMaxFragments = 10
	// overlapWarningRunes 是警告里展示片段的最大长度。
	overlapWarningRunes = 40
	// overlapCoverageWindow 用 8 个内容字的滑动窗口估字面重合。
	overlapCoverageWindow = 8
	// overlapCoverageMax 是交付上限。超过就返工，仍超则任务失败。
	overlapCoverageMax = 0.40
)

// overlapAllowedPhrases 是允许在每篇稿子里原样出现的固定要素：课程名、入口
// 和上车话术。比对前把它们打断，避免把「必须重复」判成「照搬」。
var overlapAllowedPhrases = []string{
	"财富觉醒方法论",
	"主页橱窗",
	"推广期间只要五块钱",
	"推广期间只要5块钱",
	"现在上车还来得及",
}

// contentRunes 只保留会被观众听到的字：字母、数字和百分号。
func overlapContentRunes(text string, sentinel rune) []rune {
	// 固定要素替换成 sentinel；source 和 draft 用不同 sentinel，保证匹配
	// 永远无法穿过这些短语。
	for _, phrase := range overlapAllowedPhrases {
		text = strings.ReplaceAll(text, phrase, string(sentinel))
	}
	out := make([]rune, 0, utf8.RuneCountInString(text))
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '%' || r == '％' || r == sentinel {
			out = append(out, r)
		}
	}
	return out
}

// overlapFragments 返回新稿里与原文一字不差的片段（内容字 ≥ overlapSeedRunes
// 且非数字字 ≥ overlapMinLetters），按出现顺序排列。
func overlapFragments(source, draft string) []string {
	src := overlapContentRunes(source, '\x01')
	dst := overlapContentRunes(draft, '\x02')
	if len(src) < overlapSeedRunes || len(dst) < overlapSeedRunes {
		return nil
	}
	seeds := make(map[string][]int, len(src))
	for i := 0; i+overlapSeedRunes <= len(src); i++ {
		key := string(src[i : i+overlapSeedRunes])
		seeds[key] = append(seeds[key], i)
	}
	fragments := make([]string, 0, 4)
	for i := 0; i+overlapSeedRunes <= len(dst) && len(fragments) < overlapMaxFragments; {
		positions, ok := seeds[string(dst[i:i+overlapSeedRunes])]
		if !ok {
			i++
			continue
		}
		best := overlapSeedRunes
		for _, p := range positions {
			n := overlapSeedRunes
			for p+n < len(src) && i+n < len(dst) && src[p+n] == dst[i+n] {
				n++
			}
			if n > best {
				best = n
			}
		}
		fragment := dst[i : i+best]
		if overlapLetterCount(fragment) >= overlapMinLetters {
			fragments = append(fragments, string(fragment))
		}
		i += best
	}
	return fragments
}

func overlapLetterCount(runes []rune) int {
	n := 0
	for _, r := range runes {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// overlapRuneTotal 用于比较返工前后哪版抄得更少。
func overlapRuneTotal(fragments []string) int {
	total := 0
	for _, fragment := range fragments {
		total += utf8.RuneCountInString(fragment)
	}
	return total
}

// overlapWarnings 把残留重合渲染成任务警告，控制台任务结果里能直接看到。
func overlapWarnings(fragments []string) []string {
	if len(fragments) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		runes := []rune(fragment)
		if len(runes) > overlapWarningRunes {
			fragment = string(runes[:overlapWarningRunes]) + "…"
		}
		warnings = append(warnings, "自检：该句与原文重合未修复「"+fragment+"」")
	}
	return warnings
}

func overlapCoverage(source, draft string) float64 {
	src := overlapContentRunes(source, '\x01')
	dst := overlapContentRunes(draft, '\x02')
	if len(src) < overlapCoverageWindow || len(dst) < overlapCoverageWindow {
		return 0
	}
	seeds := make(map[string]struct{}, len(src))
	for i := 0; i+overlapCoverageWindow <= len(src); i++ {
		seeds[string(src[i:i+overlapCoverageWindow])] = struct{}{}
	}
	hit := 0
	total := len(dst) - overlapCoverageWindow + 1
	for i := 0; i+overlapCoverageWindow <= len(dst); i++ {
		if _, ok := seeds[string(dst[i:i+overlapCoverageWindow])]; ok {
			hit++
		}
	}
	return float64(hit) / float64(total)
}

func overlapCoveragePercent(source, draft string) int {
	return int(overlapCoverage(source, draft)*100 + 0.5)
}

// buildOverlapRepairPrompt 是自检返工的第二轮用户消息：把照搬片段当证据列给
// 模型，只许重说这些句子。
func buildOverlapRepairPrompt(fragments []string) string {
	var b strings.Builder
	b.WriteString("自检发现成稿和同行原文字面重合过高，必须压到 40% 以下。按对标结构逐句换词换说法，数字、机构名、年份原词保留，连续 8 个字不要和原文一样。只把该换的句子换掉，按上一条回复相同的 JSON 结构返回完整结果：\n")
	for i, fragment := range fragments {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, fragment))
	}
	return b.String()
}

// repairSourceOverlap 在交付前做一轮字面重合闸门。8 字窗口覆盖超过 40%
// 必须返工；写稿模型先改一稿，配置了质检模型再交给质检模型。仍超 40% 则任务失败。
func repairSourceOverlap(client ChatClient, model, checkModel, effort, system, user, source, content string) (string, []string, string, error) {
	checkModel = strings.TrimSpace(checkModel)
	draft, err := parseRemixDraft(content)
	if err != nil || strings.TrimSpace(draft.ContinuousScript) == "" {
		if checkModel == "" {
			return content, nil, "质检未启用（质检模型留空），交付写稿模型原始输出。", nil
		}
		return content, nil, "质检未执行：草稿无法解析。", nil
	}
	coverage := overlapCoverage(source, draft.ContinuousScript)
	percent := overlapCoveragePercent(source, draft.ContinuousScript)
	fragments := overlapFragments(source, draft.ContinuousScript)
	if coverage <= overlapCoverageMax {
		if checkModel == "" {
			if len(fragments) == 0 {
				return content, nil, fmt.Sprintf("字面重合 %d%%，低于 40%%。质检未启用（质检模型留空）。", percent), nil
			}
			return content, overlapWarnings(fragments), fmt.Sprintf("字面重合 %d%%，低于 40%%。质检未启用。仍有 %d 处长句重合，见警告。", percent, len(fragments)), nil
		}
		if len(fragments) == 0 {
			return content, nil, fmt.Sprintf("质检通过（%s）：字面重合 %d%%，未发现长句照搬。", checkModel, percent), nil
		}
	}
	repairModel := strings.TrimSpace(model)
	if checkModel != "" {
		repairModel = checkModel
	}
	if repairModel == "" {
		return content, overlapWarnings(fragments), fmt.Sprintf("字面重合 %d%%，超过 40%%。", percent), fmt.Errorf("字面重合 %d%%，超过 40%%，必须换词换说法后再交", percent)
	}
	resp, err := client.Chat(ChatRequest{
		Model:           repairModel,
		ReasoningEffort: effort,
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
			{Role: "assistant", Content: content},
			{Role: "user", Content: buildOverlapRepairPrompt(fragments)},
		},
	})
	if err != nil || len(resp.Choices) == 0 {
		return content, overlapWarnings(fragments),
			fmt.Sprintf("字面重合 %d%%，返工请求失败。", percent),
			fmt.Errorf("字面重合 %d%%，超过 40%%，返工失败", percent)
	}
	retry := resp.Choices[0].Message.Content
	retryDraft, err := parseRemixDraft(retry)
	if err != nil || utf8.RuneCountInString(strings.TrimSpace(retryDraft.ContinuousScript)) < 40 {
		return content, overlapWarnings(fragments),
			fmt.Sprintf("字面重合 %d%%，返工结果无效。", percent),
			fmt.Errorf("字面重合 %d%%，超过 40%%，返工结果无效", percent)
	}
	retryCoverage := overlapCoverage(source, retryDraft.ContinuousScript)
	retryPercent := overlapCoveragePercent(source, retryDraft.ContinuousScript)
	retryFragments := overlapFragments(source, retryDraft.ContinuousScript)
	if retryCoverage <= overlapCoverageMax {
		note := fmt.Sprintf("字面重合从 %d%% 降到 %d%%。", percent, retryPercent)
		if checkModel != "" {
			note = fmt.Sprintf("质检（%s）：%s", checkModel, note)
		}
		return retry, overlapWarnings(retryFragments), note, nil
	}
	return retry, overlapWarnings(retryFragments),
		fmt.Sprintf("字面重合仍为 %d%%，超过 40%%。", retryPercent),
		fmt.Errorf("字面重合 %d%%，超过 40%%，换说法后仍未压到 40%% 以下", retryPercent)
}

const canonicalCourse = "财富觉醒方法论"

// 只处理紧邻课程名的旧商品标记，保留正文中的年份和白银话题。
var courseYearPrefix = regexp.MustCompile(`2026年?[ \t　]*财富觉醒方法论`)
var courseEditionSuffix = regexp.MustCompile(`财富觉醒方法论[ \t　]*(?:（[ \t　]*白银版[ \t　]*）|\([ \t　]*白银版[ \t　]*\)|白银版)`)

func stripCourseYear(text string) (string, bool) {
	orig := text
	text = courseYearPrefix.ReplaceAllString(text, canonicalCourse)
	text = courseEditionSuffix.ReplaceAllString(text, canonicalCourse)
	return text, text != orig
}

func courseNameIndexes(runes []rune) []int {
	name := []rune(canonicalCourse)
	if len(runes) < len(name) {
		return nil
	}
	out := make([]int, 0, 2)
	for i := 0; i <= len(runes)-len(name); i++ {
		match := true
		for j := 0; j < len(name); j++ {
			if runes[i+j] != name[j] {
				match = false
				break
			}
		}
		if match {
			out = append(out, i)
			i += len(name) - 1
		}
	}
	return out
}

func runeSentenceSpan(runes []rune, pos, width int) (int, int) {
	start := pos
	for start > 0 {
		r := runes[start-1]
		if r == '。' || r == '！' || r == '？' || r == '\n' {
			break
		}
		start--
	}
	end := pos + width
	if end > len(runes) {
		end = len(runes)
	}
	for end < len(runes) {
		r := runes[end]
		end++
		if r == '。' || r == '！' || r == '？' || r == '\n' {
			break
		}
	}
	return start, end
}

// courseTailStart 是卖课段允许开始的位置：正文前 65% 里不许出现课名。
const courseTailStart = 0.65

// collapseDuplicateCourseMentions 删掉落在正文前段（卖课段之前）的课名句：
// 卖课只能在课尾。课尾内部课名可以出现几次（读心式课尾会点两三回名），
// 这些一律保留；只有课尾里一次都没提、课名全在前段时才退回「只留第一次」。
func collapseDuplicateCourseMentions(script string) (string, bool) {
	runes := []rune(script)
	idxs := courseNameIndexes(runes)
	if len(idxs) < 2 {
		return script, false
	}
	nameLen := len([]rune(canonicalCourse))
	tailStart := int(float64(len(runes)) * courseTailStart)
	if firstTail := firstIndexAtOrAfter(idxs, tailStart); firstTail >= 0 {
		// 课尾里有课名：只删课尾之前的那些句子，课尾内部不动。
		changed := false
		for k := len(idxs) - 1; k >= 0; k-- {
			if idxs[k] >= tailStart {
				continue
			}
			start, end := courseSentenceSpan(runes, idxs[k], nameLen)
			runes = append(runes[:start], runes[end:]...)
			changed = true
		}
		out := strings.TrimSpace(string(runes))
		return out, changed && out != strings.TrimSpace(script)
	}
	for k := len(idxs) - 1; k >= 1; k-- {
		start, end := courseSentenceSpan(runes, idxs[k], nameLen)
		runes = append(runes[:start], runes[end:]...)
		idxs = courseNameIndexes(runes)
		if len(idxs) < 2 {
			break
		}
	}
	out := strings.TrimSpace(string(runes))
	return out, out != strings.TrimSpace(script)
}

// courseSentenceSpan 是要删掉的那一句的范围：句子太长时只删从课名到句末。
func courseSentenceSpan(runes []rune, idx, nameLen int) (int, int) {
	start, end := runeSentenceSpan(runes, idx, nameLen)
	if end-start > 120 {
		start = idx
		end = idx + nameLen
		for end < len(runes) && runes[end] != '。' && runes[end] != '！' && runes[end] != '？' && runes[end] != '\n' {
			end++
		}
		if end < len(runes) {
			end++
		}
	}
	return start, end
}

func firstIndexAtOrAfter(idxs []int, at int) int {
	for _, idx := range idxs {
		if idx >= at {
			return idx
		}
	}
	return -1
}

func applyLocalCopyFixes(script string) (string, []string) {
	notes := make([]string, 0, 4)
	script, stripped := stripCourseYear(script)
	if stripped {
		notes = append(notes, "统一课程名称，去掉年份与版本")
	}
	script, converted := chineseSmallNumbers(script)
	if converted {
		notes = append(notes, "修辞性小数字改汉字")
	}
	script, collapsed := collapseDuplicateCourseMentions(script)
	if collapsed {
		notes = append(notes, "删掉重复的卖课收口")
	}
	script, droppedRange := stripForbiddenDepositRange(script)
	if droppedRange {
		notes = append(notes, "去掉50到77万亿定存区间")
	}
	return script, notes
}

func stripForbiddenDepositRange(script string) (string, bool) {
	orig := script
	replacements := []string{
		"50万亿到77万亿", "50 万亿到 77 万亿", "50到77万亿", "50 到 77 万亿",
		"50至77万亿", "50–77万亿", "50—77万亿", "50-77万亿",
		"50到75万亿", "50–75万亿", "50-75万亿", "50至75万亿",
		"五十到七十七万亿", "五十万亿到七十七万亿",
	}
	for _, phrase := range replacements {
		script = strings.ReplaceAll(script, phrase, "一大批到期资金")
	}
	return script, script != orig
}

func inspectCopyIssues(script, source string) []string {
	issues := make([]string, 0, 6)
	if strings.Contains(script, "2026财富觉醒") {
		issues = append(issues, "课程名带了年份，必须改成《财富觉醒方法论》")
	}
	runes := []rune(script)
	tailStart := int(float64(len(runes)) * courseTailStart)
	for _, idx := range courseNameIndexes(runes) {
		if idx < tailStart {
			issues = append(issues, "开头或中段出现课名，卖课只能放在课尾")
			break
		}
	}
	if idx := strings.Index(script, "主页橱窗"); idx >= 0 {
		head := utf8.RuneCountInString(script[:idx])
		if len(runes) > 0 && head < tailStart {
			issues = append(issues, "开头或中段出现卖课入口，卖课只能放在全文最末")
		}
	}
	if i := strings.Index(script, canonicalCourse); i >= 0 {
		// 读心式课尾 320～480 字，课名之后留足空间；超过 600 字才算收不住。
		if utf8.RuneCountInString(script[i:]) > 600 {
			issues = append(issues, "结尾卖课段过长（课名之后超过600字），压回480字以内，逼单句要短促")
		}
	}
	if len(runes) > 180 {
		open := string(runes[:180])
		if !strings.ContainsAny(open, "？?") && !strings.Contains(open, "万亿") && !strings.Contains(open, "存") {
			issues = append(issues, "开头钩子过长或不够具体，前几句必须落到观众自家的钱")
		}
	}
	if hasForbiddenDepositRange(script) {
		issues = append(issues, "不要写50到77万亿或50到75万亿定存到期，改成到期规模很大、分批出来")
	}
	if hasSameOpeningCut(script, source) {
		issues = append(issues, "开场切口和原稿同类：不要再问2万亿去了哪儿，也不要用不是买房不是炒股黄金没接住这套切入口")
	}
	return issues
}

func hasForbiddenDepositRange(script string) bool {
	needles := []string{
		"50到77", "50 到 77", "50万亿到77", "50至77", "50–77", "50-77", "50—77",
		"50到75", "50–75", "50-75", "五十到七十七", "五十万亿到七十七",
	}
	for _, needle := range needles {
		if strings.Contains(script, needle) {
			return true
		}
	}
	return false
}

func openingHead(text string, n int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > n {
		runes = runes[:n]
	}
	return string(runes)
}

func hasSameOpeningCut(script, source string) bool {
	head := openingHead(script, 80)
	hasGone := strings.Contains(head, "去了哪儿") || strings.Contains(head, "去了哪里") || strings.Contains(head, "究竟去了") || strings.Contains(head, "到底去了") || strings.Contains(head, "到底变成了什么")
	hasNotHouseStockGold := (strings.Contains(head, "房") || strings.Contains(head, "楼")) && (strings.Contains(head, "股") || strings.Contains(head, "股市")) && strings.Contains(head, "黄金")
	has2yi := strings.Contains(head, "2万亿") || strings.Contains(head, "两万亿") || strings.Contains(head, "20500")
	if hasGone && hasNotHouseStockGold && has2yi {
		return true
	}
	if strings.TrimSpace(source) == "" {
		return false
	}
	srcHead := openingHead(source, 80)
	return overlapCoverage(srcHead, head) >= 0.40
}

func hardCopyIssue(issue string) bool {
	return strings.Contains(issue, "50到77") || strings.Contains(issue, "开场切口")
}

func replaceDraftScript(raw, script string) string {
	text := strings.TrimSpace(stripCodeFence(raw))
	extracted := text
	if !strings.HasPrefix(extracted, "{") {
		if objText := extractJSONObject(extracted); objText != "" {
			extracted = objText
		}
	}
	if strings.HasPrefix(extracted, "{") {
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(extracted), &obj) == nil {
			b, err := json.Marshal(script)
			if err == nil {
				obj["continuous_script"] = b
				if out, err := json.Marshal(obj); err == nil {
					return string(out)
				}
			}
		}
	}
	if draft, err := parseRemixDraft(raw); err == nil && strings.TrimSpace(draft.ContinuousScript) != "" {
		draft.ContinuousScript = script
		if out, err := json.Marshal(draft); err == nil {
			return string(out)
		}
	}
	return script
}

func buildCopyRepairPrompt(issues []string) string {
	var b strings.Builder
	b.WriteString("质检发现成稿开头、禁写项或卖课收口不合格。按下面几条直接改 continuous_script 后保存，数字和未揭晓的答案原词保留，按上一条回复相同的 JSON 结构返回完整结果：\n")
	for i, issue := range issues {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, issue))
	}
	b.WriteString("课程名只能写成《财富觉醒方法论》，不要带年份。卖课只在全文最末收口一次，不要重复第二遍。\n")
	return b.String()
}

func appendCheckNote(note string, extra string) string {
	note = strings.TrimSpace(note)
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return note
	}
	if note == "" {
		return extra
	}
	if strings.HasSuffix(note, "。") {
		return note + extra
	}
	return note + "。" + extra
}

// repairRemixDraft 先做原文重合质检，再查课名、开头、结尾和卖课钩子。
// 能本地改的直接改稿保存；改不干净的再交给质检模型返工。
func repairRemixDraft(client ChatClient, model, checkModel, effort, system, user, source, content string) (string, []string, string, error) {
	content, warnings, note, err := repairSourceOverlap(client, model, checkModel, effort, system, user, source, content)
	if err != nil {
		return content, warnings, note, err
	}
	draft, parseErr := parseRemixDraft(content)
	if parseErr != nil || strings.TrimSpace(draft.ContinuousScript) == "" {
		return content, warnings, note, nil
	}
	raw := content
	raw, yearStripped := stripCourseYear(raw)
	script, localNotes := applyLocalCopyFixes(draft.ContinuousScript)
	if yearStripped && !containsString(localNotes, "课名去掉年份") {
		localNotes = append([]string{"课名去掉年份"}, localNotes...)
	}
	content = replaceDraftScript(raw, script)
	issues := inspectCopyIssues(script, source)
	if len(localNotes) > 0 {
		note = appendCheckNote(note, "本地已改："+strings.Join(localNotes, "；")+"。")
	}
	if len(issues) == 0 {
		return content, warnings, note, nil
	}
	if strings.TrimSpace(checkModel) == "" {
		for _, issue := range issues {
			if hardCopyIssue(issue) {
				return content, warnings, appendCheckNote(note, "仍待改："+strings.Join(issues, "；")+"。"), fmt.Errorf("二创禁写项未过：%s", strings.Join(issues, "；"))
			}
		}
		return content, warnings, appendCheckNote(note, "仍待人工看："+strings.Join(issues, "；")+"。"), nil
	}
	resp, chatErr := client.Chat(ChatRequest{
		Model:           checkModel,
		ReasoningEffort: effort,
		Stream:          true,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
			{Role: "assistant", Content: content},
			{Role: "user", Content: buildCopyRepairPrompt(issues)},
		},
	})
	if chatErr != nil || len(resp.Choices) == 0 {
		return content, warnings, appendCheckNote(note, fmt.Sprintf("文案质检（%s）返工失败，已保存本地修改。", checkModel)), nil
	}
	retry := resp.Choices[0].Message.Content
	retry, _ = stripCourseYear(retry)
	retryDraft, parseErr := parseRemixDraft(retry)
	if parseErr != nil || utf8.RuneCountInString(strings.TrimSpace(retryDraft.ContinuousScript)) < 40 {
		return content, warnings, appendCheckNote(note, fmt.Sprintf("文案质检（%s）返工结果无效，已保存本地修改。", checkModel)), nil
	}
	retryScript, moreLocal := applyLocalCopyFixes(retryDraft.ContinuousScript)
	retry = replaceDraftScript(retry, retryScript)
	retryIssues := inspectCopyIssues(retryScript, source)
	if len(moreLocal) > 0 {
		note = appendCheckNote(note, "返工后再改："+strings.Join(moreLocal, "；")+"。")
	}
	if len(retryIssues) == 0 {
		return retry, warnings, appendCheckNote(note, fmt.Sprintf("文案质检（%s）已按课名/开头/结尾改稿保存。", checkModel)), nil
	}
	for _, issue := range retryIssues {
		if hardCopyIssue(issue) {
			return retry, warnings, appendCheckNote(note, fmt.Sprintf("文案质检（%s）禁写项仍未过：%s", checkModel, strings.Join(retryIssues, "；"))), fmt.Errorf("二创禁写项未过：%s", strings.Join(retryIssues, "；"))
		}
	}
	if len(retryIssues) <= len(issues) {
		return retry, warnings, appendCheckNote(note, fmt.Sprintf("文案质检（%s）已按课名/开头/结尾改稿保存。", checkModel)), nil
	}
	return content, warnings, appendCheckNote(note, fmt.Sprintf("文案质检（%s）返工未见改善，保留本地修改。", checkModel)), nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
