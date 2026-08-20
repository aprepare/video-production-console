package partnergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ServerOptions struct {
	Auth             *AuthService
	Store            *Store
	Policy           Policy
	Limiter          *Limiter
	UpstreamBaseURL  *url.URL
	UpstreamAPIKey   string
	AdminPassword    string
	HTTPClient       *http.Client
	Logger           *slog.Logger
	Now              func() time.Time
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

type ErrorResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

type Server struct {
	auth            *AuthService
	store           *Store
	policy          Policy
	limiter         *Limiter
	upstream        *Upstream
	adminPassword   string
	adminSessions   map[string]time.Time
	adminLoginFails map[string][]time.Time
	adminNow        func() time.Time
	logger          *slog.Logger
	maxRequestBytes int64
	mux             *http.ServeMux
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Auth == nil || options.Store == nil {
		return nil, errors.New("auth and store are required")
	}
	if options.Policy.maxRequestBytes <= 0 {
		options.Policy = DefaultPolicy()
	}
	if options.Limiter == nil {
		options.Limiter = NewLimiter()
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if options.MaxRequestBytes <= 0 {
		options.MaxRequestBytes = defaultMaxRequestBytes
	}
	upstream, err := newUpstream(
		options.UpstreamBaseURL,
		options.UpstreamAPIKey,
		options.HTTPClient,
		options.MaxResponseBytes,
	)
	if err != nil {
		return nil, err
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}
	server := &Server{
		auth:            options.Auth,
		store:           options.Store,
		policy:          options.Policy,
		limiter:         options.Limiter,
		upstream:        upstream,
		adminPassword:   strings.TrimSpace(options.AdminPassword),
		adminSessions:   map[string]time.Time{},
		adminLoginFails: map[string][]time.Time{},
		adminNow:        now,
		logger:          options.Logger,
		maxRequestBytes: options.MaxRequestBytes,
		mux:             http.NewServeMux(),
	}
	server.mux.HandleFunc("GET /healthz", server.health)
	server.mux.HandleFunc("POST /auth/activate", server.activate)
	server.mux.HandleFunc("POST /auth/verify", server.verify)
	server.mux.HandleFunc("GET /v1/models", server.withSession(server.models))
	server.mux.HandleFunc("POST /v1/chat/completions", server.withSession(server.proxyChat))
	server.mux.HandleFunc("POST /v1/images/generations", server.withSession(server.proxyImage))
	server.mux.HandleFunc("GET /admin", server.adminPage)
	server.mux.HandleFunc("GET /admin/api/session", server.adminSession)
	server.mux.HandleFunc("GET /admin/api/partners", server.adminListPartners)
	server.mux.HandleFunc("POST /admin/api/login", server.adminLogin)
	server.mux.HandleFunc("POST /admin/api/logout", server.adminLogout)
	server.mux.HandleFunc("POST /admin/api/partners", server.adminCreatePartner)
	server.mux.HandleFunc("POST /admin/api/partners/enable", server.adminEnablePartner)
	server.mux.HandleFunc("POST /admin/api/partners/disable", server.adminDisablePartner)
	server.mux.HandleFunc("POST /admin/api/partners/rotate-key", server.adminRotatePartner)
	server.mux.HandleFunc("POST /admin/api/partners/unbind", server.adminUnbindPartner)
	return server, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	startedAt := time.Now()
	metadata := &requestMetadata{
		requestID: uuid.NewString(),
		route:     registeredRoute(request.Method, request.URL.Path),
	}
	request = request.WithContext(context.WithValue(request.Context(), requestMetadataKey{}, metadata))
	recorder := &countingResponseWriter{ResponseWriter: writer}

	expectedMethod := routeMethod(request.URL.Path)
	switch {
	case expectedMethod == "":
		s.writeError(recorder, request, http.StatusNotFound, "not_found", "route not found")
	case !routeMethodMatches(expectedMethod, request.Method):
		s.writeError(recorder, request, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	default:
		s.mux.ServeHTTP(recorder, request)
	}

	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	s.logger.Info(
		"gateway request",
		"request_id", metadata.requestID,
		"partner_id", metadata.partnerID,
		"route", metadata.route,
		"model", metadata.model,
		"status", status,
		"duration", time.Since(startedAt),
		"request_bytes", metadata.requestBytes,
		"response_bytes", recorder.bytes,
	)
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)
}

func (s *Server) activate(writer http.ResponseWriter, request *http.Request) {
	var payload ActivateRequest
	if err := s.decodeRequestJSON(writer, request, &payload); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "authorization_failed", "authorization failed")
		return
	}
	response, err := s.auth.Activate(request.Context(), payload)
	if err != nil {
		status, code, message := mapAuthError(err)
		s.writeError(writer, request, status, code, message)
		return
	}
	s.writeJSON(writer, http.StatusOK, response)
}

func (s *Server) verify(writer http.ResponseWriter, request *http.Request) {
	var payload VerifyRequest
	if err := s.decodeRequestJSON(writer, request, &payload); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "authorization_failed", "authorization failed")
		return
	}
	response, err := s.auth.Verify(request.Context(), payload)
	if err != nil {
		if payload.PartnerID != "" {
			_ = s.store.incrementVerifyFailures(request.Context(), payload.PartnerID)
		}
		status, code, message := mapAuthError(err)
		s.writeError(writer, request, status, code, message)
		return
	}
	metadataFor(request).partnerID = response.PartnerID
	s.writeJSON(writer, http.StatusOK, response)
}

type sessionHandler func(http.ResponseWriter, *http.Request, Partner)

func (s *Server) withSession(next sessionHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		token, ok := bearerToken(request.Header)
		if !ok {
			s.writeError(writer, request, http.StatusUnauthorized, "authorization_failed", "authorization failed")
			return
		}
		partner, err := s.store.SessionPartner(request.Context(), token)
		if err != nil {
			s.writeError(writer, request, http.StatusUnauthorized, "authorization_failed", "authorization failed")
			return
		}
		metadataFor(request).partnerID = partner.ID
		next(writer, request, partner)
	}
}

func (s *Server) models(writer http.ResponseWriter, _ *http.Request, _ Partner) {
	s.writeJSON(writer, http.StatusOK, s.auth.capabilities)
}

func (s *Server) proxyChat(writer http.ResponseWriter, request *http.Request, partner Partner) {
	s.proxy(writer, request, partner, ChatRequest, "/chat/completions")
}

func (s *Server) proxyImage(writer http.ResponseWriter, request *http.Request, partner Partner) {
	s.proxy(writer, request, partner, ImageRequest, "/images/generations")
}

func (s *Server) proxy(
	writer http.ResponseWriter,
	request *http.Request,
	partner Partner,
	kind RequestKind,
	upstreamRoute string,
) {
	body, err := s.readRequestBody(writer, request)
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		s.writeError(writer, request, status, "model_not_allowed", "request is not permitted")
		return
	}
	if err := s.policy.Validate(kind, body); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "model_not_allowed", "request is not permitted")
		return
	}
	var model struct {
		Name string `json:"model"`
	}
	if err := json.Unmarshal(body, &model); err == nil {
		metadataFor(request).model = model.Name
	}

	release, err := s.limiter.Acquire(partner.ID)
	if err != nil {
		var limitError *LimitError
		if errors.As(err, &limitError) {
			retrySeconds := int64(math.Ceil(limitError.RetryAfter.Seconds()))
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			writer.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
		}
		_ = s.store.incrementRateLimited(request.Context(), partner.ID)
		s.writeError(writer, request, http.StatusTooManyRequests, "rate_limited", "request rate limit exceeded")
		return
	}
	defer release()

	result, err := s.upstream.forward(request.Context(), writer, request, upstreamRoute, body)
	if err != nil {
		if !result.responseStarted {
			s.writeError(writer, request, http.StatusBadGateway, "upstream_unavailable", "upstream service unavailable")
		}
		return
	}
	if result.statusCode < http.StatusOK || result.statusCode >= http.StatusMultipleChoices {
		return
	}
	switch kind {
	case ChatRequest:
		_ = s.store.incrementTextCalls(request.Context(), partner.ID)
	case ImageRequest:
		_ = s.store.incrementImageCalls(request.Context(), partner.ID)
	}
}

func (s *Server) decodeRequestJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	body, err := s.readRequestBody(writer, request)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func (s *Server) readRequestBody(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, s.maxRequestBytes))
	metadataFor(request).requestBytes = int64(len(body))
	return body, err
}

func (s *Server) writeError(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	message string,
) {
	var response ErrorResponse
	response.Error.Code = code
	response.Error.Message = message
	response.Error.RequestID = metadataFor(request).requestID
	writer.Header().Set("X-Request-ID", response.Error.RequestID)
	s.writeJSON(writer, status, response)
}

func (s *Server) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func mapAuthError(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrDeviceMismatch):
		return http.StatusForbidden, "device_mismatch", "device does not match"
	case errors.Is(err, ErrInvalidCredential),
		errors.Is(err, ErrPartnerDisabled),
		errors.Is(err, ErrSessionExpired),
		errors.Is(err, ErrVersionTooOld):
		return http.StatusUnauthorized, "authorization_failed", "authorization failed"
	default:
		return http.StatusInternalServerError, "authorization_failed", "authorization failed"
	}
}

func bearerToken(header http.Header) (string, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, found := strings.Cut(strings.TrimSpace(values[0]), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}

func registeredRoute(method, path string) string {
	if routeMethodMatches(routeMethod(path), method) {
		return path
	}
	return "unmatched"
}

func routeMethod(path string) string {
	switch path {
	case "/healthz", "/v1/models", "/admin", "/admin/api/session":
		return http.MethodGet
	case "/admin/api/partners":
		return http.MethodGet + "|" + http.MethodPost
	case "/auth/activate", "/auth/verify", "/v1/chat/completions", "/v1/images/generations",
		"/admin/api/login", "/admin/api/logout",
		"/admin/api/partners/enable", "/admin/api/partners/disable",
		"/admin/api/partners/rotate-key", "/admin/api/partners/unbind":
		return http.MethodPost
	default:
		return ""
	}
}

func routeMethodMatches(expected, actual string) bool {
	if strings.Contains(expected, "|") {
		for _, method := range strings.Split(expected, "|") {
			if routeMethodMatches(method, actual) {
				return true
			}
		}
		return false
	}
	return expected == actual || (expected == http.MethodGet && actual == http.MethodHead)
}

type requestMetadataKey struct{}

type requestMetadata struct {
	requestID    string
	partnerID    string
	route        string
	model        string
	requestBytes int64
}

func metadataFor(request *http.Request) *requestMetadata {
	if metadata, ok := request.Context().Value(requestMetadataKey{}).(*requestMetadata); ok {
		return metadata
	}
	return &requestMetadata{requestID: uuid.NewString(), route: registeredRoute(request.Method, request.URL.Path)}
}

type countingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *countingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *countingResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(body)
	w.bytes += int64(written)
	return written, err
}

func (w *countingResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *countingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
