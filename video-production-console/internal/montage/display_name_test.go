package montage

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var draftCreatedAt = time.Date(2026, 8, 21, 15, 4, 0, 0, time.Local)

func TestDraftDisplayNameUsesFirstShortTitle(t *testing.T) {
	got := BuildDraftDisplayName(
		"财富觉醒02",
		"项目兜底",
		"存款大搬家",
		draftCreatedAt,
	)
	if got != "财富觉醒02_存款大搬家_0821-1504" {
		t.Fatalf("display name=%q", got)
	}
}

func TestDraftDisplayNameSanitizesWindowsLabelsAndFallbacks(t *testing.T) {
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
			want:       "财富觉醒02_项目兜底_0821-1504",
		},
		{
			name:       "reserved characters and controls are removed",
			account:    " 财<富>:觉/醒\\02|?* ",
			shortTitle: " 存\x00款\n大\t搬家 ",
			want:       "财富觉醒02_存款大搬家_0821-1504",
		},
		{
			name:       "trailing dots spaces and repeated separators are normalized",
			account:    "...财富___觉醒...   ",
			shortTitle: "  存款____搬家... ",
			want:       "财富_觉醒_存款_搬家_0821-1504",
		},
		{
			name:       "empty labels use readable Chinese fallbacks",
			account:    "<>. ",
			project:    "***... ",
			shortTitle: "",
			want:       "未命名账号_未命名项目_0821-1504",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildDraftDisplayName(tt.account, tt.project, tt.shortTitle, draftCreatedAt)
			if got != tt.want {
				t.Fatalf("display name=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestDraftDisplayNameLimitsUnicodeRunesWithoutChangingSuffix(t *testing.T) {
	account := strings.Repeat("账", 25)
	project := strings.Repeat("项", 37)
	got := BuildDraftDisplayName(account, project, "", draftCreatedAt)
	want := strings.Repeat("账", 24) + "_" + strings.Repeat("项", 36) + "_0821-1504"
	if got != want {
		t.Fatalf("display name=%q, want %q", got, want)
	}
	if utf8.RuneCountInString(got) != 71 {
		t.Fatalf("rune count=%d, want 71", utf8.RuneCountInString(got))
	}
}
