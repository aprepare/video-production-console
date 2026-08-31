package openaicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestChatCompletionsURLJoinsV1(t *testing.T) {
	cases := map[string]string{
		"http://23.138.12.112:2001":                     "http://23.138.12.112:2001/v1/chat/completions",
		"http://23.138.12.112:2001/v1":                  "http://23.138.12.112:2001/v1/chat/completions",
		"http://23.138.12.112:2001/v1/":                 "http://23.138.12.112:2001/v1/chat/completions",
		"http://23.138.12.112:2001/v1/chat/completions": "http://23.138.12.112:2001/v1/chat/completions",
	}
	for input, want := range cases {
		got, err := chatCompletionsURL(input)
		if err != nil {
			t.Fatalf("url %q: %v", input, err)
		}
		if got != want {
			t.Fatalf("url %q = %q, want %q", input, got, want)
		}
	}
}

func TestReadSSEContentJoinsDeltas(t *testing.T) {
	raw := "data: {\"choices\":[{\"delta\":{\"content\":\"又\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"一批人\"}}]}\n\ndata: [DONE]\n"
	got, err := readSSEContent(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got != "又一批人" {
		t.Fatalf("got %q", got)
	}
}

func TestParseRemixDraftReadsJSONOrPlainText(t *testing.T) {
	draft, err := parseRemixDraft("```json\n{\"continuous_script\":\"正文来了正文来了正文来了正文来了正文来了\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if draft.ContinuousScript != "正文来了正文来了正文来了正文来了正文来了" {
		t.Fatalf("draft=%+v", draft)
	}
	plain, err := parseRemixDraft("又一批人要发财了，人民币第三次换锚已经开始。")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain.ContinuousScript, "第三次换锚") {
		t.Fatalf("plain=%+v", plain)
	}
}

func TestParseRemixDraftRecoversTrailingClosingBrace(t *testing.T) {
	raw := "{\"continuous_script\":\"正文来了正文来了正文来了正文来了正文来了\",\"machine\":{\"hook\":\"钩子\"}}\n}"
	draft, err := parseRemixDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.ContinuousScript != "正文来了正文来了正文来了正文来了正文来了" {
		t.Fatalf("structured response leaked into script: %q", draft.ContinuousScript)
	}
}

func TestIsHTTPProtocolError(t *testing.T) {
	if !isHTTPProtocolError(fmt.Errorf(`net/http: HTTP/1.x transport connection broken: malformed HTTP response`)) {
		t.Fatal("protocol mismatch should retry")
	}
	if isHTTPProtocolError(fmt.Errorf(`Post "https://example/v1/chat/completions": unexpected EOF`)) {
		t.Fatal("long-request EOF should not be treated as a protocol mismatch")
	}
}

func TestChatRequestOmitsReasoningEffort(t *testing.T) {
	raw, err := json.Marshal(ChatRequest{
		Model:    "cursor-grok-4.6-xhigh-fast",
		Messages: []Message{{Role: "user", Content: "你好"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "reasoning") || strings.Contains(string(raw), "effort") {
		t.Fatalf("request must not send thinking intensity: %s", raw)
	}
}

func TestDecodeChatResponseReadsArrayContentAndReasoning(t *testing.T) {
	arrayBody := []byte(`{"choices":[{"message":{"role":"assistant","content":[{"type":"text","text":"{\"reply\":\"好\",\"proposals\":[]}"}]}}]}`)
	got, err := decodeChatResponse(arrayBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Choices) != 1 || !strings.Contains(got.Choices[0].Message.Content, `"reply":"好"`) {
		t.Fatalf("array content=%+v", got)
	}

	reasonBody := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"{\"reply\":\"想完了\",\"proposals\":[]}"}}]}`)
	got, err = decodeChatResponse(reasonBody)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Choices[0].Message.Content, "想完了") {
		t.Fatalf("reasoning fallback=%+v", got)
	}

	objectReason := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"reasoning":{"summary":"{\"reply\":\"对象思考\",\"proposals\":[]}"}}}]}`)
	got, err = decodeChatResponse(objectReason)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Choices[0].Message.Content, "对象思考") {
		t.Fatalf("reasoning object=%+v", got)
	}
}

func TestChatRequestIncludesReasoningEffortWhenSet(t *testing.T) {
	raw, err := json.Marshal(ChatRequest{
		Model:           "gpt-5.6-sol",
		ReasoningEffort: "high",
		Messages:        []Message{{Role: "user", Content: "你好"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reasoning_effort":"high"`) {
		t.Fatalf("request missing reasoning_effort: %s", raw)
	}
}
