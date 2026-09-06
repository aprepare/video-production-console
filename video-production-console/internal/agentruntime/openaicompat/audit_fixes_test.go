package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditPublishingPreservesReviewedFields(t *testing.T) {
	draft := remixDraft{ContinuousScript: "把收支理清，去主页橱窗看课程。", CTA: "去主页橱窗看课程。",
		ShortTitles:  []string{"把家庭收支理清", "存款到期怎样办", "每月的钱去哪了"},
		Descriptions: []string{"先把家庭收支理清。", "再看存款到期安排。"}, Topics: []string{"#财经", "#收支", "#存款"}}
	pkg := publishingPackageFromDraft(draft, draft.ContinuousScript)
	if pkg["cta"] != draft.CTA {
		t.Fatalf("CTA lost: %v", pkg["cta"])
	}
	for i, s := range pkg["descriptions"].([]string) {
		if s != draft.Descriptions[i] {
			t.Fatalf("description altered: %q", s)
		}
	}
}

func TestAuditMissingFactsDoNotGateDraftOrReview(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("2026年，存款到期如何安排？"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"action": "remix.standard", "output_dir": dir, "inputs": []any{map[string]any{"type": "source_script", "role": "primary_source", "path": source}}})
	manifestPath := filepath.Join(dir, "task_manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	client := &sequenceClient{responses: []string{`{"search_queries":["存款"]}`, `{"source_facts":[]}`, `{"continuous_script":"2026年，先弄清存款到期后的选择。"}`, `{"verdict":"pass","issues":[]}`}}
	workflow := `{"nodes":[{"id":"facts","type":"agent"},{"id":"writer","type":"writer"},{"id":"reviewer","type":"reviewer"}],"edges":[["facts","writer"],["writer","reviewer"]]}`
	err := Run(Options{ManifestPath: manifestPath, OutputLastMessage: filepath.Join(dir, "last.json"), BaseURL: "http://example.invalid", APIKey: "test", Model: "claude-test", Client: client, WorkflowJSON: workflow})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 4 {
		t.Fatalf("facts attempts + writer + reviewer expected: calls=%d", len(client.requests))
	}
	if !strings.Contains(client.requests[0].Messages[1].Content, "核查日期") {
		t.Fatal("facts date missing")
	}
	last, _ := os.ReadFile(filepath.Join(dir, "last.json"))
	var result struct{ Status, Summary string }
	_ = json.Unmarshal(last, &result)
	if result.Status != "completed" {
		t.Fatalf("bad status: %s", last)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "continuous_script.txt")); err != nil || !strings.Contains(string(b), "先弄清") {
		t.Fatalf("draft lost: %v", err)
	}
	summary, _ := os.ReadFile(filepath.Join(dir, "intel_summary.json"))
	if !strings.Contains(string(summary), "事实核查未完成") {
		t.Fatalf("facts looks successful: %s", summary)
	}
	if _, err := os.Stat(filepath.Join(dir, "review.json")); err != nil {
		t.Fatal("review record missing")
	}
}

func TestAuditFactsArithmeticAndYearVariants(t *testing.T) {
	for _, equation := range []string{`"500000*0.95%=4750"`, `"200000*0.0005=100"`} {
		if !supportedSource(json.RawMessage(equation)) {
			t.Fatalf("correct arithmetic rejected: %s", equation)
		}
	}
	if supportedSource(json.RawMessage(`"500000*0.0095=950"`)) {
		t.Fatal("wrong equation accepted as evidence")
	}
	for _, source := range []string{"2006-2007年", "2006–2007年", "2006至2007年"} {
		if got := checkLockNumbers(source, "2006年至2007年"); !got.empty() {
			t.Fatalf("%s: %+v", source, got)
		}
	}
	if got := checkLockNumbers("利率0.95%", "利率10.95%"); len(got.Missing) == 0 {
		t.Fatal("substring accepted as same rate")
	}
}

func TestAuditAmmoStaysSmallAndOptional(t *testing.T) {
	items := make([]string, 100)
	for i := range items {
		items[i] = "重复意象"
	}
	raw, _ := json.Marshal(map[string]any{"banned_imagery": items, "phrase_swaps": items})
	var compact map[string][]any
	if err := json.Unmarshal([]byte(compactAmmo(string(raw))), &compact); err != nil {
		t.Fatal(err)
	}
	if len(compact["banned_imagery"]) != 6 || len(compact["phrase_swaps"]) != 3 {
		t.Fatalf("ammo not capped: %+v", compact)
	}
	if (flowSpec{Nodes: []flowNode{{ID: "ammo", Type: "agent"}}}).requiresFacts() {
		t.Fatal("optional ammo triggers fact gate")
	}
}

func TestAuditRhetoricalAndUnverifiedNumbersAreNotMandatory(t *testing.T) {
	source := "刷到就领先90%的人。2026年将有75万亿到期。"
	facts := `{"source_facts":[{"claim":"领先比例","value":"90%","status":"needs_verify","source":"无统计依据"},{"claim":"到期规模","value":"75万亿","status":"查不到","source":"未查到原始出处"}]}`
	if got := checkLockNumbersWithFacts(source, "2026年，存款到期后的选择值得关注。", facts); !got.empty() {
		t.Fatalf("unverified marketing numbers forced back: %+v", got)
	}
}

func TestAuditYearRangeAndISODateAreEquivalent(t *testing.T) {
	if got := checkLockNumbers("2006到2007年市场变化。", "2006年至2007年市场变化。"); !got.empty() {
		t.Fatalf("range: %+v", got)
	}
	facts := `{"source_facts":[{"claim":"历史低点","value":"2005-06-06","status":"成立","source":{"url":"https://www.sse.com.cn/example","date":"2005-06-06"}}]}`
	if got := checkLockNumbersWithFacts("当年市场变化。", "2005年市场变化。", facts); !got.empty() {
		t.Fatalf("date: %+v", got)
	}
}

func TestAuditNumericUnitsPreserveValueAndPrecision(t *testing.T) {
	if got := unsupportedNumericValues("50万、2400亿、0.95%", "50万元、2400亿元、0.95%", unavailableFacts); len(got) != 0 {
		t.Fatalf("equivalent units: %v", got)
	}
	if got := checkLockNumbers("存款173.59万亿", "存款173万亿"); len(got.Missing) == 0 {
		t.Fatal("rounded amount accepted")
	}
	if got := unsupportedNumericValues("50万元", "50亿元", unavailableFacts); len(got) == 0 {
		t.Fatal("scale change accepted")
	}
}

func TestAuditCorrectionRequiresValueNotExplanation(t *testing.T) {
	facts := `{"source_facts":[{"claim":"50万按0.95%计算一年利息","value":"950元","status":"纠错","latest":"4750元/年（本金乘年利率）","source":"500000*0.0095=4750"}]}`
	if got := checkLockNumbersWithFacts("50万，0.95%，利息950元。", "50万元按0.95%计算，年利息4750元。", facts); !got.empty() {
		t.Fatalf("explanation required verbatim: %+v", got)
	}
	if got := checkLockNumbersWithFacts("50万，0.95%，利息950元。", "50万元按0.95%计算，年利息950元。", facts); got.empty() {
		t.Fatal("wrong interest kept")
	}
}

func TestAuditFactsRejectSearchPlansAndEmptyConclusions(t *testing.T) {
	for _, raw := range []string{`{"search_queries":["存款"]}`, `{"source_facts":[]}`, `{"source_facts":[{}]}`} {
		if got := normalizeFacts(raw); got != unavailableFacts {
			t.Fatalf("invalid facts accepted: %s", got)
		}
	}
	if facts := checkedFacts(`{"source_facts":[{"value":"160万亿","status":"纠错","source":"央行相关报告"}]}`); len(facts) != 0 {
		t.Fatal("vague source treated as checked")
	}
}

func TestAuditFactsCannotRetireEvidenceWithUnverifiedConclusion(t *testing.T) {
	facts := `{"source_facts":[{"value":"0.95%","latest":"1.25%","status":"纠错","source":"我觉得"}]}`
	if got := unsupportedNumericValues("利率0.95%", "利率1.25%", facts); len(got) == 0 {
		t.Fatal("unverified correction accepted")
	}
}

func TestAuditCoursePolicyHasOneConsistentContract(t *testing.T) {
	if strings.Contains(SharedEditorialPolicy, "五块钱3～5次") {
		t.Fatal("old repeated price quota remains")
	}
	if !json.Valid([]byte(unavailableFacts)) {
		t.Fatal("invalid fallback")
	}
}

func TestAuditUnfixedApprovedCorrectionCannotComplete(t *testing.T) {
	dir := t.TempDir()
	facts := `{"source_facts":[{"claim":"年利息","value":"200元","status":"纠错","latest":"100元","source":"200000*0.0005=100"}]}`
	if err := os.WriteFile(filepath.Join(dir, "facts_research.json"), []byte(facts), 0600); err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("存款发生变化，先看钱往哪里走及利息如何计算。", 18) + "本金20万元，年利息200元。"
	script := strings.Repeat("把不同去处的条件想清楚，再决定家里的积蓄如何安排。", 18) + "本金20万元，年利息200元。"
	raw, _ := json.Marshal(remixDraft{ContinuousScript: script})
	_, _, _, err := selfCheckRemixRewrite(&sequenceClient{responses: []string{string(raw)}}, "m", "", "system", "user", source, string(raw), dir, defaultSelfCheckLimits())
	if err == nil {
		t.Fatal("known wrong interest passed after failed repair")
	}
}
