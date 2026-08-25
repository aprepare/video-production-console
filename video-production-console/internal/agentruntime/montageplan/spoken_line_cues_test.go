package montageplan

import (
	"testing"

	"video-production-console/internal/spokenlines"
)

func TestLineLevelSRTDetection(t *testing.T) {
	lineCues := []TimedSentence{
		{StartMS: 0, EndMS: 1500, Text: "全国法拍房挂牌，"},
		{StartMS: 1500, EndMS: 2900, Text: "已经堆到40万套。"},
		{StartMS: 2900, EndMS: 4100, Text: "67%的人在等。"},
	}
	if !lineLevelSRT(lineCues) {
		t.Fatal("口播稿行级SRT必须被识别为line-level")
	}
	digitHeavy := []TimedSentence{
		{StartMS: 0, EndMS: 1800, Text: "从1.45%直接降到0.95%"},
		{StartMS: 1800, EndMS: 3200, Text: "2023年存10万三年定期"},
		{StartMS: 3200, EndMS: 4700, Text: "手里10万、50万、100万的家庭"},
		{StartMS: 4700, EndMS: 6100, Text: "这条路到2008年走到了头"},
	}
	if !lineLevelSRT(digitHeavy) {
		t.Fatal("数字不占口播预算的行级SRT必须仍走一对一上屏，不能整条粘合重切")
	}
	wordCues := []TimedSentence{
		{StartMS: 0, EndMS: 300, Text: "全"}, {StartMS: 300, EndMS: 600, Text: "国"},
		{StartMS: 600, EndMS: 900, Text: "法"}, {StartMS: 900, EndMS: 1200, Text: "拍"},
	}
	if lineLevelSRT(wordCues) {
		t.Fatal("逐字SRT不能走行级路径")
	}
	sentenceCues := []TimedSentence{
		{StartMS: 0, EndMS: 3500, Text: "所以家庭现金流必须留出安全垫。"},
		{StartMS: 3500, EndMS: 7000, Text: "但是很多人忽视了利率变化带来的影响。"},
	}
	if lineLevelSRT(sentenceCues) {
		t.Fatal("旧式整句SRT不能走行级路径")
	}
	if lineLevelSRT(nil) {
		t.Fatal("空cue不能走行级路径")
	}
}

func TestSpokenDisplayRunesStripsEOSMarker(t *testing.T) {
	got := string(spokenDisplayRunes("咱们接着盯 <|eos|>"))
	if got != "咱们接着盯" {
		t.Fatalf("spokenDisplayRunes leaked end marker: %q", got)
	}
	items, _ := spokenCaptionsFromLineCues([]TimedSentence{
		{StartMS: 0, EndMS: 1500, Text: "咱们接着盯 <|eos|>"},
	}, 2000, nil)
	if len(items) != 1 || items[0].Text != "咱们接着盯" {
		t.Fatalf("on-screen caption leaked end marker: %#v", items)
	}
}

func TestSpokenCaptionsFromLineCuesKeepsLineBoundaries(t *testing.T) {
	cues := []TimedSentence{
		{StartMS: 0, EndMS: 1500, Text: "全国法拍房挂牌，"},
		{StartMS: 1500, EndMS: 2900, Text: "已经堆到40万套。"},
		{StartMS: 3100, EndMS: 4300, Text: "67%的人在等。"},
	}
	items, warnings := spokenCaptionsFromLineCues(cues, 5000, nil)
	if len(items) != 3 {
		t.Fatalf("每条cue必须一对一成一条字幕，got %d", len(items))
	}
	if items[0].Text != "全国法拍房挂牌" || items[1].Text != "已经堆到40万套" || items[2].Text != "67%的人在等" {
		t.Fatalf("行文本必须原样去标点保留: %q %q %q", items[0].Text, items[1].Text, items[2].Text)
	}
	// The 200ms gap between cue 2 and 3 snaps shut so the track reads
	// continuously.
	if items[1].EndS != items[2].StartS {
		t.Fatalf("小间隙必须闭合: end=%v nextStart=%v", items[1].EndS, items[2].StartS)
	}
	for _, item := range items {
		if item.Style != "spoken_v1" || item.Kind != CaptionSpokenLine || item.Intro != "none" {
			t.Fatalf("字幕样式字段不对: %#v", item)
		}
	}
	if len(warnings) != 1 || warnings[0] != "spoken_captions_line_srt: 口播稿行级SRT一对一上屏" {
		t.Fatalf("warnings = %v", warnings)
	}
	// Local fallback keyword spans still fire without a keyword doc.
	if len(items[1].Spans) == 0 {
		t.Fatalf("数字必须有本地回落高亮: %#v", items[1])
	}
}

func TestSpokenDisplayRunesKeepsDecimalPoint(t *testing.T) {
	tests := []struct{ in, want string }{
		{"降到了0.05%。", "降到了0.05%"},
		{"从0.1%，", "从0.1%"},
		{"就这样结束了。", "就这样结束了"},
		{"3.5万亿呢？", "3.5万亿呢"},
	}
	for _, test := range tests {
		if got := string(spokenDisplayRunes(test.in)); got != test.want {
			t.Fatalf("spokenDisplayRunes(%q)=%q, want %q", test.in, got, test.want)
		}
	}
}

func TestNumericKeywordSpansCoverDecimals(t *testing.T) {
	runes := []rune("降到了0.05%")
	spans := numericKeywordSpans(runes)
	if len(spans) != 1 {
		t.Fatalf("spans=%#v", spans)
	}
	if got := string(runes[spans[0].Start:spans[0].End]); got != "0.05%" {
		t.Fatalf("span text=%q, want 0.05%%", got)
	}
}

func TestLineKeywordIndexPaintsWarningRedAndNumberGold(t *testing.T) {
	doc := &spokenlines.KeywordDoc{
		SchemaVersion: spokenlines.KeywordSchemaVersion,
		Lines: []spokenlines.KeywordLine{
			{Line: "全国法拍房挂牌", Keywords: []spokenlines.Keyword{{Text: "法拍房", Kind: spokenlines.KeywordKindWarning}}},
			{Line: "已经堆到40万套", Keywords: []spokenlines.Keyword{{Text: "40万套", Kind: spokenlines.KeywordKindNumber}}},
			{Line: "很多人还在观望", Keywords: []spokenlines.Keyword{}},
		},
	}
	index := buildLineKeywordIndex(doc)

	spans := index.spansFor([]rune("全国法拍房挂牌"))
	if len(spans) != 1 || spans[0].Style != spokenWarningStyle || spans[0].Start != 2 || spans[0].End != 5 {
		t.Fatalf("警示词必须红色样式: %#v", spans)
	}
	spans = index.spansFor([]rune("已经堆到40万套"))
	if len(spans) != 1 || spans[0].Style != spokenKeywordStyle || spans[0].Start != 4 || spans[0].End != 8 {
		t.Fatalf("数字必须金色样式: %#v", spans)
	}
	// The model saw this line and marked nothing: stay plain, no local
	// fallback.
	if spans := index.spansFor([]rune("很多人还在观望")); spans != nil {
		t.Fatalf("模型明确不标的行必须保持素净: %#v", spans)
	}
	// A line the model never saw falls back to the local list.
	if spans := index.spansFor([]rune("库存高达67%")); len(spans) == 0 {
		t.Fatal("模型没见过的行必须回落本地词表")
	}
}

func TestLineKeywordIndexNilFallsBackToLocalList(t *testing.T) {
	var index *lineKeywordIndex
	if spans := index.spansFor([]rune("已经堆到40万套")); len(spans) == 0 {
		t.Fatal("无关键词资产时必须回落本地词表")
	}
}
