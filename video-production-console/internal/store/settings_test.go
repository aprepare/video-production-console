package store

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsRepositoryAtomicallyUpdatesPublicValuesAndVersion(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSettingsRepository(db)

	version, err := repo.UpdatePublic(t.Context(), map[string]string{
		"listen_addr": "127.0.0.1:2030",
		"data_root":   `C:\video-data`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version=%d, want 1", version)
	}
	values, gotVersion, err := repo.Public(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != 1 || values["listen_addr"] != "127.0.0.1:2030" || values["data_root"] != `C:\video-data` {
		t.Fatalf("values=%v version=%d", values, gotVersion)
	}

	if _, err := repo.UpdatePublic(t.Context(), map[string]string{"settings_version": "9000", "listen_addr": "bad"}); !errors.Is(err, ErrReservedSettingKey) {
		t.Fatalf("reserved update error=%v", err)
	}
	values, gotVersion, err = repo.Public(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != 1 || values["listen_addr"] != "127.0.0.1:2030" {
		t.Fatalf("reserved update was not atomic: values=%v version=%d", values, gotVersion)
	}

	version, err = repo.UpdatePublic(t.Context(), map[string]string{"listen_addr": "0.0.0.0:2040"})
	if err != nil || version != 2 {
		t.Fatalf("second update version=%d err=%v", version, err)
	}
}

func TestSettingsRepositoryStoresOnlyBase64CiphertextWithIndependentVersion(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSettingsRepository(db)
	now := time.Date(2026, time.August, 3, 1, 2, 3, 0, time.UTC)
	ciphertext := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 250})

	version, err := repo.PutSecret(t.Context(), "grok_api_key", ciphertext, now)
	if err != nil || version != 1 {
		t.Fatalf("first PutSecret() version=%d err=%v", version, err)
	}
	version, err = repo.PutSecret(t.Context(), "grok_api_key", ciphertext, now.Add(time.Minute))
	if err != nil || version != 2 {
		t.Fatalf("second PutSecret() version=%d err=%v", version, err)
	}
	secret, err := repo.Secret(t.Context(), "grok_api_key")
	if err != nil {
		t.Fatal(err)
	}
	if secret.Ciphertext != ciphertext || secret.Version != 2 || !secret.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("secret=%+v", secret)
	}
	var stored string
	if err := db.QueryRow(`SELECT ciphertext FROM encrypted_secrets WHERE key='grok_api_key'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != ciphertext || stored == string([]byte{0, 1, 2, 250}) {
		t.Fatalf("stored ciphertext=%q", stored)
	}
	if _, err := repo.PutSecret(t.Context(), "grok_api_key", "not base64!", now); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("invalid ciphertext error=%v", err)
	}
}
