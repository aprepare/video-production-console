package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/security"
)

func TestAuthStoreBootstrapAdminIsUniqueAndIdempotent(t *testing.T) {
	repo, db := newAuthStoreTest(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	first, created, err := repo.CreateAdminIfNone(context.Background(), "$2a$10$first", now)
	if err != nil || !created {
		t.Fatalf("first create = (%+v,%v,%v)", first, created, err)
	}
	second, created, err := repo.CreateAdminIfNone(context.Background(), "$2a$10$changed", now.Add(time.Hour))
	if err != nil || created {
		t.Fatalf("second create = (%+v,%v,%v)", second, created, err)
	}
	if second.ID != first.ID || second.PasswordHash != "$2a$10$first" {
		t.Fatalf("second = %+v, want unchanged first", second)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("admin count=%d err=%v", count, err)
	}
	var singleton int
	if err := db.QueryRow(`SELECT singleton FROM admins WHERE id=?`, first.ID).Scan(&singleton); err != nil || singleton != 1 {
		t.Fatalf("singleton=%d err=%v", singleton, err)
	}
}

func TestAuthStoreSessionStoresHashesAndRejectsExpiryRevocationPasswordChange(t *testing.T) {
	repo, db := newAuthStoreTest(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	admin, _, err := repo.CreateAdminIfNone(context.Background(), "bcrypt-hash", now)
	if err != nil {
		t.Fatal(err)
	}
	session := AuthSession{ID: uuid.NewString(), AdminID: admin.ID, TokenHash: security.HashSecret("raw-token"), CSRFHash: security.HashSecret("raw-csrf"), RemoteAddr: "127.0.0.1", ExpiresAt: now.Add(7 * 24 * time.Hour), LastSeenAt: now, CreatedAt: now}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	var tokenHash, csrfHash string
	if err := db.QueryRow(`SELECT token_hash, csrf_hash FROM auth_sessions WHERE id=?`, session.ID).Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatal(err)
	}
	if tokenHash != session.TokenHash || csrfHash != session.CSRFHash || strings.Contains(tokenHash, "raw-token") {
		t.Fatalf("stored token=%q csrf=%q", tokenHash, csrfHash)
	}
	got, err := repo.FindValidSession(context.Background(), session.TokenHash, now.Add(time.Hour))
	if err != nil || got.ID != session.ID {
		t.Fatalf("valid lookup=(%+v,%v)", got, err)
	}
	if _, err := repo.FindValidSession(context.Background(), session.TokenHash, session.ExpiresAt); err != ErrUnauthenticated {
		t.Fatalf("expired err=%v", err)
	}
	if err := repo.RevokeSession(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindValidSession(context.Background(), session.TokenHash, now); err != ErrUnauthenticated {
		t.Fatalf("revoked err=%v", err)
	}

	session.ID = uuid.NewString()
	session.TokenHash = security.HashSecret("second-token")
	session.CreatedAt = now.Add(2 * time.Hour)
	session.LastSeenAt = session.CreatedAt
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := repo.ChangePasswordAndRevokeAll(context.Background(), admin.ID, "bcrypt-hash", "new-bcrypt", now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindValidSession(context.Background(), session.TokenHash, now.Add(4*time.Hour)); err != ErrUnauthenticated {
		t.Fatalf("password-change session err=%v", err)
	}
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM admins WHERE id=?`, admin.ID).Scan(&hash); err != nil || hash != "new-bcrypt" {
		t.Fatalf("password hash=%q err=%v", hash, err)
	}
}

func TestAuthStoreTouchSessionUpdatesActivityAndRejectsExpired(t *testing.T) {
	repo, db := newAuthStoreTest(t)
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	admin, _, err := repo.CreateAdminIfNone(context.Background(), "hash", now)
	if err != nil {
		t.Fatal(err)
	}
	session := AuthSession{ID: uuid.NewString(), AdminID: admin.ID, TokenHash: security.HashSecret("touch-token"), CSRFHash: security.HashSecret("touch-csrf"), RemoteAddr: "x", ExpiresAt: now.Add(time.Hour), LastSeenAt: now, CreatedAt: now}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	updated := now.Add(2 * time.Minute)
	if err := repo.TouchSession(context.Background(), session.ID, updated); err != nil {
		t.Fatal(err)
	}
	var got time.Time
	if err := db.QueryRow(`SELECT last_seen_at FROM auth_sessions WHERE id=?`, session.ID).Scan(&got); err != nil || !got.Equal(updated) {
		t.Fatalf("last_seen_at=%v err=%v", got, err)
	}
	if err := repo.TouchSession(context.Background(), session.ID, now.Add(2*time.Hour)); err != ErrUnauthenticated {
		t.Fatalf("expired touch err=%v", err)
	}
}

func TestAuthStoreRejectsMalformedUUIDs(t *testing.T) {
	repo, _ := newAuthStoreTest(t)
	now := time.Now().UTC()
	if err := repo.CreateSession(context.Background(), AuthSession{ID: "bad", AdminID: "also-bad", TokenHash: "a", CSRFHash: "b", RemoteAddr: "x", ExpiresAt: now.Add(time.Hour), LastSeenAt: now, CreatedAt: now}); err == nil {
		t.Fatal("malformed UUID accepted")
	}
	if err := repo.RevokeSession(context.Background(), "bad"); err == nil {
		t.Fatal("malformed session UUID accepted")
	}
	if err := repo.ChangePasswordAndRevokeAll(context.Background(), "bad", "old", "hash", now); err == nil {
		t.Fatal("malformed admin UUID accepted")
	}
}

func TestAuthStoreRejectsMalformedTokenHashes(t *testing.T) {
	repo, _ := newAuthStoreTest(t)
	now := time.Now().UTC()
	admin, _, err := repo.CreateAdminIfNone(context.Background(), "hash", now)
	if err != nil {
		t.Fatal(err)
	}
	base := AuthSession{ID: uuid.NewString(), AdminID: admin.ID, TokenHash: "not-sha256", CSRFHash: security.HashSecret("csrf"), RemoteAddr: "x", ExpiresAt: now.Add(time.Hour), LastSeenAt: now, CreatedAt: now}
	if err := repo.CreateSession(context.Background(), base); err == nil {
		t.Fatal("malformed token hash accepted")
	}
	base.TokenHash = security.HashSecret("token")
	base.CSRFHash = "not-sha256"
	if err := repo.CreateSession(context.Background(), base); err == nil {
		t.Fatal("malformed CSRF hash accepted")
	}
}

func TestAuthStoreRotatesCSRFFromCurrentHashAtomically(t *testing.T) {
	repo, _ := newAuthStoreTest(t)
	now := time.Now().UTC()
	admin, _, err := repo.CreateAdminIfNone(context.Background(), "hash", now)
	if err != nil {
		t.Fatal(err)
	}
	session := AuthSession{ID: uuid.NewString(), AdminID: admin.ID, TokenHash: security.HashSecret("token"), CSRFHash: security.HashSecret("old-csrf"), RemoteAddr: "x", ExpiresAt: now.Add(time.Hour), LastSeenAt: now, CreatedAt: now}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	newHash := security.HashSecret("new-csrf")
	if err := repo.RotateCSRF(context.Background(), session.ID, security.HashSecret("wrong"), newHash); err != ErrUnauthenticated {
		t.Fatalf("wrong current hash err=%v", err)
	}
	got, err := repo.FindValidSession(context.Background(), session.TokenHash, now)
	if err != nil || got.CSRFHash != session.CSRFHash {
		t.Fatalf("failed CAS changed hash: got=%q err=%v", got.CSRFHash, err)
	}
	if err := repo.RotateCSRF(context.Background(), session.ID, session.CSRFHash, newHash); err != nil {
		t.Fatal(err)
	}
	got, err = repo.FindValidSession(context.Background(), session.TokenHash, now)
	if err != nil || got.CSRFHash != newHash {
		t.Fatalf("rotated hash=%q err=%v", got.CSRFHash, err)
	}
}

func TestAuthStorePasswordChangeRequiresCurrentHashWithoutRevokingOnCASFailure(t *testing.T) {
	repo, db := newAuthStoreTest(t)
	now := time.Now().UTC()
	admin, _, err := repo.CreateAdminIfNone(context.Background(), "old-hash", now)
	if err != nil {
		t.Fatal(err)
	}
	session := AuthSession{ID: uuid.NewString(), AdminID: admin.ID, TokenHash: security.HashSecret("token"), CSRFHash: security.HashSecret("csrf"), RemoteAddr: "x", ExpiresAt: now.Add(time.Hour), LastSeenAt: now, CreatedAt: now}
	if err := repo.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := repo.ChangePasswordAndRevokeAll(context.Background(), admin.ID, "stale-hash", "new-hash", now.Add(time.Minute)); err != ErrUnauthenticated {
		t.Fatalf("stale CAS err=%v", err)
	}
	var password string
	var sessions int
	if err := db.QueryRow(`SELECT password_hash FROM admins WHERE id=?`, admin.ID).Scan(&password); err != nil || password != "old-hash" {
		t.Fatalf("password=%q err=%v", password, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE admin_id=?`, admin.ID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions=%d err=%v", sessions, err)
	}
}

func TestAuthStoreConcurrentPasswordChangesOnlyOneCASWins(t *testing.T) {
	repo, db := newAuthStoreTest(t)
	now := time.Now().UTC()
	admin, _, err := repo.CreateAdminIfNone(context.Background(), "old-hash", now)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, next := range []string{"new-one", "new-two"} {
		go func(next string) {
			<-start
			results <- repo.ChangePasswordAndRevokeAll(context.Background(), admin.ID, "old-hash", next, now.Add(time.Minute))
		}(next)
	}
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("concurrent errors=(%v,%v), want exactly one success", first, second)
	}
	loser := first
	if loser == nil {
		loser = second
	}
	if loser != ErrUnauthenticated {
		t.Fatalf("loser err=%v", loser)
	}
	var password string
	if err := db.QueryRow(`SELECT password_hash FROM admins WHERE id=?`, admin.ID).Scan(&password); err != nil || password != "new-one" && password != "new-two" {
		t.Fatalf("password=%q err=%v", password, err)
	}
}

func newAuthStoreTest(t *testing.T) (*AuthStore, *sql.DB) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewAuthStore(db), db
}
