package videoqc

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"video-production-console/internal/store"
)

type Check struct {
	store.VideoQCCheck
	Report *Report `json:"report"`
	Frames []Frame `json:"frames"`
}

type Issue struct {
	ShotPosition int     `json:"shot_position,omitempty"`
	FrameIndex   int     `json:"frame_index"`
	TimeS        float64 `json:"time_s"`
	Category     string  `json:"category"`
	Severity     string  `json:"severity"`
	Description  string  `json:"description"`
	Suggestion   string  `json:"suggestion"`
}

type Report struct {
	Summary string   `json:"summary"`
	Issues  []Issue  `json:"issues"`
	Checked []string `json:"checked"`
}

type Frame struct {
	Index int     `json:"index"`
	TimeS float64 `json:"time_s"`
	URL   string  `json:"url"`
	JPEG  []byte  `json:"-"`
}

type modelResult struct {
	Verdict string `json:"verdict"`
	Score   int    `json:"score"`
	Report
}

var categories = []string{"hands", "body", "face", "product", "text", "artifact", "watermark", "quality"}

func parseReport(content string, frames []Frame) (modelResult, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		_, body, ok := strings.Cut(content, "\n")
		if !ok || !strings.HasSuffix(body, "```") {
			return modelResult{}, errors.New("审核模型报告格式无效")
		}
		content = strings.TrimSpace(strings.TrimSuffix(body, "```"))
	}
	var wire struct {
		Verdict string          `json:"verdict"`
		Score   *int            `json:"score"`
		Summary string          `json:"summary"`
		Issues  json.RawMessage `json:"issues"`
		Checked []string        `json:"checked"`
	}
	if err := json.Unmarshal([]byte(content), &wire); err != nil || wire.Score == nil || *wire.Score < 0 || *wire.Score > 100 || strings.TrimSpace(wire.Summary) == "" || len(wire.Issues) == 0 {
		return modelResult{}, errors.New("审核模型报告缺少有效的分数、结论或问题列表")
	}
	if wire.Verdict != "pass" && wire.Verdict != "warn" && wire.Verdict != "fail" {
		return modelResult{}, errors.New("审核模型没有返回有效的 verdict")
	}
	result := modelResult{Score: *wire.Score, Report: Report{Summary: wire.Summary, Issues: []Issue{}, Checked: append([]string(nil), categories...)}}
	if err := json.Unmarshal(wire.Issues, &result.Issues); err != nil {
		return modelResult{}, errors.New("审核问题列表格式无效")
	}
	if result.Issues == nil {
		return modelResult{}, errors.New("审核模型问题列表必须是数组，不能为 null")
	}
	seen := map[string]bool{}
	for _, category := range wire.Checked {
		seen[category] = true
	}
	for _, category := range categories {
		if !seen[category] {
			return modelResult{}, errors.New("审核模型未完成全部八项检查")
		}
	}
	known := map[string]bool{}
	for _, category := range categories {
		known[category] = true
	}
	high := 0
	result.Verdict = "pass"
	for i := range result.Issues {
		issue := &result.Issues[i]
		if issue.FrameIndex < 1 || issue.FrameIndex > len(frames) || frames[issue.FrameIndex-1].Index != issue.FrameIndex || !known[issue.Category] || (issue.Severity != "high" && issue.Severity != "medium" && issue.Severity != "low") || strings.TrimSpace(issue.Description) == "" || strings.TrimSpace(issue.Suggestion) == "" {
			return modelResult{}, errors.New("审核问题的帧序号、类别、等级或说明无效")
		}
		issue.TimeS = frames[issue.FrameIndex-1].TimeS
		if issue.Severity == "high" {
			high++
			if issue.Category == "hands" || issue.Category == "product" {
				result.Verdict = "fail"
			}
		}
	}
	if high >= 2 {
		result.Verdict = "fail"
	}
	if len(result.Issues) > 0 && result.Verdict != "fail" {
		result.Verdict = "warn"
	}
	return result, nil
}

func validFrames(frames []Frame, duration float64) bool {
	if len(frames) == 0 || len(frames) > 40 || !positiveFinite(duration) {
		return false
	}
	for i, f := range frames {
		if f.Index != i+1 || math.IsNaN(f.TimeS) || math.IsInf(f.TimeS, 0) || f.TimeS < 0 || f.TimeS >= duration || (i > 0 && frames[i-1].TimeS >= f.TimeS) {
			return false
		}
	}
	return true
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
