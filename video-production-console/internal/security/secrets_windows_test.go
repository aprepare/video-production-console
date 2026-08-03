//go:build windows

package security

import (
	"bytes"
	"errors"
	"testing"
)

func TestDPAPIProtectorRoundTripAndBoundaries(t *testing.T) {
	protector := NewSecretProtector()
	plain := []byte("windows-current-user-secret")
	ciphertext, err := protector.Protect(plain)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	if bytes.Equal(ciphertext, plain) || len(ciphertext) == 0 {
		t.Fatalf("ciphertext was not protected: %x", ciphertext)
	}
	got, err := protector.Unprotect(ciphertext)
	if err != nil {
		t.Fatalf("Unprotect() error = %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Unprotect() = %q, want %q", got, plain)
	}

	for _, test := range []struct {
		name string
		call func() error
		want error
	}{
		{"protect empty", func() error { _, err := protector.Protect(nil); return err }, ErrSecretEmpty},
		{"unprotect empty", func() error { _, err := protector.Unprotect(nil); return err }, ErrSecretEmpty},
		{"protect too large", func() error { _, err := protector.Protect(make([]byte, MaxSecretSize+1)); return err }, ErrSecretTooLarge},
		{"unprotect corrupt", func() error { _, err := protector.Unprotect([]byte("not-dpapi")); return err }, ErrSecretInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
