package remixlab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/store"
)

func TestAgentSettingsNeverExposeKey(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, t.TempDir(), fastStubRunner, nil, nil)
	view, err := svc.PutAgentSettings(AgentSettingsInput{Model: "claude-sonnet-4-6", APIKey: "sk-agent-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Model != "claude-sonnet-4-6" || !view.APIKeyConfigured {
		t.Fatalf("%+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-") {
		t.Fatalf("leaked key: %s", raw)
	}
}

func TestAgentChatRequiresConfirmBeforeMutating(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dataRoot := t.TempDir()
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, dataRoot, fastStubRunner, nil, nil)
	if _, err := svc.PutAgentSettings(AgentSettingsInput{Model: "agent-model"}); err != nil {
		t.Fatal(err)
	}
	exp, err := svc.CreateExperiment(context.Background(), "批注给智能体看的原文", []SlotInput{{Model: "m", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitExperimentTerminal(t, svc, exp.ID)
	if err := svc.PatchComment(context.Background(), got.Runs[0].ID, "开头不够狠"); err != nil {
		t.Fatal(err)
	}
	svc.agentChat = func(baseURL, apiKey string, req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
		if apiKey != "sk-runtime" {
			t.Fatalf("apiKey=%q", apiKey)
		}
		if !req.Stream {
			t.Fatal("agent chat must stream so the gateway does not idle-timeout")
		}
		if !strings.Contains(req.Messages[1].Content, "elder_stable") {
			t.Fatalf("user context missing library: %q", req.Messages[1].Content)
		}
		if !strings.Contains(req.Messages[1].Content, "SYSTEM:") || !strings.Contains(req.Messages[1].Content, "成功标准") {
			t.Fatalf("user context missing prompt full text")
		}
		if !strings.Contains(req.Messages[1].Content, "开头不够狠") {
			t.Fatalf("user context missing comment: %q", req.Messages[1].Content)
		}
		if !strings.Contains(req.Messages[1].Content, "【历史实验】") || !strings.Contains(req.Messages[1].Content, "[当前打开]") {
			t.Fatalf("user context missing experiment history: %q", req.Messages[1].Content)
		}
		if !strings.Contains(req.Messages[1].Content, "成稿：") {
			t.Fatalf("user context missing historical draft: %q", req.Messages[1].Content)
		}
		body := `{"reply":"先改一版","proposals":[{"type":"upsert_prompt","summary":"加一条试验提示词","payload":{"name":"批注回流3","system":"系统3","user":"用户3 {{SOURCE}}","stamp":"批注回流3"}}]}`
		raw, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": body}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var resp openaicompat.ChatResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		return resp, nil
	}
	chat, err := svc.AgentChat(context.Background(), "按批注加一条更狠的", exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Proposals) != 1 || chat.Proposals[0].Type != "upsert_prompt" {
		t.Fatalf("%+v", chat)
	}
	list, err := (Store{DataRoot: dataRoot}).ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Name == "批注回流3" {
			t.Fatal("prompt mutated before confirm")
		}
	}
	confirmed, err := svc.ConfirmProposal(context.Background(), chat.Proposals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Type != "upsert_prompt" {
		t.Fatalf("%+v", confirmed)
	}
	found := false
	list, err = (Store{DataRoot: dataRoot}).ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Name == "批注回流3" && p.System == "系统3" {
			found = true
		}
	}
	if !found {
		t.Fatal("confirmed prompt missing")
	}
	last, ok, err := svc.GetAgentLast()
	if err != nil || !ok {
		t.Fatalf("last ok=%v err=%v", ok, err)
	}
	if last.Message != "按批注加一条更狠的" || last.Reply != "先改一版" || last.Error != "" {
		t.Fatalf("last=%+v", last)
	}
}

func TestAgentChatReadsUncommentedHistoricalExperiments(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dataRoot := t.TempDir()
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, dataRoot, fastStubRunner, nil, nil)
	if _, err := svc.PutAgentSettings(AgentSettingsInput{Model: "agent-model"}); err != nil {
		t.Fatal(err)
	}
	oldExp, err := svc.CreateExperiment(context.Background(), "历史经验原文甲", []SlotInput{{Model: "old-model", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	oldGot := waitExperimentTerminal(t, svc, oldExp.ID)
	if err := svc.PatchComment(context.Background(), oldGot.Runs[0].ID, "数字别改"); err != nil {
		t.Fatal(err)
	}
	openExp, err := svc.CreateExperiment(context.Background(), "当前打开原文乙", []SlotInput{{Model: "new-model", RunCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitExperimentTerminal(t, svc, openExp.ID)

	var userContent string
	svc.agentChat = func(baseURL, apiKey string, req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
		userContent = req.Messages[len(req.Messages)-1].Content
		body := `{"reply":"对照历史","proposals":[]}`
		raw, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": body}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var resp openaicompat.ChatResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		return resp, nil
	}
	if _, err := svc.AgentChat(context.Background(), "对照历史实验总结经验", ""); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"历史经验原文甲", "当前打开原文乙", "数字别改", "stub-script", "[历史]", "old-model", "new-model"} {
		if !strings.Contains(userContent, needle) {
			t.Fatalf("compose chat missing %q in %q", needle, userContent)
		}
	}

	if _, err := svc.AgentChat(context.Background(), "对照当前这篇", openExp.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(userContent, "[当前打开]") || !strings.Contains(userContent, "当前打开原文乙") {
		t.Fatalf("focused chat missing open experiment: %q", userContent)
	}
	if !strings.Contains(userContent, "历史经验原文甲") || !strings.Contains(userContent, "数字别改") {
		t.Fatalf("focused chat dropped historical experiment: %q", userContent)
	}
}

func TestAgentChatCarriesConversationHistory(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dataRoot := t.TempDir()
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, dataRoot, fastStubRunner, nil, nil)
	if _, err := svc.PutAgentSettings(AgentSettingsInput{Model: "agent-model"}); err != nil {
		t.Fatal(err)
	}
	var lastMessages []openaicompat.Message
	svc.agentChat = func(baseURL, apiKey string, req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
		lastMessages = req.Messages
		body := `{"reply":"第一轮结论","proposals":[{"type":"set_active_prompt","summary":"设为默认","payload":{"id":"elder_stable"}}]}`
		raw, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": body}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var resp openaicompat.ChatResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		return resp, nil
	}
	if _, err := svc.AgentChat(context.Background(), "先分析批注", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AgentChat(context.Background(), "接着上一轮继续", ""); err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, m := range lastMessages {
		joined.WriteString(m.Role + ":" + m.Content + "\n")
	}
	if !strings.Contains(joined.String(), "先分析批注") {
		t.Fatalf("second request missing first user turn: %s", joined.String())
	}
	if !strings.Contains(joined.String(), "第一轮结论") || !strings.Contains(joined.String(), "设为默认") {
		t.Fatalf("second request missing first assistant turn: %s", joined.String())
	}
	if lastMessages[0].Role != "system" {
		t.Fatalf("first message role=%q", lastMessages[0].Role)
	}
	turns, err := svc.GetAgentHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 4 || turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Fatalf("turns=%+v", turns)
	}
	if len(turns[1].Proposals) != 1 || turns[1].Proposals[0].Summary != "设为默认" {
		t.Fatalf("assistant turn proposals=%+v", turns[1].Proposals)
	}
	if err := svc.ClearAgentHistory(); err != nil {
		t.Fatal(err)
	}
	turns, err = svc.GetAgentHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Fatalf("after clear turns=%+v", turns)
	}
}

func TestParseAgentReplyStripsPrefixAndTruncation(t *testing.T) {
	raw := `I{"reply":"先改一版","proposals":[{"type":"upsert_prompt","summary":"补篇幅","payload":{"id":"elder_stable","name":"中老年定稿","system":"完整稿"}},{"type":"upsert_prompt","summary":"锁课名","payload"`
	got, err := parseAgentReply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reply != "先改一版" {
		t.Fatalf("reply=%q", got.Reply)
	}
	if len(got.Proposals) != 1 || got.Proposals[0].Type != "upsert_prompt" || got.Proposals[0].Summary != "补篇幅" {
		t.Fatalf("proposals=%+v", got.Proposals)
	}
}

func TestGetAgentLastRepairsWrappedJSONDump(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/console.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dataRoot := t.TempDir()
	svc := NewService(store.NewRemixLabRepository(db), stubRuntime{view: RuntimeView{RemixAPIKey: "sk-runtime"}}, remixFakeProtector{}, dataRoot, fastStubRunner, nil, nil)
	svc.rememberAgentLast(AgentLastView{
		Message: "分析批注",
		Reply:   `I{"reply":"依据批注改篇幅","proposals":[{"type":"upsert_prompt","summary":"补篇幅","payload":{"id":"elder_stable","name":"中老年定稿"}},{"type":"upsert_prompt","summary":"锁课名","payload"`,
	})
	last, ok, err := svc.GetAgentLast()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if last.Reply != "依据批注改篇幅" || len(last.Proposals) != 1 || last.Proposals[0].Summary != "补篇幅" {
		t.Fatalf("last=%+v", last)
	}
	if strings.TrimSpace(last.Proposals[0].ID) == "" {
		t.Fatal("proposal id missing")
	}
}
