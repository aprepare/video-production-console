package openaicompat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type referenceTestClient struct {
	mu       sync.Mutex
	requests []ChatRequest
	ready    chan struct{}
	badModel string
}

func (c *referenceTestClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	if len(c.requests) == 2 && c.ready != nil {
		close(c.ready)
	}
	c.mu.Unlock()
	if c.ready != nil {
		select {
		case <-c.ready:
		case <-time.After(2 * time.Second):
			return ChatResponse{}, errors.New("reference calls were not concurrent")
		}
	}
	if req.Model == c.badModel {
		return textResponse(`{"analysis":"只有提纲，没有文案"}`), nil
	}
	raw, _ := json.Marshal(map[string]any{
		"continuous_script": req.Model + "开头。" + strings.Repeat("正文推进。", 1800) + req.Model + "完整课尾。",
		"titles":            []string{req.Model + "候选标题"}, "descriptions": []string{req.Model + "视频描述"},
		"analysis": map[string]string{"opening": "直接进入矛盾", "middle": "证据推进后接祝福", "ending": "承接问题讲学习价值"},
	})
	return textResponse(string(raw)), nil
}

func referenceTestSpec(t *testing.T) flowSpec {
	t.Helper()
	spec, ok := parseFlowSpec(`{"nodes":[{"id":"source","type":"input"},{"id":"ref_a","type":"agent","title":"参考稿1","config":{"role":"reference","model":"model-a","reasoning_effort":"high","service_tier":"priority","system_prompt":"独立写参考稿","user_template":"{{source}}"}},{"id":"ref_b","type":"agent","title":"参考稿2","config":{"role":"reference","model":"model-b","system_prompt":"独立写参考稿","user_template":"{{source}}"}},{"id":"writer","type":"writer"}],"edges":[["source","ref_a"],["source","ref_b"],["ref_a","writer"],["ref_b","writer"]]}`)
	if !ok {
		t.Fatal("invalid fixture")
	}
	return spec
}

func referenceArchiveFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "reference_drafts", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestReferenceFlowRetainsIndependentFullDraftsAndRetryHistory(t *testing.T) {
	dir := t.TempDir()
	client := &referenceTestClient{ready: make(chan struct{})}
	spec := referenceTestSpec(t)
	packet := runFlowAgents(client, Options{Model: "writer"}, "同一篇原文", dir, spec)
	for _, model := range []string{"model-a", "model-b"} {
		for _, suffix := range []string{"完整课尾。", "候选标题", "视频描述"} {
			if !strings.Contains(packet, model+suffix) {
				t.Fatalf("writer lost %s%s", model, suffix)
			}
		}
	}
	for _, req := range client.requests {
		if req.Messages[1].Content != "同一篇原文" {
			t.Fatal("reference received another candidate or lost original")
		}
		if req.Model == "model-a" && (req.ReasoningEffort != "high" || req.ServiceTier != "priority") {
			t.Fatal("reference model lost independent reasoning or Fast setting")
		}
	}
	files := referenceArchiveFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("want 2 immutable reference records, got %d", len(files))
	}
	before, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	// Resume reuses cached drafts without making more versions or requests.
	runFlowAgents(client, Options{Model: "writer"}, "同一篇原文", dir, spec)
	if len(client.requests) != 2 || len(referenceArchiveFiles(t, dir)) != 2 {
		t.Fatal("resume regenerated saved references")
	}
	// Retrying one node may replace its working file, but must keep old reference records.
	if err = os.Remove(filepath.Join(dir, "node_output_ref_a.json")); err != nil {
		t.Fatal(err)
	}
	runFlowAgents(client, Options{Model: "writer"}, "同一篇原文", dir, spec)
	if len(referenceArchiveFiles(t, dir)) != 3 {
		t.Fatal("retry did not append a reference version")
	}
	after, err := os.ReadFile(files[0])
	if err != nil || string(after) != string(before) {
		t.Fatal("retry changed a prior reference")
	}
}

type referencePipelineClient struct {
	mu       sync.Mutex
	requests []ChatRequest
	draft    string
}

func (c *referencePipelineClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	if req.Model == "reference-a" || req.Model == "reference-b" {
		raw, _ := json.Marshal(ReferenceCopy{ContinuousScript: req.Model + "参考全文及自然课尾", Titles: []string{req.Model + "标题"}, Descriptions: []string{req.Model + "描述"}})
		return textResponse(string(raw)), nil
	}
	if req.Model == "reviewer" {
		return textResponse(`{"verdict":"pass","issues":[]}`), nil
	}
	return textResponse(c.draft), nil
}

func TestReferencePipelineWriterReceivesAllCopiesAndKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	source := "完整原文：资金发生变化，先说清楚原因。"
	sourcePath := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output")
	manifest, _ := json.Marshal(map[string]any{"action": "remix.standard", "output_dir": output, "inputs": []map[string]string{{"type": "source_script", "path": sourcePath}}})
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	spec := flowSpec{Nodes: []flowNode{{ID: "source", Type: "input"}, {ID: "a", Type: "agent", Title: "参考A", Config: flowNodeConfig{Role: ReferenceDraftRole, Model: "reference-a", SystemPrompt: ReferenceSystemPrompt(), UserTemplate: ReferenceUserTemplate}}, {ID: "b", Type: "agent", Title: "参考B", Config: flowNodeConfig{Role: ReferenceDraftRole, Model: "reference-b", SystemPrompt: ReferenceSystemPrompt(), UserTemplate: ReferenceUserTemplate}}, {ID: "writer", Type: "writer"}, {ID: "review", Type: "reviewer", Config: flowNodeConfig{Model: "reviewer"}}}, Edges: [][2]string{{"source", "a"}, {"source", "b"}, {"a", "writer"}, {"b", "writer"}}}
	raw, _ := json.Marshal(spec)
	client := &referencePipelineClient{draft: currentPublishFixture(t)}
	if err := Run(Options{ManifestPath: manifestPath, OutputLastMessage: filepath.Join(dir, "last.json"), BaseURL: "http://local.invalid/v1", APIKey: "fixture", Model: "writer", Client: client, WorkflowJSON: string(raw)}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 4 {
		t.Fatalf("wanted 2 references/writer/reviewer, got %d calls", len(client.requests))
	}
	for _, req := range client.requests {
		if req.Model != "writer" {
			continue
		}
		user := req.Messages[1].Content
		for _, expected := range []string{source, "reference-a参考全文及自然课尾", "reference-b参考全文及自然课尾", "reference-a标题", "reference-b描述"} {
			if !strings.Contains(user, expected) {
				t.Fatalf("writer input lost %q", expected)
			}
		}
	}
	records, err := ReadReferenceDrafts(output)
	if err != nil || len(records) != 2 {
		t.Fatalf("missing persisted references: %d/%v", len(records), err)
	}
	if _, err = os.Stat(filepath.Join(output, "publishing_package.json")); err != nil {
		t.Fatal("final publishing package missing", err)
	}
}

func TestReferenceFlowArchivesInvalidResponseWithoutFeedingItToWriter(t *testing.T) {
	dir := t.TempDir()
	client := &referenceTestClient{ready: make(chan struct{}), badModel: "model-b"}
	packet := runFlowAgents(client, Options{Model: "writer"}, "原文", dir, referenceTestSpec(t))
	if strings.Contains(packet, "只有提纲") {
		t.Fatal("invalid reference was passed to writer")
	}
	if !strings.Contains(packet, "model-a完整课尾。") {
		t.Fatal("one failure blocked the good reference")
	}
	files := referenceArchiveFiles(t, dir)
	if len(files) != 2 {
		t.Fatal("both successful and failed attempts must be retained")
	}
	failed := false
	for _, file := range files {
		var record map[string]any
		raw, _ := os.ReadFile(file)
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record["status"] == "failed" {
			failed = true
			if record["raw"] == "" || record["error"] == "" {
				t.Fatal("failed reference lost explanation/raw response")
			}
		}
	}
	if !failed {
		t.Fatal("invalid reference not marked failed")
	}
}
