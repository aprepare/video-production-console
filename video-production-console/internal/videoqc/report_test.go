package videoqc

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestSampleFrames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration float64
		cuts     []float64
		count    int
	}{
		{"subsecond", .2, nil, 1}, {"short", 3.2, nil, 4}, {"thirty", 30, nil, 30}, {"long", 120, nil, 30},
		{"cut", 3, []float64{1.5}, 5}, {"invalid cuts", 3, []float64{-1, 0, 3, math.NaN(), math.Inf(1)}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := sampleFrames(tc.duration, 25, tc.cuts)
			if len(frames) != tc.count {
				t.Fatalf("frames=%+v want count=%d", frames, tc.count)
			}
			for i, f := range frames {
				if f.Index != i+1 || f.TimeS < 0 || f.TimeS >= tc.duration || (i > 0 && frames[i-1].TimeS >= f.TimeS) {
					t.Fatalf("invalid frames %+v", frames)
				}
			}
			if tc.name == "long" && frames[len(frames)-1].TimeS < 119 {
				t.Fatal("long sample must cover tail")
			}
			if tc.name == "cut" {
				if math.Abs(frames[2].TimeS-1.46) > 1e-6 || math.Abs(frames[3].TimeS-1.54) > 1e-6 {
					t.Fatalf("cut frames: %+v", frames)
				}
			}
		})
	}
	var cuts []float64
	for i := 1; i < 90; i++ {
		cuts = append(cuts, float64(i)+.5)
	}
	frames := sampleFrames(120, 30, cuts)
	if len(frames) != 40 {
		t.Fatalf("cap: %d", len(frames))
	}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if len(sampleFrames(bad, 30, nil)) != 0 {
			t.Fatal("bad duration accepted")
		}
	}
}

func TestReportVerdictAndFrameAuthority(t *testing.T) {
	frames := []Frame{{Index: 1, TimeS: 0}, {Index: 2, TimeS: 2.5}}
	for _, tc := range []struct {
		name, model, want string
		issues            []Issue
	}{
		{"empty model fail", "fail", "pass", nil},
		{"low", "pass", "warn", []Issue{{FrameIndex: 1, Category: "quality", Severity: "low"}}},
		{"medium", "pass", "warn", []Issue{{FrameIndex: 1, Category: "text", Severity: "medium"}}},
		{"one high", "pass", "warn", []Issue{{FrameIndex: 2, Category: "body", Severity: "high"}}},
		{"two high", "pass", "fail", []Issue{{FrameIndex: 1, Category: "body", Severity: "high"}, {FrameIndex: 2, Category: "face", Severity: "high"}}},
		{"hands high", "pass", "fail", []Issue{{FrameIndex: 1, Category: "hands", Severity: "high"}}},
		{"product high", "warn", "fail", []Issue{{FrameIndex: 2, Category: "product", Severity: "high"}}},
		{"model cannot override", "fail", "warn", []Issue{{FrameIndex: 2, Category: "quality", Severity: "low"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.issues {
				tc.issues[i].TimeS = 999
				tc.issues[i].Description = "可见异常"
				tc.issues[i].Suggestion = "重新生成该镜头"
			}
			if tc.issues == nil {
				tc.issues = []Issue{}
			}
			raw, _ := json.Marshal(map[string]any{"verdict": tc.model, "score": 72, "summary": "检查结果", "issues": tc.issues, "checked": categories})
			result, err := parseReport("```json\n"+string(raw)+"\n```", frames)
			if err != nil || result.Verdict != tc.want || result.Score != 72 {
				t.Fatalf("%+v %v", result, err)
			}
			for _, issue := range result.Report.Issues {
				if issue.TimeS != frames[issue.FrameIndex-1].TimeS {
					t.Fatal("untrusted model time")
				}
			}
			if result.Report.Issues == nil {
				t.Fatal("issues must be []")
			}
		})
	}
}

func TestInvalidReportFailsInsteadOfPassing(t *testing.T) {
	base := `{"verdict":"pass","score":90,"summary":"检查结果","issues":[{"frame_index":%d,"time_s":999,"category":"hands","severity":"high","description":"六指","suggestion":"重做"}],"checked":["hands","body","face","product","text","artifact","watermark","quality"]}`
	for _, index := range []int{-1, 0, 3} {
		if _, err := parseReport(fmt.Sprintf(base, index), []Frame{{Index: 1}, {Index: 2, TimeS: 1}}); err == nil {
			t.Fatalf("accepted frame %d", index)
		}
	}
	for _, raw := range []string{"{}", "null", strings.Replace(fmt.Sprintf(base, 1), `"hands","severity"`, `"unknown","severity"`, 1), strings.Replace(fmt.Sprintf(base, 1), `"score":90`, `"score":101`, 1), strings.Replace(fmt.Sprintf(base, 1), `"checked":["hands",`, `"checked":[`, 1)} {
		if _, err := parseReport(raw, []Frame{{Index: 1}}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestIncompleteReportsCannotPass(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(goodReport, `"issues":[]`, `"issues":null`, 1),
		strings.Replace(goodReport, `"verdict":"pass",`, "", 1),
		strings.Replace(goodReport, `"verdict":"pass"`, `"verdict":"unknown"`, 1),
	} {
		if _, err := parseReport(raw, []Frame{{Index: 1}}); err == nil {
			t.Fatalf("incomplete report passed: %s", raw)
		}
	}
}
