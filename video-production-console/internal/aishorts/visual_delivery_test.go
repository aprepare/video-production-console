package aishorts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"video-production-console/internal/narration"
)

func TestVisualNewAndLegacyLayouts(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	s, err := svc.Create("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "", "documentary", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.VisualSettings == nil || s.VisualSettings.Layout != "portrait_full" {
		t.Fatalf("new layout: %+v", s.VisualSettings)
	}
	old := &Short{Mode: ModeExplainer, Style: "documentary", Shots: []Shot{{Narration: "家庭账本放在桌上", Subject: "家庭账本", Scene: "桌上一本账本"}}}
	normalizeLegacy(old)
	if old.VisualSettings.Layout != "portrait_inset" {
		t.Fatal("legacy layout changed")
	}
	if mentionsPeople("家庭账本与手写资料") {
		t.Fatal("objects inferred as people")
	}
}

func TestNarrationVersionsAndConcurrentCacheReuse(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "narration.mp3")
	if err := os.WriteFile(old, []byte("previous voice"), 0644); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	a := &DraftAssembler{
		ScriptPath: filepath.Join(dir, "missing-script.py"),
		BuildRequest: func(context.Context, string, string) (narration.ProduceRequest, error) {
			return narration.ProduceRequest{}, nil
		},
		Produce: func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
			calls.Add(1)
			return narration.Delivery{Audio: []byte("offline voice fixture"), Duration: 2}, nil
		},
	}
	s := &Short{ID: "local-test", Mode: ModeExplainer}
	results := make([]preparedNarration, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = a.prepareNarration(context.Background(), Runtime{}, s, dir, "先看清楚存款条件。", func(string) {})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || results[0].AudioPath != results[1].AudioPath {
		t.Fatal("prewarm and assemble did not share the prepared voice")
	}
	next, err := a.prepareNarration(context.Background(), Runtime{}, s, dir, "再看清楚家庭支出。", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if next.AudioPath == results[0].AudioPath || next.AudioPath == old {
		t.Fatal("new voice reused an old asset path")
	}
	if raw, err := os.ReadFile(old); err != nil || string(raw) != "previous voice" {
		t.Fatal("old voice overwritten")
	}
	if _, err = os.Stat(results[0].AudioPath); err != nil {
		t.Fatal("previous generated voice missing")
	}
}

func TestVisualEditsLockedDuringGeneration(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	s, err := svc.Create("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "", "documentary", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !svc.tryLockShot(s.ID, 0) {
		t.Fatal("lock failed")
	}
	defer svc.unlockShot(s.ID, 0)
	next := DefaultVisualSettings()
	next.Layout = "portrait_inset"
	if _, err = svc.UpdateText(s.ID, "", "", "", "", nil, nil, nil, next); !errors.Is(err, ErrBusy) {
		t.Fatalf("text edit allowed during generation: %v", err)
	}
	if _, err = svc.UpdateShot(s.ID, 0, ShotPatch{Scene: "新场景"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("shot edit allowed during generation: %v", err)
	}
}

func TestFallbackDoesNotSendNarrationAsImageDescription(t *testing.T) {
	shots := fallbackSplit("利率0.95%，先看清条件。安排好家里的每一笔钱。")
	for _, shot := range shots {
		if shot.Scene != "" || !strings.Contains(shot.VisualIntent, "分镜回退") {
			t.Fatalf("fallback pretends to be a visual brief: %+v", shot)
		}
	}
}

func TestVisualSettingsPreserveImagesAndInvalidateOnlyRelevantWork(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil)
	s, err := svc.Create("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "", "documentary", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.Shots = []Shot{{Narration: "利率0.95%，先看风险。", Scene: "桌上的账本", Subject: "账本", ImagePath: "old.png", ImageStatus: ShotDone}}
	s.DraftPath = "old-draft"
	fillAllPrompts(s, false)
	if err = svc.store.Save(s); err != nil {
		t.Fatal(err)
	}
	move, note := "still", "先看条件"
	empty := []ShotKeyword{}
	got, err := svc.UpdateShot(s.ID, 0, ShotPatch{CameraMove: &move, Annotation: &note, Keywords: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if !got.DraftStale || got.Shots[0].ImageStale || got.Shots[0].ImagePath != "old.png" {
		t.Fatalf("metadata edit damaged image: %+v", got)
	}
	got, err = svc.Get(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shots[0].Keywords == nil || len(got.Shots[0].Keywords) != 0 || got.Shots[0].CameraMove != "still" {
		t.Fatalf("edit not preserved: %+v", got.Shots[0])
	}
	settings := *got.VisualSettings
	settings.Layout = "portrait_inset"
	got, err = svc.UpdateText(s.ID, "", "", "", "", nil, nil, nil, &settings)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Shots[0].ImageStale || got.Shots[0].ImagePath != "old.png" || got.ShotReady(got.Shots[0]) {
		t.Fatal("layout change should keep old image for review but require regeneration")
	}
	if !strings.Contains(got.Shots[0].ImagePrompt, "16:9") {
		t.Fatal(got.Shots[0].ImagePrompt)
	}
}

func TestVisualImageSizeAndVersionedFiles(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 9, 16))); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(DataURL(encoded.Bytes()), "data:image/png;base64,") {
		t.Fatal("PNG reference MIME mislabeled")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/images/generations" || body["size"] != "1152x2048" {
			t.Errorf("request %s size=%v", r.URL.Path, body["size"])
		}
		if body["aspect_ratio"] != "9:16" {
			t.Errorf("Grok aspect ratio missing: %v", body["aspect_ratio"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}}})
	}))
	defer server.Close()
	svc := NewService(t.TempDir(), nil, nil)
	s, err := svc.Create("", ModeExplainer, "", "一家人辛苦攒下来的钱，存款到期之后先把条件问清楚。", "", "documentary", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.Shots = []Shot{{Narration: "钱放在家里", Subject: "存折", Scene: "桌上的存折", ImageStatus: ShotPending}}
	fillAllPrompts(s, false)
	if err = svc.store.Save(s); err != nil {
		t.Fatal(err)
	}
	dir := svc.store.AssetDir(s.ID)
	if err = os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	client := &GenClient{BaseURL: server.URL, APIKey: "test"}
	rt := Runtime{Models: DefaultModels()}
	first := svc.generateShotImage(context.Background(), rt, client, s.ID, dir, 0, s.Shots[0], s, nil)
	second := svc.generateShotImage(context.Background(), rt, client, s.ID, dir, 0, s.Shots[0], s, nil)
	if first == "" || first == second || filepath.Ext(first) != ".png" {
		t.Fatalf("asset versioning: %q %q", first, second)
	}
	if _, err = os.Stat(first); err != nil {
		t.Fatal("previous image lost", err)
	}
	got, _ := svc.Get(s.ID)
	if got.Shots[0].ImagePath != second || got.Shots[0].ImagePromptUsed == "" {
		t.Fatal("generation result not saved")
	}
}

func TestCaptionTokenAtForcedBoundary(t *testing.T) {
	for _, token := range []string{"0.95%", "1,234.56万元", "《财富觉醒方法论》", "2026年"} {
		line := strings.Repeat("字", 29) + token + strings.Repeat("字", 24)
		parts := splitClauses(line)
		found := false
		for _, part := range parts {
			if strings.Contains(part, token) {
				found = true
			}
		}
		if !found || strings.Join(parts, "") != line {
			t.Fatalf("token lost or cut: %v", parts)
		}
	}
	words := []ShotKeyword{{Text: "0.95%", Kind: "number"}, {Text: "本金", Kind: "concept"}}
	caps := keywordsOnCaptions([]jobCaption{{Text: "利率0.95%"}, {Text: "本金保持不变"}}, words)
	if len(caps[0].Keywords) != 1 || caps[0].Keywords[0].Text != "0.95%" || len(caps[1].Keywords) != 1 {
		t.Fatalf("keywords assigned to wrong cue: %+v", caps)
	}
}

func TestVisualPromptAndKeywordOwnership(t *testing.T) {
	s := &Short{Mode: ModeExplainer, Style: "documentary", VisualSettings: DefaultVisualSettings(), Shots: []Shot{{Narration: "利率0.95%，本金10万元。", Subject: "两份存款资料", SubjectType: "comparison", Scene: "资料左右并排", VisualIntent: "相同本金前后利息对比", Keywords: []ShotKeyword{{Text: "0.95%", Kind: "number"}, {Text: "不存在", Kind: "risk"}}}}}
	fillAllPrompts(s, false)
	p := s.Shots[0].ImagePrompt
	if !strings.Contains(p, "9:16") || strings.Contains(p, "16:9 横屏") {
		t.Fatal(p)
	}
	if !strings.Contains(p, "相同本金前后利息对比") {
		t.Fatal("intent missing")
	}
	if len(s.Shots[0].Keywords) != 1 {
		t.Fatalf("keywords=%+v", s.Shots[0].Keywords)
	}
}

func TestCaptionKeepsFinancialTokens(t *testing.T) {
	for _, line := range []string{"利率0.95%，10万元一年950元。", "存款1,234.56元，先看清条件。", "学习《财富觉醒方法论》，把安排想清楚。"} {
		parts := splitClauses(line)
		for _, token := range []string{"0.95%", "1,234.56元", "《财富觉醒方法论》"} {
			if strings.Contains(line, token) {
				found := false
				for _, p := range parts {
					if strings.Contains(p, token) {
						found = true
					}
				}
				if !found {
					t.Fatalf("%q split: %v", token, parts)
				}
			}
		}
	}
}
