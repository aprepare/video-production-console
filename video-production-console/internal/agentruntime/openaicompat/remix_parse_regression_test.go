package openaicompat

import (
	"strings"
	"testing"
)

// 复刻 2026-08-26 sonnet 真实故障：JSON 围栏 + 正文里未转义的直引号 +
// 字段之间空行。旧逻辑先走有损抢救且边界不认空行，把 titles 整段塞进正文。
func TestParseRemixDraftRepairsQuotesBeforeLossyRecovery(t *testing.T) {
	script := `他来问我再存五年行不行，我说你先想想对"稳"的需求还成立吗，今天的"稳"和二十年前的"稳"已经不是一个东西了，静止本身就是代价。`
	raw := "```json\n{\n  \"continuous_script\": \"" + script + "\",\n\n  \"titles\": [\n    \"标题一\",\n    \"标题二\"\n  ],\n\n  \"short_titles\": [\"短一\"],\n\n  \"descriptions\": [\"描述一\"],\n\n  \"topics\": [\"#经济\"],\n\n  \"cta\": \"\"\n}\n```"
	draft, err := parseRemixDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(draft.ContinuousScript, "titles") || strings.Contains(draft.ContinuousScript, "{") {
		t.Fatalf("正文混进了 JSON 尾巴: %s", draft.ContinuousScript)
	}
	if !strings.Contains(draft.ContinuousScript, `对"稳"的需求`) || !strings.HasSuffix(strings.TrimSpace(draft.ContinuousScript), "静止本身就是代价。") {
		t.Fatalf("正文内容不完整: %s", draft.ContinuousScript)
	}
	if len(draft.Titles) != 2 || draft.Titles[0] != "标题一" {
		t.Fatalf("titles=%v", draft.Titles)
	}
}

func TestExtractBrokenJSONFieldToleratesBlankLineBoundary(t *testing.T) {
	text := "{\n  \"continuous_script\": \"这是一段用来验证边界的正文，长度需要超过四十个字，所以再多写一点凑数的口播内容放在这里。\",\n\n  \"titles\": [\"a\"],\n"
	got, ok := extractBrokenJSONStringField(text, "continuous_script")
	if !ok {
		t.Fatal("必须能截出正文")
	}
	if strings.Contains(got, "titles") || strings.HasSuffix(got, "\",") {
		t.Fatalf("边界没截干净: %q", got)
	}
	if !strings.HasSuffix(got, "放在这里。") {
		t.Fatalf("got=%q", got)
	}
}
