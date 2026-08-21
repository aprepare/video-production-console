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

// buildStyleOverrides encodes the user style as overrides for the Python
// skill's text_styles. Only size/color/y/font are overridable; everything
// else (borders, alignment) stays on the verified policy.
func buildStyleOverrides(style domain.MontageStyle) map[string]map[string]any {
	captionY := style.CaptionTransformY()
	entry := func(size float64, color string) map[string]any {
		return map[string]any{
			"size":  roundStyleSize(size),
			"color": hexToRGB(color),
			"y":     captionY,
			"font":  style.CaptionFont,
		}
	}
	return map[string]map[string]any{
		"spoken_v1":         entry(style.CaptionSize, style.CaptionColor),
		"spoken_plain_v1":   entry(style.PlainSize, style.CaptionColor),
		"spoken_keyword_v1": entry(style.KeywordSize, style.KeywordColor),
		"spoken_warning_v1": entry(style.KeywordSize, style.KeywordColor),
	}
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
