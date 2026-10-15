package commentinsight

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"video-production-console/internal/monitorai"
)

var errInvalidSentiment = errors.New("评论洞察情绪比例无效")

type Sentiment struct {
	Positive int `json:"positive"`
	Neutral  int `json:"neutral"`
	Negative int `json:"negative"`
}

func (s *Sentiment) UnmarshalJSON(data []byte) error {
	var values struct {
		Positive float64 `json:"positive"`
		Neutral  float64 `json:"neutral"`
		Negative float64 `json:"negative"`
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if math.IsNaN(values.Positive) || math.IsInf(values.Positive, 0) ||
		math.IsNaN(values.Neutral) || math.IsInf(values.Neutral, 0) ||
		math.IsNaN(values.Negative) || math.IsInf(values.Negative, 0) ||
		values.Positive < 0 || values.Neutral < 0 || values.Negative < 0 {
		return errInvalidSentiment
	}
	*s = normalizeSentiment(values.Positive, values.Neutral, values.Negative)
	return nil
}

func normalizeSentiment(positive, neutral, negative float64) Sentiment {
	total := positive + neutral + negative
	if total == 0 {
		return Sentiment{}
	}
	values := []float64{positive, neutral, negative}
	const epsilon = 1e-9
	if total >= 0.99-epsilon && total <= 1.01+epsilon {
		for i := range values {
			values[i] *= 100
		}
		total *= 100
	}
	normalized := []int{
		int(math.Floor(values[0]/total*100 + epsilon)),
		int(math.Floor(values[1]/total*100 + epsilon)),
		int(math.Floor(values[2]/total*100 + epsilon)),
	}
	largest := 0
	for i := 1; i < len(values); i++ {
		if values[i] > values[largest] {
			largest = i
		}
	}
	normalized[largest] += 100 - normalized[0] - normalized[1] - normalized[2]
	return Sentiment{Positive: normalized[0], Neutral: normalized[1], Negative: normalized[2]}
}

type PainPoint struct {
	Point    string   `json:"point"`
	Detail   string   `json:"detail"`
	Mentions int      `json:"mentions"`
	Quotes   []string `json:"quotes"`
}

type Question struct {
	Question string   `json:"question"`
	Mentions int      `json:"mentions"`
	Quotes   []string `json:"quotes"`
}

type Need struct {
	Need   string   `json:"need"`
	Quotes []string `json:"quotes"`
}

type Opportunity struct {
	Title  string   `json:"title"`
	Why    string   `json:"why"`
	Action string   `json:"action"`
	Quotes []string `json:"quotes"`
}

type ContentIdea struct {
	Title string `json:"title"`
	Hook  string `json:"hook"`
	Angle string `json:"angle"`
}

type Keyword struct {
	Word  string `json:"word"`
	Count int    `json:"count"`
}

type Report struct {
	Summary       string        `json:"summary"`
	Sentiment     Sentiment     `json:"sentiment"`
	PainPoints    []PainPoint   `json:"pain_points"`
	Questions     []Question    `json:"questions"`
	Needs         []Need        `json:"needs"`
	Opportunities []Opportunity `json:"opportunities"`
	ContentIdeas  []ContentIdea `json:"content_ideas"`
	Objections    []string      `json:"objections"`
	Keywords      []Keyword     `json:"keywords"`
}

func parseReport(raw string) (Report, error) {
	var report Report
	if err := monitorai.UnmarshalJSON(strings.TrimSpace(raw), &report); err != nil {
		if errors.Is(err, errInvalidSentiment) {
			return Report{}, errInvalidSentiment
		}
		return Report{}, errors.New("评论洞察模型返回格式无效")
	}
	if strings.TrimSpace(report.Summary) == "" {
		return Report{}, errors.New("评论洞察模型返回格式无效")
	}
	normalizeReportArrays(&report)
	return report, nil
}

func normalizeReportArrays(report *Report) {
	if report.PainPoints == nil {
		report.PainPoints = []PainPoint{}
	}
	for i := range report.PainPoints {
		if report.PainPoints[i].Quotes == nil {
			report.PainPoints[i].Quotes = []string{}
		}
	}
	if report.Questions == nil {
		report.Questions = []Question{}
	}
	for i := range report.Questions {
		if report.Questions[i].Quotes == nil {
			report.Questions[i].Quotes = []string{}
		}
	}
	if report.Needs == nil {
		report.Needs = []Need{}
	}
	for i := range report.Needs {
		if report.Needs[i].Quotes == nil {
			report.Needs[i].Quotes = []string{}
		}
	}
	if report.Opportunities == nil {
		report.Opportunities = []Opportunity{}
	}
	for i := range report.Opportunities {
		if report.Opportunities[i].Quotes == nil {
			report.Opportunities[i].Quotes = []string{}
		}
	}
	if report.ContentIdeas == nil {
		report.ContentIdeas = []ContentIdea{}
	}
	if report.Objections == nil {
		report.Objections = []string{}
	}
	if report.Keywords == nil {
		report.Keywords = []Keyword{}
	}
}

func validateEvidence(report *Report, sentComments []string) error {
	if report == nil {
		return errors.New("评论洞察模型返回格式无效")
	}
	commentCount := len(sentComments)
	for _, item := range report.PainPoints {
		if item.Mentions < 0 || item.Mentions > commentCount {
			return errors.New("评论洞察提及数量无效")
		}
	}
	for _, item := range report.Questions {
		if item.Mentions < 0 || item.Mentions > commentCount {
			return errors.New("评论洞察提及数量无效")
		}
	}
	for _, item := range report.Keywords {
		if item.Count < 0 {
			return errors.New("评论洞察关键词数量无效")
		}
	}

	normalizedComments := make([]string, 0, len(sentComments))
	for _, comment := range sentComments {
		normalizedComments = append(normalizedComments, normalizeEvidenceText(comment))
	}

	painPoints := make([]PainPoint, 0, len(report.PainPoints))
	for _, item := range report.PainPoints {
		item.Quotes = filterQuotes(item.Quotes, normalizedComments)
		if len(item.Quotes) > 0 {
			painPoints = append(painPoints, item)
		}
	}
	report.PainPoints = painPoints

	questions := make([]Question, 0, len(report.Questions))
	for _, item := range report.Questions {
		item.Quotes = filterQuotes(item.Quotes, normalizedComments)
		if len(item.Quotes) > 0 {
			questions = append(questions, item)
		}
	}
	report.Questions = questions

	needs := make([]Need, 0, len(report.Needs))
	for _, item := range report.Needs {
		item.Quotes = filterQuotes(item.Quotes, normalizedComments)
		if len(item.Quotes) > 0 {
			needs = append(needs, item)
		}
	}
	report.Needs = needs

	opportunities := make([]Opportunity, 0, len(report.Opportunities))
	for _, item := range report.Opportunities {
		item.Quotes = filterQuotes(item.Quotes, normalizedComments)
		opportunities = append(opportunities, item)
	}
	report.Opportunities = opportunities

	if len(report.PainPoints) == 0 && len(report.Questions) == 0 && len(report.Needs) == 0 && len(report.Opportunities) == 0 {
		return errors.New("评论洞察没有可用结论")
	}
	return nil
}

var (
	likePrefixPattern = regexp.MustCompile(`^\s*\[赞\s*[0-9,.+\-万w]+\s*\]\s*`)
	ellipsisPattern   = regexp.MustCompile(`(?:\.{3,}|…+)`)
)

func filterQuotes(quotes []string, normalizedComments []string) []string {
	filtered := make([]string, 0, len(quotes))
	for _, quote := range quotes {
		quote = stripLikePrefix(quote)
		if quoteMatchesComment(quote, normalizedComments) {
			filtered = append(filtered, quote)
		}
	}
	return filtered
}

func stripLikePrefix(quote string) string {
	quote = strings.TrimSpace(norm.NFKC.String(quote))
	return strings.TrimSpace(likePrefixPattern.ReplaceAllString(quote, ""))
}

func quoteMatchesComment(quote string, normalizedComments []string) bool {
	parts := ellipsisPattern.Split(norm.NFKC.String(quote), -1)
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeEvidenceText(part)
		if part == "" {
			continue
		}
		if utf8.RuneCountInString(part) < 2 {
			return false
		}
		normalizedParts = append(normalizedParts, part)
	}
	if len(normalizedParts) == 0 {
		return false
	}
	for _, comment := range normalizedComments {
		remaining := comment
		matched := true
		for _, part := range normalizedParts {
			index := strings.Index(remaining, part)
			if index < 0 {
				matched = false
				break
			}
			remaining = remaining[index+len(part):]
		}
		if matched {
			return true
		}
	}
	return false
}

func normalizeEvidenceText(value string) string {
	runes := []rune(norm.NFKC.String(value))
	var b strings.Builder
	for i, r := range runes {
		if isKeycapBase(r) && ((i+1 < len(runes) && runes[i+1] == '\u20e3') ||
			(i+2 < len(runes) && (runes[i+1] == '\ufe0e' || runes[i+1] == '\ufe0f') && runes[i+2] == '\u20e3')) {
			continue
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) ||
			r == '\u200d' || r == '\u20e3' || r == '\ufe0e' || r == '\ufe0f' ||
			(r >= '\U000e0020' && r <= '\U000e007f') || (r >= '\U000e0100' && r <= '\U000e01ef') {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func isKeycapBase(r rune) bool {
	return r == '#' || r == '*' || (r >= '0' && r <= '9')
}
