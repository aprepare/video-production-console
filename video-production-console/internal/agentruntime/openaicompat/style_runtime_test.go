package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniversalFlowInjectsBriefAndKeepsMode(t *testing.T) {
	for _, placeholders := range []bool{true, false} {
		name := "fallback"
		if placeholders {
			name = "placeholders"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sourcePath, output := filepath.Join(root, "source.txt"), filepath.Join(root, "output")
			if err := os.WriteFile(sourcePath, []byte("海鲜当天到货。"), 0600); err != nil {
				t.Fatal(err)
			}
			manifest, _ := json.Marshal(map[string]any{"action": "remix.standard", "output_dir": output, "inputs": []map[string]string{{"type": "source_script", "path": sourcePath}}})
			manifestPath := filepath.Join(root, "manifest.json")
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			writerUser, plannerUser, reviewerUser := "# 同行原文\n{{SOURCE}}", "{{source}}", "{{source}}\n{{draft}}"
			if placeholders {
				writerUser = "{{BRIEF}}\n" + writerUser
				plannerUser += "\n{{brief}}"
				reviewerUser += "\n{{brief}}"
			}
			wf, _ := json.Marshal(map[string]any{
				"style": "local_store", "editorial_rules": "只依据材料写门店介绍。",
				"brief": map[string]any{"industry": "海鲜餐饮", "selling_points": "双人套餐128元", "conversion": "团购链接", "duration_s": 60, "banned": "包治百病"},
				"nodes": []map[string]any{
					{"id": "source", "type": "input"},
					{"id": "hook", "type": "agent", "config": map[string]string{"system_prompt": "门店策划", "user_template": plannerUser}},
					{"id": "writer", "type": "writer", "config": map[string]string{"system_prompt": "门店写手", "user_template": writerUser}},
					{"id": "review", "type": "reviewer", "config": map[string]string{"system_prompt": "门店审稿", "user_template": reviewerUser}},
				}, "edges": [][2]string{{"source", "hook"}, {"hook", "writer"}, {"writer", "review"}},
			})
			draft := `{"continuous_script":"双人套餐128元，包治百病。点击团购链接。","titles":[],"short_titles":["海鲜套餐怎么选","双人晚餐吃什么","今天到货的海鲜"],"descriptions":["海鲜当天到货，看看双人套餐。","双人晚餐怎么选，听店主说说。"],"topics":["#美食","#海鲜","#双人套餐"],"cta":"点击团购链接。"}`
			client := &sequenceClient{responses: []string{`{"core_question":"海鲜怎么选"}`, draft, `{"verdict":"pass","summary":"可用","issues":[]}`}}
			if err := Run(Options{ManifestPath: manifestPath, OutputLastMessage: filepath.Join(root, "last.json"), BaseURL: "http://example.invalid/v1", APIKey: "test", Model: "writer", Client: client, WorkflowJSON: string(wf)}); err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 3 {
				t.Fatalf("calls = %d", len(client.requests))
			}
			for i, req := range client.requests {
				user := req.Messages[1].Content
				if !strings.Contains(user, "双人套餐128元") || !strings.Contains(user, "目标时长约 60 秒") || strings.Contains(user, "{{BRIEF}}") || strings.Contains(user, "{{brief}}") {
					t.Errorf("request %d missing brief: %s", i, user)
				}
				if strings.Contains(req.Messages[0].Content, "财富觉醒方法论") {
					t.Errorf("request %d leaked finance policy", i)
				}
			}
			if !strings.Contains(client.requests[1].Messages[0].Content, "禁用表达：包治百病") {
				t.Error("writer missing banned words")
			}
			if user := client.requests[1].Messages[1].Content; strings.Index(user, "双人套餐128元") > strings.Index(user, "# 同行原文") {
				t.Error("writer brief must precede the source heading")
			}
			var analysis struct {
				CTA map[string]string `json:"cta"`
			}
			readJSONFixture(t, filepath.Join(output, "viral_analysis.json"), &analysis)
			if analysis.CTA["conversion"] != "团购链接" || analysis.CTA["energy"] != "点击团购链接。" || analysis.CTA["course"] != "" || analysis.CTA["entry"] != "" {
				t.Errorf("universal CTA = %+v", analysis.CTA)
			}
			var ctx struct {
				Style     string `json:"style"`
				BriefHash string `json:"brief_hash"`
			}
			readJSONFixture(t, filepath.Join(output, "editorial_context.json"), &ctx)
			if ctx.Style != "local_store" || len(ctx.BriefHash) != 12 {
				t.Errorf("context attribution = %+v", ctx)
			}
			var review ReviewRecord
			readJSONFixture(t, filepath.Join(output, "review.json"), &review)
			if strings.Join(review.Warnings, "") != "正文出现禁用词：包治百病" {
				t.Errorf("banned warning = %v", review.Warnings)
			}
			var log struct {
				Events []map[string]any `json:"events"`
			}
			readJSONFixture(t, filepath.Join(output, "remix_run.json"), &log)
			found := false
			for _, e := range log.Events {
				if e["event"] == "prompt_selected" {
					found = e["style"] == "local_store"
				}
			}
			if !found {
				t.Error("prompt_selected missing style")
			}
		})
	}
}

func readJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatal(err)
	}
}

func TestUniversalFlowWithoutBriefOrRulesHasNeutralFallbacks(t *testing.T) {
	for _, style := range []string{PromptStyleRewrite, PromptStyleCopy} {
		t.Run(style, func(t *testing.T) {
			root := t.TempDir()
			source, output := filepath.Join(root, "source.txt"), filepath.Join(root, "output")
			if err := os.WriteFile(source, []byte("茶叶的产地与做法。"), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest, _ := json.Marshal(map[string]any{"action": "remix.standard", "output_dir": output, "inputs": []map[string]string{{"type": "source_script", "path": source}}, "non_secret_settings": map[string]string{"remix_prompt_style": style}})
			path := filepath.Join(root, "manifest.json")
			if err := os.WriteFile(path, manifest, 0o600); err != nil {
				t.Fatal(err)
			}
			draft := `{"continuous_script":"茶叶的产地与做法，关注了解更多。","short_titles":[],"descriptions":[],"topics":[],"cta":"关注了解更多。"}`
			client := &sequenceClient{responses: []string{draft}}
			if err := Run(Options{ManifestPath: path, OutputLastMessage: filepath.Join(root, "last.json"), BaseURL: "http://example.invalid/v1", APIKey: "test", Model: "writer", Client: client, WorkflowJSON: `{"style":"knowledge","nodes":[{"id":"writer","type":"writer"}]}`}); err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 1 {
				t.Fatalf("unexpected legacy helper calls: %d", len(client.requests))
			}
			req := client.requests[0]
			for _, needle := range []string{"财富觉醒方法论", "财经", "【内容简报", "{{BRIEF}}"} {
				if strings.Contains(req.Messages[0].Content+req.Messages[1].Content, needle) {
					t.Fatalf("empty generic workflow leaked %q", needle)
				}
			}
			if !strings.Contains(req.Messages[0].Content, WriterJSONContract) {
				t.Fatal("missing neutral JSON contract")
			}
			var analysis struct {
				CTA map[string]string `json:"cta"`
			}
			readJSONFixture(t, filepath.Join(output, "viral_analysis.json"), &analysis)
			if len(analysis.CTA) != 2 || analysis.CTA["conversion"] != "" || analysis.CTA["energy"] != "关注了解更多。" {
				t.Fatalf("CTA = %+v", analysis.CTA)
			}
		})
	}
}
