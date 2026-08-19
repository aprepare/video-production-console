package partnerclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeProtector struct {
	prefix       []byte
	protectErr   error
	unprotectErr error
}

func (p *fakeProtector) Protect(plain []byte) ([]byte, error) {
	if p.protectErr != nil {
		return nil, p.protectErr
	}
	return append(append([]byte(nil), p.prefix...), plain...), nil
}

func (p *fakeProtector) Unprotect(ciphertext []byte) ([]byte, error) {
	if p.unprotectErr != nil {
		return nil, p.unprotectErr
	}
	if !bytes.HasPrefix(ciphertext, p.prefix) {
		return nil, errors.New("invalid fake ciphertext")
	}
	return append([]byte(nil), ciphertext[len(p.prefix):]...), nil
}

func TestCredentialStoreEncryptsSecretsAndRoundTrips(t *testing.T) {
	protector := &fakeProtector{prefix: []byte("cipher:")}
	path := filepath.Join(t.TempDir(), "partner-credentials.json")
	store := NewCredentialStore(path, protector)
	want := Credentials{PartnerID: "p1", DeviceSecret: "device-secret", AuraAPIKey: "aura-secret"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("device-secret")) || bytes.Contains(raw, []byte("aura-secret")) {
		t.Fatalf("plaintext leaked: %s", raw)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestCredentialStoreSaveWritesOnlyVersionedProtectedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partner-credentials.json")
	store := NewCredentialStore(path, &fakeProtector{prefix: []byte("cipher:")})
	if err := store.Save(Credentials{
		PartnerID:    "partner-1",
		DeviceSecret: "device-secret",
		AuraAPIKey:   "aura-secret",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]bool{
		"version":                 true,
		"partner_id":              true,
		"protected_device_secret": true,
		"protected_aura_key":      true,
	}
	if len(fields) != len(wantKeys) {
		t.Fatalf("stored fields = %v, want only %v", fields, wantKeys)
	}
	for key := range fields {
		if !wantKeys[key] {
			t.Fatalf("unexpected stored field %q", key)
		}
	}

	var partnerID string
	if err := json.Unmarshal(fields["partner_id"], &partnerID); err != nil {
		t.Fatal(err)
	}
	if partnerID != "partner-1" {
		t.Fatalf("partner_id = %q, want partner-1", partnerID)
	}
	for _, key := range []string{"protected_device_secret", "protected_aura_key"} {
		var ciphertext string
		if err := json.Unmarshal(fields[key], &ciphertext); err != nil {
			t.Fatal(err)
		}
		if ciphertext == "" {
			t.Fatalf("%s must not be empty", key)
		}
	}
}

func TestCredentialStoreLoadRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partner-credentials.json")
	if err := os.WriteFile(path, []byte(`{
		"version": 2,
		"partner_id": "p1",
		"protected_device_secret": "Y2lwaGVyOmRldmljZQ==",
		"protected_aura_key": "Y2lwaGVyOmF1cmE="
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewCredentialStore(path, &fakeProtector{prefix: []byte("cipher:")}).Load(); err == nil {
		t.Fatal("Load() error = nil, want unknown version rejection")
	}
}

func TestCredentialStoreLoadRejectsEmptyCiphertext(t *testing.T) {
	for _, test := range []struct {
		name         string
		deviceCipher string
		auraCipher   string
	}{
		{name: "device secret", deviceCipher: "", auraCipher: "Y2lwaGVyOmF1cmE="},
		{name: "aura key", deviceCipher: "Y2lwaGVyOmRldmljZQ==", auraCipher: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "partner-credentials.json")
			raw := fmt.Sprintf(`{
				"version": 1,
				"partner_id": "p1",
				"protected_device_secret": %q,
				"protected_aura_key": %q
			}`, test.deviceCipher, test.auraCipher)
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := NewCredentialStore(path, &fakeProtector{prefix: []byte("cipher:")}).Load(); err == nil {
				t.Fatal("Load() error = nil, want empty ciphertext rejection")
			}
		})
	}
}

func TestCredentialStorePropagatesProtectorErrors(t *testing.T) {
	protectErr := errors.New("protect failed")
	savePath := filepath.Join(t.TempDir(), "save.json")
	if err := NewCredentialStore(savePath, &fakeProtector{protectErr: protectErr}).Save(Credentials{
		PartnerID:    "p1",
		DeviceSecret: "device",
		AuraAPIKey:   "aura",
	}); !errors.Is(err, protectErr) {
		t.Fatalf("Save() error = %v, want %v", err, protectErr)
	}

	unprotectErr := errors.New("unprotect failed")
	loadPath := filepath.Join(t.TempDir(), "load.json")
	if err := os.WriteFile(loadPath, []byte(`{
		"version": 1,
		"partner_id": "p1",
		"protected_device_secret": "Y2lwaGVyOmRldmljZQ==",
		"protected_aura_key": "Y2lwaGVyOmF1cmE="
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCredentialStore(loadPath, &fakeProtector{unprotectErr: unprotectErr}).Load(); !errors.Is(err, unprotectErr) {
		t.Fatalf("Load() error = %v, want %v", err, unprotectErr)
	}
}
