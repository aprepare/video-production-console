package security

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestNewSecretUses32BytesAndStoresOnlyHash(t *testing.T) {
	input := append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...)
	reader := bytes.NewReader(input)
	token, tokenHash, err := NewSecret(reader)
	if err != nil {
		t.Fatal(err)
	}
	csrf, csrfHash, err := NewSecret(reader)
	if err != nil {
		t.Fatal(err)
	}
	if token == csrf {
		t.Fatal("token and CSRF are not independent")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("token decoded length = %d, err %v", len(decoded), err)
	}
	if tokenHash == token || csrfHash == csrf {
		t.Fatal("hash contains raw secret")
	}
	if !SecretMatches(tokenHash, token) || !SecretMatches(csrfHash, csrf) {
		t.Fatal("secret hash mismatch")
	}
}

func TestSessionValidityIncludesExpiryAndPasswordChange(t *testing.T) {
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	if !SessionValid(now.Add(time.Hour), now.Add(-time.Hour), now, now) {
		t.Fatal("valid session rejected")
	}
	if SessionValid(now, now.Add(-time.Hour), now, now) {
		t.Fatal("expired session accepted")
	}
	if SessionValid(now.Add(time.Hour), now.Add(time.Second), now, now) {
		t.Fatal("session predating password change accepted")
	}
}
