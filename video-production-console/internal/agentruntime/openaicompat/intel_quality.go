package openaicompat

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func factsTimeContext() string {
	return "核查日期：" + time.Now().Format("2006-01-02") + "。原文对应时期：待从材料确认，未知标source_as_of=unknown；不要把原文的今年直接替换成当前年或上一年。\n\n"
}

// FactsReadinessProblem distinguishes a completed response from a usable fact
// conclusion. It is also used when displaying older run artifacts.
func FactsReadinessProblem(raw string) string {
	normalized := normalizeFacts(raw)
	if normalized == unavailableFacts {
		return "事实核查未完成：未获得有效结论，仅有搜索计划、空结果或请求失败。"
	}
	for _, f := range checkedFacts(normalized) {
		if f.Status == "成立" || f.Status == "纠错" || f.Status == "已过时" {
			return ""
		}
	}
	return "事实核查待处理：尚无带来源日期和链接或正确算式的核准事实。"
}

func isFactsNode(node flowNode) bool {
	return node.ID == "facts" || strings.Contains(node.Config.SystemPrompt, "source_facts")
}

func (s flowSpec) requiresFacts() bool {
	for _, node := range s.agents() {
		if isFactsNode(node) {
			return true
		}
	}
	return false
}

func boundedFactsClient(client ChatClient) ChatClient {
	if original, ok := client.(*HTTPChatClient); ok {
		copy := *original
		httpClient := &http.Client{Timeout: 3 * time.Minute}
		if original.HTTPClient != nil {
			clone := *original.HTTPClient
			httpClient = &clone
			if httpClient.Timeout == 0 || httpClient.Timeout > 3*time.Minute {
				httpClient.Timeout = 3 * time.Minute
			}
		}
		copy.HTTPClient = httpClient
		return &copy
	}
	return client
}

// Retry malformed facts once, without retrying transport failures or inventing
// search results. Optional hook/ammo calls retain their original behavior.
func chatIntel(client ChatClient, request ChatRequest, facts bool) (ChatResponse, error) {
	response, err := client.Chat(request)
	if !facts || err != nil {
		return response, err
	}
	if len(response.Choices) > 0 && normalizeFacts(response.Choices[0].Message.Content) != unavailableFacts {
		return response, nil
	}
	request.Messages = append(append([]Message(nil), request.Messages...), Message{Role: "user", Content: "上次未返回有效事实结论。请仅输出最终source_facts JSON；没有查证结果的项目标needs_verify或查不到，不输出检索计划，不编证据。"})
	return client.Chat(request)
}

func recordFactsOutcome(outcome *intelAgentOutcome, dir string) {
	raw := outcome.content
	if raw != "" {
		_ = os.WriteFile(filepath.Join(dir, "facts_research_raw.txt"), []byte(raw), 0644)
	}
	outcome.content = normalizeFacts(raw)
	_ = os.WriteFile(filepath.Join(dir, "facts_research.json"), []byte(outcome.content), 0644)
	if outcome.Error == "" {
		outcome.Error = FactsReadinessProblem(outcome.content)
	}
}

func preserveDraft(dir, content string) {
	draft, err := parseRemixDraft(content)
	if err != nil || strings.TrimSpace(draft.ContinuousScript) == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "continuous_script.txt"), []byte(draft.ContinuousScript), 0644)
	if raw, err := json.Marshal(draft); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "draft_v1.json"), raw, 0644)
	}
}

func compactAmmo(raw string) string {
	objects := completeJSONObjects(raw)
	if len(objects) == 0 {
		return clipRunes(raw, 0, 600)
	}
	var input map[string]json.RawMessage
	_ = json.Unmarshal([]byte(objects[len(objects)-1]), &input)
	output := map[string][]json.RawMessage{}
	for field, max := range map[string]int{"banned_imagery": 6, "center_options": 1, "scenes": 1, "phrase_swaps": 3} {
		var items []json.RawMessage
		_ = json.Unmarshal(input[field], &items)
		kept := []json.RawMessage{}
		for _, item := range items {
			if len(kept) == max {
				break
			}
			if len([]rune(string(item))) > 120 {
				continue
			}
			kept = append(kept, item)
		}
		output[field] = kept
	}
	result, err := json.Marshal(output)
	if err != nil {
		return "{}"
	}
	return string(result)
}
