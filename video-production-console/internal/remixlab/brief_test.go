package remixlab

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeBriefTrimsEveryTextField(t *testing.T) {
	in := Brief{
		Industry: " 海鲜餐饮 ", Audience: " 家庭客群 ", Offer: " 双人餐 ",
		SellingPoints: " 当日到港\n ", Conversion: " 到店 ", Persona: " 老板本人 ",
		Platform: " channels ", DurationS: 60, Banned: " 最好 ", Notes: " 不夸张 ",
	}

	got, err := NormalizeBrief(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Industry != "海鲜餐饮" || got.Audience != "家庭客群" || got.Offer != "双人餐" ||
		got.SellingPoints != "当日到港" || got.Conversion != "到店" || got.Persona != "老板本人" ||
		got.Platform != "channels" || got.Banned != "最好" || got.Notes != "不夸张" {
		t.Fatalf("fields were not normalized: %#v", got)
	}
}

func TestValidateBriefRejectsInvalidBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		brief Brief
	}{
		{name: "duration below range", brief: Brief{DurationS: 9}},
		{name: "duration above range", brief: Brief{DurationS: 601}},
		{name: "unknown platform", brief: Brief{Platform: "wechat"}},
		{name: "more than 6000 runes", brief: Brief{Notes: strings.Repeat("界", 6001)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateBrief(tt.brief); !errors.Is(err, ErrInvalidBrief) {
				t.Fatalf("error = %v, want ErrInvalidBrief", err)
			}
		})
	}

	valid := Brief{DurationS: 10, Notes: strings.Repeat("界", 6000)}
	if err := ValidateBrief(valid); err != nil {
		t.Fatalf("valid boundary rejected: %v", err)
	}
	if utf8.RuneCountInString(valid.Notes) != 6000 {
		t.Fatal("test fixture is not 6000 runes")
	}
}

func TestRenderBriefOnlyIncludesProvidedFieldsAndConvertsDuration(t *testing.T) {
	got := RenderBrief(Brief{Industry: " 茶叶 ", SellingPoints: "明前采摘", Platform: "douyin", DurationS: 61})
	want := "【内容简报｜事实边界】\n行业/品类：茶叶\n真实卖点与可用事实：明前采摘\n平台：douyin\n目标时长约 61 秒，按每分钟约 220–260 字口播折算约 223–264 字"
	if got != want {
		t.Fatalf("rendered brief mismatch\ngot:  %q\nwant: %q", got, want)
	}
	for _, absent := range []string{"目标观众", "转化动作", "其他要求"} {
		if strings.Contains(got, absent) {
			t.Fatalf("empty field %q was rendered: %q", absent, got)
		}
	}
	if got := RenderBrief(Brief{}); got != "" {
		t.Fatalf("empty brief rendered as %q", got)
	}
}
