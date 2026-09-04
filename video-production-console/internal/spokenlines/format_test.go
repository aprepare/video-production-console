package spokenlines

import (
	"strings"
	"testing"
)

func TestFormatConvertsAndCapsLines(t *testing.T) {
	got, err := Format("二零零一年房价还便宜。百分之六十七的人看不懂。法拍房快堆到四十万套。去看《财富觉醒方法论》。")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("blank lines: %q", got)
	}
	lines := Lines(got)
	if len(lines) < 4 {
		t.Fatalf("lines=%v", lines)
	}
	joined := strings.Join(lines, "")
	for _, want := range []string{"2001年", "67%", "40万套", "财富觉醒方法论"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, lines)
		}
	}
	for _, line := range lines {
		if !strings.Contains(got, line+" \n") {
			t.Fatalf("line %q is missing the trailing space+newline", line)
		}
		if n := contentCount(line); n > MaxContentRunes && !strings.Contains(line, "财富觉醒方法论") {
			t.Fatalf("line %q has %d content runes", line, n)
		}
	}
}

func TestDigitizeKeepsApproximateQuantities(t *testing.T) {
	tests := []struct{ in, want string }{
		{"至少有几十万亿会搬出来", "至少有几十万亿会搬出来"},
		{"数百万人在等", "数百万人在等"},
		{"占了百分之十几", "占了百分之十几"},
		{"活期从百分之零点一降到百分之零点零五", "活期从0.1%降到0.05%"},
		{"接近四十万套", "接近40万套"},
	}
	for _, test := range tests {
		if got := digitize(test.in); got != test.want {
			t.Fatalf("digitize(%q)=%q, want %q", test.in, got, test.want)
		}
	}
}

func TestFormatRebalancesBadBreaks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			"行首的字并回上一行",
			"凑了30万首付\n的人",
			[]string{"凑了30万首付的人"},
		},
		{
			"孤字动词并回上一行",
			"去我主页橱窗\n看",
			[]string{"去我主页橱窗看"},
		},
		{
			"行尾的字拉回名词",
			"是那堆\n撑着人民币的\n东西",
			[]string{"是那堆", "撑着人民币的东西"},
		},
		{
			"合并超过9字时保持原样",
			"等这一轮彻底打开\n的那天",
			[]string{"等这一轮彻底打开", "的那天"},
		},
		{
			"正常两字行不乱并",
			"你说他们脑子\n比咱灵\n未必",
			[]string{"你说他们脑子", "比咱灵", "未必"},
		},
		{
			"句尾的字后的新句子不并",
			"发家的\n你说他们脑子",
			[]string{"发家的", "你说他们脑子"},
		},
		{
			"行首土地的地不当助词",
			"第二桌开张\n地和房子",
			[]string{"第二桌开张", "地和房子"},
		},
		{
			"行首得字表必须不并",
			"不能自己留\n得卖给国家",
			[]string{"不能自己留", "得卖给国家"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Format(test.in)
			if err != nil {
				t.Fatal(err)
			}
			lines := Lines(got)
			if len(lines) != len(test.want) {
				t.Fatalf("lines=%v, want %v", lines, test.want)
			}
			for i, want := range test.want {
				if lines[i] != want {
					t.Fatalf("line %d = %q, want %q", i, lines[i], want)
				}
			}
		})
	}
}

func TestDigitizeExamples(t *testing.T) {
	tests := []struct{ in, want string }{
		{"二零零一年", "2001年"},
		{"二〇二六年", "2026年"},
		{"百分之六十七", "67%"},
		{"百分之零点八", "0.8%"},
		{"百分之五十五", "55%"},
		{"四十万套", "40万套"},
		{"一百七十万亿", "170万亿"},
		{"两亿户", "2亿户"},
		{"八百二十亿", "820亿"},
	}
	for _, tt := range tests {
		if got := digitize(tt.in); got != tt.want {
			t.Fatalf("%q: got %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestAppendYearToBareYears(t *testing.T) {
	tests := []struct{ in, want string }{
		{"2026下半年，房价见底", "2026年下半年，房价见底"},
		{"到了2026，一切都变了", "到了2026年，一切都变了"},
		{"2026。", "2026年。"},
		{"从2021到2026", "从2021年到2026年"},
		{"2026年已经带了", "2026年已经带了"},
		{"这套房卖2000万", "这套房卖2000万"},
		{"押金2000块", "押金2000块"},
		{"小区有2000户", "小区有2000户"},
		{"涨了3000点", "涨了3000点"},
		{"利率是3.2026", "利率是3.2026"},
		{"编号12026", "编号12026"},
		{"占比2026%", "占比2026%"},
		{"iPhone2030", "iPhone2030"},
	}
	for _, tt := range tests {
		if got := digitize(tt.in); got != tt.want {
			t.Fatalf("%q: got %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatKeepsCourseNameAtomic(t *testing.T) {
	got, err := Format("现在就去主页橱窗看《财富觉醒方法论》再决定。")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range Lines(got) {
		if strings.Contains(line, "财富觉醒") && !strings.Contains(line, "财富觉醒方法论") {
			t.Fatalf("split course name: %q", line)
		}
		if strings.Contains(line, "财富觉醒方法论") {
			found = true
		}
	}
	if !found {
		t.Fatalf("course name missing: %q", got)
	}
}

func TestFormatIsIdempotentAndSpeechTextJoins(t *testing.T) {
	first, err := Format("百分之六十七的人还在等。")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Format(first)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("not idempotent:\n%s\n%s", first, second)
	}
	speech := SpeechText(first)
	if strings.Contains(speech, " ") || strings.Contains(speech, "\n") {
		t.Fatalf("speech text should be compact: %q", speech)
	}
	if !strings.Contains(speech, "67%") {
		t.Fatalf("speech=%q", speech)
	}
}

func TestFormatExtractsJSONAndRejectsEmpty(t *testing.T) {
	got, err := Format("```json\n{\"spoken_script\":\"第一句。\\n第二句。\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(SpeechText(got), "第一句") {
		t.Fatalf("got=%q", got)
	}
	if _, err := Format("   \n\n  "); err == nil {
		t.Fatal("expected empty error")
	}
}

func TestStripSpecialTokensRemovesModelEndMarkers(t *testing.T) {
	tests := []struct{ in, want string }{
		{"咱们接着盯 <|eos|>", "咱们接着盯"},
		{"我在课里等你 <|eos|>", "我在课里等你"},
		{"第一句</s>", "第一句"},
		{"<|endoftext|>", ""},
		{"[EOS]结尾", "结尾"},
		{"正常一句", "正常一句"},
	}
	for _, test := range tests {
		if got := StripSpecialTokens(test.in); got != test.want {
			t.Fatalf("StripSpecialTokens(%q)=%q, want %q", test.in, got, test.want)
		}
	}
}

// 模型偶发把英文填充词拼在正文开头（"The9月1日之后"，2026-08-27 实测）。
// 只滤 The/Sure 这类残渣；AI、iPhone 这种正经开头不能动。
func TestFormatStripsLeadingFillerWord(t *testing.T) {
	for _, raw := range []string{
		"The9月1日之后，你存银行的钱还是你的。",
		"The 9月1日之后，你存银行的钱还是你的。",
		"The\n9月1日之后，你存银行的钱还是你的。",
	} {
		formatted, err := Format(raw)
		if err != nil {
			t.Fatalf("Format(%q): %v", raw, err)
		}
		first := Lines(formatted)[0]
		if strings.Contains(first, "The") || !strings.HasPrefix(first, "9月") {
			t.Fatalf("filler word not stripped, first line = %q", first)
		}
	}
	kept, err := Format("AI正在抢走一批人的饭碗，这不是吓唬你。")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(Lines(kept)[0], "AI") {
		t.Fatalf("legit AI opening must survive, got %q", Lines(kept)[0])
	}
}

// 并行切块拼回后，残渣会出现在中段每块的开头行，同样要滤掉。
func TestFormatStripsFillerWordAtChunkBoundaries(t *testing.T) {
	raw := "活期0.05%\nThe利息收入跌到\n历史最低\nSure 可是此同时\n全国房贷余额"
	formatted, err := Format(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range Lines(formatted) {
		if strings.HasPrefix(line, "The") || strings.HasPrefix(line, "Sure") {
			t.Fatalf("filler leaked into middle line %q; all=%v", line, Lines(formatted))
		}
	}
	if joined := strings.Join(Lines(formatted), ""); !strings.Contains(joined, "利息收入跌到") || !strings.Contains(joined, "可是此同时") {
		t.Fatalf("content lost while stripping: %v", Lines(formatted))
	}
}

func TestFormatStripsTrailingEOSFromSpokenSheet(t *testing.T) {
	got, err := Format("后面的政策节奏\n和钱的去向\n咱们接着盯 <|eos|>")
	if err != nil {
		t.Fatal(err)
	}
	lines := Lines(got)
	if len(lines) == 0 {
		t.Fatal("empty lines")
	}
	last := lines[len(lines)-1]
	if last != "咱们接着盯" {
		t.Fatalf("last line=%q, want 咱们接着盯", last)
	}
	joined := strings.Join(lines, "")
	if strings.Contains(strings.ToLower(joined), "eos") || strings.Contains(joined, "<|") {
		t.Fatalf("end marker leaked into 口播稿: %v", lines)
	}
}
