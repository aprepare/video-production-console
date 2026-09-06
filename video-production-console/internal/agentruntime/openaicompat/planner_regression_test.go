package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testWritingPlan = `{"core_question":"这次变化与过去有什么不同","opening_beats":["变化","观众关联","不同之处"],"body_beats":["历史对照","当前差别","回应开头"],"comment":{"after":"历史对照","question":"你更关心哪一个？","response_hint":"留言选一项"},"course_bridge":{"need":"理清家庭情况","lessons":["收入支出负债梳理"],"use":"看清家底"},"ending_action":"主页橱窗找到课程"}`

type plannerRunClient struct {
	requests []ChatRequest
	draft    string
}

func (c *plannerRunClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.requests = append(c.requests, req)
	system := req.Messages[0].Content
	if strings.Contains(system, "你是财经口播二创策划") {
		return textResponse(testWritingPlan), nil
	}
	if strings.Contains(system, "你是财经口播终审编辑") {
		return textResponse(`{"verdict":"pass","issues":[]}`), nil
	}
	return textResponse(c.draft), nil
}

func TestPlannerRuntimeDeliversWithoutImplicitFactsGate(t *testing.T) {
	for _, useFlow := range []bool{false, true} {
		name := "default_pipeline"
		if useFlow {
			name = "workflow"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sourcePath := filepath.Join(root, "source.txt")
			if err := os.WriteFile(sourcePath, []byte("又一批人要发财了。人民币要第三次换锚。"), 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(root, "output")
			manifest, _ := json.Marshal(map[string]any{"task_id": "planner-test", "action": "remix.standard", "output_dir": output,
				"inputs": []any{map[string]any{"type": "source_script", "role": "primary_source", "path": sourcePath}}})
			manifestPath := filepath.Join(root, "task_manifest.json")
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			client := &plannerRunClient{draft: currentPublishFixture(t)}
			opts := Options{ManifestPath: manifestPath, SkillRoot: root, OutputLastMessage: filepath.Join(root, "last.json"),
				Model: "writer", BaseURL: "http://example.invalid/v1", APIKey: "test", Client: client, Pipeline: PipelineMultiAgent, ReviewerEnabled: true}
			if useFlow {
				opts.ReasoningEffort = "high"
				opts.ServiceTier = "priority"
				spec := flowSpec{Nodes: []flowNode{{ID: "source", Type: "input"}, {ID: "hook", Type: "agent", Title: "二创策划", Config: flowNodeConfig{SystemPrompt: hookAgentSystem, Model: "planner-choice", ReasoningEffort: "medium", ServiceTier: "default"}},
					{ID: "writer", Type: "writer"}, {ID: "review", Type: "reviewer", Config: flowNodeConfig{Model: "reviewer-choice", ReasoningEffort: "low", ServiceTier: "priority"}}}, Edges: [][2]string{{"source", "hook"}, {"hook", "writer"}}}
				raw, _ := json.Marshal(spec)
				opts.WorkflowJSON = string(raw)
			}
			if err := Run(opts); err != nil {
				t.Fatal(err)
			}
			last, err := os.ReadFile(opts.OutputLastMessage)
			if err != nil || !strings.Contains(string(last), `"completed"`) {
				t.Fatalf("not delivered: %s (%v)", last, err)
			}
			ctx := loadEditorialContext(output)
			if ctx.FactsRequired || !strings.Contains(string(ctx.WritingPlan), "这次变化") {
				t.Fatalf("wrong context: %+v", ctx)
			}
			writerPrompt, _ := os.ReadFile(filepath.Join(output, "prompt_user.txt"))
			if !strings.Contains(string(writerPrompt), "core_question") {
				t.Fatal("planner did not reach writer")
			}
			if len(client.requests) != 3 {
				t.Fatalf("want planner/writer/reviewer only, got %d calls", len(client.requests))
			}
			if useFlow {
				want := [][3]string{{"planner-choice", "medium", "default"}, {"writer", "high", "priority"}, {"reviewer-choice", "low", "priority"}}
				for i, req := range client.requests {
					if req.Model != want[i][0] || req.ReasoningEffort != want[i][1] || req.ServiceTier != want[i][2] {
						t.Fatalf("role %d used wrong request settings: %s/%s/%s", i, req.Model, req.ReasoningEffort, req.ServiceTier)
					}
				}
			}
			for _, req := range client.requests {
				if strings.Contains(req.Messages[0].Content, "你是财经口播终审编辑") && !strings.Contains(req.Messages[1].Content, "这次变化与过去有什么不同") {
					t.Fatal("reviewer did not receive same plan")
				}
			}
		})
	}
}

func TestPlannerDefaultIntelOnlyOneCall(t *testing.T) {
	client, search := &scriptedIntelClient{}, &scriptedIntelClient{}
	runIntelPhase(client, Options{Model: "writer", SearchClient: search, SearchModel: "search"}, "原文", t.TempDir())
	if len(client.requests) != 1 || len(search.requests) != 0 {
		t.Fatalf("default needs only planner, got main=%d search=%d", len(client.requests), len(search.requests))
	}
	system := client.requests[0].Messages[0].Content
	if !strings.Contains(system, "core_question") || !strings.Contains(system, CourseCoreSyllabus) {
		t.Fatal("planner did not receive structure contract and actual syllabus")
	}
}

func TestPlannerReviewerReceivesFrozenPlanWithoutMechanicalAdvisories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node_output_hook.json")
	if err := os.WriteFile(path, []byte(testWritingPlan), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := currentEditorialContext(dir)
	if err := saveEditorialContext(dir, ctx); err != nil {
		t.Fatal(err)
	}
	// Rework must use the captured plan, not a subsequently changed node output.
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(testWritingPlan, "这次变化", "替换后的变化")), 0600); err != nil {
		t.Fatal(err)
	}
	client := &reviewerFakeClient{reply: `{"verdict":"pass","issues":[]}`}
	out := ReviewRemixDraft(ReviewOptions{Client: client, OutputDir: dir, DraftJSON: reviewerTestDraft(), Round: 2})
	if out.Record.Verdict != "pass" {
		t.Fatalf("advisory blocked review: %+v", out.Record)
	}
	user := client.requests[0].Messages[1].Content
	for _, want := range []string{"这次变化与过去有什么不同"} {
		if !strings.Contains(user, want) {
			t.Fatalf("review input missing %q", want)
		}
	}
	if strings.Contains(user, "程序结构提醒") || strings.Contains(user, "替换后的变化") {
		t.Fatal("rework replaced frozen plan")
	}
}

func TestPlannerStructureWarningsDoNotClaimSemanticPass(t *testing.T) {
	ctx := currentEditorialContext("")
	warnings := strings.Join(editorialWarnings("原文", remixDraft{ContinuousScript: "这条内容讲完了。点个关注。"}, ctx), "\n")
	if !strings.Contains(warnings, "评论引导待检查") || !strings.Contains(warnings, "课尾购买动作待检查") {
		t.Fatalf("missing nonblocking structural hints: %s", warnings)
	}
	warnings = strings.Join(editorialWarnings("原文", remixDraft{ContinuousScript: "你更关心哪一个？评论区说说。课在主页橱窗，点开看看。"}, ctx), "\n")
	if strings.Contains(warnings, "评论引导待检查") || strings.Contains(warnings, "课尾购买动作待检查") {
		t.Fatalf("clear interaction and ending incorrectly flagged: %s", warnings)
	}
}

func TestPlannerReaderUsesFinalPlanAndOmitsLegacyInstructions(t *testing.T) {
	dir := t.TempDir()
	final := strings.Replace(testWritingPlan, "这次变化", "最终变化", 1)
	final = strings.TrimSuffix(final, "}") + `,"replication_guide":"旧的禁止数字指令"}`
	if err := os.WriteFile(filepath.Join(dir, "node_output_hook.json"), []byte(testWritingPlan+"\n"+final), 0600); err != nil {
		t.Fatal(err)
	}
	plan := string(readWritingPlan(dir))
	if !strings.Contains(plan, "最终变化") || strings.Contains(plan, "replication_guide") {
		t.Fatalf("wrong review plan: %s", plan)
	}
}
