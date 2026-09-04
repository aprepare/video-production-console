package domain

import "testing"

func TestNormalizeMontageFontDropsUnknownImportedNames(t *testing.T) {
	if got := NormalizeMontageFont("新青年体"); got != "新青年体" {
		t.Fatalf("known font: got %q", got)
	}
	if got := NormalizeMontageFont("俪金黑"); got != "俪金黑" {
		t.Fatalf("known font: got %q", got)
	}
	// 从剪映草稿导入时 fonts[].title 常是英文件名（文悦 → WenYue），
	// pyJianYingDraft.FontType 没有这个成员，发出去会整单失败。
	if got := NormalizeMontageFont("WenYue"); got != "" {
		t.Fatalf("unknown imported font must be dropped, got %q", got)
	}
	if got := NormalizeMontageFont("  "); got != "" {
		t.Fatalf("blank: got %q", got)
	}
}

func TestMontageCaptionFontFallsBackToDefault(t *testing.T) {
	if got := MontageCaptionFont("WenYue"); got != "新青年体" {
		t.Fatalf("caption fallback: got %q", got)
	}
	if got := MontageCaptionFont("大字报"); got != "大字报" {
		t.Fatalf("known caption font: got %q", got)
	}
}
