package jianyingdraft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	return content
}

// 居中观：命名轨（标题/副标题/字幕），黑字黄底、无描边、无关键词。
func TestExtractStyleNamedTracks(t *testing.T) {
	ex := ExtractStyle(loadFixture(t, "named_tracks.json"))
	s := ex.Style
	if s.CaptionFont != "新青年体" || s.CaptionSize != 20 || s.CaptionColor != "#000000" {
		t.Fatalf("caption = %+v", s)
	}
	if s.CaptionBgColor != "#FFDE00" || s.CaptionBgAlpha != 1 {
		t.Fatalf("caption bg = %q/%v", s.CaptionBgColor, s.CaptionBgAlpha)
	}
	if !s.CaptionBorderHidden {
		t.Fatalf("caption should have no stroke: %+v", s)
	}
	if !s.KeywordsHidden {
		t.Fatal("single-run captions must disable keyword highlight")
	}
	if s.CaptionPosition != "middle" {
		t.Fatalf("position = %q", s.CaptionPosition)
	}
	if s.TitleFont != "新青年体" || s.TitleColor != "#FFDB1A" || s.TitleSize != 15.7 || s.TitleY != 0.667 {
		t.Fatalf("title = font %q color %q size %v y %v", s.TitleFont, s.TitleColor, s.TitleSize, s.TitleY)
	}
	if s.SubtitleColor != "#000000" || s.SubtitleBgColor != "#FFFFFF" || s.SubtitleSize != 9.2 || s.SubtitleY != 0.49 {
		t.Fatalf("subtitle = %+v", s)
	}
	if ex.Timeline != "" {
		t.Fatalf("plain draft should not be marked combination: %q", ex.Timeline)
	}
}

// 财富摆渡日记：组合片段、无轨名、句内关键词红色 23 号 + 米黄 17 号。
func TestExtractStyleCombinationKeywords(t *testing.T) {
	ex := ExtractStyle(loadFixture(t, "combination_keywords.json"))
	s := ex.Style
	if ex.Timeline != "combination" {
		t.Fatalf("expected combination descent, got %q", ex.Timeline)
	}
	if s.KeywordsHidden {
		t.Fatal("keyword runs present, highlight must stay on")
	}
	if s.KeywordSize != 23 || s.KeywordColor != "#FF1515" || s.PlainSize != 17 {
		t.Fatalf("keyword = size %v color %q plain %v", s.KeywordSize, s.KeywordColor, s.PlainSize)
	}
	// 无高亮的整行字幕保持 20 号；17 只是关键词行里的平排部分。
	if s.CaptionSize != 20 {
		t.Fatalf("caption size = %v, want 20 (whole-line style, not plain run)", s.CaptionSize)
	}
	if s.CaptionFont != "新青年体" || s.CaptionColor != "#F9F3C4" {
		t.Fatalf("caption = font %q color %q", s.CaptionFont, s.CaptionColor)
	}
	if s.CaptionBorderColor != "#4A4238" || s.CaptionBorderHidden {
		t.Fatalf("caption stroke = %q hidden=%v", s.CaptionBorderColor, s.CaptionBorderHidden)
	}
	if s.KeywordBorderColor != "#000000" {
		t.Fatalf("keyword stroke = %q", s.KeywordBorderColor)
	}
	if s.TitleHidden || s.SubtitleHidden {
		t.Fatalf("unnamed single tracks must still map to title/subtitle: %+v", s)
	}
	if s.TitleY <= s.SubtitleY {
		t.Fatalf("title should sit above subtitle: %v vs %v", s.TitleY, s.SubtitleY)
	}
}

func TestPickDraftFontDropsWenYueTitle(t *testing.T) {
	if got := pickDraftFont("WenYue", `D:/JianyingPro/Resources/Font/WenYue.ttf`); got != "" {
		t.Fatalf("WenYue is not a FontType member, got %q", got)
	}
	if got := pickDraftFont("WenYue", `D:/JianyingPro/Resources/Font/新青年体.ttf`); got != "新青年体" {
		t.Fatalf("path-derived FontType name should win over WenYue title, got %q", got)
	}
	if got := pickDraftFont("新青年体", ""); got != "新青年体" {
		t.Fatalf("known title: got %q", got)
	}
}

func TestFontFromPath(t *testing.T) {
	cases := map[string]string{
		`D:/JianyingPro/Resources/Font/新青年体.ttf`:               "新青年体",
		`C:\fonts\新青年体-文跃新青年体.ttf`:                          "新青年体",
		`D:/JianyingPro/Resources/Font/字语咏宏体 .ttf`:             "字语咏宏体",
		`D:/JianyingPro/Resources/Font/SystemFont/zh-hans.ttf`: "",
	}
	for in, want := range cases {
		if got := fontFromPath(in); got != want {
			t.Errorf("fontFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDraftBorderToJianying(t *testing.T) {
	if got := draftBorderToJianying(0.08); got != 40 {
		t.Fatalf("0.08 -> %v, want 40", got)
	}
}
