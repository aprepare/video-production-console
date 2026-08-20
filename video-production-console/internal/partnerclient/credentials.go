package partnerclient

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/security"
)

const credentialStoreVersion = 1

var errCredentialProtectorRequired = errors.New("credential protector is required")

type CredentialStore struct {
	path      string
	protector security.Protector
}

type storedCredentials struct {
	Version               int    `json:"version"`
	PartnerID             string `json:"partner_id"`
	ProtectedDeviceSecret string `json:"protected_device_secret"`
	ProtectedAuraKey      string `json:"protected_aura_key"`
}

func NewCredentialStore(path string, protector security.Protector) *CredentialStore {
	return &CredentialStore{path: path, protector: protector}
}

func (s *CredentialStore) Save(credentials Credentials) error {
	if s.protector == nil {
		return errCredentialProtectorRequired
	}

	protectedDeviceSecret, err := s.protector.Protect([]byte(credentials.DeviceSecret))
	if err != nil {
		return fmt.Errorf("protect device secret: %w", err)
	}
	if len(protectedDeviceSecret) == 0 {
		return errors.New("protect device secret: empty ciphertext")
	}

	protectedAuraKey, err := s.protector.Protect([]byte(credentials.AuraAPIKey))
	if err != nil {
		return fmt.Errorf("protect aura key: %w", err)
	}
	if len(protectedAuraKey) == 0 {
		return errors.New("protect aura key: empty ciphertext")
	}

	raw, err := json.Marshal(storedCredentials{
		Version:               credentialStoreVersion,
		PartnerID:             credentials.PartnerID,
		ProtectedDeviceSecret: base64.StdEncoding.EncodeToString(protectedDeviceSecret),
		ProtectedAuraKey:      base64.StdEncoding.EncodeToString(protectedAuraKey),
	})
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return writeCredentialsAtomically(s.path, raw)
}

func (s *CredentialStore) Clear() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *CredentialStore) Load() (Credentials, error) {
	if s.protector == nil {
		return Credentials{}, errCredentialProtectorRequired
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return Credentials{}, err
	}
	var stored storedCredentials
	if err := json.Unmarshal(raw, &stored); err != nil {
		return Credentials{}, fmt.Errorf("decode credentials: %w", err)
	}
	if stored.Version != credentialStoreVersion {
		return Credentials{}, fmt.Errorf("unsupported credential version %d", stored.Version)
	}
	if stored.ProtectedDeviceSecret == "" {
		return Credentials{}, errors.New("protected device secret is empty")
	}
	if stored.ProtectedAuraKey == "" {
		return Credentials{}, errors.New("protected aura key is empty")
	}

	protectedDeviceSecret, err := base64.StdEncoding.DecodeString(stored.ProtectedDeviceSecret)
	if err != nil {
		return Credentials{}, fmt.Errorf("decode protected device secret: %w", err)
	}
	if len(protectedDeviceSecret) == 0 {
		return Credentials{}, errors.New("protected device secret is empty")
	}
	protectedAuraKey, err := base64.StdEncoding.DecodeString(stored.ProtectedAuraKey)
	if err != nil {
		return Credentials{}, fmt.Errorf("decode protected aura key: %w", err)
	}
	if len(protectedAuraKey) == 0 {
		return Credentials{}, errors.New("protected aura key is empty")
	}

	deviceSecret, err := s.protector.Unprotect(protectedDeviceSecret)
	if err != nil {
		return Credentials{}, fmt.Errorf("unprotect device secret: %w", err)
	}
	defer clear(deviceSecret)

	auraAPIKey, err := s.protector.Unprotect(protectedAuraKey)
	if err != nil {
		return Credentials{}, fmt.Errorf("unprotect aura key: %w", err)
	}
	defer clear(auraAPIKey)

	return Credentials{
		PartnerID:    stored.PartnerID,
		DeviceSecret: string(deviceSecret),
		AuraAPIKey:   string(auraAPIKey),
	}, nil
}

func writeCredentialsAtomically(path string, raw []byte) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create credential temp file: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set credential file permissions: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync credentials: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace credentials: %w", err)
	}
	keepTemp = false
	return nil
}
