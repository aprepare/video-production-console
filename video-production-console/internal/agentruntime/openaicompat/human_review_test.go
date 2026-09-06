package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHumanReviewRunDoesNotRepairOrNormalizeCopy(t *testing.T) {
	for _, body := range []string{"一句短稿。", strings.Repeat("2026年利率0.95%，这段是原文也由用户自己判断。", 30)} {
		t.Run(string([]rune(body)[:2]), func(t *testing.T) {
			root := t.TempDir()
			sourcePath, dir := filepath.Join(root, "source.txt"), filepath.Join(root, "output")
			_ = os.WriteFile(sourcePath, []byte(strings.Repeat("2026年利率0.95%，这段是原文也由用户自己判断。", 30)), 0600)
			manifest, _ := json.Marshal(map[string]any{"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": dir, "inputs": []any{map[string]any{"type": "source_script", "path": sourcePath}}})
			path := filepath.Join(root, "manifest.json")
			_ = os.WriteFile(path, manifest, 0600)
			draft := remixDraft{ContinuousScript: body, ShortTitles: []string{"短"}, Descriptions: []string{strings.Repeat("描述", 30)}, Topics: []string{"自由话题"}}
			raw, _ := json.Marshal(draft)
			client := &sequenceClient{responses: []string{string(raw), `{"verdict":"pass","issues":[]}`}}
			legacy := `{"nodes":[{"id":"source","type":"input"},{"id":"writer","type":"writer"},{"id":"old_gate","type":"selfcheck","config":{"len_min_ratio":9,"overlap_max_pct":1}},{"id":"review","type":"reviewer","config":{"model":"reviewer"}}],"edges":[["source","writer"],["writer","old_gate"],["old_gate","review"]]}`
			if err := Run(Options{ManifestPath: path, SkillRoot: root, OutputLastMessage: filepath.Join(root, "last.json"), BaseURL: "http://example.invalid/v1", APIKey: "test", Model: "writer", Client: client, WorkflowJSON: legacy}); err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 2 || client.requests[1].Model != "reviewer" {
				t.Fatalf("unexpected mechanical repair calls: %d", len(client.requests))
			}
			saved, err := os.ReadFile(filepath.Join(dir, "continuous_script.txt"))
			if err != nil || string(saved) != body {
				t.Fatalf("body changed/lost: %s %v", saved, err)
			}
			pkgRaw, err := os.ReadFile(filepath.Join(dir, "publishing_package.json"))
			if err != nil {
				t.Fatal(err)
			}
			var pkg remixDraft
			if err = json.Unmarshal(pkgRaw, &pkg); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pkg.ShortTitles, draft.ShortTitles) || !reflect.DeepEqual(pkg.Descriptions, draft.Descriptions) || !reflect.DeepEqual(pkg.Topics, draft.Topics) {
				t.Fatalf("publishing fields changed: %+v", pkg)
			}
			if _, err = os.Stat(filepath.Join(dir, "self_check.json")); !os.IsNotExist(err) {
				t.Fatal("mechanical check artifact still produced")
			}
			for _, m := range client.requests[0].Messages {
				if strings.Contains(m.Content, "本次机械阈值") {
					t.Fatal("mechanical thresholds still injected")
				}
			}
		})
	}
}

func TestHumanReviewAcceptsShortRevisionAndPreservesBefore(t *testing.T) {
	before := strings.Repeat("原文2026年有一段需要人工判断的内容。", 30)
	raw, _ := json.Marshal(remixDraft{ContinuousScript: before})
	reply := `{"verdict":"fixed","issues":[{"where":"原段","problem":"重复","fix":"按批注收短"}],"revised":{"continuous_script":"2027年，按批注收短。","short_titles":["短"]}}`
	out := ReviewRemixDraft(ReviewOptions{Client: &reviewerFakeClient{reply: reply}, Source: before, DraftJSON: string(raw), OutputDir: t.TempDir()})
	if out.Record.Verdict != "fixed" || out.RevisedJSON == "" || out.Record.Before == nil || out.Record.Before.ContinuousScript != before {
		t.Fatalf("revision rejected or before lost: %+v", out.Record)
	}
	if len(out.Record.Warnings) > 0 {
		t.Fatalf("mechanical warnings remain: %v", out.Record.Warnings)
	}
}
