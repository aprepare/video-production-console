//go:build !windows

package security

import (
	"errors"
	"testing"
)

func TestSecretProtectorUnsupportedOffWindows(t *testing.T) {
	protector := NewSecretProtector()
	if _, err := protector.Protect([]byte("secret")); !errors.Is(err, ErrSecretStoreUnsupported) {
		t.Fatalf("Protect() error = %v", err)
	}
	if _, err := protector.Unprotect([]byte("ciphertext")); !errors.Is(err, ErrSecretStoreUnsupported) {
		t.Fatalf("Unprotect() error = %v", err)
	}
}
