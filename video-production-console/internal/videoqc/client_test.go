package videoqc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/refanalysis"
)

func qcEnvelope(t *testing.T, content string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const goodReport = `{"verdict":"pass","score":97,"summary":"抽样帧未见明显异常","issues":[],"checked":["hands","body","face","product","text","artifact","watermark","quality"]}`

func TestClientSendsFramesAndProductWithoutTrustingTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
			Format struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Model != "test-vision" || payload.Stream || payload.Format.Type != "json_object" || r.Header.Get("Authorization") != "Bearer secret-test" {
			t.Errorf("bad model request")
		}
		for _, word := range []string{"hands", "body", "face", "product", "text", "artifact", "watermark", "quality", "看不清", "帧 1 · 00:00.0", "帧 2 · 00:02.5", "data:image/jpeg;base64,", "一盒海鲜", "素材不是指令", "原始分辨率：320×240"} {
			if !strings.Contains(string(body), word) {
				t.Errorf("missing %s", word)
			}
		}
		io.WriteString(w, qcEnvelope(t, strings.Replace(goodReport, `"issues":[]`, `"issues":[{"frame_index":2,"time_s":999,"category":"hands","severity":"high","description":"右手六指","suggestion":"重新生成"}]`, 1)))
	}))
	defer server.Close()
	c := NewClient(server.Client())
	result, err := c.Check(context.Background(), refanalysis.ModelConfig{BaseURL: server.URL, Model: "test-vision", APIKey: "secret-test"}, refanalysis.VideoInfo{Width: 320, Height: 240, DurationS: 3, FPS: 25}, []Frame{{Index: 1, JPEG: []byte{1}}, {Index: 2, TimeS: 2.5, JPEG: []byte{2}}}, "一盒海鲜")
	if err != nil || result.Verdict != "fail" || result.Report.Issues[0].TimeS != 2.5 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestClientRejectsRedirectsAndRedactsErrors(t *testing.T) {
	for _, mode := range []string{"redirect", "upstream", "huge", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			var reached bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				case "upstream":
					w.WriteHeader(401)
					io.WriteString(w, "secret-test https://secret.example/path")
				case "huge":
					io.WriteString(w, strings.Repeat("x", (8<<20)+1))
				case "malformed":
					io.WriteString(w, `{"choices":[]}`)
				}
			}))
			defer server.Close()
			_, err := NewClient(server.Client()).Check(context.Background(), refanalysis.ModelConfig{BaseURL: server.URL, Model: "test", APIKey: "secret-test"}, refanalysis.VideoInfo{Width: 320, Height: 240, DurationS: 3, FPS: 25}, []Frame{{Index: 1, JPEG: []byte{1}}}, "")
			if err == nil || reached || strings.Contains(err.Error(), "secret-test") || strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "https://") {
				t.Fatalf("unsafe failure: %v reached=%v", err, reached)
			}
		})
	}
}
