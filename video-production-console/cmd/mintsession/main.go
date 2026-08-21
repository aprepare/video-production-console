package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"video-production-console/internal/security"
	"video-production-console/internal/store"
)

func main() {
	abs, err := filepath.Abs(filepath.Join("video-console-data", "console.db"))
	if err != nil {
		panic(err)
	}
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsnPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(dsnPath, "/") {
		dsnPath = "/" + dsnPath
	}
	dsn := (&url.URL{Scheme: "file", Path: dsnPath, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	auth := store.NewAuthStore(db)
	admin, err := auth.Admin(context.Background())
	if err != nil {
		panic(err)
	}
	token, tokenHash, err := security.NewSecret(rand.Reader)
	if err != nil {
		panic(err)
	}
	csrf, csrfHash, err := security.NewSecret(rand.Reader)
	if err != nil {
		panic(err)
	}
	now := time.Now()
	if err := auth.CreateSession(context.Background(), store.AuthSession{
		ID: uuid.NewString(), AdminID: admin.ID, TokenHash: tokenHash, CSRFHash: csrfHash,
		RemoteAddr: "127.0.0.1:1", ExpiresAt: now.Add(24 * time.Hour), LastSeenAt: now, CreatedAt: now,
	}); err != nil {
		panic(err)
	}
	cookie := "# Netscape HTTP Cookie File\n" +
		"127.0.0.1\tFALSE\t/\tFALSE\t0\tvideo_console_session\t" + token + "\n" +
		"127.0.0.1\tFALSE\t/\tFALSE\t0\tvideo_console_csrf\t" + csrf + "\n"
	if err := os.WriteFile(".tmp_console_cookies.txt", []byte(cookie), 0o600); err != nil {
		panic(err)
	}
	if err := os.WriteFile(".tmp_csrf.txt", []byte(csrf), 0o600); err != nil {
		panic(err)
	}
	os.Stdout.WriteString("session_ok\n")
}
