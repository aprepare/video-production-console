package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/security"
	"video-production-console/internal/store"
)

var (
	ErrUnauthenticated    = errors.New("authentication required")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("login temporarily unavailable")
	ErrInvalidPassword    = errors.New("new password must be 6 to 128 bytes")
	ErrInvalidCSRF        = errors.New("invalid CSRF token")
)

const SessionDuration = 7 * 24 * time.Hour

type authRepository interface {
	CreateAdminIfNone(context.Context, string, time.Time) (store.Admin, bool, error)
	Admin(context.Context) (store.Admin, error)
	CreateSession(context.Context, store.AuthSession) error
	FindValidSession(context.Context, string, time.Time) (store.AuthSession, error)
	RotateCSRF(context.Context, string, string, string) error
	RevokeSession(context.Context, string) error
	RevokeAll(context.Context, string) error
	ChangePasswordAndRevokeAll(context.Context, string, string, string, time.Time) error
}

type Options struct {
	Now           func() time.Time
	Random        io.Reader
	CheckPassword func(string, string) bool
}

type Service struct {
	repo          authRepository
	now           func() time.Time
	random        io.Reader
	limit         loginLimiter
	checkPassword func(string, string) bool
	csrfMu        sync.Mutex
	csrfFallbacks map[string]csrfFallback
}

type LoginResult struct {
	Token     string
	CSRF      string
	ExpiresAt time.Time
}

type Identity struct {
	AdminID                string
	SessionID              string
	CSRFHash               string
	InitialPasswordWarning bool
	ExpiresAt              time.Time
}

type SessionInfo struct {
	CSRFToken              string
	InitialPasswordWarning bool
}

func NewService(repo authRepository, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.CheckPassword == nil {
		options.CheckPassword = security.CheckPassword
	}
	return &Service{repo: repo, now: options.Now, random: options.Random, checkPassword: options.CheckPassword, limit: loginLimiter{entries: make(map[string]*loginFailures)}, csrfFallbacks: make(map[string]csrfFallback)}
}

func (s *Service) Bootstrap(ctx context.Context, password string) error {
	hash, err := security.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	_, _, err = s.repo.CreateAdminIfNone(ctx, hash, s.now())
	if err != nil {
		return fmt.Errorf("bootstrap administrator: %w", err)
	}
	return nil
}

func (s *Service) Login(ctx context.Context, password, remoteAddr string) (LoginResult, error) {
	now := s.now()
	remote := normalizeRemoteAddr(remoteAddr)
	attempt, ok := s.limit.begin(remote, now)
	if !ok {
		return LoginResult{}, ErrRateLimited
	}
	outcome := loginNeutral
	defer func() { attempt.finish(outcome) }()
	admin, err := s.repo.Admin(ctx)
	if errors.Is(err, store.ErrUnauthenticated) {
		outcome = loginInvalid
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}
	if !s.checkPassword(admin.PasswordHash, password) {
		outcome = loginInvalid
		return LoginResult{}, ErrInvalidCredentials
	}
	token, tokenHash, err := security.NewSecret(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	csrf, csrfHash, err := security.NewSecret(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	id, err := uuid.NewRandomFromReader(s.random)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session ID: %w", err)
	}
	expires := now.Add(SessionDuration)
	if err := s.repo.CreateSession(ctx, store.AuthSession{ID: id.String(), AdminID: admin.ID, TokenHash: tokenHash, CSRFHash: csrfHash, RemoteAddr: remote, ExpiresAt: expires, LastSeenAt: now, CreatedAt: now}); err != nil {
		return LoginResult{}, err
	}
	outcome = loginSucceeded
	return LoginResult{Token: token, CSRF: csrf, ExpiresAt: expires}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}
	session, err := s.repo.FindValidSession(ctx, security.HashSecret(token), s.now())
	if errors.Is(err, store.ErrUnauthenticated) {
		return Identity{}, ErrUnauthenticated
	}
	if err != nil {
		return Identity{}, err
	}
	return Identity{AdminID: session.AdminID, SessionID: session.ID, CSRFHash: session.CSRFHash, InitialPasswordWarning: session.InitialPasswordWarning, ExpiresAt: session.ExpiresAt}, nil
}

type csrfFallback struct {
	fromHash string
	toHash   string
	token    string
}

func (s *Service) RefreshSession(ctx context.Context, identity Identity, csrfCookie string) (SessionInfo, error) {
	if csrfCookie != "" {
		if !security.SecretMatches(identity.CSRFHash, csrfCookie) {
			return SessionInfo{}, ErrInvalidCSRF
		}
		return SessionInfo{CSRFToken: csrfCookie, InitialPasswordWarning: identity.InitialPasswordWarning}, nil
	}
	s.csrfMu.Lock()
	defer s.csrfMu.Unlock()
	if cached, ok := s.csrfFallbacks[identity.SessionID]; ok && (identity.CSRFHash == cached.fromHash || identity.CSRFHash == cached.toHash) {
		return SessionInfo{CSRFToken: cached.token, InitialPasswordWarning: identity.InitialPasswordWarning}, nil
	}
	csrf, csrfHash, err := security.NewSecret(s.random)
	if err != nil {
		return SessionInfo{}, err
	}
	if err := s.repo.RotateCSRF(ctx, identity.SessionID, identity.CSRFHash, csrfHash); err != nil {
		if errors.Is(err, store.ErrUnauthenticated) {
			return SessionInfo{}, ErrUnauthenticated
		}
		return SessionInfo{}, err
	}
	s.csrfFallbacks[identity.SessionID] = csrfFallback{fromHash: identity.CSRFHash, toHash: csrfHash, token: csrf}
	return SessionInfo{CSRFToken: csrf, InitialPasswordWarning: identity.InitialPasswordWarning}, nil
}

func (s *Service) Logout(ctx context.Context, identity Identity) error {
	return s.repo.RevokeSession(ctx, identity.SessionID)
}
func (s *Service) RevokeAll(ctx context.Context, identity Identity) error {
	return s.repo.RevokeAll(ctx, identity.AdminID)
}

func (s *Service) ChangePassword(ctx context.Context, identity Identity, currentPassword, newPassword string) error {
	if n := len([]byte(newPassword)); n < 6 || n > 128 {
		return ErrInvalidPassword
	}
	admin, err := s.repo.Admin(ctx)
	if errors.Is(err, store.ErrUnauthenticated) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if admin.ID != identity.AdminID || !security.CheckPassword(admin.PasswordHash, currentPassword) {
		return ErrInvalidCredentials
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	err = s.repo.ChangePasswordAndRevokeAll(ctx, identity.AdminID, admin.PasswordHash, hash, s.now())
	if errors.Is(err, store.ErrUnauthenticated) {
		return ErrInvalidCredentials
	}
	return err
}

type loginFailures struct {
	failures    []time.Time
	lockedUntil time.Time
	inflight    int
	generation  uint64
}
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginFailures
}

type loginOutcome uint8

const (
	loginNeutral loginOutcome = iota
	loginInvalid
	loginSucceeded
)

type loginAttempt struct {
	limiter    *loginLimiter
	entry      *loginFailures
	generation uint64
	now        time.Time
	finished   bool
}

func (l *loginLimiter) begin(remote string, now time.Time) (*loginAttempt, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[remote]
	if entry == nil {
		entry = &loginFailures{}
		l.entries[remote] = entry
	}
	if entry.lockedUntil.After(now) {
		return nil, false
	}
	if !entry.lockedUntil.IsZero() {
		entry.failures = nil
		entry.lockedUntil = time.Time{}
		entry.generation++
	}
	cutoff := now.Add(-10 * time.Minute)
	kept := entry.failures[:0]
	for _, at := range entry.failures {
		if !at.Before(cutoff) {
			kept = append(kept, at)
		}
	}
	entry.failures = kept
	if len(entry.failures)+entry.inflight >= 5 {
		return nil, false
	}
	entry.inflight++
	return &loginAttempt{limiter: l, entry: entry, generation: entry.generation, now: now}, true
}

func (a *loginAttempt) finish(outcome loginOutcome) {
	if a == nil || a.finished {
		return
	}
	a.finished = true
	l := a.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := a.entry
	if entry.inflight > 0 {
		entry.inflight--
	}
	if outcome == loginSucceeded {
		entry.failures = nil
		entry.lockedUntil = time.Time{}
		entry.generation++
	}
	if outcome == loginInvalid && entry.generation == a.generation {
		entry.failures = append(entry.failures, a.now)
		if len(entry.failures) >= 5 {
			entry.lockedUntil = a.now.Add(15 * time.Minute)
		}
	}
}

func normalizeRemoteAddr(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}
