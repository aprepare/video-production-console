package commentinsight

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"video-production-console/internal/platformapi"
)

func TestParseReportNormalizesMissingArrays(t *testing.T) {
	report, err := parseReport(`{"summary":"ok","sentiment":{"positive":100,"neutral":0,"negative":0}}`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"pain_points", "questions", "needs", "opportunities", "content_ideas", "objections", "keywords"} {
		if !strings.Contains(string(encoded), `"`+field+`":[]`) {
			t.Errorf("%s is not []: %s", field, encoded)
		}
	}
}

func TestUsableCommentsSortsBeforeTakingTop300(t *testing.T) {
	comments := make([]platformapi.Comment, 301)
	for i := range comments {
		comments[i] = platformapi.Comment{Text: "普通评论", Likes: int64(i)}
	}
	comments[300] = platformapi.Comment{Text: "最高赞评论", Likes: 10000}
	got := usableComments(comments, 300)
	if len(got) != 300 || got[0].Text != "最高赞评论" || got[len(got)-1].Likes != 1 {
		t.Fatalf("top comments boundary: first=%+v last=%+v len=%d", got[0], got[len(got)-1], len(got))
	}
}

func TestEvidenceMustComeFromTextActuallySentToModelAndCountsAreBounded(t *testing.T) {
	base := Report{
		Summary: "ok", Sentiment: Sentiment{Positive: 100},
		PainPoints: []PainPoint{{Point: "p", Mentions: 1, Quotes: []string{"可见摘录"}}},
		Questions:  []Question{}, Needs: []Need{}, Opportunities: []Opportunity{},
		ContentIdeas: []ContentIdea{}, Objections: []string{}, Keywords: []Keyword{},
	}
	if err := validateEvidence(&base, []string{"可见摘录"}); err != nil {
		t.Fatal(err)
	}
	badQuote := base
	badQuote.PainPoints = []PainPoint{{Point: "p", Mentions: 1, Quotes: []string{"原评论尾部但截断后未发送"}}}
	if err := validateEvidence(&badQuote, []string{"原评论前半段"}); err == nil || !strings.Contains(err.Error(), "结论") {
		t.Fatalf("quote error = %v", err)
	}
	badMentions := base
	badMentions.PainPoints = []PainPoint{{Point: "p", Mentions: 6, Quotes: []string{"可见摘录"}}}
	if err := validateEvidence(&badMentions, []string{"可见摘录"}); err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("mentions error = %v", err)
	}
	badCount := base
	badCount.Keywords = []Keyword{{Word: "x", Count: -1}}
	if err := validateEvidence(&badCount, []string{"可见摘录"}); err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("count error = %v", err)
	}
}

func TestEvidenceRejectsDisplayMetadataAndCrossCommentQuotes(t *testing.T) {
	for _, quote := range []string{"[赞20]", "第一条\n[赞2] 第二条"} {
		report := Report{
			Summary: "ok", Sentiment: Sentiment{Positive: 100},
			PainPoints: []PainPoint{{Point: "p", Mentions: 1, Quotes: []string{quote}}},
		}
		if err := validateEvidence(&report, []string{"第一条", "第二条"}); err == nil || !strings.Contains(err.Error(), "结论") {
			t.Errorf("quote=%q err=%v", quote, err)
		}
	}
}

func TestFormatCommentsReturnsOnlyActuallySentOriginalFragments(t *testing.T) {
	text, evidence := formatComments([]platformapi.Comment{
		{Text: "甲甲甲甲甲", Likes: 20},
		{Text: "乙乙乙", Likes: 2},
	}, 8)
	if text != "[赞20] 甲甲" || len([]rune(text)) != 8 {
		t.Fatalf("text=%q runes=%d", text, len([]rune(text)))
	}
	if len(evidence) != 1 || evidence[0] != "甲甲" {
		t.Fatalf("evidence=%#v", evidence)
	}
	truncatedQuote := Report{
		Summary: "ok", Sentiment: Sentiment{Positive: 100},
		PainPoints: []PainPoint{{Point: "p", Mentions: 1, Quotes: []string{"甲甲甲"}}},
	}
	if err := validateEvidence(&truncatedQuote, evidence); err == nil || !strings.Contains(err.Error(), "结论") {
		t.Fatalf("truncated quote error=%v", err)
	}
	report := Report{
		Summary: "ok", Sentiment: Sentiment{Positive: 100},
		PainPoints: []PainPoint{{Point: "p", Mentions: 2, Quotes: []string{"甲甲"}}},
	}
	if err := validateEvidence(&report, evidence); err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("mentions error=%v", err)
	}
}

func TestFormatIntHandlesMinInt64(t *testing.T) {
	if got := formatInt(math.MinInt64); got != "-9223372036854775808" {
		t.Fatalf("formatInt = %q", got)
	}
}

func TestParseReportNormalizesSentiment(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Sentiment
	}{
		{
			name: "integer weights",
			raw:  `{"summary":"ok","sentiment":{"positive":60,"neutral":25,"negative":16},"opportunities":[{"title":"机会"}]}`,
			want: Sentiment{Positive: 61, Neutral: 24, Negative: 15},
		},
		{
			name: "decimal ratios",
			raw:  `{"summary":"ok","sentiment":{"positive":0.6,"neutral":0.25,"negative":0.15},"opportunities":[{"title":"机会"}]}`,
			want: Sentiment{Positive: 60, Neutral: 25, Negative: 15},
		},
		{
			name: "decimal ratio integer boundary",
			raw:  `{"summary":"ok","sentiment":{"positive":0.58,"neutral":0.29,"negative":0.13},"opportunities":[{"title":"机会"}]}`,
			want: Sentiment{Positive: 58, Neutral: 29, Negative: 13},
		},
		{
			name: "ratio lower tolerance boundary",
			raw:  `{"summary":"ok","sentiment":{"positive":0.57,"neutral":0.29,"negative":0.13},"opportunities":[{"title":"机会"}]}`,
			want: Sentiment{Positive: 58, Neutral: 29, Negative: 13},
		},
		{
			name: "ratio upper tolerance boundary",
			raw:  `{"summary":"ok","sentiment":{"positive":0.59,"neutral":0.29,"negative":0.13},"opportunities":[{"title":"机会"}]}`,
			want: Sentiment{Positive: 60, Neutral: 28, Negative: 12},
		},
		{
			name: "all zero",
			raw:  `{"summary":"ok","sentiment":{"positive":0,"neutral":0,"negative":0},"opportunities":[{"title":"机会"}]}`,
			want: Sentiment{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := parseReport(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if report.Sentiment != tt.want {
				t.Fatalf("sentiment = %+v, want %+v", report.Sentiment, tt.want)
			}
		})
	}
}

func TestNormalizeEvidenceTextRemovesCompositeEmoji(t *testing.T) {
	tests := []string{
		"停车1️⃣太难",
		"停车👨‍👩‍👧‍👦太难",
		"停车🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f太难",
	}
	for _, input := range tests {
		if got := normalizeEvidenceText(input); got != "停车太难" {
			t.Errorf("normalizeEvidenceText(%q) = %q", input, got)
		}
	}
}

func TestParseReportRejectsNegativeSentimentAsSentimentError(t *testing.T) {
	_, err := parseReport(`{"summary":"ok","sentiment":{"positive":-1,"neutral":1,"negative":0},"opportunities":[{"title":"机会"}]}`)
	if err == nil || !strings.Contains(err.Error(), "情绪比例") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseReportAcceptsProseAndJSONFence(t *testing.T) {
	report, err := parseReport("模型结果如下：\n```json\n{\"summary\":\"ok\",\"sentiment\":{\"positive\":1,\"neutral\":0,\"negative\":0}}\n```\n请查收")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary != "ok" || report.Sentiment.Positive != 100 {
		t.Fatalf("report = %+v", report)
	}
}

func TestEvidenceNormalizesQuotesAndStripsLikePrefix(t *testing.T) {
	report := Report{
		Summary: "ok",
		PainPoints: []PainPoint{{
			Point:  "停车",
			Quotes: []string{"[赞１２] Ａ区，停车🚗太 难!"},
		}},
		Questions: []Question{{
			Question: "营业时间",
			Quotes:   []string{"晚上…还营业吗"},
		}},
		Needs: []Need{{Need: "停车指引", Quotes: []string{"A区...太难"}}},
	}
	comments := []string{"a区停车太难", "晚上十点以后还营业吗"}
	if err := validateEvidence(&report, comments); err != nil {
		t.Fatal(err)
	}
	if got := report.PainPoints[0].Quotes; len(got) != 1 || got[0] != "A区,停车🚗太 难!" {
		t.Fatalf("stripped quotes = %#v", got)
	}
}

func TestEvidenceDropsInventedQuotesAndUnsupportedConclusions(t *testing.T) {
	report := Report{
		Summary: "ok",
		PainPoints: []PainPoint{
			{Point: "保留", Quotes: []string{"确实太贵", "从未出现的引用"}},
			{Point: "删除", Quotes: []string{"也是编造的"}},
		},
		Questions: []Question{{Question: "删除空引用", Quotes: []string{}}},
		Needs:     []Need{{Need: "删除坏引用", Quotes: []string{"不存在"}}},
		Opportunities: []Opportunity{{
			Title: "无引用机会仍保留",
		}},
	}
	if err := validateEvidence(&report, []string{"确实太贵"}); err != nil {
		t.Fatal(err)
	}
	if len(report.PainPoints) != 1 || len(report.PainPoints[0].Quotes) != 1 || report.PainPoints[0].Quotes[0] != "确实太贵" {
		t.Fatalf("pain_points = %#v", report.PainPoints)
	}
	if len(report.Questions) != 0 || len(report.Needs) != 0 {
		t.Fatalf("questions=%#v needs=%#v", report.Questions, report.Needs)
	}
	if len(report.Opportunities) != 1 || len(report.Opportunities[0].Quotes) != 0 {
		t.Fatalf("opportunities = %#v", report.Opportunities)
	}
}

func TestEvidenceRejectsCrossCommentEllipsisJoinWithoutDiscardingOtherFindings(t *testing.T) {
	report := Report{
		Summary: "ok",
		PainPoints: []PainPoint{
			{Point: "跨评论拼接", Quotes: []string{"第一段…第二段"}},
			{Point: "有效", Quotes: []string{"真实原话"}},
		},
	}
	if err := validateEvidence(&report, []string{"第一段", "第二段", "这里有真实原话"}); err != nil {
		t.Fatal(err)
	}
	if len(report.PainPoints) != 1 || report.PainPoints[0].Point != "有效" {
		t.Fatalf("pain_points = %#v", report.PainPoints)
	}
}

func TestEvidenceFailsOnlyWhenAllFourMainConclusionGroupsAreEmpty(t *testing.T) {
	report := Report{
		Summary:      "ok",
		ContentIdeas: []ContentIdea{{Title: "内容选题不算主结论"}},
	}
	if err := validateEvidence(&report, []string{"评论"}); err == nil || !strings.Contains(err.Error(), "结论") {
		t.Fatalf("error = %v", err)
	}

	report.Opportunities = []Opportunity{{Title: "综合机会"}}
	if err := validateEvidence(&report, []string{"评论"}); err != nil {
		t.Fatalf("opportunity without quotes: %v", err)
	}
}
