//go:build windows

package security

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const MaxSecretSize = 16 << 10

var (
	ErrSecretStoreUnsupported = errors.New("secret storage is unsupported")
	ErrSecretEmpty            = errors.New("secret must not be empty")
	ErrSecretTooLarge         = errors.New("secret exceeds 16 KiB")
	ErrSecretInvalid          = errors.New("encrypted secret is invalid")
	ErrSecretProtection       = errors.New("secret protection failed")
)

// Protector encrypts and decrypts secret bytes without defining where the
// ciphertext is stored. Services accept this interface so tests and
// non-Windows builds do not need access to the current user's DPAPI profile.
type Protector interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}

type dpapiProtector struct{}

// NewSecretProtector returns a current-user DPAPI protector on Windows.
func NewSecretProtector() Protector { return dpapiProtector{} }

func (dpapiProtector) Protect(plain []byte) ([]byte, error) {
	if err := validatePlainSecret(plain); err != nil {
		return nil, err
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		runtime.KeepAlive(plain)
		return nil, ErrSecretProtection
	}
	runtime.KeepAlive(plain)
	result, err := copyAndLocalFree(out)
	if err != nil || len(result) == 0 {
		return nil, ErrSecretProtection
	}
	return result, nil
}

func (dpapiProtector) Unprotect(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, ErrSecretEmpty
	}
	in := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		runtime.KeepAlive(ciphertext)
		return nil, ErrSecretInvalid
	}
	runtime.KeepAlive(ciphertext)
	result, err := copyAndLocalFree(out)
	if err != nil || len(result) == 0 {
		return nil, ErrSecretInvalid
	}
	if len(result) > MaxSecretSize {
		clear(result)
		return nil, ErrSecretTooLarge
	}
	return result, nil
}

func validatePlainSecret(value []byte) error {
	if len(value) == 0 {
		return ErrSecretEmpty
	}
	if len(value) > MaxSecretSize {
		return ErrSecretTooLarge
	}
	return nil
}

func copyAndLocalFree(blob windows.DataBlob) ([]byte, error) {
	if blob.Data == nil {
		return nil, ErrSecretProtection
	}
	result := append([]byte(nil), unsafe.Slice(blob.Data, int(blob.Size))...)
	_, err := windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(blob.Data))))
	if err != nil {
		clear(result)
		return nil, ErrSecretProtection
	}
	return result, nil
}
