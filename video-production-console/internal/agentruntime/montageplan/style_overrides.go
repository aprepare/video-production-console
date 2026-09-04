package montageplan

import (
	"math"
	"strconv"

	"video-production-console/internal/domain"
)

// planMontageStyle resolves the manifest's montage style, falling back to
// the historical defaults so an old manifest renders exactly as before.
func planMontageStyle(ctx *planContext) domain.MontageStyle {
	if ctx != nil && ctx.manifest.NonSecretSettings.MontageStyle != nil {
		return ctx.manifest.NonSecretSettings.MontageStyle.Normalized()
	}
	return domain.DefaultMontageStyle()
}

// hideKeywordSpans strips every caption highlight span so lines render in the
// plain caption style, exactly like a line the keyword model left unmarked.
func hideKeywordSpans(items []CaptionItem) {
	for i := range items {
		items[i].Spans = nil
	}
}

// buildStyleOverrides encodes the user style as overrides for the Python
// skill's text_styles. size/color/y/font always go out; border and
// background keys are emitted only when the style sets them, so an
// untouched field keeps its verified policy value.
func buildStyleOverrides(style domain.MontageStyle) map[string]map[string]any {
	captionY := style.CaptionTransformY()
	entry := func(size float64, color, borderColor string) map[string]any {
		out := map[string]any{
			"size":  roundStyleSize(size),
			"color": hexToRGB(color),
			"y":     captionY,
			"font":  domain.MontageCaptionFont(style.CaptionFont),
		}
		switch {
		case style.CaptionBorderHidden:
			out["border_hidden"] = true
		case isHexColor(borderColor):
			out["border_color"] = hexToRGB(borderColor)
			if style.CaptionBorderWidth > 0 {
				out["border_width"] = style.CaptionBorderWidth
			}
		case style.CaptionBorderWidth > 0:
			out["border_width"] = style.CaptionBorderWidth
		}
		if isHexColor(style.CaptionBgColor) {
			out["bg_color"] = hexToRGB(style.CaptionBgColor)
			out["bg_alpha"] = alphaOrOpaque(style.CaptionBgAlpha)
		}
		return out
	}
	return map[string]map[string]any{
		"spoken_v1":         entry(style.CaptionSize, style.CaptionColor, style.CaptionBorderColor),
		"spoken_plain_v1":   entry(style.PlainSize, style.CaptionColor, style.CaptionBorderColor),
		"spoken_keyword_v1": entry(style.KeywordSize, style.KeywordColor, style.KeywordBorderColor),
		"spoken_warning_v1": entry(style.KeywordSize, style.KeywordColor, style.KeywordBorderColor),
	}
}

// brandTextDecor fills the optional font/border/background of a board title
// or subtitle from the style; empty values stay nil so Python keeps defaults.
func brandTextDecor(font, borderColor, bgColor string, bgAlpha float64) (string, []float64, []float64, float64) {
	var border, bg []float64
	alpha := 0.0
	if isHexColor(borderColor) {
		border = hexToRGB(borderColor)
	}
	if isHexColor(bgColor) {
		bg = hexToRGB(bgColor)
		alpha = alphaOrOpaque(bgAlpha)
	}
	return domain.NormalizeMontageFont(font), border, bg, alpha
}

func isHexColor(hex string) bool {
	if len(hex) != 7 || hex[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(hex[1:], 16, 32)
	return err == nil
}

func alphaOrOpaque(alpha float64) float64 {
	if alpha <= 0 || alpha > 1 {
		return 1
	}
	return math.Round(alpha*1000) / 1000
}

// customBGMPlacement encodes an analyzed local track for audio.bgm. The
// loop/climax windows reuse the same splicing contract as the verified BGM.
func customBGMPlacement(bgm manifestBGM, volume float64) map[string]any {
	if volume <= 0 {
		volume = bgm.Volume
	}
	if volume <= 0 {
		volume = domain.DefaultMontageStyle().BGMVolume
	}
	return map[string]any{
		"custom":            true,
		"name":              bgm.Name,
		"file_path":         bgm.FilePath,
		"media_duration_s":  bgm.DurationS,
		"linear_volume":     volume,
		"loop_every_s":      bgm.UsableHeadS,
		"usable_head_s":     bgm.UsableHeadS,
		"climax_start_s":    bgm.ClimaxStartS,
		"climax_duration_s": bgm.ClimaxDurationS,
		"required":          true,
	}
}

// hexToRGB converts "#RRGGBB" into the 0..1 float triple the style policy
// uses. Invalid input yields white so a corrupted manifest still renders.
func hexToRGB(hex string) []float64 {
	if len(hex) != 7 || hex[0] != '#' {
		return []float64{1, 1, 1}
	}
	value, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return []float64{1, 1, 1}
	}
	channel := func(shift uint) float64 {
		return math.Round(float64((value>>shift)&0xFF)/255*10000) / 10000
	}
	return []float64{channel(16), channel(8), channel(0)}
}

func roundStyleSize(size float64) float64 {
	return math.Round(size*10) / 10
}
