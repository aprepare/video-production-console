package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func assetFixture(t *testing.T) (*AssetRepository, string, string) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "assets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','topic',?,?)`, projectID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	return NewAssetRepository(db), accountID, projectID
}

func addTestVersion(t *testing.T, r *AssetRepository, projectID, accountID string, typ domain.AssetType, logical string, parent *string, deps ...string) domain.AssetVersion {
	t.Helper()
	v, err := r.AddVersion(context.Background(), AddAssetVersion{LogicalAssetID: logical, ProjectID: &projectID, AccountID: accountID, Type: typ, StorageKind: domain.StorageFile, Path: fmt.Sprintf("%s-%s", typ, uuid.NewString()), Filename: "asset", MIMEType: "application/octet-stream", SHA256: uuid.NewString(), Size: 1, ParentVersionID: parent, Dependencies: deps})
	if err != nil {
		t.Fatalf("AddVersion(%s): %v", typ, err)
	}
	return v
}

func TestAssetVersionLifecycle(t *testing.T) {
	r, accountID, projectID := assetFixture(t)
	first := addTestVersion(t, r, projectID, accountID, domain.AssetContinuousScript, "", nil)
	if first.Version != 1 || first.State != domain.AssetReady || first.AssetID == "" {
		t.Fatalf("first=%#v", first)
	}
	second := addTestVersion(t, r, projectID, uuid.NewString(), domain.AssetContinuousScript, first.AssetID, &first.ID)
	if second.Version != 2 || second.AssetID != first.AssetID || second.AccountID != accountID {
		t.Fatalf("second=%#v", second)
	}
	current, err := r.CurrentByProject(context.Background(), projectID)
	if err != nil || len(current) != 1 || current[0].ID != second.ID {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	history, err := r.History(context.Background(), first.AssetID)
	if err != nil || len(history) != 2 || history[0].ID != second.ID || history[1].ID != first.ID {
		t.Fatalf("history=%#v err=%v", history, err)
	}
}

func TestAssetVersionConcurrentAppendUsesUniqueContinuousVersions(t *testing.T) {
	r, accountID, projectID := assetFixture(t)
	first := addTestVersion(t, r, projectID, accountID, domain.AssetContinuousScript, "", nil)
	const count = 8
	start := make(chan struct{})
	errCh := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := r.AddVersion(context.Background(), AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: &projectID, AccountID: accountID, Type: first.Type, Path: uuid.NewString(), Filename: "x", MIMEType: "text/plain", SHA256: uuid.NewString()})
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := r.History(context.Background(), first.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != count+1 {
		t.Fatalf("history len=%d", len(history))
	}
	for i, v := range history {
		if v.Version != count+1-i {
			t.Fatalf("versions=%#v", history)
		}
	}
}

func TestAssetInvalidationTraversesOnlyRealDependencyEdges(t *testing.T) {
	r, accountID, projectID := assetFixture(t)
	a1 := addTestVersion(t, r, projectID, accountID, domain.AssetContinuousScript, "", nil)
	b1 := addTestVersion(t, r, projectID, accountID, domain.AssetSpokenScript, "", nil, a1.ID)
	c1 := addTestVersion(t, r, projectID, accountID, domain.AssetFinalVideo, "", nil, b1.ID)
	otherProject := uuid.NewString()
	now := time.Now().UTC()
	if _, err := r.db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'other','topic',?,?)`, otherProject, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	d1 := addTestVersion(t, r, otherProject, accountID, domain.AssetFinalVideo, "", nil)
	// Move B's pointer while C remains connected through historical B1.
	b2 := addTestVersion(t, r, projectID, accountID, domain.AssetSpokenScript, b1.AssetID, &b1.ID, a1.ID)
	a2 := addTestVersion(t, r, projectID, accountID, domain.AssetContinuousScript, a1.AssetID, &a1.ID)
	_ = a2
	for _, tt := range []struct {
		name string
		id   string
		want domain.AssetState
	}{{"historical b1", b1.ID, domain.AssetReady}, {"current b2", b2.ID, domain.AssetStale}, {"current c1", c1.ID, domain.AssetStale}, {"unconnected d1", d1.ID, domain.AssetReady}} {
		got, err := r.Version(context.Background(), tt.id)
		if err != nil || got.State != tt.want {
			t.Fatalf("%s version %s state=%s want=%s err=%v", tt.name, tt.id, got.State, tt.want, err)
		}
	}
}

func TestAssetVersionRejectsInvalidIDsParentsAndCrossProjectDependencies(t *testing.T) {
	r, accountID, projectID := assetFixture(t)
	upstream := addTestVersion(t, r, projectID, accountID, domain.AssetContinuousScript, "", nil)
	if _, err := r.AddVersion(context.Background(), AddAssetVersion{ProjectID: ptr("bad"), AccountID: accountID, Type: domain.AssetSpokenScript, Path: "x", Filename: "x", MIMEType: "x", SHA256: "x"}); !errors.Is(err, ErrInvalidAssetInput) {
		t.Fatalf("invalid project error=%v", err)
	}
	badParent := uuid.NewString()
	if _, err := r.AddVersion(context.Background(), AddAssetVersion{LogicalAssetID: upstream.AssetID, ProjectID: &projectID, AccountID: accountID, Type: upstream.Type, Path: "x", Filename: "x", MIMEType: "x", SHA256: "x", ParentVersionID: &badParent}); !errors.Is(err, ErrInvalidAssetParent) {
		t.Fatalf("parent error=%v", err)
	}

	db := r.db
	now := time.Now().UTC()
	otherProject := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'other','topic',?,?)`, otherProject, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddVersion(context.Background(), AddAssetVersion{ProjectID: &otherProject, AccountID: accountID, Type: domain.AssetSpokenScript, Path: "x", Filename: "x", MIMEType: "x", SHA256: "x", Dependencies: []string{upstream.ID}}); !errors.Is(err, ErrInvalidAssetDependency) {
		t.Fatalf("cross-project error=%v", err)
	}
}

func TestAssetVersionRejectsDirectoryAsFinalVideo(t *testing.T) {
	r, accountID, projectID := assetFixture(t)
	_, err := r.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetFinalVideo, StorageKind: domain.StorageDirectory, Path: "render-project", Filename: "render-project", MIMEType: "video/mp4", SHA256: "hash"})
	if !errors.Is(err, ErrInvalidAssetInput) {
		t.Fatalf("directory final video error=%v", err)
	}
}

func TestProjectRepositoryPropagatesUnknownCommitOutcome(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "commit-unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	accountID, projectID := uuid.NewString(), uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','topic',?,?)`, projectID, accountID, now, now)
	repo := NewProjectRepository(db)
	repo.assets.commit = func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return err
		}
		return errors.New("lost commit acknowledgement")
	}
	asset := domain.Asset{ProjectID: &projectID, Type: domain.AssetContinuousScript, Path: "script.txt", Filename: "script.txt", MIMEType: "text/plain", SHA256: "hash", CreatedAt: now}
	state, err := repo.AddAsset(context.Background(), &asset)
	if state != CommitUnknown || err == nil {
		t.Fatalf("state=%v err=%v", state, err)
	}
	var outcome *CommitOutcomeError
	if !errors.As(err, &outcome) || outcome.Outcome != CommitUnknown {
		t.Fatalf("error=%T %v", err, err)
	}
	var count int
	if scanErr := db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE project_id=?`, projectID).Scan(&count); scanErr != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, scanErr)
	}
}

func ptr(s string) *string { return &s }
