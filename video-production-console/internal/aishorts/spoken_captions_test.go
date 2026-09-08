package aishorts

import (
	"strings"
	"testing"
)

func TestSpokenLineCaptionsAlignToCharTimes(t *testing.T) {
	ready := []Shot{
		{Narration: "这两年，钱不能再照着前些年那么花了。", Keywords: []ShotKeyword{{Text: "钱", Kind: "concept"}}},
		{Narration: "二零二三年的五十万，相当于零八年的八十万。"},
	}
	script := joinNarrations(ready)
	stream := contentRunesOf(script)
	charTimes := make([][2]float64, len(stream))
	for i := range charTimes {
		charTimes[i] = [2]float64{float64(i) * 0.25, float64(i+1) * 0.25}
	}
	total := float64(len(stream)) * 0.25
	// 模型把数字转成了阿拉伯数字，还漏了"这两年"这一行的一个字——对齐必须照样落位。
	lines := []string{"这两年", "钱不能再照着", "前些年那么花了", "2023年的50万", "相当于08年的80万"}
	caps := spokenLineCaptions(lines, ready, script, charTimes, total, true)
	if len(caps) != len(lines) {
		t.Fatalf("captions = %d, want %d: %+v", len(caps), len(lines), caps)
	}
	for i, c := range caps {
		if c.Text != lines[i] {
			t.Fatalf("caption %d text %q", i, c.Text)
		}
		if c.EndS <= c.StartS {
			t.Fatalf("caption %d has no duration: %+v", i, c)
		}
		if i > 0 && c.StartS < caps[i-1].EndS-1e-9 {
			t.Fatalf("caption %d overlaps previous: %+v %+v", i, caps[i-1], c)
		}
	}
	// "钱不能再照着" 从第 4 个实字（下标 3）开始。
	if got := caps[1].StartS; got != charTimes[3][0] {
		t.Fatalf("caption 2 starts at %.2f, want %.2f", got, charTimes[3][0])
	}
	// 第二镜的行从第二镜第一个实字开始。
	firstShotRunes := substantiveRunes(ready[0].Narration)
	if got := caps[3].StartS; got != charTimes[firstShotRunes][0] {
		t.Fatalf("caption 4 starts at %.2f, want %.2f", got, charTimes[firstShotRunes][0])
	}
	if caps[len(caps)-1].EndS != total {
		t.Fatalf("last caption ends at %.2f, want %.2f", caps[len(caps)-1].EndS, total)
	}
	if len(caps[1].Keywords) != 1 || caps[1].Keywords[0].Text != "钱" {
		t.Fatalf("keywords not taken from the owning shot: %+v", caps[1].Keywords)
	}
}

func TestSpokenLineCaptionsFillUnmatchedLine(t *testing.T) {
	ready := []Shot{{Narration: "手里有粮，心里才能不慌。"}}
	script := joinNarrations(ready)
	stream := contentRunesOf(script)
	charTimes := make([][2]float64, len(stream))
	for i := range charTimes {
		charTimes[i] = [2]float64{float64(i), float64(i + 1)}
	}
	// 中间一行被模型整句改写，对不上任何实字。
	lines := []string{"手里有粮", "囤好口粮", "心里才能不慌"}
	caps := spokenLineCaptions(lines, ready, script, charTimes, float64(len(stream)), false)
	if len(caps) != 3 {
		t.Fatalf("captions = %+v", caps)
	}
	for i, c := range caps {
		if c.EndS <= c.StartS {
			t.Fatalf("caption %d %q has no duration: %+v", i, c.Text, caps)
		}
		if i > 0 && c.StartS < caps[i-1].EndS-1e-9 {
			t.Fatalf("overlap at %d: %+v", i, caps)
		}
	}
	if joined := strings.Join([]string{caps[0].Text, caps[2].Text}, ""); joined != "手里有粮心里才能不慌" {
		t.Fatalf("texts changed: %+v", caps)
	}
}
