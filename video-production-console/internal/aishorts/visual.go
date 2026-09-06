package aishorts

import (
	"errors"
	"regexp"
	"strings"
)

// VisualSettings belongs to a short, so later account changes do not move its captions.
type VisualSettings struct {
	Layout              string  `json:"layout"`
	CaptionEnabled      bool    `json:"caption_enabled"`
	CaptionPosition     string  `json:"caption_position"`
	CaptionSize         float64 `json:"caption_size"`
	KeywordsEnabled     bool    `json:"keywords_enabled"`
	AnnotationEnabled   bool    `json:"annotation_enabled"`
	MotionStrength      string  `json:"motion_strength"`
	Transition          string  `json:"transition"`
	SFXEnabled          bool    `json:"sfx_enabled"`
	FastOpening         bool    `json:"fast_opening"`
	OpeningVideoSeconds int     `json:"opening_video_seconds"`
	VideoModel          string  `json:"video_model,omitempty"`
}

func DefaultVisualSettings() *VisualSettings {
	return &VisualSettings{Layout: "portrait_full", CaptionEnabled: true, CaptionPosition: "lower", CaptionSize: 12, KeywordsEnabled: true, AnnotationEnabled: true, MotionStrength: "gentle", Transition: "fade", FastOpening: true}
}

func legacyVisualSettings() *VisualSettings {
	s := DefaultVisualSettings()
	s.Layout = "portrait_inset"
	s.CaptionPosition = "window"
	s.CaptionSize = 9
	s.KeywordsEnabled = false
	s.AnnotationEnabled = false
	s.SFXEnabled = true
	s.Transition = "cut"
	s.FastOpening = false
	return s
}

func normalizedVisualSettings(in *VisualSettings) (*VisualSettings, error) {
	if in == nil {
		return DefaultVisualSettings(), nil
	}
	v := *in
	if v.Layout == "" {
		v.Layout = "portrait_full"
	}
	if v.CaptionPosition == "" {
		v.CaptionPosition = "lower"
	}
	if v.CaptionSize == 0 {
		v.CaptionSize = 12
	}
	if v.MotionStrength == "" {
		v.MotionStrength = "gentle"
	}
	if v.Transition == "" {
		v.Transition = "cut"
	}
	if v.Layout != "portrait_full" && v.Layout != "portrait_inset" {
		return nil, errors.New("请选择竖图全幅或横图底板布局")
	}
	if v.CaptionPosition != "lower" && v.CaptionPosition != "middle" && v.CaptionPosition != "window" {
		return nil, errors.New("字幕位置无效")
	}
	if v.CaptionSize < 8 || v.CaptionSize > 24 {
		return nil, errors.New("字幕字号范围为8至24")
	}
	if v.MotionStrength != "gentle" && v.MotionStrength != "standard" && v.MotionStrength != "none" {
		return nil, errors.New("图片运动强度无效")
	}
	if v.Transition != "cut" && v.Transition != "fade" {
		return nil, errors.New("画面切换方式无效")
	}
	if v.OpeningVideoSeconds != 0 && v.OpeningVideoSeconds != 30 && v.OpeningVideoSeconds != 60 {
		return nil, errors.New("开场视频请选择关闭、前30秒或前60秒")
	}
	v.VideoModel = strings.TrimSpace(v.VideoModel)
	return &v, nil
}

type ShotKeyword struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}
type CaptionCue struct {
	Text     string        `json:"text"`
	StartS   float64       `json:"start_s"`
	EndS     float64       `json:"end_s"`
	Keywords []ShotKeyword `json:"keywords,omitempty"`
}

var financialTokenRE = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)*(?:%|％|万亿|亿元|万元|亿|万|元|年|点|块)?|《[^》]+》`)

func numericPunctuation(r []rune, i int) bool {
	return i > 0 && i+1 < len(r) && (r[i] == '.' || r[i] == ',') && r[i-1] >= '0' && r[i-1] <= '9' && r[i+1] >= '0' && r[i+1] <= '9'
}

// Splitting is a caption layout operation, not a rewrite. Keep a financial token whole.
func protectFinancialCut(r []rune, start, cut int) int {
	text := string(r)
	for _, span := range financialTokenRE.FindAllStringIndex(text, -1) {
		a, b := len([]rune(text[:span[0]])), len([]rune(text[:span[1]]))
		if a < cut && cut < b {
			if a > start {
				return a
			}
			return b
		}
	}
	return cut
}

func normalizedKeywords(line string, words []ShotKeyword) []ShotKeyword {
	if words == nil {
		words = []ShotKeyword{}
		for _, n := range financialTokenRE.FindAllString(line, -1) {
			kind := "number"
			if strings.HasPrefix(n, "《") {
				kind = "concept"
			}
			words = append(words, ShotKeyword{Text: n, Kind: kind})
		}
		for _, n := range []string{"存款", "利率", "本金", "风险", "收入", "支出", "负债"} {
			if strings.Contains(line, n) {
				kind := "concept"
				if n == "风险" {
					kind = "risk"
				}
				words = append(words, ShotKeyword{Text: n, Kind: kind})
			}
		}
	}
	out := []ShotKeyword{}
	seen := map[string]bool{}
	for _, w := range words {
		w.Text = strings.TrimSpace(w.Text)
		if w.Text == "" || !strings.Contains(line, w.Text) || seen[w.Text] {
			continue
		}
		if w.Kind != "number" && w.Kind != "risk" {
			w.Kind = "concept"
		}
		seen[w.Text] = true
		out = append(out, w)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func validSubjectType(s string) bool {
	switch s {
	case "object", "environment", "person", "comparison", "metaphor":
		return true
	}
	return false
}
func validCameraMove(s string) bool {
	switch s {
	case "still", "zoom_in", "zoom_out", "pan_left", "pan_right":
		return true
	}
	return false
}

func normalizeVisualShot(short *Short, i int) {
	shot := &short.Shots[i]
	if shot.SourceText == "" {
		shot.SourceText = shot.Narration
	}
	if !validSubjectType(shot.SubjectType) {
		if mentionsPeople(shot.Scene + shot.Subject) {
			shot.SubjectType = "person"
		} else {
			shot.SubjectType = "object"
		}
	}
	shot.Keywords = normalizedKeywords(shot.Narration, shot.Keywords)
	shot.CaptionLines = splitClauses(shot.Narration)
	shot.AspectRatio = "16:9"
	if short.VisualSettings != nil && short.VisualSettings.Layout == "portrait_full" {
		shot.AspectRatio = "9:16"
	}
	if !validCameraMove(shot.CameraMove) {
		if shot.SubjectType == "environment" {
			shot.CameraMove = "pan_left"
		} else if shot.SubjectType == "comparison" {
			shot.CameraMove = "still"
		} else {
			shot.CameraMove = "zoom_in"
		}
	}
}

func keywordsOnCaptions(caps []jobCaption, words []ShotKeyword) []jobCaption {
	for i := range caps {
		caps[i].Keywords = normalizedKeywords(caps[i].Text, append([]ShotKeyword{}, words...))
	}
	return caps
}

func markDraftStale(short *Short) {
	if short.DraftPath != "" {
		short.DraftStale = true
	}
	short.Captions = nil
}

func explainerOrFablePrompt(short *Short, shot Shot) string {
	if short.IsExplainer() {
		return explainerImagePrompt(shot)
	}
	return shotImagePrompt(short.Style, shot, short.Characters)
}

// UpdateVisualSettings applies the full settings snapshot; image paths stay available for comparison.
func applyVisualSettings(short *Short, settings *VisualSettings) error {
	if settings == nil || !short.IsExplainer() {
		return nil
	}
	next, err := normalizedVisualSettings(settings)
	if err != nil {
		return err
	}
	if short.VisualSettings == nil || *short.VisualSettings != *next {
		if short.VisualSettings != nil && short.VisualSettings.FastOpening != next.FastOpening && len(short.Shots) > 0 {
			short.StoryboardStale = true
		}
		short.VisualSettings = next
		markDraftStale(short)
		for _, shot := range short.Shots {
			if !short.ShotReady(shot) {
				short.Status = StatusStoryboard
				break
			}
		}
	}
	return nil
}
