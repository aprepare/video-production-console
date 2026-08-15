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
	draft := parseRemixDraft("```json\n{\"continuous_script\":\"正文来了正文来了正文来了正文来了正文来了\"}\n```")
	if draft.ContinuousScript != "正文来了正文来了正文来了正文来了正文来了" {
		t.Fatalf("draft=%+v", draft)
	}
	plain := parseRemixDraft("又一批人要发财了，人民币第三次换锚已经开始。")
	if !strings.Contains(plain.ContinuousScript, "第三次换锚") {
		t.Fatalf("plain=%+v", plain)
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
