package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"time"
)

func NewSecret(random io.Reader) (string, string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", "", fmt.Errorf("generate secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	return secret, HashSecret(secret), nil
}

func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func SecretMatches(wantHash, secret string) bool {
	got := HashSecret(secret)
	return subtle.ConstantTimeCompare([]byte(wantHash), []byte(got)) == 1
}

func SessionValid(expiresAt, passwordChangedAt, createdAt, now time.Time) bool {
	return expiresAt.After(now) && !createdAt.Before(passwordChangedAt)
}
