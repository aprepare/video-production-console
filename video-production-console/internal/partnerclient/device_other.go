//go:build !windows

package partnerclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const deviceHashDomain = "video-production-console/partner/v1\x00"

var ErrDeviceIdentityUnsupported = errors.New("device identity is unsupported")

func CurrentDeviceHash() (string, error) {
	return "", ErrDeviceIdentityUnsupported
}

func hashMachineGUID(machineGUID string) string {
	normalized := strings.ToLower(strings.TrimSpace(machineGUID))
	sum := sha256.Sum256([]byte(deviceHashDomain + normalized))
	return hex.EncodeToString(sum[:])
}
