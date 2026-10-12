package narration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuraSTDSynthesizeDownloadsURLAudioAndWordSubtitles(t *testing.T) {
	var got auraSTDRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tts", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audio":         "http://" + r.Host + "/audio.mp3",
			"subtitle_file": "http://" + r.Host + "/subs.json",
			"extra_info":    map[string]any{"usage_characters": 4},
		})
	})
	mux.HandleFunc("/audio.mp3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("FAKEMP3"))
	})
	mux.HandleFunc("/subs.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"text":"货还在","time_begin":120,"time_end":980},{"text":"买家没了","time_begin":1000,"time_end":2100}]`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := &AuraSTDClient{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	result, err := client.Synthesize(t.Context(), Request{
		Text: "货还在，买家没了。", SpeakerID: "moss_audio_test",
		Model: "speech-2.8-hd", Speed: 1.21, Volume: 1.4, Pitch: 1,
		ModifyIntensity: 5, ModifyTimbre: 6, LanguageBoost: "Chinese",
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if got.VoiceSetting.VoiceID != "moss_audio_test" || got.VoiceSetting.Speed != 1.21 || got.VoiceSetting.Vol != 1.4 || got.VoiceSetting.Pitch != 1 {
		t.Errorf("voice_setting = %+v", got.VoiceSetting)
	}
	if got.VoiceModify.Intensity != 5 || got.VoiceModify.Timbre != 6 || got.SubtitleType != "word" || !got.SubtitleEnable {
		t.Errorf("modify/subtitles = %+v / type=%s enable=%v", got.VoiceModify, got.SubtitleType, got.SubtitleEnable)
	}
	if string(result.Audio) != "FAKEMP3" {
		t.Errorf("audio = %q", result.Audio)
	}
	if result.BilledWords != 4 {
		t.Errorf("billed = %d", result.BilledWords)
	}
	if len(result.Words) != 2 || result.Words[0].Text != "货还在" || result.Words[0].StartTime != 0.12 || result.Words[1].EndTime != 2.1 {
		t.Errorf("words = %+v", result.Words)
	}
}

func TestParseAuraSTDSubtitlesAcceptsWrappedArray(t *testing.T) {
	words, err := parseAuraSTDSubtitles([]byte(`{"subtitles":[{"word":"法拍","time_begin":0,"time_end":400}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 1 || words[0].Text != "法拍" || words[0].EndTime != 0.4 {
		t.Errorf("words = %+v", words)
	}
}

func TestAuraSTDSynthesizeReportsVendorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"base_resp": map[string]any{"status_code": 2013, "status_msg": "invalid voice_id"},
		})
	}))
	t.Cleanup(server.Close)
	client := &AuraSTDClient{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	_, err := client.Synthesize(t.Context(), Request{Text: "你好", SpeakerID: "missing"})
	if err == nil || !strings.Contains(err.Error(), "invalid voice_id") {
		t.Fatalf("error = %v, want the vendor message", err)
	}
}

func TestAuraSTDSynthesizeAcceptsStringStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"status": "success",
			"audio": "http://` + r.Host + `/audio.mp3",
			"subtitle_file": "http://` + r.Host + `/subs.json",
			"data": {"status": "success", "audio": "", "subtitle_file": ""}
		}`))
	})
	mux.HandleFunc("/audio.mp3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("FAKEMP3"))
	})
	mux.HandleFunc("/subs.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"text":"货","time_begin":0,"time_end":200}]`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := &AuraSTDClient{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	result, err := client.Synthesize(t.Context(), Request{Text: "货", SpeakerID: "v"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(result.Audio) != "FAKEMP3" {
		t.Errorf("audio = %q", result.Audio)
	}
}

func TestAuraSTDHexAndSubtitleInOneResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audio":         "494433",
			"subtitle_file": "http://" + r.Host + "/subs.json",
		})
	})
	mux.HandleFunc("/subs.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"text":"货","time_begin":0,"time_end":200}]`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := &AuraSTDClient{BaseURL: server.URL, APIKey: "k", HTTPClient: server.Client()}
	result, err := client.Synthesize(t.Context(), Request{Text: "货", SpeakerID: "v", Speed: 1.21, Volume: 1.4})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Audio) != "ID3" {
		t.Errorf("hex audio = %q", result.Audio)
	}
}

func TestAuraSTDChunkingForLongText(t *testing.T) {
	var receivedTexts []string
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tts", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body auraSTDRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		receivedTexts = append(receivedTexts, body.Text)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audio":         "http://" + r.Host + fmt.Sprintf("/audio%d.mp3", callCount),
			"subtitle_file": "http://" + r.Host + fmt.Sprintf("/subs%d.json", callCount),
			"extra_info":    map[string]any{"usage_characters": len([]rune(body.Text))},
		})
	})
	mux.HandleFunc("/audio1.mp3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("CHUNK1_AUDIO"))
	})
	mux.HandleFunc("/subs1.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"text":"第一段","time_begin":0,"time_end":1500}]`))
	})
	mux.HandleFunc("/audio2.mp3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("CHUNK2_AUDIO"))
	})
	mux.HandleFunc("/subs2.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"text":"第二段","time_begin":0,"time_end":1200}]`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := &AuraSTDClient{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	// 构造一个 2000 字的长文案
	partA := strings.Repeat("中", 1200) + "。"
	partB := strings.Repeat("华", 800) + "！"
	longText := partA + partB

	result, err := client.Synthesize(t.Context(), Request{Text: longText, SpeakerID: "voice_id"})
	if err != nil {
		t.Fatalf("Synthesize long text: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 chunks called, got %d", callCount)
	}
	if string(result.Audio) != "CHUNK1_AUDIOCHUNK2_AUDIO" {
		t.Errorf("merged audio = %q", string(result.Audio))
	}
	if len(result.Words) != 2 {
		t.Fatalf("expected 2 words, got %d", len(result.Words))
	}
	// Chunk1 end_time 是 1.5 秒
	if result.Words[0].StartTime != 0 || result.Words[0].EndTime != 1.5 {
		t.Errorf("word 0 = %+v", result.Words[0])
	}
	// Chunk2 偏移量应为 1.5 秒，begin: 1.5, end: 2.7
	if result.Words[1].StartTime != 1.5 || result.Words[1].EndTime != 2.7 {
		t.Errorf("word 1 = %+v", result.Words[1])
	}
}
