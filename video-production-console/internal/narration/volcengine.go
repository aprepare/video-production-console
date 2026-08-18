// Package narration synthesizes narration audio and its subtitle track from a
// script. Word timings come from the synthesis vendor rather than a separate
// speech-recognition pass, so caption text is always the original script.
package narration

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// DefaultBaseURL is the Volcengine large-model speech endpoint host.
const DefaultBaseURL = "https://openspeech.bytedance.com"

// ResourceVoiceClone2 selects the 声音复刻 2.0 model. Subtitle timings are only
// produced by the 2.0 families; the 1.0 families use a different, incompatible
// parameter (enable_timestamp) and must not be used here.
const ResourceVoiceClone2 = "seed-icl-2.0"

// synthesisSuccess is the terminal packet code that ends a synthesis stream.
const synthesisSuccess = 20000000

// Word is one original-script token with its timing. Times are seconds measured
// from the start of the session, so they are directly comparable across the
// whole response.
type Word struct {
	Text       string  `json:"text"`
	StartTime  float64 `json:"start_time"`
	EndTime    float64 `json:"end_time"`
	Confidence float64 `json:"confidence"`
}

// Request describes one synthesis call. Text is sent whole: the vendor segments
// it internally and produces better prosody than pre-split input, and keeping a
// single session means every timestamp shares one origin.
type Request struct {
	Text       string
	SpeakerID  string
	Format     string
	SampleRate int
	// SpeechRate maps to the Volcengine vendor's [-50,100] range where 0 is
	// unmodified and 100 is double speed. AuraSTD uses Speed instead.
	SpeechRate int
	// Provider selects the synthesis backend. Empty means the client type.
	Provider string
	Model    string
	Speed    float64
	Volume   float64
	Pitch    int
	Emotion  string
	LanguageBoost   string
	ModifyPitch     int
	ModifyIntensity int
	ModifyTimbre    int
	SoundEffects    string
}

// Result carries the rendered audio plus the timings needed for captions.
type Result struct {
	Audio []byte `json:"-"`
	Words []Word `json:"words"`
	// BilledWords is the vendor's own character count for this call.
	BilledWords int `json:"billed_words"`
}

// Client calls the Volcengine large-model speech synthesis API.
type Client struct {
	BaseURL string
	// APIKey authenticates against the current console. When it is empty the
	// legacy AppID/AccessToken pair is used instead.
	APIKey      string
	AppID       string
	AccessToken string
	// ResourceID must match the voice's model generation. Defaults to
	// ResourceVoiceClone2.
	ResourceID       string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

// ErrNotConfigured reports missing endpoint or credentials.
var ErrNotConfigured = errors.New("narration synthesis is not configured")

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (c *Client) baseURL() string {
	if c != nil && strings.TrimSpace(c.BaseURL) != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) resourceID() string {
	if c != nil && strings.TrimSpace(c.ResourceID) != "" {
		return c.ResourceID
	}
	return ResourceVoiceClone2
}

func (c *Client) maxResponseBytes() int64 {
	if c != nil && c.MaxResponseBytes > 0 {
		return c.MaxResponseBytes
	}
	return 128 << 20
}

func (c *Client) endpoint(path string) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	u, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme %q", ErrNotConfigured, u.Scheme)
	}
	return u.String(), nil
}

func (c *Client) authHeaders(h http.Header) error {
	switch {
	case c != nil && strings.TrimSpace(c.APIKey) != "":
		h.Set("X-Api-Key", c.APIKey)
	case c != nil && strings.TrimSpace(c.AppID) != "" && strings.TrimSpace(c.AccessToken) != "":
		h.Set("X-Api-App-Id", c.AppID)
		h.Set("X-Api-Access-Key", c.AccessToken)
	default:
		return fmt.Errorf("%w: missing API key or app id/access token", ErrNotConfigured)
	}
	return nil
}

// Synthesize renders text with a cloned voice and returns the audio together
// with per-word timings.
func (c *Client) Synthesize(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Text) == "" {
		return Result{}, errors.New("narration text is empty")
	}
	if strings.TrimSpace(req.SpeakerID) == "" {
		return Result{}, fmt.Errorf("%w: missing speaker id", ErrNotConfigured)
	}
	target, err := c.endpoint("/api/v3/tts/unidirectional")
	if err != nil {
		return Result{}, err
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}
	sampleRate := req.SampleRate
	if sampleRate == 0 {
		sampleRate = 24000
	}
	body, err := json.Marshal(map[string]any{
		"req_params": map[string]any{
			"text":    req.Text,
			"speaker": req.SpeakerID,
			"audio_params": map[string]any{
				"format":          format,
				"sample_rate":     sampleRate,
				"speech_rate":     req.SpeechRate,
				"enable_subtitle": true,
			},
		},
	})
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Api-Resource-Id", c.resourceID())
	httpReq.Header.Set("X-Api-Request-Id", newRequestID())
	httpReq.Header.Set("X-Control-Require-Usage-Tokens-Return", "text_words")
	if err := c.authHeaders(httpReq.Header); err != nil {
		return Result{}, err
	}
	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("narration synthesis HTTP %s (logid %s)", resp.Status, resp.Header.Get("X-Tt-Logid"))
	}
	result, err := readSynthesisStream(io.LimitReader(resp.Body, c.maxResponseBytes()), resp.Header.Get("Content-Type"))
	if err != nil {
		if logID := resp.Header.Get("X-Tt-Logid"); logID != "" {
			return Result{}, fmt.Errorf("%w (logid %s)", err, logID)
		}
		return Result{}, err
	}
	return result, nil
}

// vendorPacket mirrors the wire format, which uses camelCase keys and reports
// timings in floating-point seconds.
type vendorPacket struct {
	Code     int64  `json:"code"`
	Message  string `json:"message"`
	Data     string `json:"data"`
	Sentence *struct {
		Text  string `json:"text"`
		Words []struct {
			Word       string  `json:"word"`
			StartTime  float64 `json:"startTime"`
			EndTime    float64 `json:"endTime"`
			Confidence float64 `json:"confidence"`
		} `json:"words"`
	} `json:"sentence"`
	Usage *struct {
		TextWords int `json:"text_words"`
	} `json:"usage"`
}

// readSynthesisStream collects audio chunks and subtitle packets until the
// terminal packet. Subtitle packets arrive asynchronously and may lag behind the
// audio, so words are only ordered once the stream has ended.
func readSynthesisStream(body io.Reader, contentType string) (Result, error) {
	var (
		result Result
		audio  []byte
		done   bool
	)
	err := eachPacket(body, contentType, func(raw []byte) error {
		var pkt vendorPacket
		if err := json.Unmarshal(raw, &pkt); err != nil {
			return fmt.Errorf("decode synthesis packet: %w", err)
		}
		if pkt.Code == synthesisSuccess {
			done = true
			if pkt.Usage != nil {
				result.BilledWords = pkt.Usage.TextWords
			}
			return nil
		}
		if pkt.Code != 0 {
			return fmt.Errorf("narration synthesis failed: code %d: %s", pkt.Code, pkt.Message)
		}
		if pkt.Data != "" {
			chunk, err := base64.StdEncoding.DecodeString(pkt.Data)
			if err != nil {
				return fmt.Errorf("decode audio chunk: %w", err)
			}
			audio = append(audio, chunk...)
		}
		if pkt.Sentence != nil {
			for _, w := range pkt.Sentence.Words {
				if w.Word == "" {
					continue
				}
				result.Words = append(result.Words, Word{
					Text:       w.Word,
					StartTime:  w.StartTime,
					EndTime:    w.EndTime,
					Confidence: w.Confidence,
				})
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if !done {
		return Result{}, errors.New("narration synthesis stream ended without a success packet")
	}
	if len(audio) == 0 {
		return Result{}, errors.New("narration synthesis returned no audio")
	}
	sort.SliceStable(result.Words, func(i, j int) bool {
		return result.Words[i].StartTime < result.Words[j].StartTime
	})
	result.Audio = audio
	return result, nil
}

// eachPacket walks the response body, which is either a stream of concatenated
// JSON values (chunked transfer) or server-sent events carrying the same JSON.
func eachPacket(body io.Reader, contentType string, fn func([]byte) error) error {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		dec := json.NewDecoder(body)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			if err := fn(raw); err != nil {
				return err
			}
		}
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := data.String()
		data.Reset()
		return fn([]byte(payload))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data.WriteString(strings.TrimSpace(rest))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
