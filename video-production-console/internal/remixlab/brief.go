package remixlab

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var ErrInvalidBrief = errors.New("invalid brief")

type Brief struct {
	Industry      string `json:"industry"`
	Audience      string `json:"audience"`
	Offer         string `json:"offer"`
	SellingPoints string `json:"selling_points"`
	Conversion    string `json:"conversion"`
	Persona       string `json:"persona"`
	Platform      string `json:"platform"`
	DurationS     int    `json:"duration_s"`
	Banned        string `json:"banned"`
	Notes         string `json:"notes"`
}

func NormalizeBrief(in Brief) (Brief, error) {
	out := in
	out.Industry = strings.TrimSpace(out.Industry)
	out.Audience = strings.TrimSpace(out.Audience)
	out.Offer = strings.TrimSpace(out.Offer)
	out.SellingPoints = strings.TrimSpace(out.SellingPoints)
	out.Conversion = strings.TrimSpace(out.Conversion)
	out.Persona = strings.TrimSpace(out.Persona)
	out.Platform = strings.TrimSpace(out.Platform)
	out.Banned = strings.TrimSpace(out.Banned)
	out.Notes = strings.TrimSpace(out.Notes)
	if err := validateNormalizedBrief(out); err != nil {
		return Brief{}, err
	}
	return out, nil
}

func ValidateBrief(in Brief) error {
	_, err := NormalizeBrief(in)
	return err
}

func validateNormalizedBrief(b Brief) error {
	if b.DurationS != 0 && (b.DurationS < 10 || b.DurationS > 600) {
		return fmt.Errorf("%w: duration_s must be 0 or between 10 and 600", ErrInvalidBrief)
	}
	switch b.Platform {
	case "", "douyin", "xhs", "channels":
	default:
		return fmt.Errorf("%w: unsupported platform %q", ErrInvalidBrief, b.Platform)
	}
	total := 0
	for _, value := range []string{b.Industry, b.Audience, b.Offer, b.SellingPoints, b.Conversion, b.Persona, b.Platform, b.Banned, b.Notes} {
		total += utf8.RuneCountInString(value)
	}
	if total > 6000 {
		return fmt.Errorf("%w: text exceeds 6000 runes", ErrInvalidBrief)
	}
	return nil
}

func RenderBrief(in Brief) string {
	b := in
	b.Industry = strings.TrimSpace(b.Industry)
	b.Audience = strings.TrimSpace(b.Audience)
	b.Offer = strings.TrimSpace(b.Offer)
	b.SellingPoints = strings.TrimSpace(b.SellingPoints)
	b.Conversion = strings.TrimSpace(b.Conversion)
	b.Persona = strings.TrimSpace(b.Persona)
	b.Platform = strings.TrimSpace(b.Platform)
	b.Banned = strings.TrimSpace(b.Banned)
	b.Notes = strings.TrimSpace(b.Notes)

	lines := make([]string, 0, 11)
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, label+"："+value)
		}
	}
	add("行业/品类", b.Industry)
	add("目标观众", b.Audience)
	add("产品/服务", b.Offer)
	add("真实卖点与可用事实", b.SellingPoints)
	add("转化动作", b.Conversion)
	add("出镜人设与口吻", b.Persona)
	add("平台", b.Platform)
	if b.DurationS > 0 {
		lines = append(lines, fmt.Sprintf("目标时长约 %d 秒，按每分钟约 220–260 字口播折算约 %d–%d 字", b.DurationS, b.DurationS*220/60, b.DurationS*260/60))
	}
	add("禁用词/不能说的话", b.Banned)
	add("其他要求", b.Notes)
	if len(lines) == 0 {
		return ""
	}
	return "【内容简报｜事实边界】\n" + strings.Join(lines, "\n")
}
