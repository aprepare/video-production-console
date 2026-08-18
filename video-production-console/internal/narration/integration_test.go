package narration

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests talk to the real vendor and are skipped unless credentials are
// present in the environment. They exist because two things cannot be settled
// from the documentation: the vendor never states the unit of its subtitle
// timestamps, and the subtitle event code is absent from the published event
// table.
//
//	VOLC_SPEECH_API_KEY   new-console API key
//	VOLC_SPEECH_APP_ID    legacy console app id
//	VOLC_SPEECH_TOKEN     legacy console access token
//	VOLC_SPEECH_SPEAKER   speaker id to synthesize with
//	VOLC_SPEECH_RESOURCE  resource id, defaults to the voice-clone 2.0 family
func integrationClient(t *testing.T) *Client {
	t.Helper()
	apiKey := os.Getenv("VOLC_SPEECH_API_KEY")
	appID := os.Getenv("VOLC_SPEECH_APP_ID")
	token := os.Getenv("VOLC_SPEECH_TOKEN")
	if apiKey == "" && (appID == "" || token == "") {
		t.Skip("set VOLC_SPEECH_API_KEY or VOLC_SPEECH_APP_ID+VOLC_SPEECH_TOKEN to run vendor tests")
	}
	client := &Client{APIKey: apiKey, AppID: appID, AccessToken: token}
	if resource := os.Getenv("VOLC_SPEECH_RESOURCE"); resource != "" {
		client.ResourceID = resource
	}
	return client
}

// TestVendorCredentialsAreAccepted probes a voice-management endpoint with a
// speaker that cannot exist. A "not found" answer proves the credentials were
// accepted, and costs nothing because no audio is synthesized.
func TestVendorCredentialsAreAccepted(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := client.VoiceStatus(ctx, "S_definitely_absent_probe")
	if err == nil {
		t.Logf("unexpected success, status: %+v", status)
		return
	}
	message := err.Error()
	t.Logf("probe response: %v", message)
	for _, denied := range []string{"access denied", "permission denied", "authenticate", "45000000"} {
		if strings.Contains(strings.ToLower(message), denied) {
			t.Fatalf("credentials were rejected: %v", err)
		}
	}
}

// TestVendorTrainVoice clones a voice from a local recording. It spends one of
// the speaker's limited training attempts, so it only runs when the recording
// path is given explicitly.
//
//	VOLC_SPEECH_TRAIN_AUDIO  path to the reference recording
//	VOLC_SPEECH_SPEAKER      speaker slot to train into
func TestVendorTrainVoice(t *testing.T) {
	client := integrationClient(t)
	path := os.Getenv("VOLC_SPEECH_TRAIN_AUDIO")
	speaker := os.Getenv("VOLC_SPEECH_SPEAKER")
	if path == "" || speaker == "" {
		t.Skip("set VOLC_SPEECH_TRAIN_AUDIO and VOLC_SPEECH_SPEAKER to train a voice")
	}
	audio, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read reference recording: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	before, err := client.VoiceStatus(ctx, speaker)
	if err != nil {
		t.Logf("status before training: %v", err)
	} else {
		t.Logf("status before training: %+v", before)
	}

	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if err := client.TrainVoice(ctx, TrainVoiceRequest{
		SpeakerID: speaker, Audio: audio, Format: format,
	}); err != nil {
		t.Fatalf("TrainVoice: %v", err)
	}

	status, err := client.WaitForVoice(ctx, speaker, 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForVoice: %v (status %+v)", err, status)
	}
	t.Logf("trained: status=%d remaining_trainings=%d", status.Status, status.RemainingTrainings)
	// One training run producing several variants confirms that the model
	// generation is picked at synthesis time via the resource id.
	for _, model := range status.Models {
		t.Logf("model_type=%d audition=%s", model.ModelType, model.DemoAudio)
	}
}

// TestVendorTimestampsAreSeconds resolves the unit question by measuring the
// audio instead of trusting the documentation. Raw PCM has an exactly computable
// duration, so the final word's end time can be compared against it: if the
// numbers were milliseconds they would be off by three orders of magnitude.
func TestVendorTimestampsAreSeconds(t *testing.T) {
	client := integrationClient(t)
	speaker := os.Getenv("VOLC_SPEECH_SPEAKER")
	if speaker == "" {
		t.Skip("set VOLC_SPEECH_SPEAKER to run the synthesis test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const script = "今天我们聊一个很重要的话题，这个话题关系到你的钱包。"
	const sampleRate = 24000

	result, err := client.Synthesize(ctx, Request{
		Text: script, SpeakerID: speaker, Format: "pcm", SampleRate: sampleRate,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(result.Words) == 0 {
		t.Fatal("no subtitle words returned; enable_subtitle may not apply to this voice or resource id")
	}

	// 16-bit mono PCM at the requested rate.
	audioSeconds := float64(len(result.Audio)) / float64(sampleRate*2)
	last := result.Words[len(result.Words)-1].EndTime
	t.Logf("audio %.3fs, %d words, last end time %.4f, billed %d",
		audioSeconds, len(result.Words), last, result.BilledWords)
	t.Logf("first three words: %+v", result.Words[:min(3, len(result.Words))])

	if math.Abs(last-audioSeconds) > math.Max(1.0, audioSeconds*0.25) {
		t.Errorf("last end time %.4f does not match the %.3fs of audio; timestamps are not seconds",
			last, audioSeconds)
	}
	for i := 1; i < len(result.Words); i++ {
		if result.Words[i].StartTime < result.Words[i-1].StartTime {
			t.Errorf("words are not in order at %d: %+v", i, result.Words[i-1:i+1])
			break
		}
	}

	captions, report, err := Compose(script, result.Words, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	t.Logf("coverage %.4f, pass %v, failures %v, warnings %v",
		report.TextCoverage, report.Pass, report.Failures, report.Warnings)
	for _, caption := range captions {
		t.Logf("%7.3f -> %7.3f  %s", caption.Start, caption.End, caption.Text)
	}
	if report.TextCoverage < 1 {
		t.Errorf("coverage %.4f: the vendor did not return the script verbatim, "+
			"so caption text cannot be taken from its tokens as-is", report.TextCoverage)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
