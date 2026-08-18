package security

import (
	"strings"
	"testing"
)

func TestPasswordHashUsesBcryptAndVerifies(t *testing.T) {
	hash, err := HashPassword("sensitive-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("hash = %q, want bcrypt", hash)
	}
	if hash == "sensitive-password" {
		t.Fatal("password stored in plaintext")
	}
	if !CheckPassword(hash, "sensitive-password") {
		t.Fatal("correct password rejected")
	}
	if CheckPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestPasswordHashSupportsContractMaximumLength(t *testing.T) {
	password := strings.Repeat("x", 128)
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash 128-byte password: %v", err)
	}
	if !CheckPassword(hash, password) {
		t.Fatal("128-byte password rejected")
	}
}

func TestHashPasswordValidatesUTF8ByteLength(t *testing.T) {
	for _, password := range []string{"", "12345", strings.Repeat("密", 43)} {
		if _, err := HashPassword(password); err == nil {
			t.Errorf("HashPassword accepted %d-byte password", len([]byte(password)))
		}
	}
	for _, password := range []string{"123456", "密码", strings.Repeat("x", 128)} {
		if _, err := HashPassword(password); err != nil {
			t.Errorf("HashPassword rejected %d-byte password: %v", len([]byte(password)), err)
		}
	}
}
