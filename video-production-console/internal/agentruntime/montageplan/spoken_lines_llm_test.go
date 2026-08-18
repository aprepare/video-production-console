package montageplan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCaptionPackReadsKeywordsAndBoardTitles(t *testing.T) {
	pack, err := parseCaptionPack(`{
  "board_title": "接盘之后五个要命难题",
  "board_subtitle": "法拍房快堆到四十万",
  "sentences": [[
    {"text": "三年前大家挤破头", "keywords": ["挤破头"]},
    {"text": "往房子里砸钱", "keywords": ["砸钱"]},
    {"text": "还叫投资", "keywords": ["投资"]}
  ]]
}`)
	if err != nil {
		t.Fatal(err)
	}
	if pack.BoardTitle != "接盘之后五个要命难题" || pack.BoardSubtitle != "法拍房快堆到四十万" {
		t.Fatalf("board titles = %q / %q", pack.BoardTitle, pack.BoardSubtitle)
	}
	if len(pack.Groups) != 1 || len(pack.Groups[0]) != 3 {
		t.Fatalf("groups = %#v", pack.Groups)
	}
	if pack.Groups[0][2].Text != "还叫投资" || strings.Join(pack.Groups[0][2].Keywords, ",") != "投资" {
		t.Fatalf("last line = %#v", pack.Groups[0][2])
	}
}

func TestParseCaptionPackAcceptsLegacyLineArrays(t *testing.T) {
	pack, err := parseCaptionPack(`[["全国法拍房挂牌","接近四十万套"]]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Groups) != 1 || pack.Groups[0][1].Text != "接近四十万套" {
		t.Fatalf("legacy pack = %#v", pack)
	}
	if pack.Groups[0][1].Keywords != nil {
		t.Fatalf("legacy lines must not invent keywords: %#v", pack.Groups[0][1])
	}
}

func TestAcceptedLinesRejectIncompleteSubjectCuts(t *testing.T) {
	_, err := acceptedLines([]CaptionLine{{Text: "三年前大家"}, {Text: "挤破头往房子里"}, {Text: "砸钱还叫投资"}}, "三年前大家挤破头往房子里砸钱还叫投资")
	if err == nil {
		t.Fatal("a hanging 大家 cut must be rejected")
	}
}

func TestSanitizeModelKeywordsStayInsideTheLine(t *testing.T) {
	got := sanitizeModelKeywords("砸钱还叫投资", []string{"投资", "理财", "砸钱还叫投资"})
	if strings.Join(got, ",") != "投资" {
		t.Fatalf("keywords = %v, want only 投资", got)
	}
}

func TestSpokenKeywordSpansPreferModelTerms(t *testing.T) {
	runes := []rune("砸钱还叫投资")
	spans := spokenKeywordSpans(runes, []string{"投资"})
	if len(spans) != 1 || string(runes[spans[0].Start:spans[0].End]) != "投资" {
		t.Fatalf("spans = %#v", spans)
	}
	lexicon := spokenKeywordSpans(runes, nil)
	if len(lexicon) != 0 {
		t.Fatalf("lexicon should not invent 投资, got %#v", lexicon)
	}
}

func TestFitModelBoardTitleRejectsColumnFallbacks(t *testing.T) {
	if got := fitModelBoardTitle("时代观察笔记"); got != "" {
		t.Fatalf("column fallback leaked: %q", got)
	}
	if got := fitModelBoardTitle("接盘之后五个要命难题"); got != "接盘之后五个要命难题" {
		t.Fatalf("valid title = %q", got)
	}
	if got := fitModelBoardTitle("短"); got != "" {
		t.Fatalf("too short title = %q", got)
	}
}

func TestHTTPLineBreakerSplitsLongNarrationIntoBatches(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Stream          bool   `json:"stream"`
			MaxTokens       int    `json:"max_tokens"`
			ReasoningEffort string `json:"reasoning_effort"`
			Messages        []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &req); err != nil || len(req.Messages) < 2 {
			t.Errorf("bad request: %v %s", err, raw)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !req.Stream || req.ReasoningEffort != defaultLineBreakerReasoningEffort || req.MaxTokens != lineBreakerMaxTokens {
			t.Errorf("stream=%v effort=%q max_tokens=%d", req.Stream, req.ReasoningEffort, req.MaxTokens)
		}
		user := req.Messages[1].Content
		marker := "batch="
		idx := strings.Index(user, marker)
		if idx < 0 {
			t.Errorf("missing batch payload: %s", user)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		batchJSON := user[idx+len(marker):]
		if nl := strings.Index(batchJSON, "\n"); nl >= 0 {
			batchJSON = batchJSON[:nl]
		}
		var batch []string
		if err := json.Unmarshal([]byte(batchJSON), &batch); err != nil {
			t.Errorf("batch json: %v %s", err, batchJSON)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		groups := make([][]CaptionLine, len(batch))
		for i, sentence := range batch {
			groups[i] = []CaptionLine{{Text: strippedSpeech(sentence), Keywords: []string{}}}
		}
		content, _ := json.Marshal(CaptionPack{
			BoardTitle:    "接盘之后五个要命难题",
			BoardSubtitle: "法拍房快堆到四十万",
			Groups:        groups,
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": string(content)}}},
		})
	}))
	t.Cleanup(server.Close)
	breaker := NewHTTPLineBreaker(LineBreakerConfig{
		BaseURL:    server.URL,
		Model:      "grok-4.6",
		HTTPClient: server.Client(),
	})
	sentences := make([]string, lineBreakerBatchSize+2)
	for i := range sentences {
		sentences[i] = "全国法拍房挂牌接近四十万套"
	}
	pack, err := breaker.BreakLines(context.Background(), sentences)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 batches", calls)
	}
	if len(pack.Groups) != len(sentences) {
		t.Fatalf("groups = %d, want %d", len(pack.Groups), len(sentences))
	}
	if pack.BoardTitle != "接盘之后五个要命难题" {
		t.Fatalf("title = %q", pack.BoardTitle)
	}
}

func TestHTTPLineBreakerReadsSSEStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		content := `{"board_title":"接盘之后五个要命难题","board_subtitle":"法拍房快堆到四十万","sentences":[[{"text":"全国法拍房挂牌","keywords":["法拍房"]},{"text":"接近四十万套","keywords":["四十万套"]}]]}`
		mid := len(content) / 2
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", jsonString(content[:mid]))
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", jsonString(content[mid:]))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	breaker := NewHTTPLineBreaker(LineBreakerConfig{
		BaseURL:    server.URL,
		Model:      "grok-4.6",
		HTTPClient: server.Client(),
	})
	pack, err := breaker.BreakLines(context.Background(), []string{"全国法拍房挂牌接近四十万套"})
	if err != nil {
		t.Fatal(err)
	}
	if pack.BoardTitle != "接盘之后五个要命难题" {
		t.Fatalf("title = %q", pack.BoardTitle)
	}
	if len(pack.Groups) != 1 || pack.Groups[0][1].Text != "接近四十万套" {
		t.Fatalf("groups = %#v", pack.Groups)
	}
}

func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestHTTPLineBreakerUsesConfiguredReasoningEffort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("bad request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.ReasoningEffort != "medium" {
			t.Errorf("effort=%q", req.ReasoningEffort)
		}
		content, _ := json.Marshal(CaptionPack{
			Groups: [][]CaptionLine{{{Text: "全国法拍房挂牌接近四十万套"}}},
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": string(content)}}},
		})
	}))
	t.Cleanup(server.Close)
	breaker := NewHTTPLineBreaker(LineBreakerConfig{
		BaseURL:         server.URL,
		Model:           "grok-4.6",
		ReasoningEffort: "medium",
		HTTPClient:      server.Client(),
	})
	if _, err := breaker.BreakLines(context.Background(), []string{"全国法拍房挂牌接近四十万套"}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeLineBreakerEffortDefaultsToHigh(t *testing.T) {
	if got := normalizeLineBreakerEffort(""); got != "high" {
		t.Fatalf("empty = %q", got)
	}
	if got := normalizeLineBreakerEffort("LOW"); got != "low" {
		t.Fatalf("low = %q", got)
	}
}

func TestSpokenCaptionsIgnoreHTTPLineBreakerWhileDisabled(t *testing.T) {
	if CaptionLLMLineBreakerEnabled {
		t.Skip("caption LLM line breaker is on")
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("montage must not call the caption model while it is disabled")
	}))
	t.Cleanup(server.Close)
	breaker := NewHTTPLineBreaker(LineBreakerConfig{
		BaseURL:    server.URL,
		Model:      "grok-4.6",
		HTTPClient: server.Client(),
	})
	sentences := []TimedSentence{{StartMS: 0, EndMS: 2000, Text: "全国法拍房挂牌接近四十万套"}}
	items, _, notes := spokenCaptionsFromSentences(sentences, 2000, breaker, "")
	if len(items) == 0 {
		t.Fatalf("local splitter should still produce lines: %v", notes)
	}
	for _, note := range notes {
		if strings.HasPrefix(note, "caption_lines_unavailable") {
			t.Fatalf("HTTP breaker leaked into captions: %v", notes)
		}
	}
}
