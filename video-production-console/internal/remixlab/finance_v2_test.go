package remixlab

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
)

func TestFinanceV2UsesUnchangedSkillPrompt(t *testing.T) {
	raw, err := os.ReadFile("stylefiles/finance_v2_writer.txt")
	if err != nil {
		t.Fatal(err)
	}
	if hash := fmt.Sprintf("%x", sha256.Sum256(raw)); !strings.HasPrefix(hash, "cf7b8b4d4ad44b6c") {
		t.Fatalf("skill prompt changed: %s", hash)
	}
	styles := BuiltinStyles()
	style := styles[len(styles)-1]
	if style.ID != "finance_v2" || !style.Finance || !style.Builtin {
		t.Fatalf("v2 must be appended: %+v", style)
	}
	if style.Name != "视频号爆款二创 v2" || style.Category != "财经卖课" || style.Planner != "" || style.Reviewer != "" || style.Rules != "" {
		t.Fatalf("v2 metadata: %+v", style)
	}
	body, _, found := strings.Cut(string(raw), "【本次补充要求】")
	if !found || !strings.HasPrefix(style.Writer, strings.TrimSpace(body)) {
		t.Fatal("writer did not preserve the full skill body")
	}
	if strings.Contains(style.Writer, "{{NOTES}}") || strings.Contains(style.Writer, "{{SOURCE}}") {
		t.Fatal("writer system still contains input placeholders")
	}
	if style.WriterUser != "【本次补充要求】\n{{NOTES}}\n\n【原文】\n{{SOURCE}}" {
		t.Fatalf("writer input = %q", style.WriterUser)
	}
	for _, want := range []string{"continuous_script", "JSON", "cover_titles", "short_titles", "descriptions", "topics", "cta"} {
		if !strings.Contains(style.Writer[len(strings.TrimSpace(body)):], want) {
			t.Fatalf("writer adapter missing %s", want)
		}
	}
}

type financeV2Client struct {
	requests []openaicompat.ChatRequest
}

func (c *financeV2Client) Chat(req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	c.requests = append(c.requests, req)
	content := `{"continuous_script":"管好家里的钱，先看清自己的收支。现在就去主页橱窗看看这门课。","titles":[],"short_titles":["手里有存款的人别乱动","银行柜台少了一样东西","管好家里的钱才有底气"],"descriptions":["认知#楼市#认知提升#经济趋势","#楼市#认知提升#经济趋势","养老#楼市#认知提升#经济趋势"],"topics":["#楼市","#认知提升","#经济趋势"],"cover_titles":["家里的钱别乱动","先看清自己的账"],"cta":"现在就去主页橱窗看看这门课。"}`
	if len(c.requests) > 1 {
		content = `{"verdict":"pass","summary":"可交付。","issues":[]}`
	}
	raw, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": content}}}})
	var response openaicompat.ChatResponse
	err := json.Unmarshal(raw, &response)
	return response, err
}

func TestFinanceV2RunsWriterAndReviewerWithPublishingFields(t *testing.T) {
	root := t.TempDir()
	st := Store{DataRoot: root}
	view, err := st.ApplyStyleForAccount("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "finance_v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := st.freezeWorkflowWriter(view.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	workflowJSON, _ := json.Marshal(wf)
	sourcePath, output := filepath.Join(root, "source.txt"), filepath.Join(root, "output")
	if err := os.WriteFile(sourcePath, []byte("先看清家庭收支，才知道存款要怎么安排。"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"action": "remix.standard", "output_dir": output, "inputs": []map[string]string{{"type": "source_script", "path": sourcePath}}})
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &financeV2Client{}
	if err := openaicompat.Run(openaicompat.Options{ManifestPath: manifestPath, SkillRoot: root, OutputLastMessage: filepath.Join(root, "last.json"), Client: client, BaseURL: "http://example.invalid/v1", APIKey: "test", Model: "writer", WorkflowJSON: string(workflowJSON)}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("want one writer and one reviewer call, got %d", len(client.requests))
	}
	writer := client.requests[0]
	if !strings.Contains(writer.Messages[0].Content, financeV2Writer()) || strings.Contains(writer.Messages[0].Content, openaicompat.SharedEditorialPolicy) || !strings.Contains(writer.Messages[0].Content, openaicompat.WriterJSONContract) {
		t.Fatal("writer did not retain the v2 system and JSON contracts")
	}
	if !strings.Contains(writer.Messages[1].Content, "【本次补充要求】") || !strings.Contains(writer.Messages[1].Content, "【原文】") || strings.Contains(writer.Messages[1].Content, "{{") {
		t.Fatalf("writer placeholders not resolved: %s", writer.Messages[1].Content)
	}
	if !strings.Contains(client.requests[1].Messages[0].Content, openaicompat.ChannelsPublishingContract) {
		t.Fatal("reviewer did not receive channels rules")
	}
	packagePath := filepath.Join(output, "publishing_package.json")
	packageJSON := buildRunPackageJSON("管好家里的钱。", packagePath)
	var pkg PackageInput
	if err := json.Unmarshal([]byte(packageJSON), &pkg); err != nil || !reflect.DeepEqual(pkg.CoverTitles, []string{"家里的钱别乱动", "先看清自己的账"}) {
		t.Fatalf("run package lost cover titles: %s %v", packageJSON, err)
	}
	if got := publishingCopyExtra(packageJSON)["cover_titles"]; !reflect.DeepEqual(got, pkg.CoverTitles) {
		t.Fatalf("publishing stage lost cover titles: %#v", got)
	}
}

func TestFinanceV2AccountWorkflowAndSwitchBack(t *testing.T) {
	st := Store{DataRoot: t.TempDir()}
	const accountID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	original := DefaultWorkflow(AgentPrompts{ReviewerSystem: "已保存的审稿要求"})
	if err := st.SaveWorkflowForAccount(accountID, original); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyStyleForAccount(accountID, "finance_v2", nil); err != nil {
		t.Fatal(err)
	}
	w, err := st.LoadWorkflowForAccount(accountID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(w)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if string(fields["publish"]) != `"channels_v2"` || !w.FinanceMode() || w.EditorialRules == nil || *w.EditorialRules != "" {
		t.Fatalf("v2 workflow mode/rules not persisted: %s", raw)
	}
	if !reflect.DeepEqual(workflowReviewer(w), workflowReviewer(original)) {
		t.Fatal("v2 replaced the saved reviewer")
	}
	for _, node := range w.Nodes {
		if node.ID == "hook" {
			t.Fatal("v2 retained the planner")
		}
	}
	for _, edge := range w.Edges {
		if edge[0] == "hook" || edge[1] == "hook" {
			t.Fatal("v2 retained planner edges")
		}
	}
	if err := ValidateWorkflow(w); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"finance_course", "universal_remix"} {
		view, err := st.ApplyStyleForAccount(accountID, id, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(view.Workflow)
		fields = nil
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["publish"]; exists {
			t.Fatalf("switch to %s kept publish mode: %s", id, raw)
		}
		if view.Workflow.FinanceMode() != (id == "finance_course") {
			t.Fatalf("switch to %s changed finance mode", id)
		}
	}
}
