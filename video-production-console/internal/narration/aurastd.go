package narration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAuraSTDBaseURL = "https://tts.aurastd.com"
	DefaultAuraSTDModel   = "speech-2.8-hd"
	auraSTDTTsPath        = "/api/v1/tts"
	defaultAuraSTDMaxRunesPerChunk = 1500
)

// AuraSTDClient calls the Aura Studio MiniMax-compatible TTS API.
type AuraSTDClient struct {
	BaseURL          string
	APIKey           string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

func (c *AuraSTDClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 8 * time.Minute}
}

func (c *AuraSTDClient) baseURL() string {
	if c != nil && strings.TrimSpace(c.BaseURL) != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultAuraSTDBaseURL
}

func (c *AuraSTDClient) maxResponseBytes() int64 {
	if c != nil && c.MaxResponseBytes > 0 {
		return c.MaxResponseBytes
	}
	return 64 << 20
}

func (c *AuraSTDClient) endpoint() (string, error) {
	if c == nil || strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("%w: missing Aura Studio API key", ErrNotConfigured)
	}
	u, err := url.Parse(c.baseURL() + auraSTDTTsPath)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme %q", ErrNotConfigured, u.Scheme)
	}
	return u.String(), nil
}

type auraSTDVoiceSetting struct {
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed"`
	Vol     float64 `json:"vol"`
	Pitch   int     `json:"pitch"`
	Emotion string  `json:"emotion,omitempty"`
}

type auraSTDAudioSetting struct {
	SampleRate int    `json:"sample_rate"`
	Bitrate    int    `json:"bitrate"`
	Format     string `json:"format"`
	Channel    int    `json:"channel"`
}

type auraSTDVoiceModify struct {
	Pitch        int    `json:"pitch"`
	Intensity    int    `json:"intensity"`
	Timbre       int    `json:"timbre"`
	SoundEffects string `json:"sound_effects,omitempty"`
}

type auraSTDRequest struct {
	Model           string              `json:"model"`
	Text            string              `json:"text"`
	Stream          bool                `json:"stream"`
	LanguageBoost   string              `json:"language_boost,omitempty"`
	OutputFormat    string              `json:"output_format"`
	VoiceSetting    auraSTDVoiceSetting `json:"voice_setting"`
	AudioSetting    auraSTDAudioSetting `json:"audio_setting"`
	VoiceModify     auraSTDVoiceModify  `json:"voice_modify"`
	SubtitleEnable  bool                `json:"subtitle_enable"`
	SubtitleType    string              `json:"subtitle_type"`
	ContinuousSound bool                `json:"continuous_sound"`
}

type auraSTDEnvelope struct {
	Audio        string           `json:"audio"`
	SubtitleFile string           `json:"subtitle_file"`
	Status       auraSTDStatus    `json:"status"`
	TraceID      string           `json:"trace_id"`
	Message      string           `json:"message"`
	Error        string           `json:"error"`
	Data         *auraSTDData     `json:"data"`
	ExtraInfo    *auraSTDExtra    `json:"extra_info"`
	BaseResp     *auraSTDBaseResp `json:"base_resp"`
}

type auraSTDData struct {
	Audio        string        `json:"audio"`
	SubtitleFile string        `json:"subtitle_file"`
	Status       auraSTDStatus `json:"status"`
}

// auraSTDStatus accepts MiniMax integer codes and Aura Studio strings such as "success".
type auraSTDStatus int

func (s *auraSTDStatus) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		*s = 0
		return nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			*s = 0
			return nil
		}
		if n, err := strconv.Atoi(text); err == nil {
			*s = auraSTDStatus(n)
			return nil
		}
		*s = 0
		return nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	*s = auraSTDStatus(n)
	return nil
}

type auraSTDExtra struct {
	UsageCharacters int `json:"usage_characters"`
	WordCount       int `json:"word_count"`
}

type auraSTDBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type auraSTDCue struct {
	Text      string  `json:"text"`
	Word      string  `json:"word"`
	TimeBegin float64 `json:"time_begin"`
	TimeEnd   float64 `json:"time_end"`
}

// Synthesize renders text with a cloned MiniMax voice and word-level timings.
// Synthesize renders text with a cloned MiniMax voice and word-level timings.
// When the script exceeds defaultAuraSTDMaxRunesPerChunk, it transparently chunks
// the input to stay within Aura Studio's per-request limit and merges the output.
func (c *AuraSTDClient) Synthesize(ctx context.Context, req Request) (Result, error) {
	trimmed := strings.TrimSpace(req.Text)
	if trimmed == "" {
		return Result{}, fmt.Errorf("narration text is empty")
	}
	runes := []rune(trimmed)
	if len(runes) <= defaultAuraSTDMaxRunesPerChunk {
		return c.synthesizeSingle(ctx, req)
	}
	chunks := splitTextIntoChunks(trimmed, defaultAuraSTDMaxRunesPerChunk)
	if len(chunks) <= 1 {
		return c.synthesizeSingle(ctx, req)
	}

	var mergedAudio []byte
	var mergedWords []Word
	totalBilled := 0
	timeOffset := 0.0

	for _, chunkText := range chunks {
		chunkReq := req
		chunkReq.Text = chunkText
		chunkRes, err := c.synthesizeSingle(ctx, chunkReq)
		if err != nil {
			return Result{}, err
		}
		mergedAudio = append(mergedAudio, chunkRes.Audio...)
		totalBilled += chunkRes.BilledWords
		for _, w := range chunkRes.Words {
			mergedWords = append(mergedWords, Word{
				Text:       w.Text,
				StartTime:  w.StartTime + timeOffset,
				EndTime:    w.EndTime + timeOffset,
				Confidence: w.Confidence,
			})
		}
		if len(chunkRes.Words) > 0 {
			timeOffset += chunkRes.Words[len(chunkRes.Words)-1].EndTime
		}
	}
	if len(mergedAudio) > 0 {
		if cleaned, err := repackageConcatenatedMP3(ctx, mergedAudio); err == nil && len(cleaned) > 0 {
			mergedAudio = cleaned
		}
	}
	return Result{Audio: mergedAudio, Words: mergedWords, BilledWords: totalBilled}, nil
}

func (c *AuraSTDClient) synthesizeSingle(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Text) == "" {
		return Result{}, fmt.Errorf("narration text is empty")
	}
	if strings.TrimSpace(req.SpeakerID) == "" {
		return Result{}, fmt.Errorf("%w: missing voice id", ErrNotConfigured)
	}
	target, err := c.endpoint()
	if err != nil {
		return Result{}, err
	}
	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = "mp3"
	}
	sampleRate := req.SampleRate
	if sampleRate == 0 {
		sampleRate = 32000
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = DefaultAuraSTDModel
	}
	emotion := strings.TrimSpace(req.Emotion)
	body := auraSTDRequest{
		Model:         model,
		Text:          req.Text,
		Stream:        false,
		LanguageBoost: strings.TrimSpace(req.LanguageBoost),
		OutputFormat:  "url",
		VoiceSetting: auraSTDVoiceSetting{
			VoiceID: req.SpeakerID,
			Speed:   req.Speed,
			Vol:     req.Volume,
			Pitch:   req.Pitch,
			Emotion: emotion,
		},
		AudioSetting: auraSTDAudioSetting{
			SampleRate: sampleRate,
			Bitrate:    128000,
			Format:     format,
			Channel:    1,
		},
		VoiceModify: auraSTDVoiceModify{
			Pitch:        req.ModifyPitch,
			Intensity:    req.ModifyIntensity,
			Timbre:       req.ModifyTimbre,
			SoundEffects: strings.TrimSpace(req.SoundEffects),
		},
		SubtitleEnable:  true,
		SubtitleType:    "word",
		ContinuousSound: true,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.APIKey))
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes()))
	if err != nil {
		return Result{}, fmt.Errorf("read Aura Studio response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("Aura Studio HTTP %s: %s", resp.Status, clipAuraSTDError(raw))
	}
	var envelope auraSTDEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Result{}, fmt.Errorf("decode Aura Studio response: %w", err)
	}
	if envelope.BaseResp != nil && envelope.BaseResp.StatusCode != 0 {
		msg := strings.TrimSpace(envelope.BaseResp.StatusMsg)
		if msg == "" {
			msg = "synthesis failed"
		}
		return Result{}, fmt.Errorf("Aura Studio failed: code %d: %s", envelope.BaseResp.StatusCode, msg)
	}
	audioRef := envelope.Audio
	subtitleRef := envelope.SubtitleFile
	if envelope.Data != nil {
		if audioRef == "" {
			audioRef = envelope.Data.Audio
		}
		if subtitleRef == "" {
			subtitleRef = envelope.Data.SubtitleFile
		}
	}
	if msg := firstNonEmpty(envelope.Error, envelope.Message); audioRef == "" && msg != "" {
		return Result{}, fmt.Errorf("Aura Studio failed: %s", msg)
	}
	audio, err := c.resolveAudio(ctx, audioRef)
	if err != nil {
		return Result{}, err
	}
	if len(audio) == 0 {
		return Result{}, fmt.Errorf("Aura Studio returned no audio")
	}
	words, err := c.resolveWords(ctx, subtitleRef)
	if err != nil {
		return Result{}, err
	}
	billed := 0
	if envelope.ExtraInfo != nil {
		billed = envelope.ExtraInfo.UsageCharacters
		if billed == 0 {
			billed = envelope.ExtraInfo.WordCount
		}
	}
	if billed == 0 {
		billed = len([]rune(req.Text))
	}
	return Result{Audio: audio, Words: words, BilledWords: billed}, nil
}

func (c *AuraSTDClient) resolveAudio(ctx context.Context, ref string) ([]byte, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("Aura Studio returned empty audio")
	}
	if looksLikeURL(ref) {
		return c.download(ctx, ref)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("decode Aura Studio audio hex: %w", err)
	}
	return decoded, nil
}

func (c *AuraSTDClient) resolveWords(ctx context.Context, ref string) ([]Word, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("Aura Studio returned no subtitle file")
	}
	payload := []byte(ref)
	if looksLikeURL(ref) {
		body, err := c.download(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("download Aura Studio subtitles: %w", err)
		}
		payload = body
	}
	return parseAuraSTDSubtitles(payload)
}

func (c *AuraSTDClient) download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: HTTP %s", rawURL, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes()))
}

func parseAuraSTDSubtitles(raw []byte) ([]Word, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("Aura Studio subtitle file is empty")
	}
	var cues []auraSTDCue
	if err := json.Unmarshal(trimmed, &cues); err != nil {
		var wrapped struct {
			Subtitles []auraSTDCue `json:"subtitles"`
		}
		if wrapErr := json.Unmarshal(trimmed, &wrapped); wrapErr != nil || len(wrapped.Subtitles) == 0 {
			return nil, fmt.Errorf("decode Aura Studio subtitles: %w", err)
		}
		cues = wrapped.Subtitles
	}
	words := make([]Word, 0, len(cues))
	for _, cue := range cues {
		text := strings.TrimSpace(firstNonEmpty(cue.Text, cue.Word))
		if text == "" {
			continue
		}
		words = append(words, Word{
			Text:      text,
			StartTime: cue.TimeBegin / 1000,
			EndTime:   cue.TimeEnd / 1000,
		})
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("Aura Studio subtitle file contained no cues")
	}
	return words, nil
}

func looksLikeURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func clipAuraSTDError(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "empty body"
	}
	if len([]rune(text)) > 300 {
		return string([]rune(text)[:300])
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func splitTextIntoChunks(text string, maxRunes int) []string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return []string{text}
	}
	var chunks []string
	start := 0
	total := len(runes)
	delims := map[rune]bool{
		'。': true, '！': true, '？': true, '；': true, '\n': true,
		'.': true, '!': true, '?': true, ';': true,
	}
	secondaryDelims := map[rune]bool{
		'，': true, ',': true, '、': true,
	}

	for start < total {
		if total-start <= maxRunes {
			chunk := strings.TrimSpace(string(runes[start:]))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		end := start + maxRunes
		splitAt := -1

		lookback := 400
		if lookback > maxRunes/2 {
			lookback = maxRunes / 2
		}
		searchStart := end - lookback
		if searchStart < start {
			searchStart = start
		}

		for i := end - 1; i >= searchStart; i-- {
			if delims[runes[i]] {
				splitAt = i + 1
				break
			}
		}

		if splitAt == -1 {
			for i := end - 1; i >= searchStart; i-- {
				if secondaryDelims[runes[i]] {
					splitAt = i + 1
					break
				}
			}
		}

		if splitAt == -1 {
			for i := end - 1; i >= searchStart; i-- {
				if runes[i] == ' ' || runes[i] == '\t' {
					splitAt = i + 1
					break
				}
			}
		}

		if splitAt <= start {
			splitAt = end
		}

		chunk := strings.TrimSpace(string(runes[start:splitAt]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = splitAt
	}
	return chunks
}
func repackageConcatenatedMP3(ctx context.Context, rawMP3 []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-f", "mp3", "-i", "pipe:0", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "32000", "-f", "mp3", "pipe:1")
	cmd.Stdin = bytes.NewReader(rawMP3)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
