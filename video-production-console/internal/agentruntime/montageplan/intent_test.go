package montageplan

import (
	"strings"
	"testing"
)

func TestParseSRTSentencesAggregatesWordLevelEntries(t *testing.T) {
	srt := strings.Join([]string{
		"1",
		"00:00:00,000 --> 00:00:00,600",
		"真正",
		"",
		"2",
		"00:00:00,600 --> 00:00:01,400",
		"危险的是",
		"",
		"3",
		"00:00:01,400 --> 00:00:02,800",
		"家庭现金流变薄。",
		"",
		"4",
		"00:00:03,000 --> 00:00:06,200",
		"所以要提前准备应急资金。",
		"",
	}, "\r\n")
	sentences, err := parseSRTSentences(strings.NewReader(srt))
	if err != nil {
		t.Fatalf("parseSRTSentences: %v", err)
	}
	if len(sentences) != 2 {
		t.Fatalf("sentence count = %d, want 2: %#v", len(sentences), sentences)
	}
	first := sentences[0]
	if first.StartMS != 0 || first.EndMS != 2800 || first.Text != "真正危险的是家庭现金流变薄。" {
		t.Fatalf("first sentence = %#v", first)
	}
	second := sentences[1]
	if second.StartMS != 3000 || second.EndMS != 6200 || second.Text != "所以要提前准备应急资金。" {
		t.Fatalf("second sentence = %#v", second)
	}
}

func TestParseSRTSentencesTreatsLongCuesAsPhrases(t *testing.T) {
	srt := strings.Join([]string{
		"1",
		"00:00:00,000 --> 00:00:02,439",
		"听好了",
		"",
		"2",
		"00:00:02,439 --> 00:00:04,878",
		"下一波发财的名单正在重写",
		"",
		"3",
		"00:00:04,878 --> 00:00:07,317",
		"不是谁突然开窍了",
		"",
		"4",
		"00:00:21,888 --> 00:00:24,316",
		"百分之九十九还在看热闹",
		"",
		"5",
		"00:00:26,891 --> 00:00:29,505",
		"所以你点开这期就把心思收回来",
		"",
	}, "\n")
	sentences, err := parseSRTSentences(strings.NewReader(srt))
	if err != nil {
		t.Fatalf("parseSRTSentences: %v", err)
	}
	if len(sentences) != 5 {
		t.Fatalf("sentence count = %d, want 5: %#v", len(sentences), sentences)
	}
	if sentences[0].Text != "听好了" || sentences[4].Text != "所以你点开这期就把心思收回来" {
		t.Fatalf("sentences = %#v", sentences)
	}
	items, _ := selectHighlightCaptions(sentences, 331668, CaptionHighlightsOnly)
	if len(items) == 0 {
		t.Fatal("phrase-level SRT must yield highlight captions")
	}
}

func TestParseSRTSentencesFlushesTrailingTextWithoutBoundary(t *testing.T) {
	srt := strings.Join([]string{
		"1",
		"00:00:00,000 --> 00:00:01,000",
		"没有标点的",
		"",
		"2",
		"00:00:01,000 --> 00:00:02,000",
		"结尾",
		"",
	}, "\n")
	sentences, err := parseSRTSentences(strings.NewReader(srt))
	if err != nil {
		t.Fatalf("parseSRTSentences: %v", err)
	}
	if len(sentences) != 1 || sentences[0].Text != "没有标点的结尾" ||
		sentences[0].StartMS != 0 || sentences[0].EndMS != 2000 {
		t.Fatalf("sentences = %#v", sentences)
	}
}

func TestBuildVisualSegmentsGroupsSixToTenSecondsAndIsolatesHighlights(t *testing.T) {
	sentences := []TimedSentence{
		{StartMS: 0, EndMS: 2500, Text: "第一句普通描述。"},
		{StartMS: 2500, EndMS: 5000, Text: "第二句普通描述。"},
		{StartMS: 5000, EndMS: 7500, Text: "第三句普通描述。"},
		{StartMS: 7500, EndMS: 10000, Text: "第四句普通描述。"},
		{StartMS: 10000, EndMS: 12500, Text: "第五句普通描述。"},
		{StartMS: 12500, EndMS: 15000, Text: "2024年利率是百分之五。"},
		{StartMS: 15000, EndMS: 17500, Text: "但是风险仍然存在。"},
		{StartMS: 17500, EndMS: 20000, Text: "所以要记住这个结论。"},
	}
	segments := buildVisualSegments(sentences)
	if len(segments) != 5 {
		t.Fatalf("segment count = %d: %#v", len(segments), segments)
	}
	first := segments[0]
	if first.StartMS != 0 || first.EndMS != 7500 || first.Kind != "" {
		t.Fatalf("first segment = %#v", first)
	}
	if dur := first.EndMS - first.StartMS; dur < 6000 || dur > 10000 {
		t.Fatalf("first plain segment duration = %dms, want 6000-10000", dur)
	}
	second := segments[1]
	if second.StartMS != 7500 || second.EndMS != 12500 || second.Kind != "" {
		t.Fatalf("second segment = %#v", second)
	}
	number := segments[2]
	if number.StartMS != 12500 || number.EndMS != 15000 || number.Kind != CaptionNumber {
		t.Fatalf("number segment = %#v", number)
	}
	turning := segments[3]
	if turning.Kind != CaptionTurningPoint || turning.StartMS != 15000 {
		t.Fatalf("turning segment = %#v", turning)
	}
	conclusion := segments[4]
	if conclusion.Kind != CaptionConclusion || conclusion.EndMS != 20000 {
		t.Fatalf("conclusion segment = %#v", conclusion)
	}
}

func TestSelectHighlightCaptionsOffReturnsEmpty(t *testing.T) {
	sentences := []TimedSentence{
		{StartMS: 5000, EndMS: 8000, Text: "所以要记住这个结论。"},
	}
	items, warnings := selectHighlightCaptions(sentences, 60000, CaptionOff)
	if len(items) != 0 {
		t.Fatalf("off mode items = %#v", items)
	}
	if len(warnings) != 0 {
		t.Fatalf("off mode warnings = %#v", warnings)
	}
}

func TestHighlightCaptionsCoverFifteenToTwentyFivePercent(t *testing.T) {
	const narrationMS = int64(300000)
	sentences := make([]TimedSentence, 0, 30)
	for i := 0; i < 30; i++ {
		start := int64(5000 + i*9000)
		sentences = append(sentences, TimedSentence{
			StartMS: start,
			EndMS:   start + 3500,
			Text:    "所以第" + string(rune('a'+i)) + "点必须记住。",
		})
	}
	items, warnings := selectHighlightCaptions(sentences, narrationMS, CaptionHighlightsOnly)
	if len(warnings) != 0 {
		t.Fatalf("rich candidates must not warn: %#v", warnings)
	}
	if len(items) == 0 {
		t.Fatal("no captions selected")
	}
	coverage := intervalCoverage(items, narrationMS)
	if coverage < 0.15 || coverage > 0.25 {
		t.Fatalf("coverage = %.4f, want [0.15, 0.25]", coverage)
	}
	for i, item := range items {
		dur := item.EndS - item.StartS
		if dur < 2.0-1e-9 || dur > 4.0+1e-9 {
			t.Fatalf("item %d duration = %v, want 2-4s", i, dur)
		}
		if item.Kind != CaptionConclusion {
			t.Fatalf("item %d kind = %q", i, item.Kind)
		}
		if item.Style != "highlight_v1" {
			t.Fatalf("item %d style = %q", i, item.Style)
		}
		if i > 0 && items[i].StartS < items[i-1].EndS {
			t.Fatalf("items %d/%d overlap or are unsorted: %#v", i-1, i, items)
		}
	}
}

func TestHighlightsMergeOverlapsBeforeCoverage(t *testing.T) {
	const narrationMS = int64(60000)
	sentences := []TimedSentence{
		// The number sentence overlaps the conclusion; only the higher weight survives.
		{StartMS: 12000, EndMS: 15000, Text: "2024年利率是百分之五。"},
		{StartMS: 10000, EndMS: 13000, Text: "所以结论很重要。"},
		{StartMS: 30000, EndMS: 33000, Text: "但是风险仍然存在。"},
		{StartMS: 45000, EndMS: 48000, Text: "月供上涨了30个百分点。"},
	}
	items, warnings := selectHighlightCaptions(sentences, narrationMS, CaptionHighlightsOnly)
	if len(warnings) != 0 {
		t.Fatalf("coverage reaches target, warnings = %#v", warnings)
	}
	if len(items) != 3 {
		t.Fatalf("item count = %d: %#v", len(items), items)
	}
	if items[0].Kind != CaptionConclusion || items[0].StartS != 10.0 {
		t.Fatalf("first item = %#v", items[0])
	}
	if items[1].Kind != CaptionTurningPoint {
		t.Fatalf("second item = %#v", items[1])
	}
	if items[2].Kind != CaptionNumber || items[2].StartS != 45.0 {
		t.Fatalf("third item = %#v", items[2])
	}
	for _, item := range items {
		if item.Text == "2024年利率是百分之五。" {
			t.Fatalf("overlapping lower-weight candidate must be dropped: %#v", items)
		}
	}
}

func TestSelectHighlightCaptionsWarnsBelowTarget(t *testing.T) {
	sentences := []TimedSentence{
		{StartMS: 5000, EndMS: 8000, Text: "所以要记住这个结论。"},
	}
	items, warnings := selectHighlightCaptions(sentences, 300000, CaptionHighlightsOnly)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "caption_coverage_below_target") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestIntervalCoverageUsesUnionOfIntervals(t *testing.T) {
	items := []CaptionItem{
		{StartS: 0, EndS: 3},
		{StartS: 2, EndS: 5}, // overlaps the first: union adds 2s, not 3s
		{StartS: 10, EndS: 12},
	}
	got := intervalCoverage(items, 100000)
	if diff := got - 0.07; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("coverage = %v, want 0.07", got)
	}
	if intervalCoverage(nil, 100000) != 0 {
		t.Fatal("empty items must cover 0")
	}
	if intervalCoverage(items, 0) != 0 {
		t.Fatal("zero narration must cover 0")
	}
}
