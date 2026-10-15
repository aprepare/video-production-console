package commentinsight

import (
	"encoding/json"
	"errors"
	"strings"
)

type Sentiment struct {
	Positive int `json:"positive"`
	Neutral  int `json:"neutral"`
	Negative int `json:"negative"`
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
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	var report Report
	if err := json.Unmarshal([]byte(raw), &report); err != nil || strings.TrimSpace(report.Summary) == "" {
		return Report{}, errors.New("评论洞察模型返回格式无效")
	}
	if report.Sentiment.Positive < 0 || report.Sentiment.Neutral < 0 || report.Sentiment.Negative < 0 ||
		report.Sentiment.Positive+report.Sentiment.Neutral+report.Sentiment.Negative != 100 {
		return Report{}, errors.New("评论洞察情绪比例无效")
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

func validateEvidence(report Report, sentComments []string) error {
	commentCount := len(sentComments)
	all := make([][]string, 0, len(report.PainPoints)+len(report.Questions)+len(report.Needs)+len(report.Opportunities))
	for _, item := range report.PainPoints {
		if item.Mentions < 0 || item.Mentions > commentCount {
			return errors.New("评论洞察提及数量无效")
		}
		all = append(all, item.Quotes)
	}
	for _, item := range report.Questions {
		if item.Mentions < 0 || item.Mentions > commentCount {
			return errors.New("评论洞察提及数量无效")
		}
		all = append(all, item.Quotes)
	}
	for _, item := range report.Needs {
		all = append(all, item.Quotes)
	}
	for _, item := range report.Opportunities {
		all = append(all, item.Quotes)
	}
	for _, item := range report.Keywords {
		if item.Count < 0 {
			return errors.New("评论洞察关键词数量无效")
		}
	}
	for _, quotes := range all {
		if len(quotes) == 0 {
			return errors.New("评论洞察结论缺少引用证据")
		}
		for _, quote := range quotes {
			quote = strings.TrimSpace(quote)
			found := false
			if quote != "" {
				for _, comment := range sentComments {
					if strings.Contains(comment, quote) {
						found = true
						break
					}
				}
			}
			if !found {
				return errors.New("评论洞察引用不是原评论摘录")
			}
		}
	}
	return nil
}
