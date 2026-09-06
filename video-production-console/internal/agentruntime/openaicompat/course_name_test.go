package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCourseNameCanonicalization(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"《2026 财富觉醒方法论 白银版》", "《财富觉醒方法论》"},
		{"《财富觉醒方法论白银版》", "《财富觉醒方法论》"},
		{"财富觉醒方法论（白银版）", "财富觉醒方法论"},
		{"2026年 财富觉醒方法论 (白银版)", "财富觉醒方法论"},
		{"2026年白银价格变化。去看《财富觉醒方法论》。", "2026年白银价格变化。去看《财富觉醒方法论》。"},
		{"《财富觉醒方法论》\n白银版画另售。", "《财富觉醒方法论》\n白银版画另售。"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, changed := stripCourseYear(tc.in)
			if got != tc.want || changed != (tc.in != tc.want) {
				t.Fatalf("got=%q changed=%v want=%q", got, changed, tc.want)
			}
		})
	}
}

func TestDeliveryPreservesCourseTextForHumanReview(t *testing.T) {
	dir := t.TempDir()
	script := strings.Repeat("先看清这件事与自己的关系，再理解前后变化。", 4) + "去主页橱窗看《财富觉醒方法论 白银版》。"
	raw, _ := json.Marshal(remixDraft{ContinuousScript: script, CTA: "去主页橱窗看《2026 财富觉醒方法论 白银版》。"})
	if err := writeRemixDeliverable(dir, "course-name-test", "remix.standard", string(raw), nil, ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"continuous_script.txt", "publishing_package.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(b), "白银版") || !strings.Contains(string(b), "财富觉醒方法论") {
			t.Fatalf("%s: %s err=%v", name, b, err)
		}
	}
	kept, err := os.ReadFile(filepath.Join(dir, "model_raw.txt"))
	if err != nil || string(kept) != string(raw) {
		t.Fatal("raw model evidence changed")
	}
}
