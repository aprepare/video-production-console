package partnerclient

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestHashMachineGUIDNeverReturnsRawValue(t *testing.T) {
	raw := "01234567-89ab-cdef-0123-456789abcdef"
	got := hashMachineGUID(raw)
	if got == raw || len(got) != 64 {
		t.Fatalf("hash=%q", got)
	}
	if got != hashMachineGUID(strings.ToUpper(raw)) {
		t.Fatalf("hash must normalize GUID")
	}
}

func TestCurrentDeviceHashIsUnsupportedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reads the real machine GUID; hash behavior is tested in isolation")
	}

	if _, err := CurrentDeviceHash(); !errors.Is(err, ErrDeviceIdentityUnsupported) {
		t.Fatalf("CurrentDeviceHash() error = %v, want %v", err, ErrDeviceIdentityUnsupported)
	}
}
