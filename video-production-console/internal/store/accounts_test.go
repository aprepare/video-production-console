package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestAccountOverridesRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "accounts-overrides.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	accountID := uuid.NewString()
	repo := NewAccountRepository(db)
	state, err := repo.CreateWithBackground(ctx, domain.Account{ID: accountID, Name: "覆盖账号", Color: "#fff", CreatedAt: now, UpdatedAt: now}, NewBackground{ID: uuid.NewString(), Path: "bg.png", Filename: "bg.png", MIMEType: "image/png", Size: 1, SHA256: "one"})
	if err != nil || state != CommitCommitted {
		t.Fatalf("create state=%v err=%v", state, err)
	}

	created, err := repo.Get(ctx, accountID)
	if err != nil || created.Overrides != nil {
		t.Fatalf("new account should have nil overrides, got %+v err=%v", created.Overrides, err)
	}

	style := domain.MontageStyle{CaptionFont: "俪金黑", KeywordColor: "#00AAFF", KeywordsHidden: true, BGMID: "track-9"}
	overrides := &domain.AccountOverrides{
		MontageStyle: &style,
		Voice:        &domain.VoiceOverride{AuraSTDVoiceID: "voice-ju-zhong-guan", VolcSpeechSpeakerID: "S_abc123"},
	}
	updated, err := repo.UpdateOverrides(ctx, accountID, overrides, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Overrides == nil || updated.Overrides.MontageStyle == nil || updated.Overrides.Voice == nil {
		t.Fatalf("overrides not persisted: %+v", updated.Overrides)
	}
	if updated.Overrides.MontageStyle.CaptionFont != "俪金黑" || !updated.Overrides.MontageStyle.KeywordsHidden ||
		updated.Overrides.MontageStyle.BGMID != "track-9" {
		t.Fatalf("montage style mismatch: %+v", *updated.Overrides.MontageStyle)
	}
	if updated.Overrides.Voice.AuraSTDVoiceID != "voice-ju-zhong-guan" || updated.Overrides.Voice.VolcSpeechSpeakerID != "S_abc123" {
		t.Fatalf("voice mismatch: %+v", *updated.Overrides.Voice)
	}

	listed, err := repo.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].Overrides == nil {
		t.Fatalf("list should carry overrides, got %+v err=%v", listed, err)
	}

	cleared, err := repo.UpdateOverrides(ctx, accountID, &domain.AccountOverrides{}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Overrides != nil {
		t.Fatalf("empty overrides should clear to nil, got %+v", cleared.Overrides)
	}

	if _, err := repo.UpdateOverrides(ctx, uuid.NewString(), overrides, now); err != ErrAccountNotFound {
		t.Fatalf("missing account should return ErrAccountNotFound, got %v", err)
	}
}

func TestAccountBackgroundUsesStableLogicalAssetAndReplacementInvalidatesDependents(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	accountID := uuid.NewString()
	repo := NewAccountRepository(db)
	state, err := repo.CreateWithBackground(ctx, domain.Account{ID: accountID, Name: "account", Color: "#fff", CreatedAt: now, UpdatedAt: now}, NewBackground{ID: uuid.NewString(), Path: "background-v1.png", Filename: "background.png", MIMEType: "image/png", Size: 1, SHA256: "one"})
	if err != nil || state != CommitCommitted {
		t.Fatalf("create state=%v err=%v", state, err)
	}
	var logicalID, v1 string
	if err := db.QueryRow(`SELECT background_asset_item_id FROM accounts WHERE id=?`, accountID).Scan(&logicalID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT current_version_id FROM asset_items WHERE id=?`, logicalID).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if logicalID == "" || v1 == "" || logicalID == v1 {
		t.Fatalf("logical=%q version=%q", logicalID, v1)
	}

	projectID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, projectID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	assets := NewAssetRepository(db)
	connected := addTestVersion(t, assets, projectID, accountID, domain.AssetMixDraft, "", nil, v1)
	otherProject := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'other','script',?,?)`, otherProject, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	unconnected := addTestVersion(t, assets, otherProject, accountID, domain.AssetMixDraft, "", nil)

	account, state, err := repo.ReplaceBackground(ctx, accountID, NewBackground{ID: uuid.NewString(), Path: "background-v2.png", Filename: "background.png", MIMEType: "image/png", Size: 2, SHA256: "two"}, now.Add(time.Minute))
	if err != nil || state != CommitCommitted {
		t.Fatalf("replace state=%v err=%v", state, err)
	}
	if account.BackgroundAssetID == nil || *account.BackgroundAssetID != logicalID {
		t.Fatalf("account=%#v logical=%s", account, logicalID)
	}
	var current string
	var count int
	if err := db.QueryRow(`SELECT current_version_id FROM asset_items WHERE id=?`, logicalID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE asset_id=?`, logicalID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if current == v1 || count != 2 {
		t.Fatalf("current=%s v1=%s versions=%d", current, v1, count)
	}
	for _, tt := range []struct {
		id   string
		want domain.AssetState
	}{{connected.ID, domain.AssetStale}, {unconnected.ID, domain.AssetReady}} {
		got, err := assets.Version(ctx, tt.id)
		if err != nil || got.State != tt.want {
			t.Fatalf("version=%s state=%s want=%s err=%v", tt.id, got.State, tt.want, err)
		}
	}
	var legacy int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assets WHERE account_id=? AND type='account_background'`, accountID).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("new account wrote %d legacy backgrounds", legacy)
	}
}
