package runninghub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientUploadRunQueryAndRedactsKey(t *testing.T) {
	const key = "rh-secret-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Errorf("authorization=%q", got)
		}
		switch r.URL.Path {
		case uploadPath:
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			body, _ := io.ReadAll(file)
			if header.Filename != "首帧.png" || string(body) != "image" {
				t.Fatalf("upload=%q %q", header.Filename, body)
			}
			io.WriteString(w, `{"data":{"filename":"uploaded.png"}}`)
		case runPath:
			var body struct {
				WebappID     string `json:"webappId"`
				APIKey       string `json:"apiKey"`
				InstanceType string `json:"instanceType"`
				NodeInfoList []Node `json:"nodeInfoList"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.WebappID != H3WebappID || body.APIKey != key || body.InstanceType != "default" || len(body.NodeInfoList) == 0 {
				t.Fatalf("run body=%+v", body)
			}
			io.WriteString(w, `{"data":{"taskId":"task-1"}}`)
		case queryPath:
			var request struct {
				TaskID string `json:"taskId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.TaskID == "bad" {
				http.Error(w, "provider rejected "+key, http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"status":"SUCCESS","results":[{"outputType":"mp4","url":"https://cdn.example.test/out.mp4"}],"usage":{"coins":35}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := Client{HTTP: server.Client(), BaseURL: server.URL}
	name, err := client.Upload(t.Context(), key, "首帧.png", []byte("image"))
	if err != nil || name != "uploaded.png" {
		t.Fatalf("Upload()=%q, %v", name, err)
	}
	nodes, err := BuildH3Nodes(H3Request{Prompt: "推进", DurationS: 6, Ratio: "9:16", PromptOptimize: true}, name)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := client.RunApp(t.Context(), key, H3WebappID, nodes, "")
	if err != nil || taskID != "task-1" {
		t.Fatalf("RunApp()=%q, %v", taskID, err)
	}
	status, err := client.Query(t.Context(), key, taskID)
	if err != nil || status.State != "SUCCESS" || status.VideoURL() != "https://cdn.example.test/out.mp4" || status.Usage == nil {
		t.Fatalf("Query()=%+v, %v", status, err)
	}
	_, err = client.Query(t.Context(), key, "bad")
	if err == nil || strings.Contains(err.Error(), key) || !ShouldDisable(err) {
		t.Fatalf("unsafe error=%v disable=%v", err, ShouldDisable(err))
	}
}

func TestClientRejectsRedirectsAndLimitsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == uploadPath {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		io.WriteString(w, strings.Repeat("x", maxResponseBody+1))
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: server.URL}
	if _, err := client.Upload(t.Context(), "secret", "a.png", []byte("x")); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect err=%v", err)
	}

	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("x", maxResponseBody+1))
	})
	if _, err := client.Query(t.Context(), "secret", "task"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("limit err=%v", err)
	}
}

func TestClientTreatsHTTP200BusinessCodesAsErrors(t *testing.T) {
	const key = "rh-business-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case uploadPath:
			io.WriteString(w, `{"code":401}`)
		case queryPath:
			io.WriteString(w, `{"status":"FAILED","errorCode":"402"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: server.URL}
	if _, err := client.Upload(t.Context(), key, "a.png", []byte("x")); err == nil || !ShouldDisable(err) || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "auth_failed") {
		t.Fatalf("business upload error=%v", err)
	}
	status, err := client.Query(t.Context(), key, "task")
	if err == nil || !ShouldDisable(err) || status.ErrorCode != "balance_insufficient" || strings.Contains(err.Error()+status.ErrorCode, key) {
		t.Fatalf("query status=%+v err=%v", status, err)
	}
}

func TestProviderErrorCodesAreStableAndTranslatable(t *testing.T) {
	tests := []struct {
		status  int
		code    string
		message string
		want    string
	}{
		{http.StatusGatewayTimeout, "", "", "timeout"},
		{http.StatusPaymentRequired, "", "", "balance_insufficient"},
		{0, "401", "", "auth_failed"},
		{0, "403", "", "auth_failed"},
		{0, "402", "", "balance_insufficient"},
		{0, "TASK_TIMEOUT", "queue timeout", "timeout"},
		{0, "PROMPT_TOO_LONG", "", "prompt_too_long"},
		{0, "bad_request", "提示词过长", "prompt_too_long"},
	}
	for _, test := range tests {
		err := providerError("test", test.status, test.code, test.message)
		var provider *ProviderError
		if !errors.As(err, &provider) || provider.Code != test.want {
			t.Errorf("status=%d code=%q message=%q got=%v", test.status, test.code, test.message, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadAllowsPublicRedirectAndEnforcesLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "93.184.216.34") {
			http.Redirect(w, r, "http://1.1.1.1/video.mp4", http.StatusFound)
			return
		}
		io.WriteString(w, "video")
	}))
	defer server.Close()
	transport := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		t.Fatal("download transport used a proxy")
		return nil, nil
	}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	client := Client{HTTP: &http.Client{Transport: transport}}
	var out bytes.Buffer
	if err := client.Download(t.Context(), "http://93.184.216.34/out", &out, 5); err != nil || out.String() != "video" {
		t.Fatalf("Download()=%q, %v", out.String(), err)
	}
	out.Reset()
	if err := client.Download(t.Context(), "http://93.184.216.34/out", &out, 4); err == nil {
		t.Fatal("expected size error")
	}
	for _, rawURL := range []string{"file:///tmp/x", "http://127.0.0.1/x", "http://169.254.169.254/x", "http://192.0.2.1/x", "http://[2001:db8::1]/x"} {
		if err := client.Download(t.Context(), rawURL, io.Discard, 10); err == nil {
			t.Fatalf("accepted %s", rawURL)
		}
	}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("must not run") })}
	if err := client.Download(t.Context(), "http://93.184.216.34/out", io.Discard, 10); err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("custom transport err=%v", err)
	}
}
