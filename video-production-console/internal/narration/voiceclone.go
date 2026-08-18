package narration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Voice training states as reported by the vendor. Both Success and Active can
// synthesize; the split exists because a voice may be explicitly locked after
// training, which stops further retraining.
const (
	VoiceNotFound = 0
	VoiceTraining = 1
	VoiceSuccess  = 2
	VoiceFailed   = 3
	VoiceActive   = 4
)

// VoiceModel is one rendered variant of a trained voice. A single training run
// produces several, and which one a synthesis call reaches is decided by the
// resource id rather than by anything chosen at training time.
type VoiceModel struct {
	ModelType int    `json:"model_type"`
	DemoAudio string `json:"demo_audio"`
}

// VoiceStatus is the result of a training status query.
type VoiceStatus struct {
	SpeakerID string `json:"speaker_id"`
	Status    int    `json:"status"`
	Language  int    `json:"language"`
	// RemainingTrainings counts retraining attempts still allowed. The vendor's
	// own docs disagree on the cap, so trust this value over any documented
	// number.
	RemainingTrainings int          `json:"remaining_trainings"`
	Models             []VoiceModel `json:"models"`
}

// Ready reports whether the voice can be used for synthesis.
func (s VoiceStatus) Ready() bool {
	return s.Status == VoiceSuccess || s.Status == VoiceActive
}

// PostpaidSpeakerPlaceholder is the literal the vendor requires in speaker_id
// when the real name travels in custom_speaker_id instead.
const PostpaidSpeakerPlaceholder = "custom_speaker_id"

// reservedSpeakerName is the vendor's own rejection pattern for self-chosen
// voice names, reproduced verbatim from its documentation. It encodes the
// length and character limits alongside the reserved prefixes and suffixes, so
// a match means the name is unusable. Note that it also rejects any name whose
// first two characters are lowercase letters followed by an underscore, which
// rules out otherwise plausible names such as "my_voice".
var reservedSpeakerName = regexp.MustCompile(`^((?i:S_|ICL_|MIX_|DiT_|BV)|[a-z]{2}_|(?i:(wvae|moon|mercury|venus|earth|mars|jupiter|saturn|uranus|neptune|pluto|umm)_)).*|.*_(?i:bigtts|bigtts_cc|tob|cs_tob|streaming)$|^[^a-zA-Z]|.*[-_]$|^.{0,7}$|^.{257,}$|.*[^a-zA-Z0-9_-].*`)

// ValidateCustomSpeakerName reports whether a self-chosen postpaid voice name
// is one the vendor will accept. Checking locally keeps a rejected name from
// consuming a training call.
func ValidateCustomSpeakerName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("custom speaker name is empty")
	}
	if reservedSpeakerName.MatchString(name) {
		return fmt.Errorf("custom speaker name %q is reserved or malformed: it must be 8-256 characters of letters, digits, hyphen or underscore, start with a letter, not end with a hyphen or underscore, and must not begin with a reserved prefix such as S_ or two lowercase letters and an underscore", name)
	}
	return nil
}

// TrainVoiceRequest uploads a reference recording for cloning. Keep the
// recording to roughly 20 seconds of clean single-channel speech: the vendor
// truncates at 25 seconds by default, and a truncated tail can preserve exactly
// the flawed part of the take.
type TrainVoiceRequest struct {
	SpeakerID string
	// CustomSpeakerID names a postpaid voice, which needs no purchased slot.
	// Setting it replaces SpeakerID with the vendor's fixed placeholder. A
	// postpaid voice may be retrained without limit until the first synthesis
	// call, which fixes the voice and bills for it.
	CustomSpeakerID string
	Audio           []byte
	// Format is required for pcm and m4a and optional otherwise.
	Format string
	// Language selects the audition script's language; 0 is Chinese. The
	// recording must match it.
	Language int
	// AuditionText must be between 4 and 300 characters when set. It is
	// synthesized for audition, which is billed as ordinary synthesis.
	AuditionText string
}

// TrainVoice submits a reference recording for cloning. It returns once the
// vendor has accepted the upload; training itself completes asynchronously, so
// poll with VoiceStatus.
func (c *Client) TrainVoice(ctx context.Context, req TrainVoiceRequest) error {
	if strings.TrimSpace(req.SpeakerID) == "" && strings.TrimSpace(req.CustomSpeakerID) == "" {
		return fmt.Errorf("%w: missing speaker id", ErrNotConfigured)
	}
	if len(req.Audio) == 0 {
		return errors.New("reference recording is empty")
	}
	if len(req.Audio) > 10<<20 {
		return fmt.Errorf("reference recording is %d bytes, above the 10MB limit", len(req.Audio))
	}
	if n := len([]rune(req.AuditionText)); req.AuditionText != "" && (n < 4 || n > 300) {
		return fmt.Errorf("audition text must be 4-300 characters, got %d", n)
	}
	audio := map[string]any{"data": base64.StdEncoding.EncodeToString(req.Audio)}
	if req.Format != "" {
		audio["format"] = req.Format
	}
	payload := map[string]any{
		"audio":    audio,
		"language": req.Language,
	}
	if err := addSpeakerFields(payload, req.SpeakerID, req.CustomSpeakerID); err != nil {
		return err
	}
	if req.AuditionText != "" {
		payload["extra_params"] = map[string]any{"demo_text": req.AuditionText}
	}
	var out struct{}
	return c.doVoiceJSON(ctx, "/api/v3/tts/voice_clone", payload, &out)
}

// addSpeakerFields writes the vendor's two-field voice addressing. A postpaid
// voice's self-chosen name is only accepted in custom_speaker_id, with a fixed
// placeholder standing in for the slot id it does not have.
func addSpeakerFields(payload map[string]any, speakerID, customSpeakerID string) error {
	custom := strings.TrimSpace(customSpeakerID)
	if custom == "" {
		payload["speaker_id"] = speakerID
		return nil
	}
	if err := ValidateCustomSpeakerName(custom); err != nil {
		return err
	}
	payload["speaker_id"] = PostpaidSpeakerPlaceholder
	payload["custom_speaker_id"] = custom
	return nil
}

// VoiceStatus reports training progress for a speaker id. A postpaid voice is
// addressed by passing its self-chosen name as customSpeakerID, in which case
// speakerID is ignored.
func (c *Client) VoiceStatus(ctx context.Context, speakerID string, customSpeakerID ...string) (VoiceStatus, error) {
	if len(customSpeakerID) > 1 {
		return VoiceStatus{}, errors.New("at most one custom speaker id may be supplied")
	}
	custom := ""
	if len(customSpeakerID) == 1 {
		custom = strings.TrimSpace(customSpeakerID[0])
	}
	if strings.TrimSpace(speakerID) == "" && custom == "" {
		return VoiceStatus{}, fmt.Errorf("%w: missing speaker id", ErrNotConfigured)
	}
	var out struct {
		SpeakerID              string `json:"speaker_id"`
		Status                 int    `json:"status"`
		Language               int    `json:"language"`
		AvailableTrainingTimes int    `json:"available_training_times"`
		SpeakerStatus          []struct {
			ModelType int    `json:"model_type"`
			DemoAudio string `json:"demo_audio"`
		} `json:"speaker_status"`
	}
	payload := map[string]any{}
	if err := addSpeakerFields(payload, speakerID, custom); err != nil {
		return VoiceStatus{}, err
	}
	if err := c.doVoiceJSON(ctx, "/api/v3/tts/get_voice", payload, &out); err != nil {
		return VoiceStatus{}, err
	}
	status := VoiceStatus{
		SpeakerID:          out.SpeakerID,
		Status:             out.Status,
		Language:           out.Language,
		RemainingTrainings: out.AvailableTrainingTimes,
	}
	for _, model := range out.SpeakerStatus {
		status.Models = append(status.Models, VoiceModel{ModelType: model.ModelType, DemoAudio: model.DemoAudio})
	}
	return status, nil
}

// WaitForVoice polls until the voice is usable, training fails, or the context
// ends.
func (c *Client) WaitForVoice(ctx context.Context, speakerID string, interval time.Duration, customSpeakerID ...string) (VoiceStatus, error) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	name := speakerID
	if len(customSpeakerID) == 1 && strings.TrimSpace(customSpeakerID[0]) != "" {
		name = strings.TrimSpace(customSpeakerID[0])
	}
	for {
		status, err := c.VoiceStatus(ctx, speakerID, customSpeakerID...)
		if err != nil {
			return VoiceStatus{}, err
		}
		switch {
		case status.Ready():
			return status, nil
		case status.Status == VoiceFailed:
			return status, fmt.Errorf("voice training failed for %q", name)
		case status.Status == VoiceNotFound:
			return status, fmt.Errorf("voice %q was not found", name)
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// doVoiceJSON posts to a voice-management endpoint. These endpoints name the app
// credential header differently from the synthesis endpoint, so the header set is
// built separately rather than shared.
func (c *Client) doVoiceJSON(ctx context.Context, path string, payload any, out any) error {
	target, err := c.endpoint(path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Resource-Id", c.resourceID())
	req.Header.Set("X-Api-Request-Id", newRequestID())
	switch {
	case strings.TrimSpace(c.APIKey) != "":
		req.Header.Set("X-Api-Key", c.APIKey)
	case strings.TrimSpace(c.AppID) != "" && strings.TrimSpace(c.AccessToken) != "":
		// Voice management expects X-Api-App-Key here, where synthesis expects
		// X-Api-App-Id. Sending the wrong one fails authentication.
		req.Header.Set("X-Api-App-Key", c.AppID)
		req.Header.Set("X-Api-Access-Key", c.AccessToken)
	default:
		return fmt.Errorf("%w: missing API key or app id/access token", ErrNotConfigured)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("voice management HTTP %s (logid %s): %s",
			resp.Status, resp.Header.Get("X-Tt-Logid"), strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		BaseResp struct {
			StatusCode    int    `json:"StatusCode"`
			StatusMessage string `json:"StatusMessage"`
		} `json:"BaseResp"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.BaseResp.StatusCode != 0 {
		return fmt.Errorf("voice management failed: code %d: %s",
			envelope.BaseResp.StatusCode, envelope.BaseResp.StatusMessage)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
