package partnergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatProxyReplacesAuthorizationAndPreservesStreaming(t *testing.T) {
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"ok\":true}\n\n")
	}))
	defer upstream.Close()

	server, session := newHTTPFixture(t, upstream.URL+"/v1", "server-upstream-key")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.6-sol","reasoning_effort":"high","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+session)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotAuth != "Bearer server-upstream-key" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if !strings.Contains(w.Body.String(), `data: {"ok":true}`) {
		t.Fatalf("body=%q", w.Body.String())
	}
}

func TestChatProxyRejectsSessionFailures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*httpFixture)
		token   func(*httpFixture) string
	}{
		{
			name:  "missing session",
			token: func(*httpFixture) string { return "" },
		},
		{
			name: "expired session",
			prepare: func(f *httpFixture) {
				f.clock.Advance(2 * time.Hour)
			},
			token: func(f *httpFixture) string { return f.activated.SessionToken },
		},
		{
			name: "disabled partner",
			prepare: func(f *httpFixture) {
				if err := f.auth.SetPartnerStatus(context.Background(), f.activated.PartnerID, PartnerDisabled); err != nil {
					t.Fatal(err)
				}
			},
			token: func(f *httpFixture) string { return f.activated.SessionToken },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("unauthorized request reached upstream")
			}))
			defer upstream.Close()
			fixture := newHTTPTestEnvironment(t, upstream.URL+"/v1", "upstream-key", nil)
			if tc.prepare != nil {
				tc.prepare(fixture)
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(validChatBody))
			if token := tc.token(fixture); token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			fixture.handler.ServeHTTP(w, req)

			assertErrorResponse(t, w, http.StatusUnauthorized, "authorization_failed")
		})
	}
}

func TestChatProxyRejectsOversizedBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("oversized request reached upstream")
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", func(options *ServerOptions) {
		options.MaxRequestBytes = 96
	})
	body := `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"` + strings.Repeat("x", 256) + `"}]}`
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", body, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	assertErrorResponse(t, w, http.StatusRequestEntityTooLarge, "model_not_allowed")
}

func TestChatProxyMapsUpstreamTimeoutToStableError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, `{"late":true}`)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", func(options *ServerOptions) {
		options.HTTPClient = &http.Client{Timeout: 20 * time.Millisecond}
	})
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", validChatBody, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	assertErrorResponse(t, w, http.StatusBadGateway, "upstream_unavailable")
}

func TestChatProxyDefaultClientRefusesUpstreamRedirect(t *testing.T) {
	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		_, _ = io.WriteString(w, `{"stolen":true}`)
	}))
	defer redirectTarget.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", redirectTarget.URL+"/capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", func(options *ServerOptions) {
		options.HTTPClient = nil
	})
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", validChatBody, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	assertErrorResponse(t, w, http.StatusBadGateway, "upstream_unavailable")
	if redirectedRequests.Load() != 0 {
		t.Fatalf("redirect target requests=%d", redirectedRequests.Load())
	}
}

func TestChatProxyRejectsOversizedUpstreamResponseWithoutCountingSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"response exceeds the configured maximum"}`)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", func(options *ServerOptions) {
		options.MaxResponseBytes = 16
	})
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", validChatBody, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	assertErrorResponse(t, w, http.StatusBadGateway, "upstream_unavailable")
	partner, err := fixture.store.PartnerByID(context.Background(), fixture.activated.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if partner.TextCalls != 0 {
		t.Fatalf("text_calls=%d", partner.TextCalls)
	}
}

func TestChatProxySignalsOversizedStreamingResponseWithoutCountingSuccess(t *testing.T) {
	const upstreamAPIKey = "upstream-secret-key"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", 64)+"\n\n")
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, upstreamAPIKey, func(options *ServerOptions) {
		options.MaxResponseBytes = 16
	})
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", validChatBody, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	const errorEventPrefix = "event: error\ndata: "
	body := w.Body.String()
	eventIndex := strings.LastIndex(body, errorEventPrefix)
	if eventIndex == -1 {
		t.Fatalf("missing terminal SSE error event: body=%q", body)
	}
	var payload ErrorResponse
	payloadJSON := strings.TrimSpace(body[eventIndex+len(errorEventPrefix):])
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode terminal SSE error: %v payload=%q", err, payloadJSON)
	}
	if payload.Error.Code != "upstream_unavailable" {
		t.Fatalf("error code=%q payload=%q", payload.Error.Code, payloadJSON)
	}
	if strings.Contains(body, upstream.URL) || strings.Contains(body, upstreamAPIKey) {
		t.Fatalf("terminal SSE error leaked upstream details: body=%q", body)
	}
	partner, err := fixture.store.PartnerByID(context.Background(), fixture.activated.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if partner.TextCalls != 0 {
		t.Fatalf("text_calls=%d", partner.TextCalls)
	}
}

func TestImageProxyUsesFixedRouteAndCountsSuccess(t *testing.T) {
	var gotAuth, gotPath, gotBody, gotPrivateHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotBody = string(body)
		gotPrivateHeader = r.Header.Get("X-Partner-Secret")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", "upstream-request")
		w.Header().Set("X-Upstream-Secret", "do-not-forward")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"result"}]}`)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL+"/v1", "server-upstream-key", nil)
	body := `{"model":"gpt-image-2","n":1,"size":"1024x1024","prompt":"draw this"}`
	req := authenticatedRequest(http.MethodPost, "/v1/images/generations", body, fixture.activated.SessionToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Partner-Secret", "client-private")
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if gotPath != "/v1/images/generations" || gotAuth != "Bearer server-upstream-key" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if gotBody != body {
		t.Fatalf("body=%q", gotBody)
	}
	if gotPrivateHeader != "" {
		t.Fatalf("private request header forwarded=%q", gotPrivateHeader)
	}
	if w.Body.String() != `{"data":[{"b64_json":"result"}]}` {
		t.Fatalf("response body=%q", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") != "upstream-request" {
		t.Fatalf("safe response headers=%v", w.Header())
	}
	if w.Header().Get("X-Upstream-Secret") != "" {
		t.Fatalf("unsafe response header=%q", w.Header().Get("X-Upstream-Secret"))
	}
	partner, err := fixture.store.PartnerByID(context.Background(), fixture.activated.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if partner.ImageCalls != 1 {
		t.Fatalf("image_calls=%d", partner.ImageCalls)
	}
}

func TestChatProxyCountsSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", nil)
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", validChatBody, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	partner, err := fixture.store.PartnerByID(context.Background(), fixture.activated.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if partner.TextCalls != 1 {
		t.Fatalf("text_calls=%d", partner.TextCalls)
	}
}

func TestChatProxyRateLimitReturnsRetryAfterAndCounts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", func(options *ServerOptions) {
		options.Limiter = NewLimiter(LimiterOptions{RequestsPerMinute: 1, MaxConcurrent: 1, Clock: fixtureClockNow})
	})
	for attempt := 0; attempt < 2; attempt++ {
		req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", validChatBody, fixture.activated.SessionToken)
		w := httptest.NewRecorder()
		fixture.handler.ServeHTTP(w, req)
		if attempt == 0 {
			if w.Code != http.StatusOK {
				t.Fatalf("first status=%d body=%s", w.Code, w.Body.String())
			}
			continue
		}
		assertErrorResponse(t, w, http.StatusTooManyRequests, "rate_limited")
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
	}

	partner, err := fixture.store.PartnerByID(context.Background(), fixture.activated.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if partner.RateLimited != 1 {
		t.Fatalf("rate_limited=%d", partner.RateLimited)
	}
}

func TestActivateHandlerReturnsJSONSuccessAndDeviceMismatch(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", nil)
	created, err := fixture.auth.CreatePartner(context.Background(), "second partner")
	if err != nil {
		t.Fatal(err)
	}
	successBody := `{"activation_key":"` + created.ActivationKey + `","device_hash":"device-b","app_version":"0.1.0","edition":"partner"}`
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/activate", strings.NewReader(successBody)))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response AuthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.PartnerID != created.PartnerID || response.DeviceSecret == "" || response.SessionToken == "" {
		t.Fatalf("response=%+v", response)
	}

	mismatchBody := `{"activation_key":"` + created.ActivationKey + `","device_hash":"other-device","app_version":"0.1.0","edition":"partner"}`
	w = httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/activate", strings.NewReader(mismatchBody)))
	assertErrorResponse(t, w, http.StatusForbidden, "device_mismatch")
}

func TestVerifyHandlerReturnsJSONSuccessAndAuthorizationError(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", nil)
	successBody := `{"partner_id":"` + fixture.activated.PartnerID + `","device_secret":"` + fixture.activated.DeviceSecret + `","device_hash":"device-a","app_version":"0.1.0"}`
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(successBody)))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response AuthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionToken == "" || response.SessionToken == fixture.activated.SessionToken || response.DeviceSecret != "" {
		t.Fatalf("response=%+v", response)
	}

	failureBody := `{"partner_id":"` + fixture.activated.PartnerID + `","device_secret":"wrong","device_hash":"device-a","app_version":"0.1.0"}`
	w = httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(failureBody)))
	assertErrorResponse(t, w, http.StatusUnauthorized, "authorization_failed")

	partner, err := fixture.store.PartnerByID(context.Background(), fixture.activated.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if partner.VerifyFailures != 1 {
		t.Fatalf("verify_failures=%d", partner.VerifyFailures)
	}
}

func TestModelsReturnsInjectedCapabilitiesWithoutSecretsOrURLs(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL+"/private/path", "server-upstream-key", nil)
	req := authenticatedRequest(http.MethodGet, "/v1/models", "", fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response Capabilities
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, fixture.capabilities) {
		t.Fatalf("capabilities=%+v want=%+v", response, fixture.capabilities)
	}
	for _, forbidden := range []string{"server-upstream-key", upstream.URL, "aura-secret", "https://aura.private"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("models response leaked %q: %s", forbidden, w.Body.String())
		}
	}
}

func TestStableErrorJSONDoesNotExposePolicyOrUpstreamDetails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rejected model reached upstream")
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL+"/private", "upstream-secret", nil)
	req := authenticatedRequest(http.MethodPost, "/v1/chat/completions", `{"model":"unknown-private-model","messages":[]}`, fixture.activated.SessionToken)
	w := httptest.NewRecorder()

	fixture.handler.ServeHTTP(w, req)

	response := assertErrorResponse(t, w, http.StatusBadRequest, "model_not_allowed")
	if response.Error.Message != "request is not permitted" {
		t.Fatalf("message=%q", response.Error.Message)
	}
	for _, forbidden := range []string{"unknown-private-model", upstream.URL, "upstream-secret", "text model is not allowed"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("error leaked %q: %s", forbidden, w.Body.String())
		}
	}
}

func TestServerDoesNotLogSecrets(t *testing.T) {
	var logs bytes.Buffer
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL+"/v1", "server-upstream-key", func(options *ServerOptions) {
		options.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	})
	prompt := "PRIVATE-PROMPT-f9cf1978"
	imagePayload := "BASE64-IMAGE-aW1hZ2Utc2VjcmV0"

	activateBody := `{"activation_key":"` + fixture.created.ActivationKey + `","device_hash":"other-device","app_version":"0.1.0","edition":"partner"}`
	fixture.handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/activate", strings.NewReader(activateBody)))

	verifyBody := `{"partner_id":"` + fixture.activated.PartnerID + `","device_secret":"` + fixture.activated.DeviceSecret + `","device_hash":"device-a","app_version":"0.1.0"}`
	fixture.handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(verifyBody)))

	chatBody := `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"` + prompt + `"}]}`
	fixture.handler.ServeHTTP(httptest.NewRecorder(), authenticatedRequest(http.MethodPost, "/v1/chat/completions", chatBody, fixture.activated.SessionToken))

	imageBody := `{"model":"gpt-image-2","n":1,"size":"1024x1024","prompt":"safe","input_image":"` + imagePayload + `"}`
	fixture.handler.ServeHTTP(httptest.NewRecorder(), authenticatedRequest(http.MethodPost, "/v1/images/generations", imageBody, fixture.activated.SessionToken))

	for _, secret := range []string{
		fixture.created.ActivationKey,
		fixture.activated.DeviceSecret,
		fixture.activated.SessionToken,
		"server-upstream-key",
		prompt,
		imagePayload,
		upstream.URL,
	} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs contain secret %q: %s", secret, logs.String())
		}
	}
}

func TestHealthzReturnsOK(t *testing.T) {
	upstreamURL, err := url.Parse("http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewServer(ServerOptions{
		Auth:            &AuthService{},
		Store:           &Store{},
		Policy:          DefaultPolicy(),
		Limiter:         NewLimiter(),
		UpstreamBaseURL: upstreamURL,
		UpstreamAPIKey:  "key",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUnmatchedRoutesAndWrongMethodsReturnStableJSONErrors(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", nil)
	tests := []struct {
		name   string
		method string
		target string
		status int
		code   string
	}{
		{
			name:   "unmatched path",
			method: http.MethodGet,
			target: "/nope",
			status: http.StatusNotFound,
			code:   "not_found",
		},
		{
			name:   "wrong method",
			method: http.MethodPost,
			target: "/healthz",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			fixture.handler.ServeHTTP(w, httptest.NewRequest(test.method, test.target, nil))
			assertErrorResponse(t, w, test.status, test.code)
		})
	}
}

const validChatBody = `{"model":"gpt-5.6-sol","reasoning_effort":"high","messages":[]}`

type httpFixture struct {
	handler      http.Handler
	auth         *AuthService
	store        *Store
	clock        *testClock
	created      CreatedPartner
	activated    AuthResponse
	capabilities Capabilities
}

func newHTTPFixture(t *testing.T, upstreamBaseURL, upstreamAPIKey string) (http.Handler, string) {
	t.Helper()
	fixture := newHTTPTestEnvironment(t, upstreamBaseURL, upstreamAPIKey, nil)
	return fixture.handler, fixture.activated.SessionToken
}

func newHTTPTestEnvironment(
	t *testing.T,
	upstreamBaseURL string,
	upstreamAPIKey string,
	configure func(*ServerOptions),
) *httpFixture {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	clock := &testClock{now: fixtureClockNow()}
	capabilities := Capabilities{
		Features:         []string{"text", "image"},
		TextModels:       []string{"gpt-5.6-sol", "grok-4.6"},
		ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
		ImageModel:       "gpt-image-2",
	}
	auth := NewAuthService(
		store,
		&deterministicReader{},
		clock.Now,
		time.Hour,
		"0.1.0",
		capabilities,
		AuraRuntime{BaseURL: "https://aura.private", APIKey: "aura-secret", Model: "aura-model"},
	)
	created, err := auth.CreatePartner(context.Background(), "fixture partner")
	if err != nil {
		t.Fatal(err)
	}
	activated, err := auth.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}
	baseURL, err := url.Parse(upstreamBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	options := ServerOptions{
		Auth:             auth,
		Store:            store,
		Policy:           DefaultPolicy(),
		Limiter:          NewLimiter(),
		UpstreamBaseURL:  baseURL,
		UpstreamAPIKey:   upstreamAPIKey,
		HTTPClient:       &http.Client{Timeout: time.Second},
		Logger:           slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MaxRequestBytes:  1 << 20,
		MaxResponseBytes: 1 << 20,
	}
	if configure != nil {
		configure(&options)
	}
	handler, err := NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	return &httpFixture{
		handler:      handler,
		auth:         auth,
		store:        store,
		clock:        clock,
		created:      created,
		activated:    activated,
		capabilities: capabilities,
	}
}

func authenticatedRequest(method, target, body, session string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Authorization", "Bearer "+session)
	return request
}

func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) ErrorResponse {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content-type=%q", contentType)
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, recorder.Body.String())
	}
	if response.Error.Code != code || response.Error.Message == "" || response.Error.RequestID == "" {
		t.Fatalf("error=%+v", response.Error)
	}
	return response
}

func fixtureClockNow() time.Time {
	return time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC)
}
