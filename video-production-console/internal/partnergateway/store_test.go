package partnergateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenStoreCreatesIndependentWALSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode=%q", mode)
	}

	partner := Partner{ID: "p1", DisplayName: "天中观局", KeyPrefix: "vpc_abcd", KeyHash: []byte("hash"), Status: PartnerActive, SessionVersion: 1}
	if err := store.CreatePartner(context.Background(), partner); err != nil {
		t.Fatal(err)
	}
	got, err := store.PartnerByID(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "天中观局" || string(got.KeyHash) != "hash" {
		t.Fatalf("got=%+v", got)
	}

	rows, err := store.db.Query(`PRAGMA table_info(partners)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "activation_key" || name == "device_secret" || name == "session_token" {
			t.Fatalf("plaintext column %q", name)
		}
	}
}

func TestNullPrimaryKeysRejected(t *testing.T) {
	t.Run("partners.id", func(t *testing.T) {
		store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })

		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = store.db.Exec(`
			INSERT INTO partners (
				id, display_name, key_prefix, key_hash, status, device_hash,
				session_version, text_calls, image_calls, verify_failures,
				rate_limited, created_at, updated_at, last_verified_at
			) VALUES (NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			"null-id partner", "vpc_null", []byte("hash"), PartnerActive, "device-hash",
			1, 0, 0, 0, 0, now, now, now,
		)
		if err == nil {
			t.Fatal("expected NULL partners.id to be rejected")
		}
	})

	t.Run("sessions.token_hash", func(t *testing.T) {
		store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })

		partner := Partner{
			ID:             "p1",
			DisplayName:    "session partner",
			KeyPrefix:      "vpc_session",
			KeyHash:        []byte("hash"),
			Status:         PartnerActive,
			SessionVersion: 1,
		}
		if err := store.CreatePartner(context.Background(), partner); err != nil {
			t.Fatal(err)
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = store.db.Exec(`
			INSERT INTO sessions (token_hash, partner_id, session_version, expires_at, created_at)
			VALUES (NULL, ?, ?, ?, ?)
		`, partner.ID, partner.SessionVersion, now, now)
		if err == nil {
			t.Fatal("expected NULL sessions.token_hash to be rejected")
		}
	})
}

func TestCreatePartnerRejectsDuplicateDisplayNameAndKeyPrefix(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	createdAt := time.Date(2026, time.August, 19, 1, 2, 3, 456789, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	lastVerifiedAt := createdAt.Add(2 * time.Minute)
	partner := Partner{
		ID:               "p1",
		DisplayName:      "天中观局",
		KeyPrefix:        "vpc_abcd",
		KeyHash:          []byte("key-hash"),
		Status:           PartnerDisabled,
		DeviceHash:       "device-hash",
		DeviceSecretHash: []byte("device-secret-hash"),
		SessionVersion:   7,
		TextCalls:        11,
		ImageCalls:       12,
		VerifyFailures:   13,
		RateLimited:      14,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		LastVerifiedAt:   lastVerifiedAt,
	}
	if err := store.CreatePartner(context.Background(), partner); err != nil {
		t.Fatal(err)
	}

	duplicateDisplayName := partner
	duplicateDisplayName.ID = "p2"
	duplicateDisplayName.KeyPrefix = "vpc_efgh"
	if err := store.CreatePartner(context.Background(), duplicateDisplayName); err == nil {
		t.Fatal("expected duplicate display name to fail")
	}

	duplicateKeyPrefix := partner
	duplicateKeyPrefix.ID = "p3"
	duplicateKeyPrefix.DisplayName = "另一伙伴"
	if err := store.CreatePartner(context.Background(), duplicateKeyPrefix); err == nil {
		t.Fatal("expected duplicate key prefix to fail")
	}

	got, err := store.PartnerByID(context.Background(), partner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != partner.ID ||
		got.DisplayName != partner.DisplayName ||
		got.KeyPrefix != partner.KeyPrefix ||
		string(got.KeyHash) != string(partner.KeyHash) ||
		got.Status != partner.Status ||
		got.DeviceHash != partner.DeviceHash ||
		string(got.DeviceSecretHash) != string(partner.DeviceSecretHash) ||
		got.SessionVersion != partner.SessionVersion ||
		got.TextCalls != partner.TextCalls ||
		got.ImageCalls != partner.ImageCalls ||
		got.VerifyFailures != partner.VerifyFailures ||
		got.RateLimited != partner.RateLimited ||
		!got.CreatedAt.Equal(partner.CreatedAt) ||
		!got.UpdatedAt.Equal(partner.UpdatedAt) ||
		!got.LastVerifiedAt.Equal(partner.LastVerifiedAt) {
		t.Fatalf("got=%+v want=%+v", got, partner)
	}
}

func TestSessionCascadeDeletesSessionsWithPartner(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	partner := Partner{ID: "p1", DisplayName: "天中观局", KeyPrefix: "vpc_abcd", KeyHash: []byte("hash"), Status: PartnerActive, SessionVersion: 1}
	if err := store.CreatePartner(context.Background(), partner); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`
		INSERT INTO sessions (token_hash, partner_id, session_version, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, []byte("token-hash"), partner.ID, partner.SessionVersion, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM partners WHERE id = ?`, partner.ID); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE partner_id = ?`, partner.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("session count=%d", count)
	}
}
