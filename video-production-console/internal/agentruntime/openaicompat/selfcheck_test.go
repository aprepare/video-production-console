package openaicompat

import (
	"strings"
	"testing"
)

func TestSelfCheckMeasureCatchesRepunctuatedCopy(t *testing.T) {
	source := "全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱。"
	draft := "全国老百姓，存在银行里的钱，突然少了整整2万亿！而且不是买了房、不是炒个股，连最火的黄金都没接住这笔钱。"
	stats := selfCheckMeasure(source, draft)
	if stats.overlap < 0.8 {
		t.Fatalf("只改标点的照搬必须被抓住, overlap=%.2f", stats.overlap)
	}
	if len(stats.spans) == 0 || !strings.Contains(stats.spans[0], "2万亿") {
		t.Fatalf("spans=%v", stats.spans)
	}
}

func TestSelfCheckMeasureIgnoresAllowedPhrases(t *testing.T) {
	source := "去我主页橱窗看《财富觉醒方法论》，推广期间只要五块钱。"
	draft := "先点开我主页橱窗，找到《财富觉醒方法论》，推广期间只要五块钱就能进。"
	stats := selfCheckMeasure(source, draft)
	if stats.overlap != 0 {
		t.Fatalf("固定要素不算照搬: overlap=%.2f spans=%v", stats.overlap, stats.spans)
	}
}

// 固定住 run 级测试文本的刻度：干净稿必须真的通过自检（锁词政策事实除外），
// 照抄稿必须超硬性上限。文本改动导致刻度漂移时在这里先暴露。
func TestSelfCheckFixturesCalibration(t *testing.T) {
	limits := defaultSelfCheckLimits()
	clean := selfCheckMeasure(selfCheckTestSource, selfCheckCleanScript)
	if !clean.passes(limits) {
		t.Fatalf("干净稿必须通过: overlap=%.2f len_ratio=%.2f spans=%v", clean.overlap, clean.lenRatio, clean.spans)
	}
	if clean.overlap == 0 {
		t.Fatal("干净稿保留了锁词政策事实，重合不应为零")
	}
	copied := selfCheckMeasure(selfCheckTestSource, selfCheckTestSource+"去我主页橱窗看《财富觉醒方法论》。")
	if !copied.hardFails(limits) {
		t.Fatalf("整篇照抄必须超硬性上限: overlap=%.2f", copied.overlap)
	}
}

func TestBuildSelfCheckRepairPromptListsSpansAndLength(t *testing.T) {
	stats := selfCheckStats{
		overlap:  0.22,
		spans:    []string{"全国老百姓存在银行里的钱突然少了整整2万亿"},
		lenRatio: 0.61,
	}
	prompt := buildSelfCheckRepairPrompt(stats, defaultSelfCheckLimits())
	if !strings.Contains(prompt, "全国老百姓存在银行里的钱") {
		t.Fatalf("prompt=%s", prompt)
	}
	if !strings.Contains(prompt, "扩写三到四句") || !strings.Contains(prompt, "0.61") {
		t.Fatalf("篇幅不足必须要求补写: %s", prompt)
	}
	if !strings.Contains(prompt, "22%") {
		t.Fatalf("必须报出重合率: %s", prompt)
	}
}
