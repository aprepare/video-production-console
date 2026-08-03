package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"video-production-console/internal/security"
)

func TestMiddlewareRejectsUnauthenticatedAndEnforcesCSRFAcrossMethods(t *testing.T) {
	csrf := "csrf-secret"
	authn := stubAuthenticator{identity: Identity{AdminID: "a", SessionID: "s", CSRFHash: security.HashSecret(csrf)}}
	handler := NewMiddleware(authn).Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := IdentityFromContext(r.Context()); !ok {
			t.Fatal("identity missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing cookie status=%d", recorder.Code)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		request = httptest.NewRequest(method, "/api/private", nil)
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "token"})
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s status=%d", method, recorder.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		request = httptest.NewRequest(method, "/api/private?csrf_token="+csrf, nil)
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "token"})
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s query CSRF status=%d", method, recorder.Code)
		}
		request.Header.Set(CSRFHeader, csrf)
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s missing double-submit cookie status=%d", method, recorder.Code)
		}
		request.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: csrf})
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s valid CSRF status=%d", method, recorder.Code)
		}
	}
}

func TestMiddlewareRejectsWrongCSRFAndAuthenticatorErrorsUniformly(t *testing.T) {
	authn := stubAuthenticator{identity: Identity{CSRFHash: security.HashSecret("right")}}
	handler := NewMiddleware(authn).Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next called") }))
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "token"})
	request.Header.Set(CSRFHeader, "wrong")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong CSRF status=%d", recorder.Code)
	}
	authn.err = ErrUnauthenticated
	handler = NewMiddleware(authn).Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next called") }))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("auth error status=%d", recorder.Code)
	}
	authn.err = errors.New("database unavailable")
	handler = NewMiddleware(authn).Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next called") }))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("database auth error status=%d", recorder.Code)
	}
}

type stubAuthenticator struct {
	identity Identity
	err      error
}

func (s stubAuthenticator) Authenticate(context.Context, string) (Identity, error) {
	return s.identity, s.err
}
