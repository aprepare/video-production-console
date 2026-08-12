package narration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestProduceBuildsAudioAndSubtitleFromOneSynthesis(t *testing.T) {
	var sent struct {
		ReqParams struct {
			Text    string `json:"text"`
			Speaker string `json:"speaker"`
		} `json:"req_params"`
	}
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		fmt.Fprint(w, audioPacket("AUDIO")+subtitlePacket+terminalPacket)
	})

	delivery, err := Produce(context.Background(), client, ProduceRequest{
		Script: "其他人。", SpeakerID: "custom_voice_one",
	})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if sent.ReqParams.Text != "其他人。" || sent.ReqParams.Speaker != "custom_voice_one" {
		t.Errorf("synthesis request = %+v", sent.ReqParams)
	}
	if string(delivery.Audio) != "AUDIO" {
		t.Errorf("audio = %q", delivery.Audio)
	}
	if delivery.AudioFormat != "mp3" {
		t.Errorf("format = %q, want the stored narration format", delivery.AudioFormat)
	}
	want := "1\n00:00:00,205 --> 00:00:00,815\n其他人。\n\n"
	if delivery.SRT != want {
		t.Errorf("SRT = %q, want %q", delivery.SRT, want)
	}
	if !delivery.Report.Pass || delivery.Report.TextCoverage != 1 {
		t.Errorf("report = %+v, want a passing gate over the whole script", delivery.Report)
	}
	if delivery.BilledWords != 4 {
		t.Errorf("billed characters = %d, want the vendor's own count", delivery.BilledWords)
	}
	if delivery.Duration != 0.815 {
		t.Errorf("duration = %v, want the last caption's end", delivery.Duration)
	}
}

// A cue that flashes too briefly to read is not deliverable, and the fix is an
// edit to the script rather than a retry, so the failure has to name the cue.
func TestProduceReportsQualityGateFailureButKeepsTheAudio(t *testing.T) {
	packet := `{"code":0,"message":"","data":null,"sentence":{"text":"短句","words":[` +
		`{"confidence":0.9,"startTime":0.0,"endTime":0.1,"word":"短句"}]}}`
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, audioPacket("AUDIO")+packet+terminalPacket)
	})

	delivery, err := Produce(context.Background(), client, ProduceRequest{Script: "短句", SpeakerID: "S_example"})
	var gate *QualityGateError
	if !errors.As(err, &gate) {
		t.Fatalf("error = %v, want a quality gate error", err)
	}
	if len(gate.Report.Failures) == 0 || !strings.Contains(gate.Error(), "below the") {
		t.Errorf("gate error = %v, want the failing cue named", gate)
	}
	if string(delivery.Audio) != "AUDIO" {
		t.Error("a failed gate must still return the audio it was billed for")
	}
}

// Word timings are the whole reason synthesis is used instead of a separate
// transcription pass, so their absence points at a misconfigured resource id
// rather than at the script.
func TestProduceRejectsSynthesisWithoutTimings(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, audioPacket("AUDIO")+terminalPacket)
	})

	_, err := Produce(context.Background(), client, ProduceRequest{Script: "文本", SpeakerID: "S_example"})
	if err == nil || !strings.Contains(err.Error(), "resource id") {
		t.Fatalf("error = %v, want the resource id named", err)
	}
}

func TestProduceRejectsEmptyAndOversizedScripts(t *testing.T) {
	client := &Client{BaseURL: "https://example.invalid", APIKey: "k"}
	if _, err := Produce(context.Background(), client, ProduceRequest{Script: "  \n\n", SpeakerID: "S_x"}); err == nil {
		t.Error("a script of only whitespace must be rejected before the vendor is called")
	}
	long := strings.Repeat("字", MaxScriptRunes+1)
	_, err := Produce(context.Background(), client, ProduceRequest{Script: long, SpeakerID: "S_x"})
	if err == nil || !strings.Contains(err.Error(), "above the") {
		t.Fatalf("error = %v, want the character limit named", err)
	}
}

func TestNormalizeScriptRemovesOnlyUnspokenDecoration(t *testing.T) {
	for _, tt := range []struct{ name, in, want string }{
		{"heading", "# 开场\n正文", "开场\n正文"},
		{"quote", "> 引用一句\n正文", "引用一句\n正文"},
		{"bullets", "- 第一点\n* 第二点\n+ 第三点", "第一点\n第二点\n第三点"},
		{"horizontal rules", "上半段\n---\n下半段", "上半段\n下半段"},
		{"blank runs", "一段\n\n\n二段", "一段\n二段"},
		{"windows newlines", "一段\r\n二段", "一段\n二段"},
		{"minus inside a sentence", "收益是 -3% 到 5%", "收益是 -3% 到 5%"},
		{"plain text is untouched", "今天聊一个话题，关系到你的钱包。", "今天聊一个话题，关系到你的钱包。"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeScript(tt.in); got != tt.want {
				t.Errorf("NormalizeScript(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
