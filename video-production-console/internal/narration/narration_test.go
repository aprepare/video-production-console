package narration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// subtitlePacket mirrors the shape published in the vendor's own example, where
// timings are floating-point seconds relative to the whole session.
const subtitlePacket = `{"code":0,"message":"","data":null,"sentence":{"phonemes":[],"text":"其他人。","words":[` +
	`{"confidence":0.8531248,"endTime":0.315,"startTime":0.205,"word":"其"},` +
	`{"confidence":0.9710379,"endTime":0.515,"startTime":0.315,"word":"他"},` +
	`{"confidence":0.9189944,"endTime":0.815,"startTime":0.515,"word":"人。"}]}}`

func audioPacket(payload string) string {
	return fmt.Sprintf(`{"code":0,"message":"","data":%q}`, base64.StdEncoding.EncodeToString([]byte(payload)))
}

const terminalPacket = `{"code":20000000,"message":"ok","data":null,"usage":{"text_words":4}}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Client{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
}

func TestSynthesizeCollectsAudioAndWordTimings(t *testing.T) {
	var got struct {
		ReqParams struct {
			Text        string `json:"text"`
			Speaker     string `json:"speaker"`
			AudioParams struct {
				Format         string `json:"format"`
				SampleRate     int    `json:"sample_rate"`
				SpeechRate     int    `json:"speech_rate"`
				EnableSubtitle bool   `json:"enable_subtitle"`
			} `json:"audio_params"`
		} `json:"req_params"`
	}
	var headers http.Header
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		fmt.Fprint(w, audioPacket("AUDIO-")+subtitlePacket+audioPacket("BYTES")+terminalPacket)
	})

	result, err := client.Synthesize(context.Background(), Request{
		Text: "其他人。", SpeakerID: "S_example", SpeechRate: 12,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if headers.Get("X-Api-Resource-Id") != ResourceVoiceClone2 {
		t.Errorf("resource id = %q, want %q", headers.Get("X-Api-Resource-Id"), ResourceVoiceClone2)
	}
	if headers.Get("X-Api-Key") != "test-key" {
		t.Errorf("api key header = %q", headers.Get("X-Api-Key"))
	}
	if headers.Get("X-Api-Request-Id") == "" {
		t.Error("request id header is required by the vendor but was empty")
	}
	if !got.ReqParams.AudioParams.EnableSubtitle {
		t.Error("enable_subtitle must be set or no timings come back")
	}
	if got.ReqParams.Speaker != "S_example" || got.ReqParams.Text != "其他人。" {
		t.Errorf("unexpected request params: %+v", got.ReqParams)
	}
	if got.ReqParams.AudioParams.SpeechRate != 12 {
		t.Errorf("speech_rate = %d, want 12", got.ReqParams.AudioParams.SpeechRate)
	}
	if string(result.Audio) != "AUDIO-BYTES" {
		t.Errorf("audio = %q, want %q", result.Audio, "AUDIO-BYTES")
	}
	if result.BilledWords != 4 {
		t.Errorf("billed words = %d, want 4", result.BilledWords)
	}
	if len(result.Words) != 3 {
		t.Fatalf("words = %d, want 3", len(result.Words))
	}
	if result.Words[0].Text != "其" || result.Words[0].StartTime != 0.205 || result.Words[2].EndTime != 0.815 {
		t.Errorf("unexpected word timings: %+v", result.Words)
	}
}

func TestSynthesizeReadsServerSentEvents(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: 352\ndata: %s\n\nevent: 351\ndata: %s\n\nevent: 152\ndata: %s\n\n",
			audioPacket("SSE"), subtitlePacket, terminalPacket)
	})

	result, err := client.Synthesize(context.Background(), Request{Text: "其他人。", SpeakerID: "S_example"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(result.Audio) != "SSE" {
		t.Errorf("audio = %q, want %q", result.Audio, "SSE")
	}
	if len(result.Words) != 3 {
		t.Errorf("words = %d, want 3", len(result.Words))
	}
}

func TestSynthesizeSurfacesVendorErrorPacket(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":45000000,"message":"speaker permission denied: access denied"}`)
	})

	_, err := client.Synthesize(context.Background(), Request{Text: "文本", SpeakerID: "S_missing"})
	if err == nil {
		t.Fatal("expected an error for a vendor failure packet")
	}
	if !strings.Contains(err.Error(), "45000000") || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should carry the vendor code and message, got %v", err)
	}
}

// A stream that stops early would otherwise look like a short but valid
// narration, so the missing terminal packet has to be an error.
func TestSynthesizeRejectsTruncatedStream(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, audioPacket("PARTIAL")+subtitlePacket)
	})

	_, err := client.Synthesize(context.Background(), Request{Text: "文本", SpeakerID: "S_example"})
	if err == nil || !strings.Contains(err.Error(), "success packet") {
		t.Fatalf("expected a truncated-stream error, got %v", err)
	}
}

func TestSynthesizeUsesLegacyCredentials(t *testing.T) {
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		fmt.Fprint(w, audioPacket("A")+terminalPacket)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, AppID: "123456789", AccessToken: "token", HTTPClient: server.Client()}

	if _, err := client.Synthesize(context.Background(), Request{Text: "文本", SpeakerID: "S_example"}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if headers.Get("X-Api-App-Id") != "123456789" || headers.Get("X-Api-Access-Key") != "token" {
		t.Errorf("legacy credential headers missing: %v", headers)
	}
	if headers.Get("X-Api-Key") != "" {
		t.Error("api key header must not be sent alongside legacy credentials")
	}
}

func TestSynthesizeRequiresCredentials(t *testing.T) {
	client := &Client{BaseURL: "https://example.invalid"}
	_, err := client.Synthesize(context.Background(), Request{Text: "文本", SpeakerID: "S_example"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

func TestSynthesizeRequiresSpeaker(t *testing.T) {
	client := &Client{BaseURL: "https://example.invalid", APIKey: "k"}
	_, err := client.Synthesize(context.Background(), Request{Text: "文本"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

// wordsFrom lays tokens out back to back so segmentation can be asserted without
// depending on real vendor timings.
func wordsFrom(tokens []string, step float64) []Word {
	words := make([]Word, 0, len(tokens))
	at := 0.0
	for _, token := range tokens {
		words = append(words, Word{Text: token, StartTime: at, EndTime: at + step})
		at += step
	}
	return words
}

func captionTexts(captions []Caption) []string {
	out := make([]string, 0, len(captions))
	for _, c := range captions {
		out = append(out, c.Text)
	}
	return out
}

func TestComposeBreaksAtSentenceEnd(t *testing.T) {
	tokens := []string{"今天", "我们", "聊", "一个", "很", "重要", "的", "话题。", "这个", "话题", "关系", "到", "你的", "钱包。"}
	words := wordsFrom(tokens, 0.3)
	script := strings.Join(tokens, "")

	captions, report, err := Compose(script, words, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !report.Pass {
		t.Fatalf("expected a passing report, got failures %v", report.Failures)
	}
	want := []string{"今天我们聊一个很重要的话题。", "这个话题关系到你的钱包。"}
	if got := captionTexts(captions); !equalStrings(got, want) {
		t.Errorf("captions = %q, want %q", got, want)
	}
	if captions[0].Start != 0 || captions[1].Start != 2.4 {
		t.Errorf("unexpected cue timing: %+v", captions)
	}
}

// An over-long sentence must divide into even cues rather than a full line plus a
// stranded tail.
func TestComposeSplitsLongSentenceEvenly(t *testing.T) {
	tokens := []string{"我们", "今天", "要", "讲", "的", "是", "一个", "关于", "财务", "自由", "的", "故事"}
	words := wordsFrom(tokens, 0.3)

	captions, report, err := Compose(strings.Join(tokens, ""), words, Options{MaxLineRunes: 16})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !report.Pass {
		t.Fatalf("expected a passing report, got failures %v", report.Failures)
	}
	if len(captions) != 2 {
		t.Fatalf("captions = %q, want 2 cues", captionTexts(captions))
	}
	for _, c := range captions {
		runes := len([]rune(c.Text))
		if runes > 16 {
			t.Errorf("cue %q exceeds the line limit at %d runes", c.Text, runes)
		}
		if runes < 4 {
			t.Errorf("cue %q is a stranded fragment", c.Text)
		}
	}
}

func TestComposeKeepsLatinTokensTogether(t *testing.T) {
	tokens := []string{"我们", "用", "Claude", "Max", "来", "写", "代码。"}
	words := wordsFrom(tokens, 0.3)

	captions, _, err := Compose("我们用Claude Max来写代码。", words, Options{MaxLineRunes: 16})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	joined := strings.Join(captionTexts(captions), "|")
	if !strings.Contains(joined, "Claude Max") {
		t.Errorf("latin name was split or lost its space: %q", joined)
	}
}

func TestComposeMovesTrailingConnectorToNextCue(t *testing.T) {
	tokens := []string{"这个", "方法", "看起来", "很", "美好", "但是", "实际上", "根本", "行不通。"}
	words := wordsFrom(tokens, 0.4)

	captions, _, err := Compose(strings.Join(tokens, ""), words, Options{MaxLineRunes: 16})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(captions) < 2 {
		t.Fatalf("expected the sentence to split, got %q", captionTexts(captions))
	}
	for i, c := range captions[:len(captions)-1] {
		if strings.HasSuffix(c.Text, "但是") {
			t.Errorf("cue %d ends on a connector: %q", i+1, c.Text)
		}
	}
	if !strings.HasPrefix(captions[1].Text, "但是") {
		t.Errorf("connector should open the next cue, got %q", captions[1].Text)
	}
}

// The vendor reports the original script, so a shortfall means the narration
// skipped content. This is the gate that replaces recognition-based coverage.
func TestComposeDetectsSkippedNarration(t *testing.T) {
	script := "第一句话在这里。第二句话也在这里。"
	words := wordsFrom([]string{"第一句话在这里。"}, 1.5)

	_, report, err := Compose(script, words, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if report.Pass {
		t.Fatal("a narration missing half the script must not pass")
	}
	if report.TextCoverage >= 1 {
		t.Errorf("coverage = %v, want below 1", report.TextCoverage)
	}
	if !strings.Contains(strings.Join(report.Failures, " "), "skipped content") {
		t.Errorf("failures should name the cause, got %v", report.Failures)
	}
}

func TestComposeFailsUnreadablePace(t *testing.T) {
	tokens := strings.Split("这段话念得实在是太快了啦", "")
	words := wordsFrom(tokens, 0.5/float64(len(tokens)))

	_, report, err := Compose(strings.Join(tokens, ""), words, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if report.Pass {
		t.Fatal("a cue above the reading-speed limit must fail the gate")
	}
	if !strings.Contains(strings.Join(report.Failures, " "), "units/s") {
		t.Errorf("failures should report the pace, got %v", report.Failures)
	}
}

func TestComposeUsesFifteenRuneSpokenLinesByDefault(t *testing.T) {
	tokens := []string{"今", "天", "我", "们", "要", "讲", "一", "个", "关", "于", "财", "务", "的", "问", "题", "了"}
	captions, report, err := Compose(strings.Join(tokens, ""), wordsFrom(tokens, 0.4), Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !report.Pass {
		t.Fatalf("expected a passing report, got %v", report.Failures)
	}
	if len(captions) < 2 {
		t.Fatalf("captions = %q, want a 16-rune sentence split to spoken lines", captionTexts(captions))
	}
	for _, c := range captions {
		if n := len([]rune(c.Text)); n > 15 {
			t.Errorf("cue %q is %d runes, want ≤15", c.Text, n)
		}
	}
}

func TestComposeKeepsBookTitleOnOneSpokenLine(t *testing.T) {
	tokens := []string{"现", "在", "就", "去", "主", "页", "橱", "窗", "看", "《", "财", "富", "觉", "醒", "方", "法", "论", "》", "。"}
	captions, _, err := Compose(strings.Join(tokens, ""), wordsFrom(tokens, 0.4), Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	joined := strings.Join(captionTexts(captions), "|")
	if !strings.Contains(joined, "《财富觉醒方法论》") {
		t.Errorf("book title was split: %q", joined)
	}
	for _, c := range captions {
		text := c.Text
		open := strings.Contains(text, "《")
		close := strings.Contains(text, "》")
		if open != close {
			t.Errorf("book title brackets split across cues: %q", joined)
		}
	}
}

func TestComposeKeepsYearAndPercentTogether(t *testing.T) {
	tokens := []string{"截", "止", "到", "2024", "年", "这", "个", "利", "率", "已", "经", "是", "3", ".", "5", "%", "。"}
	captions, _, err := Compose(strings.Join(tokens, ""), wordsFrom(tokens, 0.4), Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	joined := strings.Join(captionTexts(captions), "|")
	if !strings.Contains(joined, "2024年") {
		t.Errorf("year was split: %q", joined)
	}
	if !strings.Contains(joined, "3.5%") {
		t.Errorf("percent was split: %q", joined)
	}
}

func TestRenderSpokenScriptMatchesCaptionLinesWithoutBlanks(t *testing.T) {
	got := RenderSpokenScript([]Caption{
		{Text: "第一句。", Start: 0, End: 1},
		{Text: "  ", Start: 1, End: 1.1},
		{Text: "第二句。", Start: 1.2, End: 2},
	})
	if got != "第一句。\n第二句。" {
		t.Errorf("spoken script = %q", got)
	}
}

func TestRenderSRT(t *testing.T) {
	got := RenderSRT([]Caption{
		{Text: "其他人。", Start: 0.205, End: 0.815},
		{Text: "下一句。", Start: 3661.5, End: 3662},
	})
	want := "1\n00:00:00,205 --> 00:00:00,815\n其他人。\n\n" +
		"2\n01:01:01,500 --> 01:01:02,000\n下一句。\n\n"
	if got != want {
		t.Errorf("RenderSRT =\n%q\nwant\n%q", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
