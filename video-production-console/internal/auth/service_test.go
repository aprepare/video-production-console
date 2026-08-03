package auth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-production-console/internal/security"
	"video-production-console/internal/store"
)

func TestBootstrapCreatesBcryptAdminWithoutPlaintextAndIsIdempotent(t *testing.T) {
	service, db, _ := newServiceTest(t)
	if err := service.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM admins`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, "123321") || !security.CheckPassword(hash, "123321") {
		t.Fatalf("unsafe password hash %q", hash)
	}
	if err := service.Bootstrap(context.Background(), "different-value"); err != nil {
		t.Fatal(err)
	}
	var unchanged string
	if err := db.QueryRow(`SELECT password_hash FROM admins`).Scan(&unchanged); err != nil || unchanged != hash {
		t.Fatalf("bootstrap changed hash: %q err=%v", unchanged, err)
	}
	var settings int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE value='123321'`).Scan(&settings); err != nil || settings != 0 {
		t.Fatalf("plaintext setting count=%d err=%v", settings, err)
	}
}

func TestBootstrapRejectsPasswordOutsideContractLength(t *testing.T) {
	service, db, _ := newServiceTest(t)
	if err := service.Bootstrap(context.Background(), "short"); err == nil {
		t.Fatal("short bootstrap password accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("admins=%d err=%v", count, err)
	}
}

func TestLoginStoresOnlyHashesAndSessionExpiresAfterSevenDays(t *testing.T) {
	service, db, clock := newServiceTest(t)
	if err := service.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(context.Background(), "123321", "127.0.0.1:4500")
	if err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || login.CSRF == "" || login.Token == login.CSRF {
		t.Fatalf("login = %+v", login)
	}
	var tokenHash, csrfHash string
	if err := db.QueryRow(`SELECT token_hash,csrf_hash FROM auth_sessions`).Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatal(err)
	}
	if tokenHash == login.Token || csrfHash == login.CSRF {
		t.Fatal("raw login secrets persisted")
	}
	if !security.SecretMatches(tokenHash, login.Token) || !security.SecretMatches(csrfHash, login.CSRF) {
		t.Fatal("stored hashes do not match secrets")
	}
	identity, err := service.Authenticate(context.Background(), login.Token)
	if err != nil || identity.SessionID == "" {
		t.Fatalf("authenticate=(%+v,%v)", identity, err)
	}
	clock.now = clock.now.Add(7 * 24 * time.Hour)
	if _, err := service.Authenticate(context.Background(), login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expiry err=%v", err)
	}
}

func TestLogoutRevokeAllAndPasswordChangeInvalidateSessions(t *testing.T) {
	service, _, _ := newServiceTest(t)
	ctx := context.Background()
	if err := service.Bootstrap(ctx, "123321"); err != nil {
		t.Fatal(err)
	}
	one, _ := service.Login(ctx, "123321", "127.0.0.1:1")
	idOne, _ := service.Authenticate(ctx, one.Token)
	if err := service.Logout(ctx, idOne); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, one.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("logout err=%v", err)
	}
	two, _ := service.Login(ctx, "123321", "127.0.0.1:2")
	idTwo, _ := service.Authenticate(ctx, two.Token)
	if err := service.ChangePassword(ctx, idTwo, "wrong", "654321"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password err=%v", err)
	}
	if err := service.ChangePassword(ctx, idTwo, "123321", "short"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password err=%v", err)
	}
	if err := service.ChangePassword(ctx, idTwo, "123321", strings.Repeat("x", 129)); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("long password err=%v", err)
	}
	if err := service.ChangePassword(ctx, idTwo, "123321", "654321"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, two.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("password-change err=%v", err)
	}
	if _, err := service.Login(ctx, "123321", "127.0.0.1:3"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login err=%v", err)
	}
	three, err := service.Login(ctx, "654321", "127.0.0.1:3")
	if err != nil {
		t.Fatal(err)
	}
	idThree, _ := service.Authenticate(ctx, three.Token)
	if err := service.RevokeAll(ctx, idThree); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, three.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoke all err=%v", err)
	}
}

func TestLoginRateLimitNormalizesRemoteAddrAndUsesInjectedClock(t *testing.T) {
	service, _, clock := newServiceTest(t)
	ctx := context.Background()
	if err := service.Bootstrap(ctx, "123321"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		port := 1000 + i
		if _, err := service.Login(ctx, "wrong", "192.0.2.10:"+fmtInt(port)); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d err=%v", i, err)
		}
	}
	if _, err := service.Login(ctx, "123321", "192.0.2.10:9999"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("same IP not locked: %v", err)
	}
	if _, err := service.Login(ctx, "123321", "192.0.2.11:9999"); err != nil {
		t.Fatalf("different IP locked: %v", err)
	}
	clock.now = clock.now.Add(15 * time.Minute)
	if _, err := service.Login(ctx, "123321", "192.0.2.10:9999"); err != nil {
		t.Fatalf("lock did not expire: %v", err)
	}
	for i := 0; i < 4; i++ {
		_, _ = service.Login(ctx, "wrong", "198.51.100.2:1")
	}
	if _, err := service.Login(ctx, "123321", "198.51.100.2:1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_, _ = service.Login(ctx, "wrong", "198.51.100.2:2")
	}
	if _, err := service.Login(ctx, "123321", "198.51.100.2:3"); err != nil {
		t.Fatalf("successful login did not clear failure window: %v", err)
	}
}

func TestLoginRateLimitLinearizesConcurrentPasswordChecks(t *testing.T) {
	service, _, _ := newServiceTest(t)
	if err := service.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 6)
	release := make(chan struct{})
	var checks atomic.Int32
	service.checkPassword = func(string, string) bool { checks.Add(1); entered <- struct{}{}; <-release; return false }
	start := make(chan struct{})
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func() {
			<-start
			_, err := service.Login(context.Background(), "wrong", "192.0.2.55:1234")
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 5; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("fewer than five password checks entered")
		}
	}
	select {
	case err := <-results:
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("sixth concurrent result=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sixth concurrent login was not rejected before password checks completed")
	}
	close(release)
	for i := 0; i < 5; i++ {
		if err := <-results; !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("password check result=%v", err)
		}
	}
	if got := checks.Load(); got != 5 {
		t.Fatalf("password checks=%d, want 5", got)
	}
}

func TestLoginRandomFailureDoesNotCreateSession(t *testing.T) {
	service, db, _ := newServiceTest(t)
	if err := service.Bootstrap(context.Background(), "123321"); err != nil {
		t.Fatal(err)
	}
	service.random = bytes.NewReader(nil)
	if _, err := service.Login(context.Background(), "123321", "127.0.0.1:1"); err == nil {
		t.Fatal("random failure ignored")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sessions=%d err=%v", count, err)
	}
}

func TestLoginInfrastructureFailuresDoNotConsumeRateLimit(t *testing.T) {
	hash, err := security.HashPassword("123321")
	if err != nil {
		t.Fatal(err)
	}
	repo := &outageAuthRepository{admin: store.Admin{ID: "4d739048-2e42-45d3-8128-4dd9b1cae664", PasswordHash: hash}}
	service := NewService(repo, Options{Random: bytes.NewReader(bytes.Repeat([]byte{3}, 4096)), CheckPassword: func(string, string) bool { return true }})
	repo.adminErr = errors.New("database unavailable")
	for i := 0; i < 8; i++ {
		if _, err := service.Login(context.Background(), "123321", "203.0.113.1:1"); err == nil || errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrRateLimited) {
			t.Fatalf("database outage %d err=%v", i, err)
		}
	}
	repo.adminErr = nil
	if _, err := service.Login(context.Background(), "123321", "203.0.113.1:1"); err != nil {
		t.Fatalf("database outages consumed limit: %v", err)
	}
	repo.createErr = errors.New("store unavailable")
	for i := 0; i < 8; i++ {
		if _, err := service.Login(context.Background(), "123321", "203.0.113.2:1"); err == nil || errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrRateLimited) {
			t.Fatalf("store outage %d err=%v", i, err)
		}
	}
	repo.createErr = nil
	if _, err := service.Login(context.Background(), "123321", "203.0.113.2:1"); err != nil {
		t.Fatalf("store outages consumed limit: %v", err)
	}
	service.random = bytes.NewReader(nil)
	for i := 0; i < 8; i++ {
		if _, err := service.Login(context.Background(), "123321", "203.0.113.3:1"); err == nil || errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrRateLimited) {
			t.Fatalf("RNG outage %d err=%v", i, err)
		}
	}
	service.random = bytes.NewReader(bytes.Repeat([]byte{4}, 128))
	if _, err := service.Login(context.Background(), "123321", "203.0.113.3:1"); err != nil {
		t.Fatalf("RNG outages consumed limit: %v", err)
	}
}

func TestRefreshSessionMapsStoreCASFailureToUnauthenticated(t *testing.T) {
	repo := &outageAuthRepository{rotateErr: store.ErrUnauthenticated}
	service := NewService(repo, Options{Random: bytes.NewReader(bytes.Repeat([]byte{9}, 128))})
	identity := Identity{SessionID: "4d739048-2e42-45d3-8128-4dd9b1cae664", CSRFHash: security.HashSecret("old")}
	if _, err := service.RefreshSession(context.Background(), identity, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("refresh CAS err=%v", err)
	}
}

func TestChangePasswordPreservesAdminDatabaseError(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	repo := &outageAuthRepository{adminErr: databaseErr}
	service := NewService(repo, Options{})
	err := service.ChangePassword(context.Background(), Identity{AdminID: "4d739048-2e42-45d3-8128-4dd9b1cae664"}, "123321", "654321")
	if !errors.Is(err, databaseErr) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("change password DB err=%v", err)
	}
}

func TestChangePasswordMapsCASConflictToInvalidCredentials(t *testing.T) {
	hash, err := security.HashPassword("123321")
	if err != nil {
		t.Fatal(err)
	}
	repo := &outageAuthRepository{admin: store.Admin{ID: "4d739048-2e42-45d3-8128-4dd9b1cae664", PasswordHash: hash}, changeErr: store.ErrUnauthenticated}
	service := NewService(repo, Options{})
	err = service.ChangePassword(context.Background(), Identity{AdminID: repo.admin.ID}, "123321", "654321")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CAS conflict err=%v", err)
	}
	if errors.Is(err, store.ErrUnauthenticated) {
		t.Fatalf("store CAS sentinel leaked: %v", err)
	}
}

type outageAuthRepository struct {
	admin     store.Admin
	adminErr  error
	createErr error
	rotateErr error
	changeErr error
}

func (r *outageAuthRepository) CreateAdminIfNone(context.Context, string, time.Time) (store.Admin, bool, error) {
	return r.admin, false, r.adminErr
}
func (r *outageAuthRepository) Admin(context.Context) (store.Admin, error) {
	return r.admin, r.adminErr
}
func (r *outageAuthRepository) CreateSession(context.Context, store.AuthSession) error {
	return r.createErr
}
func (r *outageAuthRepository) FindValidSession(context.Context, string, time.Time) (store.AuthSession, error) {
	return store.AuthSession{}, store.ErrUnauthenticated
}
func (r *outageAuthRepository) TouchSession(context.Context, string, time.Time) error { return nil }
func (r *outageAuthRepository) RotateCSRF(context.Context, string, string, string) error {
	return r.rotateErr
}
func (r *outageAuthRepository) RevokeSession(context.Context, string) error { return nil }
func (r *outageAuthRepository) RevokeAll(context.Context, string) error     { return nil }
func (r *outageAuthRepository) ChangePasswordAndRevokeAll(context.Context, string, string, string, time.Time) error {
	return r.changeErr
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func fmtInt(v int) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(digits[v%10]) + out
		v /= 10
	}
	return out
}

func newServiceTest(t *testing.T) (*Service, *sql.DB, *fakeClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := &fakeClock{now: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)}
	randomBytes := make([]byte, 4096)
	for i := range randomBytes {
		randomBytes[i] = byte(i)
	}
	random := bytes.NewReader(randomBytes)
	return NewService(store.NewAuthStore(db), Options{Now: clock.Now, Random: random}), db, clock
}
