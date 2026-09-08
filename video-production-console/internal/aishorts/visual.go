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
	// VideoPlan 决定哪些镜头做图生视频：none / opening（前 30 或 60 秒，沿用 OpeningVideoSeconds）/
	// hooks（开头 60 秒 + 中段祝福钩子 + 课尾三镜）/ all / first_n（前 VideoFirstN 镜）。
	// 空值按 OpeningVideoSeconds 推导，兼容旧记录。
	VideoPlan   string `json:"video_plan,omitempty"`
	VideoFirstN int    `json:"video_first_n,omitempty"`
	// CaptionStyle：outline 白字黑边（默认）/ band 白字 + 半透明底色块。
	CaptionStyle string `json:"caption_style,omitempty"`
	// CaptionColor：字幕和顶部标题的字色 #RRGGBB；默认黄字 #FFDE00（用户在剪映里定稿的样式）。
	CaptionColor string `json:"caption_color,omitempty"`
	// HeadlineSeconds：顶部大标题只在开头停几秒；默认 10，HeadlineFullVideo 表示贯穿全片。
	HeadlineSeconds int `json:"headline_seconds,omitempty"`
	// SegmentStyles 分段画风：开头 / 讲钱 / 祝福词 / 结尾处境各用什么画风，空 = 跟随项目底色。见 segment_styles.go。
	SegmentStyles SegmentStyles `json:"segment_styles,omitzero"`
}

const (
	DefaultCaptionColor    = "#FFDE00"
	DefaultHeadlineSeconds = 10
	HeadlineFullVideo      = -1
)

const (
	VideoPlanNone     = "none"
	VideoPlanOpening  = "opening"
	VideoPlanHooks    = "hooks"
	VideoPlanAll      = "all"
	VideoPlanFirstN   = "first_n"
	hooksOpeningSecs  = 60.0
	hooksClosingShots = 3
)

// Plan 返回归一化后的视频档位。
func (v *VisualSettings) Plan() string {
	if v == nil {
		return VideoPlanNone
	}
	switch v.VideoPlan {
	case VideoPlanNone, VideoPlanOpening, VideoPlanHooks, VideoPlanAll, VideoPlanFirstN:
		return v.VideoPlan
	}
	if v.OpeningVideoSeconds > 0 {
		return VideoPlanOpening
	}
	return VideoPlanNone
}

// OpeningLimit 是 opening 档的秒数；hooks 档固定 60。
func (v *VisualSettings) OpeningLimit() float64 {
	if v == nil {
		return 0
	}
	switch v.Plan() {
	case VideoPlanOpening:
		if v.OpeningVideoSeconds > 0 {
			return float64(v.OpeningVideoSeconds)
		}
		return hooksOpeningSecs
	case VideoPlanHooks:
		return hooksOpeningSecs
	}
	return 0
}

// DefaultVisualSettings 是竖版全幅 AI 短片的默认包装，按用户在剪映里改定的草稿反推：
// 黄字 18 号黑边字幕、一行一屏；顶部大标题只停前 10 秒；不放章节式的"重点标注"轨，也不在字幕里变色高亮。
func DefaultVisualSettings() *VisualSettings {
	return &VisualSettings{
		Layout: "portrait_full", CaptionEnabled: true, CaptionPosition: "lower", CaptionSize: 18, CaptionColor: DefaultCaptionColor,
		KeywordsEnabled: false, AnnotationEnabled: false, MotionStrength: "gentle", Transition: "fade", FastOpening: true,
		HeadlineSeconds: DefaultHeadlineSeconds,
	}
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
		v.CaptionSize = 18
	}
	v.CaptionColor = strings.ToUpper(strings.TrimSpace(v.CaptionColor))
	if v.CaptionColor == "" {
		v.CaptionColor = DefaultCaptionColor
	}
	if !hexColorRE.MatchString(v.CaptionColor) {
		return nil, errors.New("字幕颜色请填 #RRGGBB")
	}
	if v.HeadlineSeconds == 0 {
		v.HeadlineSeconds = DefaultHeadlineSeconds
	}
	if v.HeadlineSeconds != HeadlineFullVideo && (v.HeadlineSeconds < 3 || v.HeadlineSeconds > 120) {
		return nil, errors.New("标题显示时长请填 3～120 秒，或选贯穿全片")
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
	v.VideoPlan = strings.TrimSpace(v.VideoPlan)
	switch v.VideoPlan {
	case "", VideoPlanNone, VideoPlanOpening, VideoPlanHooks, VideoPlanAll, VideoPlanFirstN:
	default:
		return nil, errors.New("视频档位无效")
	}
	if v.VideoPlan == VideoPlanFirstN && v.VideoFirstN <= 0 {
		return nil, errors.New("请填写前几镜使用视频")
	}
	if v.VideoPlan == VideoPlanOpening && v.OpeningVideoSeconds == 0 {
		v.OpeningVideoSeconds = 60
	}
	if v.VideoPlan == VideoPlanNone {
		v.OpeningVideoSeconds = 0
	}
	v.CaptionStyle = strings.TrimSpace(v.CaptionStyle)
	if v.CaptionStyle == "" {
		v.CaptionStyle = "outline"
	}
	if v.CaptionStyle != "outline" && v.CaptionStyle != "band" {
		return nil, errors.New("字幕样式无效")
	}
	v.VideoModel = strings.TrimSpace(v.VideoModel)
	seg, err := normalizeSegmentStyles(v.SegmentStyles)
	if err != nil {
		return nil, err
	}
	v.SegmentStyles = seg
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

var hexColorRE = regexp.MustCompile(`^#[0-9A-F]{6}$`)

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
		segmentChanged := short.VisualSettings == nil || short.VisualSettings.SegmentStyles != next.SegmentStyles
		short.VisualSettings = next
		if segmentChanged {
			// 分段画风变了：重新打角色、重新解析每镜画风，变了的镜标旧图待重生。
			reapplyShotStyles(short)
		}
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
