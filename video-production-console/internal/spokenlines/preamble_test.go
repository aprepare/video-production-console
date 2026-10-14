package spokenlines

import "testing"

func TestStripPreambleDropsModelNarrationBeforeTheScript(t *testing.T) {
	source := "中国正在打造一个比房地产体量更大的资金蓄水池，一场跨度长达十年的财富大迁徙已经悄悄启动。"
	cases := map[string]struct{ reply, want string }{
		"same line as the script": {
			"正在按规则将连续文\n案切分为口播稿，不另\n写新内容。中国正在打\n造\n一个比房地产体量更大的",
			"中国正在打\n造\n一个比房地产体量更大的",
		},
		"own lines without punctuation": {
			"正在将定稿文案按口\n播规则拆分不另写\n中国正在打造\n一个比房地产\n",
			"中国正在打造\n一个比房地产\n",
		},
		"no preamble": {
			"中国正在打造\n一个比房地产\n",
			"中国正在打造\n一个比房地产\n",
		},
		"model rewrote the opening": {
			"正在切分。我国正在建设\n更大的资金池\n",
			"正在切分。我国正在建设\n更大的资金池\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := StripPreamble(tc.reply, source); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripPreambleKeepsDigitsThatOpenTheScript(t *testing.T) {
	source := "二零二六年下半年，银行存款利率还会继续往下走，普通家庭要提前准备。"
	reply := "按规则切分如下：\n2026年下半年\n银行存款利率\n"
	if got, want := StripPreamble(reply, source), "2026年下半年\n银行存款利率\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
