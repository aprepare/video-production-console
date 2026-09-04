package openaicompat

import (
	"fmt"
	"sort"
	"strings"
)

// rewrite 路径的出稿自检：出稿后按「连续 10 个内容字与原文一样即计入重合」
// 对照原文，超标把撞车片段原样回传给写稿模型、只重写这些句子；同时卡篇幅
// 下限，短稿要求补写。刻度参考（2026-08 标定）：全换说法的稿约 4%～8%，
// 人工认可的洗稿对约 20%，照抄结构的坏稿更高。2026-08-26 操作员定线：
// 允许贴爆款开头等部分原句直接交付，重合 ≤20% 且篇幅 ≥0.9 倍即放行。
const (
	// selfCheckSpanRunes 判抄窗口：连续多少个内容字与原文一致算一处撞车。
	selfCheckSpanRunes = 10
	// selfCheckMaxOverlap 触发返工的重合率上限，低于它直接交付。
	selfCheckMaxOverlap = 0.20
	// selfCheckHardOverlap 返工用尽后仍超过此值，任务直接失败。
	selfCheckHardOverlap = 0.30
	// selfCheckMinLenRatio 成稿内容字数与原文之比的下限，短于此触发补写。
	// 2026-08-26 操作员定线：宁短勿凑，0.8 以上讲透即可交付。
	selfCheckMinLenRatio = 0.80
	// selfCheckHardLenRatio 返工用尽后仍短于此比例，任务直接失败（拦摘要稿）。
	selfCheckHardLenRatio = 0.65
	// selfCheckMaxRounds 最多几轮返工。
	selfCheckMaxRounds = 2
	// selfCheckMaxSpans 返工提示里最多列几处撞车片段。
	selfCheckMaxSpans = 12
	// selfCheckSpanPreviewRunes 撞车片段在提示/警告里的展示长度上限。
	selfCheckSpanPreviewRunes = 60
	// selfCheckMinSourceRunes 原文内容字少于此数不做自检（测试桩、垫稿）。
	selfCheckMinSourceRunes = 200
)

// SelfCheckLimits 机械自检阈值。默认值来自上面的常量；工作流的「机械自检」
// 节点可以按篇调整（连抄上限、硬上限、篇幅下限、返工轮数）。
type SelfCheckLimits struct {
	MaxOverlap   float64
	HardOverlap  float64
	MinLenRatio  float64
	HardLenRatio float64
	MaxRounds    int
}

func defaultSelfCheckLimits() SelfCheckLimits {
	return SelfCheckLimits{
		MaxOverlap:   selfCheckMaxOverlap,
		HardOverlap:  selfCheckHardOverlap,
		MinLenRatio:  selfCheckMinLenRatio,
		HardLenRatio: selfCheckHardLenRatio,
		MaxRounds:    selfCheckMaxRounds,
	}
}

// SelfCheckDefaults 暴露默认阈值给工作流界面展示（%和倍数口径）。
func SelfCheckDefaults() (overlapMaxPct, overlapHardPct int, lenMinRatio, lenHardRatio float64, maxRounds int) {
	return int(selfCheckMaxOverlap * 100), int(selfCheckHardOverlap * 100),
		selfCheckMinLenRatio, selfCheckHardLenRatio, selfCheckMaxRounds
}

type selfCheckStats struct {
	overlap     float64
	spans       []string
	sourceRunes int
	draftRunes  int
	lenRatio    float64
}

func (s selfCheckStats) overlapPercent() int {
	return int(s.overlap*100 + 0.5)
}

func (s selfCheckStats) passes(limits SelfCheckLimits) bool {
	return s.overlap <= limits.MaxOverlap && s.lenRatio >= limits.MinLenRatio
}

func (s selfCheckStats) hardFails(limits SelfCheckLimits) bool {
	return s.overlap > limits.HardOverlap || s.lenRatio < limits.HardLenRatio
}

// selfCheckScore 把「超标多少」折成一个可比较的分数，用来判断返工稿有没有变好。
func selfCheckScore(s selfCheckStats, limits SelfCheckLimits) float64 {
	score := 0.0
	if s.overlap > limits.MaxOverlap {
		score += (s.overlap - limits.MaxOverlap) / limits.MaxOverlap
	}
	if s.lenRatio < limits.MinLenRatio {
		score += (limits.MinLenRatio - s.lenRatio) / limits.MinLenRatio
	}
	return score
}

// selfCheckMeasure 在内容字（去空白标点，固定要素已被 sentinel 打断）上做
// 贪心最长延伸匹配，返回重合率、撞车片段和篇幅比。
func selfCheckMeasure(source, draft string) selfCheckStats {
	src := overlapContentRunes(source, '\x01')
	dst := overlapContentRunes(draft, '\x02')
	stats := selfCheckStats{sourceRunes: len(src), draftRunes: len(dst)}
	if len(src) > 0 {
		stats.lenRatio = float64(len(dst)) / float64(len(src))
	}
	k := selfCheckSpanRunes
	if len(src) < k || len(dst) < k {
		return stats
	}
	seeds := make(map[string][]int, len(src))
	for i := 0; i+k <= len(src); i++ {
		key := string(src[i : i+k])
		seeds[key] = append(seeds[key], i)
	}
	type span struct{ start, length int }
	spans := make([]span, 0, 8)
	covered := 0
	for i := 0; i+k <= len(dst); {
		positions, ok := seeds[string(dst[i:i+k])]
		if !ok {
			i++
			continue
		}
		best := k
		for _, p := range positions {
			n := k
			for p+n < len(src) && i+n < len(dst) && src[p+n] == dst[i+n] {
				n++
			}
			if n > best {
				best = n
			}
		}
		spans = append(spans, span{start: i, length: best})
		covered += best
		i += best
	}
	stats.overlap = float64(covered) / float64(len(dst))
	sort.Slice(spans, func(a, b int) bool { return spans[a].length > spans[b].length })
	for idx, s := range spans {
		if idx >= selfCheckMaxSpans {
			break
		}
		stats.spans = append(stats.spans, string(dst[s.start:s.start+s.length]))
	}
	return stats
}

func selfCheckClipSpan(span string) string {
	runes := []rune(span)
	if len(runes) <= selfCheckSpanPreviewRunes {
		return span
	}
	return string(runes[:selfCheckSpanPreviewRunes]) + "…"
}

// buildSelfCheckRepairPrompt 把撞车片段和篇幅缺口当证据列给模型，只许改这些。
func buildSelfCheckRepairPrompt(stats selfCheckStats, limits SelfCheckLimits) string {
	var b strings.Builder
	b.WriteString("后台自检不通过，按下面的清单修改，然后按上一条回复相同的 JSON 结构返回完整结果：\n")
	item := 1
	if stats.overlap > limits.MaxOverlap {
		fmt.Fprintf(&b, "%d. 与原文连续 %d 字以上原样重合的内容占全稿 %d%%，上限 %d%%。下面这些片段必须换说法重写：数字、机构名、政策文件这些事实原词保留，其余的字不要和原文连排一致。\n",
			item, selfCheckSpanRunes, stats.overlapPercent(), int(limits.MaxOverlap*100+0.5))
		for i, span := range stats.spans {
			fmt.Fprintf(&b, "  %d)「%s」\n", i+1, selfCheckClipSpan(span))
		}
		item++
	}
	if stats.lenRatio < limits.MinLenRatio {
		fmt.Fprintf(&b, "%d. 成稿只有原文篇幅的 %.2f 倍，低于下限 %.2f 倍。把讲得薄的信息节点各自扩写三到四句口播，补够篇幅；不许压缩或删掉其他段落来凑。\n",
			item, stats.lenRatio, limits.MinLenRatio)
	}
	b.WriteString("清单之外的句子保持原样，不要整篇重写。\n")
	return b.String()
}

func selfCheckWarnings(stats selfCheckStats) []string {
	if len(stats.spans) == 0 {
		return nil
	}
	limit := 5
	warnings := make([]string, 0, limit)
	for _, span := range stats.spans {
		if len(warnings) >= limit {
			break
		}
		warnings = append(warnings, "自检：该句与原文连抄未修复「"+selfCheckClipSpan(span)+"」")
	}
	return warnings
}

// selfCheckRemixRewrite 是 rewrite 路径（进化台提示词走的就是这条）的出稿闸门：
// 本地先改课名等硬伤，再做重合/篇幅自检，超标按 limits 送回模型返工。
// 返工用尽仍超硬性上限则返回错误，任务失败暴露给操作台。
func selfCheckRemixRewrite(client ChatClient, model, effort, system, user, source, content, outputDir string, limits SelfCheckLimits) (string, []string, string, error) {
	logEvent := func(event map[string]any) {
		event["event"] = "self_check"
		appendRemixRunLog(outputDir, event)
	}
	draft, err := parseRemixDraft(content)
	if err != nil || strings.TrimSpace(draft.ContinuousScript) == "" {
		logEvent(map[string]any{"verdict": "skip", "reason": "draft_unparsable"})
		return content, nil, "自检未执行：草稿无法解析。", nil
	}
	note := ""
	script, localNotes := applyLocalCopyFixes(draft.ContinuousScript)
	if len(localNotes) > 0 {
		content = replaceDraftScript(content, script)
		note = "本地已改：" + strings.Join(localNotes, "；") + "。"
	}
	stats := selfCheckMeasure(source, script)
	if stats.sourceRunes < selfCheckMinSourceRunes {
		logEvent(map[string]any{"verdict": "skip", "reason": "source_too_short", "source_runes": stats.sourceRunes})
		return content, nil, appendCheckNote(note, "自检跳过：原文过短，不足以判定重合。"), nil
	}
	firstPercent := stats.overlapPercent()
	logEvent(map[string]any{
		"round": 0, "verdict": verdictOf(stats, limits), "overlap_pct": firstPercent,
		"len_ratio": round2(stats.lenRatio), "spans": len(stats.spans),
	})
	rounds := 0
	for rounds < limits.MaxRounds && !stats.passes(limits) {
		rounds++
		resp, chatErr := client.Chat(ChatRequest{
			Model:           strings.TrimSpace(model),
			ReasoningEffort: effort,
			Stream:          true,
			Messages: []Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
				{Role: "assistant", Content: content},
				{Role: "user", Content: buildSelfCheckRepairPrompt(stats, limits)},
			},
		})
		if chatErr != nil || len(resp.Choices) == 0 {
			detail := "empty choices"
			if chatErr != nil {
				detail = chatErr.Error()
			}
			logEvent(map[string]any{"round": rounds, "verdict": "repair_request_failed", "error": detail})
			note = appendCheckNote(note, fmt.Sprintf("自检第 %d 轮返工请求失败。", rounds))
			break
		}
		retry := resp.Choices[0].Message.Content
		retryDraft, parseErr := parseRemixDraft(retry)
		if parseErr != nil || strings.TrimSpace(retryDraft.ContinuousScript) == "" {
			logEvent(map[string]any{"round": rounds, "verdict": "repair_unparsable"})
			note = appendCheckNote(note, fmt.Sprintf("自检第 %d 轮返工结果无法解析，保留上一稿。", rounds))
			break
		}
		retryScript, moreLocal := applyLocalCopyFixes(retryDraft.ContinuousScript)
		retry = replaceDraftScript(retry, retryScript)
		retryStats := selfCheckMeasure(source, retryScript)
		logEvent(map[string]any{
			"round": rounds, "verdict": verdictOf(retryStats, limits), "overlap_pct": retryStats.overlapPercent(),
			"len_ratio": round2(retryStats.lenRatio), "spans": len(retryStats.spans),
		})
		if selfCheckScore(retryStats, limits) >= selfCheckScore(stats, limits) {
			note = appendCheckNote(note, fmt.Sprintf("自检第 %d 轮返工未见改善，保留上一稿。", rounds))
			break
		}
		content = retry
		script = retryScript
		stats = retryStats
		if len(moreLocal) > 0 {
			note = appendCheckNote(note, "返工后再改："+strings.Join(moreLocal, "；")+"。")
		}
	}
	// 锁词数字校对：年份写错是硬错，一轮定向返工后仍错就失败；原文数字漏掉
	// 只补一轮，补不回来当警告交付（可能是口播化写法，交人工复核）。
	lockIssues := checkLockNumbers(source, script)
	if !lockIssues.empty() {
		logEvent(map[string]any{"verdict": "lock_numbers", "foreign_years": lockIssues.ForeignYears, "missing": lockIssues.Missing})
		resp, chatErr := client.Chat(ChatRequest{
			Model:           strings.TrimSpace(model),
			ReasoningEffort: effort,
			Stream:          true,
			Messages: []Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
				{Role: "assistant", Content: content},
				{Role: "user", Content: buildLockNumberRepairPrompt(lockIssues)},
			},
		})
		if chatErr == nil && len(resp.Choices) > 0 {
			if retryDraft, parseErr := parseRemixDraft(resp.Choices[0].Message.Content); parseErr == nil && strings.TrimSpace(retryDraft.ContinuousScript) != "" {
				retryScript, _ := applyLocalCopyFixes(retryDraft.ContinuousScript)
				retryIssues := checkLockNumbers(source, retryScript)
				retryStats := selfCheckMeasure(source, retryScript)
				// 数字修好且没把连抄/篇幅改坏才采用。
				if len(retryIssues.ForeignYears)+len(retryIssues.Missing) < len(lockIssues.ForeignYears)+len(lockIssues.Missing) &&
					selfCheckScore(retryStats, limits) <= selfCheckScore(stats, limits) {
					content = replaceDraftScript(resp.Choices[0].Message.Content, retryScript)
					script = retryScript
					stats = retryStats
					lockIssues = retryIssues
					note = appendCheckNote(note, "锁词数字已定向返工。")
				}
			}
		}
		logEvent(map[string]any{"verdict": "lock_numbers_after", "foreign_years": lockIssues.ForeignYears, "missing": lockIssues.Missing})
		if lockIssues.hasForeign() {
			return content, []string{"锁词数字错误：" + lockNumberSummary(lockIssues)}, appendCheckNote(note, "锁词校对不通过："+lockNumberSummary(lockIssues)),
				fmt.Errorf("锁词数字错误：成稿出现原文没有的年份 %s，返工后仍未改正", strings.Join(lockIssues.ForeignYears, "、"))
		}
	}
	summary := fmt.Sprintf("10字连抄重合 %d%%（上限 %d%%），篇幅 %.2f 倍（下限 %.2f）。",
		stats.overlapPercent(), int(limits.MaxOverlap*100+0.5), stats.lenRatio, limits.MinLenRatio)
	if len(lockIssues.Missing) > 0 {
		summary += "锁词数字未全部出现（" + strings.Join(lockIssues.Missing, "、") + "），请人工核对。"
	}
	if rounds > 0 && firstPercent != stats.overlapPercent() {
		summary = fmt.Sprintf("10字连抄重合 %d%%→%d%%（返工 %d 轮），篇幅 %.2f 倍。", firstPercent, stats.overlapPercent(), rounds, stats.lenRatio)
	}
	var lockWarnings []string
	if len(lockIssues.Missing) > 0 {
		lockWarnings = []string{"锁词数字未全部出现：" + strings.Join(lockIssues.Missing, "、")}
	}
	if stats.passes(limits) {
		return content, lockWarnings, appendCheckNote(note, "自检通过："+summary), nil
	}
	warnings := append(selfCheckWarnings(stats), lockWarnings...)
	if stats.hardFails(limits) {
		note = appendCheckNote(note, "自检不通过："+summary)
		return content, warnings, note, fmt.Errorf("自检不通过：%s返工 %d 轮后仍超硬性上限（重合 %d%%、篇幅 %.2f 倍）", summary, rounds, stats.overlapPercent(), stats.lenRatio)
	}
	return content, warnings, appendCheckNote(note, "自检超标但未到硬性上限，已交付并列出未修复片段，请人工复核："+summary), nil
}

func verdictOf(stats selfCheckStats, limits SelfCheckLimits) string {
	switch {
	case stats.passes(limits):
		return "pass"
	case stats.hardFails(limits):
		return "hard_fail"
	default:
		return "soft_fail"
	}
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
