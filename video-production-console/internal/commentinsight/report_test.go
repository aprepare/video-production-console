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
	if err := validateEvidence(base, []string{"可见摘录"}); err != nil {
		t.Fatal(err)
	}
	badQuote := base
	badQuote.PainPoints = []PainPoint{{Point: "p", Mentions: 1, Quotes: []string{"原评论尾部但截断后未发送"}}}
	if err := validateEvidence(badQuote, []string{"原评论前半段"}); err == nil || !strings.Contains(err.Error(), "引用") {
		t.Fatalf("quote error = %v", err)
	}
	badMentions := base
	badMentions.PainPoints = []PainPoint{{Point: "p", Mentions: 6, Quotes: []string{"可见摘录"}}}
	if err := validateEvidence(badMentions, []string{"可见摘录"}); err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("mentions error = %v", err)
	}
	badCount := base
	badCount.Keywords = []Keyword{{Word: "x", Count: -1}}
	if err := validateEvidence(badCount, []string{"可见摘录"}); err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("count error = %v", err)
	}
}

func TestEvidenceRejectsDisplayMetadataAndCrossCommentQuotes(t *testing.T) {
	for _, quote := range []string{"[赞20]", "第一条\n[赞2] 第二条"} {
		report := Report{
			Summary: "ok", Sentiment: Sentiment{Positive: 100},
			PainPoints: []PainPoint{{Point: "p", Mentions: 1, Quotes: []string{quote}}},
		}
		if err := validateEvidence(report, []string{"第一条", "第二条"}); err == nil || !strings.Contains(err.Error(), "引用") {
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
	if err := validateEvidence(truncatedQuote, evidence); err == nil || !strings.Contains(err.Error(), "引用") {
		t.Fatalf("truncated quote error=%v", err)
	}
	report := Report{
		Summary: "ok", Sentiment: Sentiment{Positive: 100},
		PainPoints: []PainPoint{{Point: "p", Mentions: 2, Quotes: []string{"甲甲"}}},
	}
	if err := validateEvidence(report, evidence); err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("mentions error=%v", err)
	}
}

func TestFormatIntHandlesMinInt64(t *testing.T) {
	if got := formatInt(math.MinInt64); got != "-9223372036854775808" {
		t.Fatalf("formatInt = %q", got)
	}
}
