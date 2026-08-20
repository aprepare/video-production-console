package partnergateway

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed admin.html
var adminPageHTML []byte

const (
	adminCookieName  = "pgw_admin"
	adminSessionTTL  = 12 * time.Hour
	adminLoginWindow = 10 * time.Minute
	adminLoginLimit  = 8
)

type adminLoginRequest struct {
	Password string `json:"password"`
}

type adminPartnerIDRequest struct {
	ID string `json:"id"`
}

type adminCreateRequest struct {
	DisplayName string `json:"display_name"`
}

type adminPartnerView struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	DeviceBound bool      `json:"device_bound"`
	KeyPrefix   string    `json:"key_prefix"`
	TextCalls   int64     `json:"text_calls"`
	ImageCalls  int64     `json:"image_calls"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) adminEnabled() bool {
	return s != nil && s.adminPassword != ""
}

func (s *Server) adminPage(writer http.ResponseWriter, request *http.Request) {
	if !s.adminEnabled() {
		s.writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(adminPageHTML)
}

func (s *Server) adminLogin(writer http.ResponseWriter, request *http.Request) {
	if !s.adminEnabled() {
		s.writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if !s.allowAdminLogin(request) {
		s.writeError(writer, request, http.StatusTooManyRequests, "rate_limited", "try again later")
		return
	}
	var payload adminLoginRequest
	if err := s.decodeRequestJSON(writer, request, &payload); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	if !s.adminPasswordOK(payload.Password) {
		s.recordAdminLoginFailure(request)
		s.writeError(writer, request, http.StatusUnauthorized, "authorization_failed", "authorization failed")
		return
	}
	token, err := newAdminSessionToken()
	if err != nil {
		s.writeError(writer, request, http.StatusInternalServerError, "admin_failed", "admin request failed")
		return
	}
	s.putAdminSession(token)
	http.SetCookie(writer, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/admin",
		MaxAge:   int(adminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	s.writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) adminLogout(writer http.ResponseWriter, request *http.Request) {
	if !s.adminEnabled() {
		s.writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if cookie, err := request.Cookie(adminCookieName); err == nil {
		s.deleteAdminSession(cookie.Value)
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	s.writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) adminSession(writer http.ResponseWriter, request *http.Request) {
	if !s.requireAdmin(writer, request) {
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) adminListPartners(writer http.ResponseWriter, request *http.Request) {
	if !s.requireAdmin(writer, request) {
		return
	}
	partners, err := s.auth.ListPartners(request.Context())
	if err != nil {
		s.writeError(writer, request, http.StatusInternalServerError, "admin_failed", "admin request failed")
		return
	}
	views := make([]adminPartnerView, 0, len(partners))
	for _, partner := range partners {
		views = append(views, adminPartnerView{
			ID:          partner.ID,
			DisplayName: partner.DisplayName,
			Status:      string(partner.Status),
			DeviceBound: partner.DeviceHash != "",
			KeyPrefix:   partner.KeyPrefix,
			TextCalls:   partner.TextCalls,
			ImageCalls:  partner.ImageCalls,
			CreatedAt:   partner.CreatedAt,
		})
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"partners": views})
}

func (s *Server) adminCreatePartner(writer http.ResponseWriter, request *http.Request) {
	if !s.requireAdmin(writer, request) {
		return
	}
	var payload adminCreateRequest
	if err := s.decodeRequestJSON(writer, request, &payload); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	created, err := s.auth.CreatePartner(request.Context(), strings.TrimSpace(payload.DisplayName))
	if err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "create_failed", "partner could not be created")
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{
		"id":             created.PartnerID,
		"display_name":   created.DisplayName,
		"activation_key": created.ActivationKey,
	})
}

func (s *Server) adminEnablePartner(writer http.ResponseWriter, request *http.Request) {
	s.adminMutatePartner(writer, request, PartnerActive)
}

func (s *Server) adminDisablePartner(writer http.ResponseWriter, request *http.Request) {
	s.adminMutatePartner(writer, request, PartnerDisabled)
}

func (s *Server) adminMutatePartner(writer http.ResponseWriter, request *http.Request, status PartnerStatus) {
	if !s.requireAdmin(writer, request) {
		return
	}
	id, ok := s.decodePartnerID(writer, request)
	if !ok {
		return
	}
	if err := s.auth.SetPartnerStatus(request.Context(), id, status); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "update_failed", "partner could not be updated")
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": id, "status": status})
}

func (s *Server) adminRotatePartner(writer http.ResponseWriter, request *http.Request) {
	if !s.requireAdmin(writer, request) {
		return
	}
	id, ok := s.decodePartnerID(writer, request)
	if !ok {
		return
	}
	key, err := s.auth.RotateKey(request.Context(), id)
	if err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "rotate_failed", "activation key could not be rotated")
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"id": id, "activation_key": key})
}

func (s *Server) adminUnbindPartner(writer http.ResponseWriter, request *http.Request) {
	if !s.requireAdmin(writer, request) {
		return
	}
	id, ok := s.decodePartnerID(writer, request)
	if !ok {
		return
	}
	if err := s.auth.UnbindDevice(request.Context(), id); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "unbind_failed", "device could not be unbound")
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) decodePartnerID(writer http.ResponseWriter, request *http.Request) (string, bool) {
	var payload adminPartnerIDRequest
	if err := s.decodeRequestJSON(writer, request, &payload); err != nil || strings.TrimSpace(payload.ID) == "" {
		s.writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid request")
		return "", false
	}
	return strings.TrimSpace(payload.ID), true
}

func (s *Server) requireAdmin(writer http.ResponseWriter, request *http.Request) bool {
	if !s.adminEnabled() {
		s.writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
		return false
	}
	cookie, err := request.Cookie(adminCookieName)
	if err != nil || !s.validAdminSession(cookie.Value) {
		s.writeError(writer, request, http.StatusUnauthorized, "authorization_failed", "authorization failed")
		return false
	}
	return true
}

func (s *Server) adminPasswordOK(got string) bool {
	wantSum := sha256.Sum256([]byte(s.adminPassword))
	gotSum := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wantSum[:], gotSum[:]) == 1
}

func (s *Server) allowAdminLogin(request *http.Request) bool {
	s.adminLock()
	defer s.adminUnlock()
	ip := adminRequestIP(request)
	cutoff := s.adminNow().Add(-adminLoginWindow)
	kept := s.adminLoginFails[ip][:0]
	for _, at := range s.adminLoginFails[ip] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	s.adminLoginFails[ip] = kept
	return len(kept) < adminLoginLimit
}

func (s *Server) recordAdminLoginFailure(request *http.Request) {
	s.adminLock()
	defer s.adminUnlock()
	ip := adminRequestIP(request)
	s.adminLoginFails[ip] = append(s.adminLoginFails[ip], s.adminNow())
}

func (s *Server) putAdminSession(token string) {
	s.adminLock()
	defer s.adminUnlock()
	s.adminSessions[token] = s.adminNow().Add(adminSessionTTL)
}

func (s *Server) validAdminSession(token string) bool {
	s.adminLock()
	defer s.adminUnlock()
	expires, ok := s.adminSessions[token]
	if !ok || !s.adminNow().Before(expires) {
		delete(s.adminSessions, token)
		return false
	}
	return true
}

func (s *Server) deleteAdminSession(token string) {
	s.adminLock()
	defer s.adminUnlock()
	delete(s.adminSessions, token)
}

var adminMu sync.Mutex

func (s *Server) adminLock()   { adminMu.Lock() }
func (s *Server) adminUnlock() { adminMu.Unlock() }

func newAdminSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func adminRequestIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}
