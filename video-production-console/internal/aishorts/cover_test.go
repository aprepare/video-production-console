package aishorts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoverImagePromptPrintsHeadline(t *testing.T) {
	headline := "普通人的钱，要第三次搬家了"
	p := coverImagePrompt(headline, "cinematic_doc")
	if !strings.Contains(p, "主标题「"+headline+"」") {
		t.Fatalf("cover must ask the model to print the headline:\n%s", p)
	}
	if strings.Contains(p, "禁止任何文字") || strings.Contains(p, "不渲染文字") {
		t.Fatalf("cover prompt must not ban the title:\n%s", p)
	}
	for _, want := range []string{"9:16", "电影级写实", "准确印", "除主标题外", "封面"} {
		if !strings.Contains(p, want) {
			t.Fatalf("cover prompt missing %q:\n%s", want, p)
		}
	}
}

func TestCoverStyleResolvesMixedStrategy(t *testing.T) {
	short := &Short{Mode: ModeExplainer, Style: financeEditorial, Headline: "老百姓的钱开始值钱了"}
	key := coverStyleKey(short)
	if key != "paper_collage" {
		t.Fatalf("mixed strategy cover style = %s", key)
	}
	p := coverImagePrompt(short.Headline, key)
	if !strings.Contains(p, "纸张拼贴") || strings.Contains(p, "财经编辑混合") {
		t.Fatalf("cover should use paper_collage, not the strategy name:\n%s", p)
	}
	short.Style = "ink_wash"
	if key := coverStyleKey(short); key != "ink_wash" {
		t.Fatalf("project style should win: %s", key)
	}
}

func TestGenerateCoverRequiresHeadline(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	s, err := svc.Create("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "", "documentary", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateCover(s.ID); err == nil || !strings.Contains(err.Error(), "大标题") {
		t.Fatalf("empty headline should be rejected: %v", err)
	}
}

func TestGenerateCoverSavesAssetAndMarksStale(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 9, 16))); err != nil {
		t.Fatal(err)
	}
	raw := encoded.Bytes()
	var sawPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/images/generations" || body["size"] != "1152x2048" || body["aspect_ratio"] != "9:16" {
			t.Errorf("request %s size=%v ratio=%v", r.URL.Path, body["size"], body["aspect_ratio"])
		}
		if p, _ := body["prompt"].(string); p != "" {
			sawPrompt = p
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(raw)}}})
	}))
	defer server.Close()

	svc := NewService(t.TempDir(), func(context.Context) (Runtime, error) {
		return Runtime{BaseURL: server.URL, APIKey: "test", Models: DefaultModels()}, nil
	}, nil)
	s, err := svc.Create("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "普通人的钱，要第三次搬家了", "cinematic_doc", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateCover(s.ID); err != nil {
		t.Fatal(err)
	}
	got := waitCover(t, svc, s.ID, ShotDone)
	if got.Cover == nil || got.Cover.Path == "" || !strings.Contains(filepath.Base(got.Cover.Path), "cover_") {
		t.Fatalf("cover not saved: %+v", got.Cover)
	}
	if _, err := os.Stat(got.Cover.Path); err != nil {
		t.Fatal(err)
	}
	if got.Cover.HeadlineUsed != s.Headline || got.Cover.StyleKey != "cinematic_doc" || got.Cover.PromptUsed == "" {
		t.Fatalf("cover metadata incomplete: %+v", got.Cover)
	}
	if !strings.Contains(sawPrompt, "普通人的钱，要第三次搬家了") || !strings.Contains(sawPrompt, "电影级写实") {
		t.Fatalf("cover request used the wrong prompt:\n%s", sawPrompt)
	}
	if got.Status == StatusGenerating {
		t.Fatal("cover generation must not flip the short into generating")
	}

	if _, err := svc.UpdateText(s.ID, "", s.Story, "中国人的钱要第三次搬家了", "ink_wash", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.Get(s.ID)
	if err != nil || stale.Cover == nil || !stale.Cover.Stale {
		t.Fatalf("changed headline/style should stale the cover: %+v %v", stale.Cover, err)
	}
}

func waitCover(t *testing.T, svc *Service, id, want string) *Short {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Cover != nil && got.Cover.Status == want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := svc.Get(id)
	t.Fatalf("cover status != %s: %+v", want, got.Cover)
	return got
}
