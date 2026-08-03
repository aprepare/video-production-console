//go:build !windows

package security

import "errors"

const MaxSecretSize = 16 << 10

var (
	ErrSecretStoreUnsupported = errors.New("secret storage is unsupported")
	ErrSecretEmpty            = errors.New("secret must not be empty")
	ErrSecretTooLarge         = errors.New("secret exceeds 16 KiB")
	ErrSecretInvalid          = errors.New("encrypted secret is invalid")
	ErrSecretProtection       = errors.New("secret protection failed")
)

type Protector interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}

type unsupportedProtector struct{}

func NewSecretProtector() Protector { return unsupportedProtector{} }

func (unsupportedProtector) Protect([]byte) ([]byte, error) {
	return nil, ErrSecretStoreUnsupported
}

func (unsupportedProtector) Unprotect([]byte) ([]byte, error) {
	return nil, ErrSecretStoreUnsupported
}
