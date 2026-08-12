package narration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// voiceStatusBody mirrors the vendor's published get_voice example, where one
// training run has produced two model variants.
const voiceStatusBody = `{
  "available_training_times": 15,
  "create_time": 1772026663000,
  "language": 0,
  "speaker_id": "S_example",
  "speaker_status": [
    {"demo_audio": "https://x.bytespeech.com/one", "model_type": 1},
    {"demo_audio": "https://x.bytespeech.com/two", "model_type": 4}
  ],
  "status": 2
}`

func TestTrainVoiceUploadsRecording(t *testing.T) {
	var (
		headers http.Header
		path    string
		payload struct {
			SpeakerID string `json:"speaker_id"`
			Language  int    `json:"language"`
			Audio     struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"audio"`
			ExtraParams struct {
				DemoText string `json:"demo_text"`
			} `json:"extra_params"`
		}
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers, path = r.Header.Clone(), r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, `{"BaseResp":{"StatusCode":0,"StatusMessage":"success"}}`)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, AppID: "123456789", AccessToken: "token", HTTPClient: server.Client()}

	err := client.TrainVoice(context.Background(), TrainVoiceRequest{
		SpeakerID: "S_example", Audio: []byte("RIFFxxxx"), Format: "wav", AuditionText: "这是一段试听文本",
	})
	if err != nil {
		t.Fatalf("TrainVoice: %v", err)
	}

	if path != "/api/v3/tts/voice_clone" {
		t.Errorf("path = %q", path)
	}
	// Voice management names this header differently from synthesis; sending
	// X-Api-App-Id here fails authentication.
	if headers.Get("X-Api-App-Key") != "123456789" {
		t.Errorf("X-Api-App-Key = %q, want the app id", headers.Get("X-Api-App-Key"))
	}
	if headers.Get("X-Api-App-Id") != "" {
		t.Error("X-Api-App-Id must not be used on voice management endpoints")
	}
	if headers.Get("X-Api-Request-Id") == "" {
		t.Error("request id is mandatory on voice management endpoints")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.Audio.Data)
	if err != nil {
		t.Fatalf("audio payload is not base64: %v", err)
	}
	if !bytes.Equal(decoded, []byte("RIFFxxxx")) {
		t.Errorf("audio = %q, want the uploaded bytes", decoded)
	}
	if payload.SpeakerID != "S_example" || payload.Audio.Format != "wav" {
		t.Errorf("unexpected payload: %+v", payload)
	}
	if payload.ExtraParams.DemoText != "这是一段试听文本" {
		t.Errorf("audition text = %q", payload.ExtraParams.DemoText)
	}
}

func TestTrainVoiceValidatesInput(t *testing.T) {
	client := &Client{BaseURL: "https://example.invalid", APIKey: "k"}
	cases := map[string]TrainVoiceRequest{
		"missing speaker":   {Audio: []byte("a")},
		"empty recording":   {SpeakerID: "S_example"},
		"oversized":         {SpeakerID: "S_example", Audio: make([]byte, (10<<20)+1)},
		"short audition":    {SpeakerID: "S_example", Audio: []byte("a"), AuditionText: "太短"},
		"overlong audition": {SpeakerID: "S_example", Audio: []byte("a"), AuditionText: strings.Repeat("字", 301)},
	}
	for name, req := range cases {
		if err := client.TrainVoice(context.Background(), req); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

// A postpaid voice has no purchased slot, so its self-chosen name travels in a
// second field while speaker_id carries a fixed placeholder.
func TestTrainVoiceAddressesPostpaidVoiceByName(t *testing.T) {
	var payload struct {
		SpeakerID       string `json:"speaker_id"`
		CustomSpeakerID string `json:"custom_speaker_id"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, `{"BaseResp":{"StatusCode":0,"StatusMessage":"success"}}`)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()}

	err := client.TrainVoice(context.Background(), TrainVoiceRequest{
		CustomSpeakerID: "ConsoleVoiceOne", Audio: []byte("RIFFxxxx"), Format: "wav",
	})
	if err != nil {
		t.Fatalf("TrainVoice: %v", err)
	}
	if payload.SpeakerID != PostpaidSpeakerPlaceholder {
		t.Errorf("speaker_id = %q, want the postpaid placeholder", payload.SpeakerID)
	}
	if payload.CustomSpeakerID != "ConsoleVoiceOne" {
		t.Errorf("custom_speaker_id = %q, want the self-chosen name", payload.CustomSpeakerID)
	}
}

func TestVoiceStatusAddressesPostpaidVoiceByName(t *testing.T) {
	var payload struct {
		SpeakerID       string `json:"speaker_id"`
		CustomSpeakerID string `json:"custom_speaker_id"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, voiceStatusBody)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()}

	if _, err := client.VoiceStatus(context.Background(), "", "ConsoleVoiceOne"); err != nil {
		t.Fatalf("VoiceStatus: %v", err)
	}
	if payload.SpeakerID != PostpaidSpeakerPlaceholder || payload.CustomSpeakerID != "ConsoleVoiceOne" {
		t.Errorf("payload = %+v, want the postpaid addressing pair", payload)
	}
}

// Checking the vendor's naming rules locally matters because a rejected name
// still spends one of the account's limited training calls.
func TestValidateCustomSpeakerName(t *testing.T) {
	valid := []string{"ConsoleVoiceOne", "Anchor-Female-01", "NarratorVoice_2"}
	for _, name := range valid {
		if err := ValidateCustomSpeakerName(name); err != nil {
			t.Errorf("ValidateCustomSpeakerName(%q) = %v, want no error", name, err)
		}
	}
	invalid := map[string]string{
		"empty":              "",
		"too short":          "Voice1",
		"reserved prefix":    "S_ConsoleVoice",
		"lowercase pair":     "my_voice_one",
		"reserved suffix":    "ConsoleVoice_bigtts",
		"leading digit":      "1ConsoleVoice",
		"trailing separator": "ConsoleVoice_",
		"illegal character":  "Console Voice",
		"non-ascii":          "旁白音色主播",
	}
	for name, value := range invalid {
		if err := ValidateCustomSpeakerName(value); err == nil {
			t.Errorf("%s: ValidateCustomSpeakerName(%q) should be rejected", name, value)
		}
	}
}

func TestTrainVoiceRejectsIllegalPostpaidName(t *testing.T) {
	client := &Client{BaseURL: "https://example.invalid", APIKey: "k"}
	err := client.TrainVoice(context.Background(), TrainVoiceRequest{
		CustomSpeakerID: "S_taken", Audio: []byte("a"),
	})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error = %v, want the naming rule explained before any upload", err)
	}
}

func TestVoiceStatusParsesModelVariants(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/tts/get_voice" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, voiceStatusBody)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()}

	status, err := client.VoiceStatus(context.Background(), "S_example")
	if err != nil {
		t.Fatalf("VoiceStatus: %v", err)
	}
	if !status.Ready() {
		t.Errorf("status %d should be usable for synthesis", status.Status)
	}
	if status.RemainingTrainings != 15 {
		t.Errorf("remaining trainings = %d, want 15", status.RemainingTrainings)
	}
	// One training run yielding several variants is the evidence that the model
	// generation is chosen at synthesis time, not at training time.
	if len(status.Models) != 2 || status.Models[1].ModelType != 4 {
		t.Errorf("models = %+v, want two variants including model_type 4", status.Models)
	}
}

func TestWaitForVoicePollsUntilReady(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"speaker_id":"S_example","status":1,"available_training_times":14}`)
			return
		}
		fmt.Fprint(w, voiceStatusBody)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()}

	status, err := client.WaitForVoice(context.Background(), "S_example", time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForVoice: %v", err)
	}
	if !status.Ready() {
		t.Errorf("final status = %d", status.Status)
	}
	if calls.Load() < 2 {
		t.Errorf("expected polling, got %d call(s)", calls.Load())
	}
}

func TestWaitForVoiceReportsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"speaker_id":"S_example","status":3}`)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()}

	_, err := client.WaitForVoice(context.Background(), "S_example", time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "training failed") {
		t.Fatalf("error = %v, want a training failure", err)
	}
}

func TestVoiceManagementSurfacesVendorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"BaseResp":{"StatusCode":45001107,"StatusMessage":"SpeakerID not found"}}`)
	}))
	t.Cleanup(server.Close)
	client := &Client{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()}

	_, err := client.VoiceStatus(context.Background(), "S_missing")
	if err == nil {
		t.Fatal("expected an error for a vendor failure envelope")
	}
	if !strings.Contains(err.Error(), "45001107") {
		t.Errorf("error should carry the vendor code, got %v", err)
	}
}

func TestVoiceManagementRequiresCredentials(t *testing.T) {
	client := &Client{BaseURL: "https://example.invalid"}
	if _, err := client.VoiceStatus(context.Background(), "S_example"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}
