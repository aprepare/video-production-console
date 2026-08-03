package security

import (
	"crypto/sha256"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var ErrPasswordLength = errors.New("password must be 6 to 128 bytes")

func HashPassword(password string) (string, error) {
	if size := len([]byte(password)); size < 6 || size > 128 {
		return "", ErrPasswordLength
	}
	digest := sha256.Sum256([]byte(password))
	hash, err := bcrypt.GenerateFromPassword(digest[:], bcrypt.DefaultCost)
	return string(hash), err
}

func CheckPassword(hash, password string) bool {
	digest := sha256.Sum256([]byte(password))
	return bcrypt.CompareHashAndPassword([]byte(hash), digest[:]) == nil
}
