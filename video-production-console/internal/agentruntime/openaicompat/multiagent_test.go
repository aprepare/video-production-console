package openaicompat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scriptedIntelClient 按系统提示词里的角色关键字返回对应情报，记录收到的请求。
type scriptedIntelClient struct {
	mu       sync.Mutex
	requests []ChatRequest
	failHook bool
}

func (c *scriptedIntelClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	system := ""
	if len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}
	reply := ""
	switch {
	case strings.Contains(system, "二创策划"):
		if c.failHook {
			return ChatResponse{}, errors.New("hook agent down")
		}
		reply = testWritingPlan
	case strings.Contains(system, "事实核查员"):
		reply = `{"source_facts":[{"claim":"利率","value":"0.95%","status":"成立","source":{"url":"https://www.icbc.com.cn/example","date":"2026-09-01"}}],"fresh_ammo":[]}`
	case strings.Contains(system, "可选表达建议"):
		reply = `{"banned_imagery":["搬家"],"center_options":["赶集"]}`
	default:
		reply = `{"continuous_script":"写手成稿"}`
	}
	var resp ChatResponse
	resp.Choices = append(resp.Choices, struct {
		Message Message `json:"message"`
	}{Message: Message{Role: "assistant", Content: reply}})
	return resp, nil
}

func TestRunIntelPhaseWritesArtifactsAndBuildsIntelBlock(t *testing.T) {
	dir := t.TempDir()
	client := &scriptedIntelClient{}
	intel := runIntelPhase(client, Options{Model: "main-m"}, "原文正文", dir)

	for _, section := range []string{"二创策划", "core_question", "comment", "course_bridge", "ending_action"} {
		if !strings.Contains(intel, section) {
			t.Fatalf("intel missing %q:\n%s", section, intel)
		}
	}
	for _, file := range []string{"hook_analysis.json", "intel_summary.json"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Fatalf("artifact %s missing: %v", file, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "intel_summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		SearchUsed bool `json:"search_used"`
		Agents     []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	// 默认只有策划，不再隐式请求事实或弹药。
	if summary.SearchUsed {
		t.Fatal("search_used should be false without a search channel")
	}
	if len(summary.Agents) != 1 || summary.Agents[0].Name != "hook" {
		t.Fatalf("want only planner, got %+v", summary.Agents)
	}
	offlineSeen := false
	client.mu.Lock()
	for _, req := range client.requests {
		if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "离线事实核查员") {
			offlineSeen = true
		}
	}
	client.mu.Unlock()
	if offlineSeen {
		t.Fatal("default planner must not invoke an offline facts agent")
	}
}

func TestRunIntelPhaseDoesNotImplicitlyUseConfiguredSearchClient(t *testing.T) {
	dir := t.TempDir()
	main := &scriptedIntelClient{}
	search := &scriptedIntelClient{}
	intel := runIntelPhase(main, Options{
		Model: "main-m", SearchClient: search, SearchModel: "grok-search",
	}, "原文正文", dir)
	if intel == "" {
		t.Fatal("intel should not be empty")
	}
	search.mu.Lock()
	defer search.mu.Unlock()
	if len(search.requests) != 0 {
		t.Fatalf("configured search must stay unused in planner flow, got %d", len(search.requests))
	}
}

func TestRunIntelPhaseSurvivesPlannerFailure(t *testing.T) {
	dir := t.TempDir()
	client := &scriptedIntelClient{failHook: true}
	intel := runIntelPhase(client, Options{Model: "main-m"}, "原文正文", dir)
	if intel != "" {
		t.Fatalf("writer should proceed without failed planner output: %s", intel)
	}
	if _, err := os.Stat(filepath.Join(dir, "hook_analysis.json")); !os.IsNotExist(err) {
		t.Fatal("failed agent should not write an artifact")
	}
}

func TestInjectIntelBeforeSourceMarker(t *testing.T) {
	user := "写作指令在这里。\n{{NOTES}}\n# 同行原文\n原文……"
	got := injectIntel(user, "【情报包】内容")
	idxIntel := strings.Index(got, "【情报包】")
	idxSource := strings.Index(got, "# 同行原文")
	if idxIntel < 0 || idxSource < 0 || idxIntel > idxSource {
		t.Fatalf("intel should sit before the source marker:\n%s", got)
	}
	if injectIntel(user, "  ") != user {
		t.Fatal("blank intel should leave user untouched")
	}
}

func TestCapIntelSectionTruncates(t *testing.T) {
	long := strings.Repeat("字", intelSectionRuneCap+100)
	got := capIntelSection(long)
	if !strings.Contains(got, "已截断") {
		t.Fatal("expected truncation notice")
	}
	if len([]rune(got)) > intelSectionRuneCap+80 {
		t.Fatalf("capped section still too long: %d runes", len([]rune(got)))
	}
}
