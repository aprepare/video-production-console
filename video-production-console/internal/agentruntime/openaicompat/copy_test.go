package openaicompat

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubCopyClient struct {
	mu      sync.Mutex
	calls   []string
	hooks   string
	scripts string
	err     map[string]error
	delay   time.Duration
}

func (s *stubCopyClient) Copy(processType, sourceContent string) (string, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	s.calls = append(s.calls, processType)
	s.mu.Unlock()
	if s.err != nil {
		if err := s.err[processType]; err != nil {
			return "", err
		}
	}
	_ = sourceContent
	switch processType {
	case "hooks":
		return s.hooks, nil
	case "scripts":
		return s.scripts, nil
	default:
		return "", nil
	}
}

func TestFetchCopyMaterialsCallsHooksAndScripts(t *testing.T) {
	client := &stubCopyClient{hooks: "钩子A", scripts: "脚本B", delay: 20 * time.Millisecond}
	hooks, scripts, err := fetchCopyMaterials(client, "全国老百姓存在银行里的钱少了2万亿。")
	if err != nil {
		t.Fatal(err)
	}
	if hooks != "钩子A" || scripts != "脚本B" {
		t.Fatalf("hooks=%q scripts=%q", hooks, scripts)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.calls) != 2 {
		t.Fatalf("calls=%v", client.calls)
	}
}

func TestFetchCopyMaterialsSurfacesHooksError(t *testing.T) {
	client := &stubCopyClient{
		scripts: "脚本B",
		err:     map[string]error{"hooks": io.EOF},
	}
	_, _, err := fetchCopyMaterials(client, "全国老百姓存在银行里的钱少了2万亿。")
	if err == nil || !strings.Contains(err.Error(), "copy hooks") {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPCopyClientPostsExactFields(t *testing.T) {
	var gotType, gotKey, gotContentType string
	var gotBody copyRequest
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := concurrent.Add(1)
		for {
			cur := maxConcurrent.Load()
			if n <= cur || maxConcurrent.CompareAndSwap(cur, n) {
				break
			}
		}
		defer concurrent.Add(-1)
		time.Sleep(30 * time.Millisecond)
		gotType = r.URL.Path
		gotKey = r.Header.Get("X-API-Key")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"task": map[string]any{
				"status":      2,
				"title":       "Hooks重写",
				"content":     "钩子正文",
				"processType": "rewrite_hooks",
			},
		})
	}))
	defer server.Close()

	client := &HTTPCopyClient{BaseURL: server.URL, APIKey: "tg-test-key", HTTPClient: server.Client()}
	content, err := client.Copy("hooks", "全国老百姓存在银行里的钱少了2万亿。")
	if err != nil {
		t.Fatal(err)
	}
	if content != "钩子正文" {
		t.Fatalf("content=%q", content)
	}
	if gotType != "/v1/copy" || gotKey != "tg-test-key" || gotContentType != "application/json" {
		t.Fatalf("path=%s key=%s type=%s", gotType, gotKey, gotContentType)
	}
	if gotBody.ProcessType != "hooks" || !strings.Contains(gotBody.SourceContent, "2万亿") {
		t.Fatalf("body=%+v", gotBody)
	}
}

func TestHTTPCopyClientRejectsEmptyContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "task": map[string]any{"content": "  "}})
	}))
	defer server.Close()
	client := &HTTPCopyClient{BaseURL: server.URL, APIKey: "k", HTTPClient: server.Client()}
	if _, err := client.Copy("scripts", "全国老百姓存在银行里的钱少了2万亿。"); err == nil {
		t.Fatal("expected empty content error")
	}
}

func TestCopyEndpointJoinsV1(t *testing.T) {
	got, err := copyEndpoint("http://example.test:8866")
	if err != nil || got != "http://example.test:8866/v1/copy" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
