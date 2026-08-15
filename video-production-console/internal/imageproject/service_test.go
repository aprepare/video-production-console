package imageproject

import (
	"strings"
	"testing"
)

func TestSplitScriptPreservesExactTextOrderAndRequestedCount(t *testing.T) {
	script := "第一段讲家庭存款。\n\n第二段讲房子卖不掉，现金流会断。第三段讲养老钱。"
	parts, err := SplitScript(script, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts=%d want=3: %#v", len(parts), parts)
	}
	joined := strings.Join(parts, "")
	want := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(script)
	got := strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(joined)
	if got != want {
		t.Fatalf("split rewrote or lost script\ngot=%q\nwant=%q", got, want)
	}
	if !strings.Contains(parts[1], "房子卖不掉") {
		t.Fatalf("semantic order changed: %#v", parts)
	}
}

func TestSplitScriptPreservesWhitespaceAndReturnsExactRequestedCount(t *testing.T) {
	script := "第一段。\n\n第二段很长，没有丢字。  第三段。"
	parts, err := SplitScript(script, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 5 {
		t.Fatalf("parts=%d want=5: %#v", len(parts), parts)
	}
	if got := strings.Join(parts, ""); got != script {
		t.Fatalf("split changed exact script\ngot=%q\nwant=%q", got, script)
	}
}

func TestSplitScriptRejectsImpossibleCounts(t *testing.T) {
	for _, count := range []int{0, 19} {
		if _, err := SplitScript("有效文案。", count); err == nil {
			t.Fatalf("count %d accepted", count)
		}
	}
	if _, err := SplitScript("短。", 3); err == nil {
		t.Fatal("count greater than available runes accepted")
	}
}

func TestBuildPromptUsesStyleRatioAndForbidsInventedText(t *testing.T) {
	prompt := BuildPrompt(PromptInput{
		SourceText: "退休前五年，先把现金流算清楚。",
		Ratio:      "3:4",
		Style:      "ledger_investigation",
	})
	for _, required := range []string{"退休前五年", "3:4", "账本", "中国", "画面文字规则", "主标题 6-14 字", "日期", "人物面部和核心主体"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing %q: %s", required, prompt)
		}
	}
	if strings.Contains(prompt, "不要生成可读文字") {
		t.Fatal("legacy no-readable-text prohibition must be absent")
	}
}
