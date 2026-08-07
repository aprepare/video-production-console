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

func TestNewProjectStartsAtScript(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	accountID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "account", now, now); err != nil {
		t.Fatal(err)
	}
	projectID := uuid.NewString()
	repo := NewProjectRepository(db)
	if err := repo.CreateProject(context.Background(), domain.Project{ID: projectID, AccountID: accountID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	p, err := repo.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Untitled-"+projectID[:8] || p.Stage != domain.StageScript {
		t.Fatalf("project=%+v", p)
	}
	if err := repo.SetTopicCardPath(context.Background(), projectID, "cards/topic.png", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	p, _ = repo.GetProject(context.Background(), projectID)
	if p.TopicCardPath == nil || *p.TopicCardPath != "cards/topic.png" {
		t.Fatalf("topic path=%v", p.TopicCardPath)
	}
}

func TestMoveProjectUsesExpectedStageAndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "move.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	accountID := uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
	id := uuid.NewString()
	repo := NewProjectRepository(db)
	_ = repo.CreateProject(context.Background(), domain.Project{ID: id, AccountID: accountID, Title: "p", CreatedAt: now, UpdatedAt: now})
	readyAt := now.Add(time.Minute)
	p, err := repo.MoveProject(context.Background(), id, domain.StageScript, domain.StageReview, readyAt)
	if err != nil {
		t.Fatal(err)
	}
	original := *p.ReadyAt
	p, err = repo.MoveProject(context.Background(), id, domain.StageReview, domain.StageReview, readyAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadyAt.Equal(original) || !p.UpdatedAt.Equal(readyAt) {
		t.Fatalf("idempotent timestamps=%+v", p)
	}
	if _, err = repo.MoveProject(context.Background(), id, domain.StageScript, domain.StagePublished, time.Now()); !errors.Is(err, ErrProjectStageConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestPublishProjectRequiresReviewAndReadyFinalVideo(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "publish.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	accountID, projectID := uuid.NewString(), uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,publication_status,created_at,updated_at) VALUES(?,?,'p','review','producing',?,?)`, projectID, accountID, now, now)
	repo := NewProjectRepository(db)
	if _, err := repo.PublishProject(context.Background(), projectID, now.Add(time.Minute)); !errors.Is(err, ErrFinalVideoMissing) {
		t.Fatalf("missing final video err=%v", err)
	}
	assertProjectPublication(t, db, projectID, domain.StageReview, "producing", nil, now)
	if _, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, Type: domain.AssetFinalVideo, Path: "final.mp4", Filename: "final.mp4", MIMEType: "video/mp4", SHA256: "final"}); err != nil {
		t.Fatal(err)
	}
	publishedAt := now.Add(2 * time.Minute)
	p, err := repo.PublishProject(context.Background(), projectID, publishedAt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Stage != domain.StagePublished || p.PublishedAt == nil || !p.PublishedAt.Equal(publishedAt) {
		t.Fatalf("published=%+v", p)
	}
	assertProjectPublication(t, db, projectID, domain.StagePublished, "published", &publishedAt, publishedAt)
}

func TestPublishProjectWrongStageDoesNotPartiallyWrite(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "publish-stage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	accountID, projectID := uuid.NewString(), uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,publication_status,created_at,updated_at) VALUES(?,?,'p','mixing','producing',?,?)`, projectID, accountID, now, now)
	repo := NewProjectRepository(db)
	if _, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, Type: domain.AssetFinalVideo, Path: "final.mp4", Filename: "final.mp4", MIMEType: "video/mp4", SHA256: "final"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PublishProject(context.Background(), projectID, now.Add(time.Minute)); !errors.Is(err, ErrProjectNotInReview) {
		t.Fatalf("wrong stage err=%v", err)
	}
	assertProjectPublication(t, db, projectID, domain.StageMixing, "producing", nil, now)
}

func TestPublishProjectRejectsNonReadyCurrentFinalVideo(t *testing.T) {
	for _, currentState := range []domain.AssetState{domain.AssetStale, domain.AssetFailed} {
		t.Run(string(currentState), func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "publish-current.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Date(2026, 8, 7, 11, 0, 0, 0, time.UTC)
			accountID, projectID := uuid.NewString(), uuid.NewString()
			_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
			_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,publication_status,created_at,updated_at) VALUES(?,?,'p','review','producing',?,?)`, projectID, accountID, now, now)
			repo := NewProjectRepository(db)
			if _, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, Type: domain.AssetFinalVideo, Path: "ready.mp4", Filename: "ready.mp4", MIMEType: "video/mp4", SHA256: "ready"}); err != nil {
				t.Fatal(err)
			}
			current, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &projectID, Type: domain.AssetFinalVideo, Path: "current.mp4", Filename: "current.mp4", MIMEType: "video/mp4", SHA256: "current"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE asset_versions SET state=? WHERE id=?`, currentState, current.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.PublishProject(context.Background(), projectID, now.Add(time.Minute)); !errors.Is(err, ErrFinalVideoMissing) {
				t.Fatalf("current %s err=%v", currentState, err)
			}
			assertProjectPublication(t, db, projectID, domain.StageReview, "producing", nil, now)
		})
	}
}

func assertProjectPublication(t *testing.T, db *sql.DB, id string, wantStage domain.ProjectStage, wantStatus string, wantPublished *time.Time, wantUpdated time.Time) {
	t.Helper()
	var stage domain.ProjectStage
	var status string
	var publishedAt *time.Time
	var updatedAt time.Time
	if err := db.QueryRow(`SELECT stage,publication_status,published_at,updated_at FROM projects WHERE id=?`, id).Scan(&stage, &status, &publishedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if stage != wantStage || status != wantStatus || (publishedAt == nil) != (wantPublished == nil) || (publishedAt != nil && !publishedAt.Equal(*wantPublished)) || !updatedAt.Equal(wantUpdated) {
		t.Fatalf("publication stage=%s status=%s published=%v updated=%v", stage, status, publishedAt, updatedAt)
	}
}

func TestSyncStageFromAssets(t *testing.T) {
	tests := []struct {
		name   string
		start  domain.ProjectStage
		assets []domain.AssetType
		want   domain.ProjectStage
	}{
		{name: "empty", want: domain.StageScript},
		{name: "topic card only", assets: []domain.AssetType{domain.AssetTopicCard}, want: domain.StageScript},
		{name: "continuous script", assets: []domain.AssetType{domain.AssetContinuousScript}, want: domain.StageAssets},
		{name: "narration and subtitles", assets: []domain.AssetType{domain.AssetNarration, domain.AssetSubtitleSRT}, want: domain.StageMixing},
		{name: "mix draft", assets: []domain.AssetType{domain.AssetMixDraft}, want: domain.StageReview},
		{name: "final video only", assets: []domain.AssetType{domain.AssetFinalVideo}, want: domain.StageReview},
		{name: "spoken script only", assets: []domain.AssetType{domain.AssetSpokenScript}, want: domain.StageScript},
		{name: "review does not regress", start: domain.StageReview, want: domain.StageReview},
		{name: "published is terminal for sync", start: domain.StagePublished, want: domain.StagePublished},
		{name: "archived is terminal for sync", start: domain.StageArchived, want: domain.StageArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "sync.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Now().UTC()
			aid, pid := uuid.NewString(), uuid.NewString()
			if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now); err != nil {
				t.Fatal(err)
			}
			repo := NewProjectRepository(db)
			if err := repo.CreateProject(context.Background(), domain.Project{ID: pid, AccountID: aid, Title: "p", Stage: domain.StageScript, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if tt.start != "" {
				if _, err := db.Exec(`UPDATE projects SET stage=? WHERE id=?`, tt.start, pid); err != nil {
					t.Fatal(err)
				}
			}
			for _, typ := range tt.assets {
				if typ == domain.AssetSpokenScript {
					itemID, versionID := uuid.NewString(), uuid.NewString()
					if _, err := db.Exec(`INSERT INTO asset_items(id,project_id,account_id,type,created_at,updated_at) VALUES(?,?,?,?,?,?)`, itemID, pid, aid, typ, now, now); err != nil {
						t.Fatalf("insert historical %s item: %v", typ, err)
					}
					if _, err := db.Exec(`INSERT INTO asset_versions(id,asset_id,project_id,account_id,type,version,storage_kind,path,filename,mime_type,size,sha256,state,created_at) VALUES(?,?,?,?,?,1,'file',?,?,?,0,?,'ready',?)`, versionID, itemID, pid, aid, typ, string(typ), string(typ), "application/octet-stream", string(typ), now); err != nil {
						t.Fatalf("insert historical %s version: %v", typ, err)
					}
					if _, err := db.Exec(`UPDATE asset_items SET current_version_id=? WHERE id=?`, versionID, itemID); err != nil {
						t.Fatalf("point historical %s item: %v", typ, err)
					}
					continue
				}
				if _, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &pid, AccountID: aid, Type: typ, Path: string(typ), Filename: string(typ), MIMEType: "application/octet-stream", SHA256: string(typ)}); err != nil {
					t.Fatalf("add %s: %v", typ, err)
				}
			}
			got, err := repo.SyncStageFromAssets(context.Background(), pid, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if got.Stage != tt.want {
				t.Fatalf("stage=%s, want %s", got.Stage, tt.want)
			}
		})
	}
}

func TestSyncStageFromAssetsSetsAndPreservesReadyAt(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "sync-ready-at.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	aid, pid := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewProjectRepository(db)
	if err := repo.CreateProject(context.Background(), domain.Project{ID: pid, AccountID: aid, Title: "p", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &pid, Type: domain.AssetMixDraft, Path: "draft", Filename: "draft", MIMEType: "video/mp4", SHA256: "draft"}); err != nil {
		t.Fatal(err)
	}
	firstReview := now.Add(time.Minute)
	got, err := repo.SyncStageFromAssets(context.Background(), pid, firstReview)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadyAt == nil || !got.ReadyAt.Equal(firstReview) {
		t.Fatalf("first review ready_at=%v, want %v", got.ReadyAt, firstReview)
	}

	originalReadyAt := now.Add(-time.Hour)
	if _, err := db.Exec(`UPDATE projects SET stage='mixing',ready_at=? WHERE id=?`, originalReadyAt, pid); err != nil {
		t.Fatal(err)
	}
	got, err = repo.SyncStageFromAssets(context.Background(), pid, firstReview.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadyAt == nil || !got.ReadyAt.Equal(originalReadyAt) {
		t.Fatalf("existing ready_at=%v, want %v", got.ReadyAt, originalReadyAt)
	}
}

func TestSyncStageFromAssetsDoesNotOverwriteConcurrentArchivedStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync-cas.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	now := time.Now().UTC()
	aid, pid := uuid.NewString(), uuid.NewString()
	if _, err := db1.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now); err != nil {
		t.Fatal(err)
	}
	repo := NewProjectRepository(db1)
	if err := repo.CreateProject(context.Background(), domain.Project{ID: pid, AccountID: aid, Title: "p", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.assets.AddVersion(context.Background(), AddAssetVersion{ProjectID: &pid, Type: domain.AssetContinuousScript, Path: "script", Filename: "script", MIMEType: "text/plain", SHA256: "script"}); err != nil {
		t.Fatal(err)
	}

	conn, err := db2.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	result := make(chan error, 1)
	go func() {
		_, syncErr := repo.SyncStageFromAssets(context.Background(), pid, now.Add(time.Minute))
		result <- syncErr
	}()
	// The second connection holds the writer lock, allowing Sync to observe the
	// old stage and then wait at its write before the terminal transition commits.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if db1.Stats().InUse > 0 {
			time.Sleep(50 * time.Millisecond)
			if db1.Stats().InUse > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("SyncStageFromAssets did not reach its blocked write")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := conn.ExecContext(context.Background(), `UPDATE projects SET stage='archived' WHERE id=?`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `COMMIT`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SyncStageFromAssets did not finish after the concurrent commit")
	}
	got, err := repo.GetProject(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != domain.StageArchived {
		t.Fatalf("stage=%s, want archived", got.Stage)
	}
}

func TestAddAssetAllocatesUniqueVersionsAcrossDatabaseHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	now := time.Now().UTC()
	aid, pid := uuid.NewString(), uuid.NewString()
	_, _ = db1.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now)
	_, _ = db1.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, pid, aid, now, now)
	repos := []*ProjectRepository{NewProjectRepository(db1), NewProjectRepository(db2)}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			a := domain.Asset{ID: uuid.NewString(), ProjectID: &pid, Type: domain.AssetAudio, Path: fmt.Sprintf("v%d.mp3", i), Filename: "v.mp3", MIMEType: "audio/mpeg", Size: 1, SHA256: "x", CreatedAt: now}
			_, err := repos[i].AddAsset(context.Background(), &a)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db1.Query(`SELECT version FROM asset_versions WHERE project_id=? ORDER BY version`, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		_ = rows.Scan(&v)
		versions = append(versions, v)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("versions=%v", versions)
	}
}

func TestProjectRepositoryWritesAndReadsOnlyV2Assets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "project-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	aid, pid := uuid.NewString(), uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now)
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, pid, aid, now, now)
	repo := NewProjectRepository(db)
	a := domain.Asset{ID: uuid.NewString(), ProjectID: &pid, AccountID: uuid.NewString(), Type: domain.AssetNarration, Path: "voice.mp3", Filename: "voice.mp3", MIMEType: "audio/mpeg", Size: 1, SHA256: "hash", CreatedAt: now}
	state, err := repo.AddAsset(context.Background(), &a)
	if err != nil || state != CommitCommitted {
		t.Fatalf("state=%v err=%v", state, err)
	}
	if a.AccountID != aid || a.Version != 1 {
		t.Fatalf("asset=%#v", a)
	}
	var legacy int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assets WHERE project_id=?`, pid).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("legacy rows=%d", legacy)
	}
	assets, err := repo.ListAssets(context.Background(), pid)
	if err != nil || len(assets) != 1 || assets[0].ID != a.ID {
		t.Fatalf("assets=%#v err=%v", assets, err)
	}
	got, err := repo.GetAsset(context.Background(), a.ID)
	if err != nil || got.Path != a.Path {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
