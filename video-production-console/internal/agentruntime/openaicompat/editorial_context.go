package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type editorialContext struct {
	CustomPolicy     bool            `json:"custom_policy,omitempty"`
	ReviewerUser     string          `json:"reviewer_user,omitempty"`
	Version          string          `json:"version"`
	Policy           string          `json:"policy"`
	Facts            json.RawMessage `json:"facts"`
	Limits           SelfCheckLimits `json:"limits"`
	ReviewerPrompt   string          `json:"reviewer_prompt"`
	FactsRequired    bool            `json:"facts_required,omitempty"`
	WritingPlan      json.RawMessage `json:"writing_plan,omitempty"`
	PlannedStructure bool            `json:"planned_structure,omitempty"`
}

// Decode complete outer objects in encounter order. InputOffset skips their
// children, so a trailing nested metadata object cannot replace the conclusion.
func completeJSONObjects(text string) []string {
	var objects []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var obj map[string]json.RawMessage
		if dec.Decode(&obj) == nil && obj != nil {
			n := int(dec.InputOffset())
			objects = append(objects, text[i:i+n])
			i += n - 1
		}
	}
	return objects
}

const unavailableFacts = `{"source_facts":[],"fresh_ammo":[],"status":"不可用","risk_notes":"未获得完整结构化事实结论，关键数据需人工复核，不得当成已核实。"}`

func normalizeFacts(text string) string {
	objects := completeJSONObjects(text)
	for i := len(objects) - 1; i >= 0; i-- {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal([]byte(objects[i]), &obj)
		var facts []json.RawMessage
		if raw, ok := obj["source_facts"]; ok && strings.HasPrefix(strings.TrimSpace(string(raw)), "[") && json.Unmarshal(raw, &facts) == nil {
			var entries []map[string]json.RawMessage
			for _, item := range facts {
				var f checkedFact
				if json.Unmarshal(item, &f) != nil || (strings.TrimSpace(f.Claim) == "" && strings.TrimSpace(f.Value) == "") {
					continue
				}
				switch f.Status {
				case "成立", "纠错", "已过时", "查不到", "needs_verify":
				default:
					continue
				}
				var entry map[string]json.RawMessage
				_ = json.Unmarshal(item, &entry)
				if (f.Status == "成立" || f.Status == "纠错" || f.Status == "已过时") && !supportedSource(f.Source) {
					entry["status"] = json.RawMessage(`"needs_verify"`)
					entry["verification_note"], _ = json.Marshal("缺少可定位的来源日期/链接或正确算式，尚未核准")
				}
				entries = append(entries, entry)
			}
			if len(entries) == 0 {
				continue
			}
			entriesJSON, _ := json.Marshal(entries)
			// Keep conclusions, not search traces or provider accounting fields.
			out := map[string]json.RawMessage{"source_facts": entriesJSON}
			for _, key := range []string{"fresh_ammo", "risk_notes", "status", "source_as_of", "checked_at"} {
				if v, ok := obj[key]; ok {
					out[key] = v
				}
			}
			raw, _ := json.Marshal(out)
			return string(raw)
		}
	}
	return unavailableFacts
}

func readEditorialFacts(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return unavailableFacts
	}
	for _, name := range []string{"facts_research.json", "node_output_facts.json"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if normalized := normalizeFacts(string(b)); normalized != unavailableFacts {
				return normalized
			}
		}
	}
	return unavailableFacts
}

func currentEditorialContext(dir string) editorialContext {
	return editorialContext{Version: EditorialPolicyVersion, Policy: SharedEditorialPolicy, Facts: json.RawMessage(readEditorialFacts(dir)), Limits: defaultSelfCheckLimits(), ReviewerPrompt: reviewerRolePrompt, WritingPlan: readWritingPlan(dir), PlannedStructure: true}
}

func loadEditorialContext(dir string) editorialContext {
	if strings.TrimSpace(dir) != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "editorial_context.json")); err == nil {
			var ctx editorialContext
			if json.Unmarshal(b, &ctx) == nil && ctx.Version != "" && (ctx.Policy != "" || ctx.CustomPolicy) {
				return ctx
			}
		}
	}
	return currentEditorialContext(dir)
}

func saveEditorialContext(dir string, ctx editorialContext) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	b, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "editorial_context.json"), b, 0644)
}

func withEditorialPolicy(prompt, policy string) string {
	if strings.TrimSpace(policy) == "" {
		return prompt
	}
	if strings.Contains(prompt, policy) {
		return prompt
	}
	return prompt + "\n\n以下共同规则优先于上文冲突的风格要求：\n" + policy
}

type checkedFact struct {
	Claim         string          `json:"claim"`
	Value         string          `json:"value"`
	Status        string          `json:"status"`
	Latest        string          `json:"latest"`
	Source        json.RawMessage `json:"source"`
	RewriteAction string          `json:"rewrite_action"`
}

func checkedFacts(raw string) []checkedFact {
	var doc struct {
		SourceFacts []json.RawMessage `json:"source_facts"`
	}
	_ = json.Unmarshal([]byte(raw), &doc)
	var out []checkedFact
	for _, r := range doc.SourceFacts {
		var f checkedFact
		if json.Unmarshal(r, &f) == nil && supportedSource(f.Source) {
			out = append(out, f)
		}
	}
	return out
}

// Corrections replace required values, while sourced optional fresh data merely
// permits its year; it must not become compulsory content in every rewrite.
func checkLockNumbersWithFacts(source, draft, facts string) lockNumberIssues {
	issues := checkLockNumbers(source, draft)
	// Raw source numbers may be unsupported rhetoric. Only approved facts can
	// require retention; a missing source percentage is not itself an error.
	issues.Missing = nil
	allowed := map[string]bool{}
	for _, f := range checkedFacts(facts) {
		value := f.Value
		switch f.Status {
		case "纠错", "已过时":
			if f.Latest == "" {
				continue
			}
			value = f.Latest
		case "成立":
		default:
			continue
		}
		for y := range lockNumberTokens(value+" "+f.Claim, lockYearRe) {
			allowed[y] = true
		}
		if f.RewriteAction == "omit" {
			continue
		}
		for _, v := range sortedKeys(lockNumberTokens(value, numericFactRe)) {
			if !hasNumericValue(draft, v) {
				issues.Missing = append(issues.Missing, v)
			}
		}
	}
	var doc struct {
		Fresh []struct {
			Value  string          `json:"value"`
			Fact   string          `json:"fact"`
			Source json.RawMessage `json:"source"`
		} `json:"fresh_ammo"`
	}
	_ = json.Unmarshal([]byte(facts), &doc)
	for _, f := range doc.Fresh {
		if supportedSource(f.Source) {
			for y := range lockNumberTokens(f.Value+" "+f.Fact, lockYearRe) {
				allowed[y] = true
			}
		}
	}
	var foreign []string
	for _, v := range issues.ForeignYears {
		if !allowed[v] {
			foreign = append(foreign, v)
		}
	}
	issues.ForeignYears = foreign
	return issues
}

func reviewRegression(source string, before, after remixDraft, ctx editorialContext) string {
	a, b := selfCheckMeasure(source, before.ContinuousScript), selfCheckMeasure(source, after.ContinuousScript)
	if a.sourceRunes >= selfCheckMinSourceRunes {
		if b.overlap > ctx.Limits.MaxOverlap && b.overlap > a.overlap+0.000001 {
			return fmt.Sprintf("修订重合率 %.2f%%→%.2f%%，超出本轮 %.0f%% 线并恶化，保留审前稿。", a.overlap*100, b.overlap*100, ctx.Limits.MaxOverlap*100)
		}
		if b.lenRatio < ctx.Limits.MinLenRatio && b.lenRatio < a.lenRatio || b.lenRatio > 1.2 && b.lenRatio > a.lenRatio {
			return fmt.Sprintf("修订篇幅 %.2f→%.2f 倍，越过篇幅目标且继续恶化，保留审前稿。", a.lenRatio, b.lenRatio)
		}
	}
	old := checkLockNumbersWithFacts(source, before.ContinuousScript, string(ctx.Facts))
	next := checkLockNumbersWithFacts(source, after.ContinuousScript, string(ctx.Facts))
	for _, v := range next.ForeignYears {
		if !containsExact(old.ForeignYears, v) {
			return "修订新增未经事实表支持的年份：" + v
		}
	}
	for _, v := range next.Missing {
		if !containsExact(old.Missing, v) {
			return "修订丢失或违背核准数字：" + v
		}
	}
	for _, v := range unsupportedNumericValues(source, after.ContinuousScript, string(ctx.Facts)) {
		if !containsExact(unsupportedNumericValues(source, before.ContinuousScript, string(ctx.Facts)), v) {
			return "修订新增未获事实支持的数值：" + v
		}
	}
	oldFields := publicationIssueCounts(before)
	for rule, count := range publicationIssueCounts(after) {
		if count > oldFields[rule] {
			return "修订新增发布字段问题，保留审前稿。"
		}
	}
	return ""
}

var numericFactRe = regexp.MustCompile(`\d+(?:\.\d+)?(?:[%％]|万亿元|万亿|亿元|万元|亿|万|元|块)`)
var issueQuoteRe = regexp.MustCompile(`「[^」]*」`)
var issueNumberRe = regexp.MustCompile(`\d+`)

func publicationIssueCounts(draft remixDraft) map[string]int {
	counts := map[string]int{}
	for _, issue := range publishFieldIssues(draft) {
		counts[issueNumberRe.ReplaceAllString(issueQuoteRe.ReplaceAllString(issue, ""), "N")]++
	}
	return counts
}

// Detect explicit monetary/rate values; leave unlabelled counts and Chinese
// numerals to semantic review rather than treating every rhetorical number as fact.
func unsupportedNumericValues(source, draft, facts string) []string {
	allowed := numericValues(source)
	for _, v := range []string{"5元", "5块"} {
		allowed[numericValueKey(v)] = true
	}
	for _, f := range checkedFacts(facts) {
		if f.Status == "成立" || f.Status == "纠错" || f.Status == "已过时" {
			for v := range lockNumberTokens(f.Value+" "+f.Latest, numericFactRe) {
				allowed[numericValueKey(v)] = true
			}
		}
	}
	var doc struct {
		Fresh []struct {
			Fact   string          `json:"fact"`
			Value  string          `json:"value"`
			Source json.RawMessage `json:"source"`
		} `json:"fresh_ammo"`
	}
	_ = json.Unmarshal([]byte(facts), &doc)
	for _, f := range doc.Fresh {
		if supportedSource(f.Source) {
			for v := range lockNumberTokens(f.Fact+" "+f.Value, numericFactRe) {
				allowed[numericValueKey(v)] = true
			}
		}
	}
	var issues []string
	for _, v := range sortedKeys(lockNumberTokens(draft, numericFactRe)) {
		if !allowed[numericValueKey(v)] {
			issues = append(issues, v)
		}
	}
	return issues
}

func containsExact(items []string, s string) bool {
	for _, v := range items {
		if v == s {
			return true
		}
	}
	return false
}

// Used after field repair as well as by the reviewer guard. Semantic course
// boundaries remain the reviewer's job; do not guess the transition from a regex.
func editorialWarnings(source string, draft remixDraft, ctx editorialContext) []string {
	warnings := publishFieldIssues(draft)
	issues := checkLockNumbersWithFacts(source, draft.ContinuousScript, string(ctx.Facts))
	if !issues.empty() {
		warnings = append(warnings, "数字待复核："+lockNumberSummary(issues))
	}
	if values := unsupportedNumericValues(source, draft.ContinuousScript, string(ctx.Facts)); len(values) > 0 {
		warnings = append(warnings, "未获事实支持的金额或利率："+strings.Join(values, "、"))
	}
	for i, topic := range draft.Topics {
		if i > 0 && strings.HasPrefix(topic, "#") && !strings.Contains(draft.ContinuousScript, strings.TrimPrefix(topic, "#")) {
			warnings = append(warnings, "话题词未在正文出现："+topic)
		}
	}
	if ctx.FactsRequired && strings.Contains(string(ctx.Facts), `"status":"不可用"`) {
		warnings = append(warnings, "事实结论不可用，关键数据待人工复核。")
	}
	if ctx.PlannedStructure {
		warnings = append(warnings, structureAdvisories(draft.ContinuousScript)...)
	}
	stats := selfCheckMeasure(source, draft.ContinuousScript)
	if stats.sourceRunes >= selfCheckMinSourceRunes && (!stats.passes(ctx.Limits) || stats.lenRatio > 1.2) {
		warnings = append(warnings, fmt.Sprintf("终稿复检：重合 %.2f%%，篇幅 %.2f 倍，请复核。", stats.overlap*100, stats.lenRatio))
	}
	return warnings
}
