package publishing

import (
	"strings"
	"testing"
)

const remixCopyJSON = `{
  "continuous_script": "又一批人要发财了。人民币第三次换锚已经开始。",
  "titles": ["人民币第三次换锚来了", "下一批先富的人在哪", "旧锚退潮钱去哪", "一百七十万亿在找出口", "第三个锚先不说完", "窗口不会一直开着", "看懂资金方向先上车", "别只盯着工资存款"],
  "short_titles": ["第三次换锚来了", "钱会流向哪里", "下一批赢家是谁", "窗口不会等人", "现在就上车吧"],
  "descriptions": ["前两次换锚分别推高了外贸和房子。", "看懂资金上游的人先拿位置。", "答案先留着，窗口不会一直开着。"],
  "topics": ["#经济", "#思维认知", "#干货分享"],
  "cta": "关掉干扰，现在就去主页橱窗看《财富觉醒方法论》。"
}`

func TestUnwrapContinuousScriptLeavesPlainText(t *testing.T) {
	got, pkg, fromJSON, err := UnwrapContinuousScript("八月这一波要发财的人")
	if err != nil {
		t.Fatal(err)
	}
	if fromJSON || pkg != nil {
		t.Fatalf("plain text was treated as JSON: fromJSON=%v pkg=%v", fromJSON, pkg)
	}
	if got != "八月这一波要发财的人" {
		t.Fatalf("script=%q", got)
	}
}

func TestUnwrapContinuousScriptReadsRemixJSON(t *testing.T) {
	got, pkg, fromJSON, err := UnwrapContinuousScript("```json\n" + remixCopyJSON + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !fromJSON || pkg == nil {
		t.Fatal("remix JSON was not unwrapped")
	}
	if got != "又一批人要发财了。人民币第三次换锚已经开始。" {
		t.Fatalf("script=%q", got)
	}
	if strings.Contains(got, `"titles"`) || strings.Contains(got, "short_titles") {
		t.Fatalf("script still contains JSON fields: %q", got)
	}
	if len(pkg.Titles) != 8 || pkg.ShortTitles[0] != "第三次换锚来了" || pkg.CTA == "" {
		t.Fatalf("package=%+v", pkg)
	}
}

func TestUnwrapContinuousScriptRejectsJSONWithoutScript(t *testing.T) {
	_, _, _, err := UnwrapContinuousScript(`{"titles":["人民币第三次换锚来了"],"cta":"上车"}`)
	if err == nil {
		t.Fatal("JSON without continuous_script must be rejected")
	}
}
