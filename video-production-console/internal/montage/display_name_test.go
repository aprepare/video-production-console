package montage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDraftDisplayNameUsesFirstShortTitle(t *testing.T) {
	got := BuildDraftDisplayName(
		"财富觉醒02",
		"项目兜底",
		"存款大搬家",
		"984c42ec-67b8-4d3f-99e3-d3d7a4b66205",
	)
	if got != "财富觉醒02_存款大搬家_b66205" {
		t.Fatalf("display name=%q", got)
	}
}

func TestDraftDisplayNameSanitizesWindowsLabelsAndFallbacks(t *testing.T) {
	taskID := "984C42EC-67B8-4D3F-99E3-D3D7A4B66205"
	tests := []struct {
		name       string
		account    string
		project    string
		shortTitle string
		want       string
	}{
		{
			name:       "missing short title uses project",
			account:    "财富觉醒02",
			project:    "项目兜底",
			shortTitle: "  ",
			want:       "财富觉醒02_项目兜底_b66205",
		},
		{
			name:       "reserved characters and controls are removed",
			account:    " 财<富>:觉/醒\\02|?* ",
			shortTitle: " 存\x00款\n大\t搬家 ",
			want:       "财富觉醒02_存款大搬家_b66205",
		},
		{
			name:       "trailing dots spaces and repeated separators are normalized",
			account:    "...财富___觉醒...   ",
			shortTitle: "  存款____搬家... ",
			want:       "财富_觉醒_存款_搬家_b66205",
		},
		{
			name:       "empty labels use readable Chinese fallbacks",
			account:    "<>. ",
			project:    "***... ",
			shortTitle: "",
			want:       "未命名账号_未命名项目_b66205",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildDraftDisplayName(tt.account, tt.project, tt.shortTitle, taskID)
			if got != tt.want {
				t.Fatalf("display name=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestDraftDisplayNameLimitsUnicodeRunesWithoutChangingSuffix(t *testing.T) {
	account := strings.Repeat("账", 25)
	project := strings.Repeat("项", 37)
	got := BuildDraftDisplayName(account, project, "", "984c42ec-67b8-4d3f-99e3-d3d7a4b66205")
	want := strings.Repeat("账", 24) + "_" + strings.Repeat("项", 36) + "_b66205"
	if got != want {
		t.Fatalf("display name=%q, want %q", got, want)
	}
	if utf8.RuneCountInString(got) != 68 {
		t.Fatalf("rune count=%d, want 68", utf8.RuneCountInString(got))
	}
}
