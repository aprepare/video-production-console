package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	consoleauth "video-production-console/internal/auth"
	"video-production-console/internal/store"
)

func TestAuthLoginHasGenericErrorsAndSecureCookieAttributes(t *testing.T) {
	handler, service := newAuthHTTPTest(t)
	missing := newUnbootstrappedAuthHTTPTest(t)
	wantBody := loginRequest(t, missing, "wrong", false, nil)
	gotBody := loginRequest(t, handler, "wrong", false, nil)
	if gotBody.status != http.StatusUnauthorized || wantBody.status != gotBody.status || wantBody.body != gotBody.body {
		t.Fatalf("login errors differ: missing=%+v wrong=%+v", wantBody, gotBody)
	}
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	httpLogin := loginRequest(t, handler, "123321", false, nil)
	if httpLogin.status != http.StatusOK {
		t.Fatalf("HTTP login=%+v", httpLogin)
	}
	if httpLogin.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("login Cache-Control=%q", httpLogin.header.Get("Cache-Control"))
	}
	assertSessionCookie(t, httpLogin.cookie, false, consoleauth.SessionDuration)
	tlsLogin := loginRequest(t, handler, "123321", true, nil)
	if tlsLogin.status != http.StatusOK {
		t.Fatalf("TLS login=%+v", tlsLogin)
	}
	assertSessionCookie(t, tlsLogin.cookie, true, consoleauth.SessionDuration)
}

func TestAuthLoginSetsDoubleSubmitCSRFCookie(t *testing.T) {
	handler, service := newAuthHTTPTest(t)
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	login := loginRequest(t, handler, "123321", false, nil)
	if login.csrfCookie == nil {
		t.Fatal("login did not set CSRF cookie")
	}
	if login.csrfCookie.Value != csrfFromBody(t, login.body) {
		t.Fatal("CSRF cookie differs from login response token")
	}
	if login.csrfCookie.HttpOnly || login.csrfCookie.Path != "/" || login.csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("CSRF cookie=%+v", login.csrfCookie)
	}
}

func TestAuthMeMissingCSRFCookieIsConcurrentAndIdempotent(t *testing.T) {
	handler, service := newAuthHTTPTest(t)
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	login := loginRequest(t, handler, "123321", false, nil)
	type result struct {
		status int
		token  string
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			request.AddCookie(login.cookie)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			var body struct {
				CSRFToken string `json:"csrfToken"`
			}
			_ = json.Unmarshal(recorder.Body.Bytes(), &body)
			results <- result{recorder.Code, body.CSRFToken}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	stable := ""
	for got := range results {
		if got.status != http.StatusOK {
			t.Errorf("concurrent /me status=%d", got.status)
			continue
		}
		if got.token == "" {
			t.Error("concurrent /me returned empty token")
			continue
		}
		if stable == "" {
			stable = got.token
		} else if got.token != stable {
			t.Errorf("concurrent /me token=%q, want stable %q", got.token, stable)
		}
	}
}

func TestAuthProtectedMutationsRequireSessionAndCSRF(t *testing.T) {
	handler, service := newAuthHTTPTest(t)
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	login := loginRequest(t, handler, "123321", false, nil)
	var payload struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(login.body), &payload); err != nil {
		t.Fatal(err)
	}
	meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meRequest.AddCookie(login.cookie)
	meRequest.AddCookie(login.csrfCookie)
	meRecorder := httptest.NewRecorder()
	handler.ServeHTTP(meRecorder, meRequest)
	if meRecorder.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", meRecorder.Code, meRecorder.Body.String())
	}
	if meRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("me Cache-Control=%q", meRecorder.Header().Get("Cache-Control"))
	}
	var me struct {
		CSRFToken              string `json:"csrfToken"`
		InitialPasswordWarning bool   `json:"initialPasswordWarning"`
	}
	if err := json.Unmarshal(meRecorder.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.CSRFToken == "" || me.CSRFToken != payload.CSRF {
		t.Fatalf("me csrfToken=%q, want stable token %q", me.CSRFToken, payload.CSRF)
	}
	if !me.InitialPasswordWarning {
		t.Fatal("initialPasswordWarning=false before first password change")
	}
	legacyRequest := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	legacyRequest.AddCookie(login.cookie)
	legacyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(legacyRecorder, legacyRequest)
	if legacyRecorder.Code != http.StatusNotFound {
		t.Fatalf("legacy session endpoint status=%d", legacyRecorder.Code)
	}
	for _, path := range []string{"/api/auth/logout", "/api/auth/password", "/api/auth/revoke-all"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s unauth status=%d", path, rec.Code)
		}
		req = httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(login.cookie)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s missing CSRF status=%d", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(login.cookie)
	req.AddCookie(login.csrfCookie)
	req.Header.Set(consoleauth.CSRFHeader, payload.CSRF)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", rec.Code, rec.Body.String())
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 2 || cleared[0].MaxAge >= 0 || cleared[1].MaxAge >= 0 {
		t.Fatalf("cleared cookies=%+v", cleared)
	}
}

func TestAuthPasswordAndRevokeAllEndpointsInvalidateSessions(t *testing.T) {
	handler, service := newAuthHTTPTest(t)
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	login := loginRequest(t, handler, "123321", false, nil)
	csrf := csrfFromBody(t, login.body)
	request := httptest.NewRequest(http.MethodPut, "/api/auth/password", strings.NewReader(`{"current_password":"123321","new_password":"654321"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(consoleauth.CSRFHeader, csrf)
	request.AddCookie(login.cookie)
	request.AddCookie(login.csrfCookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("password status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(login.cookie)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("old session status=%d", recorder.Code)
	}
	newLogin := loginRequest(t, handler, "654321", false, nil)
	meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meRequest.AddCookie(newLogin.cookie)
	meRequest.AddCookie(newLogin.csrfCookie)
	meRecorder := httptest.NewRecorder()
	handler.ServeHTTP(meRecorder, meRequest)
	if meRecorder.Code != http.StatusOK {
		t.Fatalf("post-change me status=%d body=%s", meRecorder.Code, meRecorder.Body.String())
	}
	var me struct {
		CSRFToken              string `json:"csrfToken"`
		InitialPasswordWarning bool   `json:"initialPasswordWarning"`
	}
	if err := json.Unmarshal(meRecorder.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.InitialPasswordWarning {
		t.Fatal("initialPasswordWarning remained true after password change")
	}
	csrf = me.CSRFToken
	request = httptest.NewRequest(http.MethodPost, "/api/auth/revoke-all", nil)
	request.AddCookie(newLogin.cookie)
	request.AddCookie(newLogin.csrfCookie)
	request.Header.Set(consoleauth.CSRFHeader, csrf)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(newLogin.cookie)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d", recorder.Code)
	}
}

func TestAuthLoginIgnoresForwardedFor(t *testing.T) {
	handler, service := newAuthHTTPTest(t)
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		headers := map[string]string{"X-Forwarded-For": fmtHTTPInt(i) + ".example"}
		result := loginRequest(t, handler, "wrong", false, headers)
		if result.status != http.StatusUnauthorized {
			t.Fatalf("failure %d status=%d", i, result.status)
		}
	}
	result := loginRequest(t, handler, "123321", false, map[string]string{"X-Forwarded-For": "new.example"})
	if result.status != http.StatusTooManyRequests {
		t.Fatalf("forwarded header bypassed lock: %+v", result)
	}
	var rateError struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(result.body), &rateError); err != nil {
		t.Fatal(err)
	}
	if rateError.Code != "rate_limited" {
		t.Fatalf("rate code=%q", rateError.Code)
	}
	if result.header.Get("Retry-After") == "" {
		t.Fatal("rate response missing Retry-After")
	}
}

func TestAuthInfrastructureErrorsReturnGeneric500(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	randomBytes := make([]byte, 1024)
	for i := range randomBytes {
		randomBytes[i] = byte(i)
	}
	service := consoleauth.NewService(store.NewAuthStore(db), consoleauth.Options{Random: bytes.NewReader(randomBytes)})
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	handler := NewAuthHandler(service)
	login := loginRequest(t, handler, "123321", false, nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	outage := loginRequest(t, handler, "123321", false, nil)
	assertAPIError(t, httptestResponse(outage), http.StatusInternalServerError, "internal_error")
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(login.cookie)
	request.AddCookie(login.csrfCookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assertAPIError(t, recorder.Result(), http.StatusInternalServerError, "internal_error")
}

func TestAuthRandomFailureReturnsGeneric500(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := consoleauth.NewService(store.NewAuthStore(db), consoleauth.Options{Random: bytes.NewReader(nil)})
	if err := service.Bootstrap(t.Context(), "123321"); err != nil {
		t.Fatal(err)
	}
	result := loginRequest(t, NewAuthHandler(service), "123321", false, nil)
	assertAPIError(t, httptestResponse(result), http.StatusInternalServerError, "internal_error")
}

func TestAuthMeMapsSessionCASConflictToUnauthorized(t *testing.T) {
	service := &typedAuthService{identity: consoleauth.Identity{SessionID: "4d739048-2e42-45d3-8128-4dd9b1cae664"}, refreshErr: consoleauth.ErrUnauthenticated}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: consoleauth.SessionCookieName, Value: "session"})
	recorder := httptest.NewRecorder()
	NewAuthHandler(service).ServeHTTP(recorder, request)
	assertAPIError(t, recorder.Result(), http.StatusUnauthorized, "authentication_required")
}

type typedAuthService struct {
	identity   consoleauth.Identity
	authErr    error
	refreshErr error
	changeErr  error
}

func (s *typedAuthService) Authenticate(context.Context, string) (consoleauth.Identity, error) {
	return s.identity, s.authErr
}
func (s *typedAuthService) Login(context.Context, string, string) (consoleauth.LoginResult, error) {
	return consoleauth.LoginResult{}, nil
}
func (s *typedAuthService) Logout(context.Context, consoleauth.Identity) error { return nil }
func (s *typedAuthService) ChangePassword(context.Context, consoleauth.Identity, string, string) error {
	return s.changeErr
}
func (s *typedAuthService) RevokeAll(context.Context, consoleauth.Identity) error { return nil }
func (s *typedAuthService) RefreshSession(context.Context, consoleauth.Identity, string) (consoleauth.SessionInfo, error) {
	return consoleauth.SessionInfo{}, s.refreshErr
}

type httpLoginResult struct {
	status     int
	body       string
	cookie     *http.Cookie
	csrfCookie *http.Cookie
	header     http.Header
}

func loginRequest(t *testing.T, handler http.Handler, password string, tls bool, headers map[string]string) httpLoginResult {
	t.Helper()
	target := "http://console.test/api/auth/login"
	if tls {
		target = "https://console.test/api/auth/login"
	}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"password":"`+password+`"}`))
	req.RemoteAddr = "192.0.2.1:4321"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	result := rec.Result()
	body, _ := io.ReadAll(result.Body)
	cookies := result.Cookies()
	var cookie, csrfCookie *http.Cookie
	for _, candidate := range cookies {
		switch candidate.Name {
		case consoleauth.SessionCookieName:
			cookie = candidate
		case "video_console_csrf":
			csrfCookie = candidate
		}
	}
	return httpLoginResult{status: rec.Code, body: string(body), cookie: cookie, csrfCookie: csrfCookie, header: result.Header.Clone()}
}

func httptestResponse(result httpLoginResult) *http.Response {
	return &http.Response{StatusCode: result.status, Header: result.header, Body: io.NopCloser(strings.NewReader(result.body))}
}
func assertSessionCookie(t *testing.T, cookie *http.Cookie, secure bool, duration time.Duration) {
	t.Helper()
	if cookie == nil || cookie.Name != consoleauth.SessionCookieName || cookie.Path != "/" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure != secure || cookie.MaxAge != int(duration.Seconds()) {
		t.Fatalf("cookie=%+v", cookie)
	}
}
func csrfFromBody(t *testing.T, body string) string {
	t.Helper()
	var value struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatal(err)
	}
	return value.CSRF
}
func fmtHTTPInt(v int) string { return string(rune('0' + v)) }
func newAuthHTTPTest(t *testing.T) (http.Handler, *consoleauth.Service) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	randomBytes := make([]byte, 8192)
	for i := range randomBytes {
		randomBytes[i] = byte(i)
	}
	service := consoleauth.NewService(store.NewAuthStore(db), consoleauth.Options{Random: bytes.NewReader(randomBytes)})
	return NewAuthHandler(service), service
}
func newUnbootstrappedAuthHTTPTest(t *testing.T) http.Handler {
	handler, _ := newAuthHTTPTest(t)
	return handler
}
