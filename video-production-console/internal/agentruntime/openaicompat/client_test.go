package openaicompat

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTP2ClientPinsPartnerCAFromEnv(t *testing.T) {
	caPEM := testCAPEM(t)
	caFile := filepath.Join(t.TempDir(), "partner-ca.crt")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIDEO_CONSOLE_PARTNER_CA_FILE", caFile)
	client := http2Client()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("remix HTTP client must pin the partner CA")
	}
	if got := transport.TLSClientConfig.ServerName; got != "23.138.12.112" {
		t.Fatalf("server name=%q", got)
	}
}

func TestHTTP2ClientUsesSystemRootsWithoutPartnerCA(t *testing.T) {
	t.Setenv("VIDEO_CONSOLE_PARTNER_CA_FILE", "")
	client := http2Client()
	if client.Transport != nil {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil && transport.TLSClientConfig != nil && transport.TLSClientConfig.RootCAs != nil {
			t.Fatal("owner remix client must not pin a partner CA")
		}
	}
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "openaicompat-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

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
