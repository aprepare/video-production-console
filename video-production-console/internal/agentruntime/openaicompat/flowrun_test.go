package openaicompat

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type flowFakeClient struct {
	mu       sync.Mutex
	requests []ChatRequest
}

func (c *flowFakeClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	system := req.Messages[0].Content
	switch {
	case strings.Contains(system, "钩子专家"):
		return textResponse(`{"hook":"数字砸脸"}`), nil
	case strings.Contains(system, "链式加工"):
		user := req.Messages[1].Content
		return textResponse(`{"refined":"` + user + `"}`), nil
	default:
		return textResponse(`{"misc":"ok"}`), nil
	}
}

func TestRunFlowAgentsChainsAndInjectsByEdges(t *testing.T) {
	dir := t.TempDir()
	client := &flowFakeClient{}
	specJSON := `{
	  "nodes": [
	    {"id":"source","type":"input","title":"对标原文"},
	    {"id":"hooker","type":"agent","title":"钩子专家节点","config":{"system_prompt":"你是钩子专家。","user_template":"拆这篇：{{source}}","inject_title":"钩子情报"}},
	    {"id":"refiner","type":"agent","title":"链式加工节点","config":{"system_prompt":"你是链式加工agent。","user_template":"用上游产出：{{node:hooker}}","inject_title":"加工情报","inject_rule":"只当参考"}},
	    {"id":"writer","type":"writer","title":"写手"},
	    {"id":"selfcheck","type":"selfcheck","title":"机械自检"},
	    {"id":"final","type":"output","title":"定稿"}
	  ],
	  "edges": [["source","hooker"],["hooker","refiner"],["source","refiner"],["hooker","writer"],["refiner","writer"],["writer","selfcheck"],["selfcheck","final"]]
	}`
	spec, ok := parseFlowSpec(specJSON)
	if !ok {
		t.Fatal("spec parse failed")
	}

	intel := runFlowAgents(client, Options{Model: "m-main"}, "原文正文内容", dir, spec)

	if !strings.Contains(intel, "〔钩子情报〕") || !strings.Contains(intel, "〔加工情报〕") {
		t.Fatalf("intel missing sections: %s", intel)
	}
	if !strings.Contains(intel, "只当参考：") {
		t.Fatalf("inject rule missing: %s", intel)
	}
	// 链式依赖：refiner 的用户消息里必须带 hooker 的输出
	var refinerUser string
	for _, req := range client.requests {
		if strings.Contains(req.Messages[0].Content, "链式加工") {
			refinerUser = req.Messages[1].Content
		}
	}
	if !strings.Contains(refinerUser, `{"hook":"数字砸脸"}`) {
		t.Fatalf("chained upstream output missing: %s", refinerUser)
	}
	for _, name := range []string{"node_output_hooker.json", "node_output_refiner.json", "intel_summary.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}

func TestRunFlowAgentsSkipsInjectionForUnwiredAgent(t *testing.T) {
	dir := t.TempDir()
	client := &flowFakeClient{}
	specJSON := `{
	  "nodes": [
	    {"id":"source","type":"input","title":"原文"},
	    {"id":"hooker","type":"agent","title":"钩子专家节点","config":{"system_prompt":"你是钩子专家。","user_template":"拆：{{source}}"}},
	    {"id":"sidecar","type":"agent","title":"旁路节点","config":{"system_prompt":"旁路分析。","user_template":"看：{{source}}"}},
	    {"id":"writer","type":"writer","title":"写手"}
	  ],
	  "edges": [["source","hooker"],["source","sidecar"],["hooker","writer"]]
	}`
	spec, _ := parseFlowSpec(specJSON)
	intel := runFlowAgents(client, Options{Model: "m"}, "原文", dir, spec)
	if !strings.Contains(intel, "钩子专家节点") && !strings.Contains(intel, "钩子") {
		t.Fatalf("wired agent missing from intel: %s", intel)
	}
	if strings.Contains(intel, "旁路") {
		t.Fatalf("unwired agent must not inject: %s", intel)
	}
}

func TestResolveFlowFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hook_library.json"), []byte("{\"entries\":[1,2]}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := resolveFlowFiles("前缀 {{file:hook_library.json}} 后缀", dir)
	if got != "前缀 {\"entries\":[1,2]} 后缀" {
		t.Fatalf("resolved = %q", got)
	}

	// 路径穿越只取文件名部分。
	got = resolveFlowFiles("{{file:../../hook_library.json}}", dir)
	if !strings.Contains(got, "{\"entries\":[1,2]}") {
		t.Fatalf("basename resolve failed: %q", got)
	}

	// 文件缺失与目录未配置都不拦运行，替换成说明文字。
	if got = resolveFlowFiles("{{file:missing.json}}", dir); !strings.Contains(got, "读取失败") {
		t.Fatalf("missing file note absent: %q", got)
	}
	if got = resolveFlowFiles("{{file:hook_library.json}}", ""); !strings.Contains(got, "未配置") {
		t.Fatalf("empty dir note absent: %q", got)
	}

	// 没有占位符原样返回。
	if got = resolveFlowFiles("普通文本", dir); got != "普通文本" {
		t.Fatalf("plain text changed: %q", got)
	}
}
