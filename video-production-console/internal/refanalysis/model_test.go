package refanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"video-production-console/internal/store"
)

func TestBuildRequestContentOrderTimesAndFocus(t *testing.T) {
	post := store.ReferencePost{Platform: "douyin", Title: "早餐店", Likes: 12, Comments: 3, Shares: 4,
		TranscriptJSON: `[{"start_ms":3200,"end_ms":5800,"text":"现做只要九块九"}]`}
	raw, err := buildRequest(ModelConfig{Model: "custom"}, post, VideoInfo{DurationS: 10, Width: 1080, Height: 1920, FPS: 30},
		[]Shot{{Index: 3, StartS: 4, EndS: 5}}, []Frame{{Index: 1, TimeS: 4.5, Shot: 3, JPEG: []byte{255, 216, 255}}}, strings.Repeat("重点", 200))
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "custom" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
		t.Fatalf("消息角色或顺序错误: %s", raw)
	}
	var content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(request.Messages[1].Content, &content); err != nil {
		t.Fatal(err)
	}
	if len(content) != 6 {
		t.Fatalf("content 数量: %d", len(content))
	}
	for _, wanted := range []string{"douyin", "早餐店", "1080×1920", "点赞：12", "评论：3", "转发：4"} {
		if !strings.Contains(content[0].Text, wanted) {
			t.Fatalf("缺少基础信息 %q", wanted)
		}
	}
	if content[0].Type != "text" || !strings.Contains(content[1].Text, "[00:03.2–00:05.8] 现做只要九块九") || !strings.Contains(content[2].Text, "镜头 3") {
		t.Fatalf("基础信息、转写、镜头表顺序错误: %+v", content)
	}
	if content[3].Type != "text" || content[3].Text != "帧 1 · 00:04.5 · 镜头 3" || content[4].Type != "image_url" || content[4].ImageURL.URL != "data:image/jpeg;base64,/9j/" {
		t.Fatal("帧标签与图像未交替排列")
	}
	if content[5].Type != "text" || utf8.RuneCountInString(strings.TrimPrefix(content[5].Text, "用户补充的分析重点：\n")) != 300 {
		t.Fatal("focus 未按中文字数截断并放在最后")
	}
	if formatTime(59.96) != "01:00.0" {
		t.Fatal("时间舍入进位错误")
	}
}

func TestBuildRequestWithoutTranscriptDoesNotUseCaption(t *testing.T) {
	for _, transcript := range []string{"", "[]", "null", "broken"} {
		raw, err := buildRequest(ModelConfig{Model: "m"}, store.ReferencePost{Caption: "不能把分享文案当口播", TranscriptJSON: transcript}, VideoInfo{DurationS: 2}, nil, nil, "")
		if err != nil || !strings.Contains(string(raw), "无口播转写") || strings.Contains(string(raw), "不能把分享文案当口播") {
			t.Fatalf("无转写提示: %s %v", raw, err)
		}
	}
}

func TestParseReportExtractsFirstObjectAndAcceptsMissingFields(t *testing.T) {
	for _, content := range []string{
		`{"summary":"开头反差","ignored":"extra"}`,
		"说明\n```json\n{\"summary\":\"开头反差\"}\n```\n尾声",
		`前缀 {不是 JSON} 后面 {"summary":"开头反差","hook":{"first_3s":"字幕 {9.9} 与 \"反差\""}} {"summary":"第二份"}`,
	} {
		report, err := ParseReport(content)
		if err != nil || report.Summary != "开头反差" {
			t.Fatalf("JSON 兜底: %+v %v", report, err)
		}
		normalized, err := json.Marshal(report)
		if err != nil || !json.Valid(normalized) || strings.Contains(string(normalized), "ignored") {
			t.Fatalf("报告没有规范化: %s %v", normalized, err)
		}
	}
	if _, err := ParseReport(`{}`); err != nil {
		t.Fatal("缺少字段不应报错", err)
	}
	for _, content := range []string{"null", "", `{"summary":`, `{"structure":"wrong type"}`} {
		if _, err := ParseReport(content); err == nil {
			t.Fatalf("接受了无效报告 %q", content)
		}
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelClientRequestAndSafeFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("模型请求地址、方法或鉴权错误")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, exists := body["max_tokens"]; exists {
			t.Error("不允许限制 max_tokens")
		}
		if body["temperature"] != 0.3 || body["stream"] != false || body["response_format"].(map[string]any)["type"] != "json_object" {
			t.Error("模型请求参数不符合约定")
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"说明：{\"summary\":\"成功\"}"}}]}`)
	}))
	defer server.Close()
	client := NewClient(server.Client())
	report, err := client.Analyze(t.Context(), ModelConfig{BaseURL: server.URL + "/v1", Model: "m", APIKey: "test-key"}, store.ReferencePost{}, VideoInfo{}, nil, nil, "")
	if err != nil || report.Summary != "成功" {
		t.Fatalf("模型调用: %+v %v", report, err)
	}
	client = NewClient(httpDoerFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 6*time.Minute || time.Until(deadline) < 5*time.Minute {
			t.Error("模型请求没有 6 分钟超时")
		}
		return nil, errors.New("https://private.test/v1?secret=test-key test-key")
	}))
	_, err = client.Analyze(context.Background(), ModelConfig{BaseURL: "https://private.test/v1", Model: "m", APIKey: "test-key"}, store.ReferencePost{}, VideoInfo{}, nil, nil, "")
	if err == nil || strings.Contains(err.Error(), "private.test") || strings.Contains(err.Error(), "test-key") {
		t.Fatal("模型网络错误泄漏敏感信息")
	}
	for _, response := range []struct {
		code int
		body string
	}{{500, "https://private.test test-key"}, {200, `{"choices":[]}`}, {200, `not-json`}} {
		client = NewClient(httpDoerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: response.code, Body: io.NopCloser(strings.NewReader(response.body))}, nil
		}))
		_, err := client.Analyze(t.Context(), ModelConfig{BaseURL: "https://private.test/v1", Model: "m", APIKey: "test-key"}, store.ReferencePost{}, VideoInfo{}, nil, nil, "")
		if err == nil || strings.Contains(err.Error(), "private.test") || strings.Contains(err.Error(), "test-key") {
			t.Fatal("模型 HTTP/JSON 错误未安全处理")
		}
	}
}
