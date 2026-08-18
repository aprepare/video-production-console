package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	consoleauth "video-production-console/internal/auth"
)

type authService interface {
	consoleauth.Authenticator
	Login(context.Context, string, string) (consoleauth.LoginResult, error)
	Logout(context.Context, consoleauth.Identity) error
	ChangePassword(context.Context, consoleauth.Identity, string, string) error
	RevokeAll(context.Context, consoleauth.Identity) error
	RefreshSession(context.Context, consoleauth.Identity, string) (consoleauth.SessionInfo, error)
}

type authHandler struct {
	service   authService
	protected http.Handler
}

func NewAuthHandler(service authService) http.Handler {
	h := &authHandler{service: service}
	h.protected = consoleauth.NewMiddleware(service).Protect(http.HandlerFunc(h.serveProtected))
	return h
}

func (h *authHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api/auth/login" || request.URL.Path == "/api/auth/me" {
		response.Header().Set("Cache-Control", "no-store")
	}
	if request.URL.Path == "/api/auth/login" {
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.login(response, request)
		return
	}
	h.protected.ServeHTTP(response, request)
}

func (h *authHandler) login(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(response, request, maxSmallJSONRequest, &body); err != nil {
		writeAuthError(response, http.StatusUnauthorized)
		return
	}
	result, err := h.service.Login(request.Context(), body.Password, request.RemoteAddr)
	if err != nil {
		if errors.Is(err, consoleauth.ErrRateLimited) {
			response.Header().Set("Retry-After", "900")
			writeError(response, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Try again later.")
			return
		}
		if errors.Is(err, consoleauth.ErrInvalidCredentials) {
			writeAuthError(response, http.StatusUnauthorized)
			return
		}
		writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	setSessionCookie(response, request, result.Token, result.ExpiresAt, int(consoleauth.SessionDuration.Seconds()))
	setCSRFCookie(response, request, result.CSRF, result.ExpiresAt, int(consoleauth.SessionDuration.Seconds()))
	writeJSON(response, http.StatusOK, map[string]any{"csrf_token": result.CSRF, "expires_at": result.ExpiresAt.Format(time.RFC3339Nano)})
}

func (h *authHandler) serveProtected(response http.ResponseWriter, request *http.Request) {
	identity, ok := consoleauth.IdentityFromContext(request.Context())
	if !ok {
		writeAuthError(response, http.StatusUnauthorized)
		return
	}
	switch request.URL.Path {
	case "/api/auth/me":
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		csrfCookie := ""
		if cookie, cookieErr := request.Cookie(consoleauth.CSRFCookieName); cookieErr == nil {
			csrfCookie = cookie.Value
		}
		info, err := h.service.RefreshSession(request.Context(), identity, csrfCookie)
		if err != nil {
			if errors.Is(err, consoleauth.ErrUnauthenticated) {
				writeError(response, http.StatusUnauthorized, "authentication_required", "Authentication is required.")
				return
			}
			if errors.Is(err, consoleauth.ErrInvalidCSRF) {
				writeError(response, http.StatusForbidden, "csrf_invalid", "The CSRF token is invalid.")
				return
			}
			writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		if csrfCookie == "" {
			setCSRFCookie(response, request, info.CSRFToken, identity.ExpiresAt, int(time.Until(identity.ExpiresAt).Seconds()))
		}
		writeJSON(response, http.StatusOK, map[string]any{"csrfToken": info.CSRFToken, "initialPasswordWarning": info.InitialPasswordWarning})
	case "/api/auth/logout":
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := h.service.Logout(request.Context(), identity); err != nil {
			writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		setSessionCookie(response, request, "", time.Unix(1, 0), -1)
		setCSRFCookie(response, request, "", time.Unix(1, 0), -1)
		response.WriteHeader(http.StatusNoContent)
	case "/api/auth/password":
		if request.Method != http.MethodPut {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Current string `json:"current_password"`
			New     string `json:"new_password"`
		}
		if err := decodeJSON(response, request, maxSmallJSONRequest, &body); err != nil {
			writeDecodeError(response, err, "invalid_request", "The request is invalid.")
			return
		}
		err := h.service.ChangePassword(request.Context(), identity, body.Current, body.New)
		switch {
		case errors.Is(err, consoleauth.ErrInvalidCredentials):
			writeAuthError(response, http.StatusUnauthorized)
			return
		case errors.Is(err, consoleauth.ErrInvalidPassword):
			writeError(response, http.StatusBadRequest, "invalid_password", "The new password must be 6 to 128 bytes.")
			return
		case err != nil:
			writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		setSessionCookie(response, request, "", time.Unix(1, 0), -1)
		setCSRFCookie(response, request, "", time.Unix(1, 0), -1)
		response.WriteHeader(http.StatusNoContent)
	case "/api/auth/revoke-all":
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := h.service.RevokeAll(request.Context(), identity); err != nil {
			writeError(response, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		setSessionCookie(response, request, "", time.Unix(1, 0), -1)
		setCSRFCookie(response, request, "", time.Unix(1, 0), -1)
		response.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(response, request)
	}
}

func setSessionCookie(response http.ResponseWriter, request *http.Request, value string, expires time.Time, maxAge int) {
	http.SetCookie(response, &http.Cookie{Name: consoleauth.SessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: request.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge})
}
func setCSRFCookie(response http.ResponseWriter, request *http.Request, value string, expires time.Time, maxAge int) {
	http.SetCookie(response, &http.Cookie{Name: consoleauth.CSRFCookieName, Value: value, Path: "/", Secure: request.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge})
}
func writeAuthError(response http.ResponseWriter, status int) {
	writeJSON(response, status, map[string]string{"code": "invalid_credentials", "message": "The credentials could not be verified."})
}
