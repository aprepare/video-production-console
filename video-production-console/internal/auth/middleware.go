package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"video-production-console/internal/security"
)

const (
	SessionCookieName = "video_console_session"
	CSRFCookieName    = "video_console_csrf"
	CSRFHeader        = "X-CSRF-Token"
)

type Authenticator interface {
	Authenticate(context.Context, string) (Identity, error)
}
type Middleware struct{ authenticator Authenticator }

func NewMiddleware(authenticator Authenticator) *Middleware {
	return &Middleware{authenticator: authenticator}
}

type identityContextKey struct{}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

func (m *Middleware) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			authError(w, http.StatusUnauthorized, "authentication_required")
			return
		}
		identity, err := m.authenticator.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, ErrUnauthenticated) {
				authError(w, http.StatusUnauthorized, "authentication_required")
			} else {
				authError(w, http.StatusInternalServerError, "internal_error")
			}
			return
		}
		if csrfRequired(r.Method) && !validCSRFRequest(r, identity.CSRFHash) {
			authError(w, http.StatusForbidden, "csrf_invalid")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity)))
	})
}
func validCSRFRequest(r *http.Request, hash string) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return false
	}
	header := r.Header.Get(CSRFHeader)
	return header != "" && security.SecretMatches(hash, header) && security.SecretMatches(hash, cookie.Value)
}
func csrfRequired(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}
func authError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	message := "The request could not be authenticated."
	if status == http.StatusInternalServerError {
		message = "An internal error occurred."
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
