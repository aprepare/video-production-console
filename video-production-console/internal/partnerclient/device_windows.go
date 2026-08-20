//go:build windows

package partnerclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const deviceHashDomain = "video-production-console/partner/v1\x00"

var ErrDeviceIdentityUnsupported = errors.New("device identity is unsupported")

func CurrentDeviceHash() (string, error) {
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return "", err
	}
	defer key.Close()

	machineGUID, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(machineGUID) == "" {
		return "", errors.New("machine GUID is empty")
	}
	return hashMachineGUID(machineGUID), nil
}

func hashMachineGUID(machineGUID string) string {
	normalized := strings.ToLower(strings.TrimSpace(machineGUID))
	sum := sha256.Sum256([]byte(deviceHashDomain + normalized))
	return hex.EncodeToString(sum[:])
}
