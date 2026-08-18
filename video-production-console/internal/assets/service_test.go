package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

const testAccountID = "b605e068-6ada-4e34-a966-dc730746d49f"

func TestSaveAccountBackgroundUsesDetectedPNGAndSHA256(t *testing.T) {
	root := t.TempDir()
	data := encodeImage(t, "png")
	got, err := NewService(root).SaveAccountBackground(testAccountID, "misleading.jpg", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveAccountBackground() error = %v", err)
	}
	if got.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", got.MIMEType)
	}
	if got.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", got.Size, len(data))
	}
	wantHash := sha256.Sum256(data)
	if got.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("SHA256 = %q, want %q", got.SHA256, hex.EncodeToString(wantHash[:]))
	}
	if filepath.Ext(got.Path) != ".png" {
		t.Errorf("extension = %q, want .png", filepath.Ext(got.Path))
	}
	wantDir := filepath.Join(root, "accounts", testAccountID, "background")
	if filepath.Dir(got.Path) != wantDir {
		t.Errorf("directory = %q, want %q", filepath.Dir(got.Path), wantDir)
	}
	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(stored, data) {
		t.Error("stored file differs from upload")
	}
}

func TestSaveAccountBackgroundAcceptsJPEG(t *testing.T) {
	data := encodeImage(t, "jpeg")
	got, err := NewService(t.TempDir()).SaveAccountBackground(testAccountID, "background.png", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveAccountBackground() error = %v", err)
	}
	if got.MIMEType != "image/jpeg" || filepath.Ext(got.Path) != ".jpg" {
		t.Errorf("saved as MIME %q path %q, want image/jpeg and .jpg", got.MIMEType, got.Path)
	}
}

func TestSaveAccountBackgroundRejectsInvalidInputsWithoutArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		data      []byte
	}{
		{name: "text disguised as PNG", accountID: testAccountID, data: []byte("this is not a PNG")},
		{name: "malformed WebP header", accountID: testAccountID, data: malformedWebP()},
		{name: "path traversal account ID", accountID: "../outside", data: encodeImage(t, "png")},
		{name: "oversized", accountID: testAccountID, data: bytes.Repeat([]byte{'x'}, int(MaxBackgroundSize+1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := NewService(root).SaveAccountBackground(tt.accountID, "background.png", bytes.NewReader(tt.data))
			if err == nil {
				t.Fatal("SaveAccountBackground() succeeded, want error")
			}
			if files := allFiles(t, root); len(files) != 0 {
				t.Fatalf("files remain after failed upload: %v", files)
			}
		})
	}
}

func TestSaveAccountBackgroundRejectsTruncatedAndOversizedDimensions(t *testing.T) {
	valid := encodeImage(t, "png")
	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{name: "truncated after header", data: valid[:len(valid)-8], wantErr: ErrInvalidImage},
		{name: "width over limit", data: encodeSizedPNG(t, 8193, 1), wantErr: ErrImageDimensions},
		{name: "pixel count over limit", data: encodeSizedPNG(t, 7000, 7000), wantErr: ErrImageDimensions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := NewService(root).SaveAccountBackground(testAccountID, "image.png", bytes.NewReader(tt.data))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if files := allFiles(t, root); len(files) != 0 {
				t.Fatalf("files remain after rejected image: %v", files)
			}
		})
	}
}

func TestReconcileAccountBackgroundsRemovesOnlyOrphansAndTemps(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts (id, name, color, status, created_at, updated_at)
        VALUES (?, 'account', '#fff', 'active', ?, ?)`, testAccountID, now, now); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	directory := filepath.Join(root, "accounts", testAccountID, "background")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("mkdir background: %v", err)
	}
	referencedOld := filepath.Join(directory, "old.png")
	referencedCurrent := filepath.Join(directory, "current.png")
	orphan := filepath.Join(directory, "orphan.png")
	temporary := filepath.Join(directory, ".upload-crash")
	outside := filepath.Join(root, "outside.png")
	for _, path := range []string{referencedOld, referencedCurrent, orphan, temporary, outside} {
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatalf("write fixture %q: %v", path, err)
		}
	}
	repo := store.NewAssetRepository(db)
	first, err := repo.AddVersion(context.Background(), store.AddAssetVersion{AccountID: testAccountID, Type: domain.AssetAccountBackground, Path: referencedOld, Filename: "file.png", MIMEType: "image/png", Size: 1, SHA256: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddVersion(context.Background(), store.AddAssetVersion{LogicalAssetID: first.AssetID, AccountID: testAccountID, Type: domain.AssetAccountBackground, Path: referencedCurrent, Filename: "file.png", MIMEType: "image/png", Size: 1, SHA256: "current", ParentVersionID: &first.ID}); err != nil {
		t.Fatal(err)
	}
	if err := NewService(root).ReconcileAccountBackgrounds(context.Background(), db, nil); err != nil {
		t.Fatalf("ReconcileAccountBackgrounds() error = %v", err)
	}
	for _, path := range []string{referencedOld, referencedCurrent, outside} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("preserved file %q: %v", path, err)
		}
	}
	for _, path := range []string{orphan, temporary} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("orphan %q still exists, stat error = %v", path, err)
		}
	}
}

func TestReconcileAccountBackgroundsReportsInvalidAccountsRoot(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := os.WriteFile(filepath.Join(root, "accounts"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write invalid accounts root: %v", err)
	}
	if err := NewService(root).ReconcileAccountBackgrounds(context.Background(), db, nil); err == nil {
		t.Fatal("ReconcileAccountBackgrounds() succeeded for non-directory accounts root")
	}
}

func TestReconcileProjectAssetsKeepsAllVersionsAndRemovesOrphans(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	aid := uuid.NewString()
	pid := uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now)
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, pid, aid, now, now)
	dir := filepath.Join(root, "projects", pid, "audio")
	_ = os.MkdirAll(dir, 0755)
	keep1 := filepath.Join(dir, "v1.mp3")
	keep2 := filepath.Join(dir, "v2.mp3")
	orphan := filepath.Join(dir, "orphan.mp3")
	temp := filepath.Join(dir, ".upload-x")
	for _, p := range []string{keep1, keep2, orphan, temp} {
		_ = os.WriteFile(p, []byte("x"), 0600)
	}
	repo := store.NewAssetRepository(db)
	first, err := repo.AddVersion(context.Background(), store.AddAssetVersion{ProjectID: &pid, AccountID: aid, Type: domain.AssetNarration, Path: keep1, Filename: "x.mp3", MIMEType: "audio/mpeg", Size: 1, SHA256: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddVersion(context.Background(), store.AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: &pid, AccountID: aid, Type: domain.AssetNarration, Path: keep2, Filename: "x.mp3", MIMEType: "audio/mpeg", Size: 1, SHA256: "two", ParentVersionID: &first.ID}); err != nil {
		t.Fatal(err)
	}
	if err := NewService(root).ReconcileProjectAssets(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{keep1, keep2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("removed referenced %s", p)
		}
	}
	for _, p := range []string{orphan, temp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("kept orphan %s", p)
		}
	}
}

func TestReconcileProjectAssetsPreservesRegisteredTaskDirectories(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	accountID, taskID, orphanRootID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now); err != nil {
		t.Fatal(err)
	}
	task := domain.CodexTask{
		ID: taskID, AccountID: accountID, Type: "topic_select", SkillName: "finance-topic-selector",
		Action: domain.ActionTopicBrainstorm, Status: domain.TaskCompleted, PromptSnapshot: "topic", CreatedAt: now,
	}
	if err := store.NewTaskRepository(db).CreateV2(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "projects", taskID, "tasks", taskID, "output", "topic_candidates.json")
	orphan := filepath.Join(root, "projects", orphanRootID, "tasks", orphanRootID, "output", "orphan.json")
	for _, path := range []string{keep, orphan} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := NewService(root).ReconcileProjectAssets(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("registered task output removed: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("unregistered task-like file remains: %v", err)
	}
}

func TestReconcileProjectAssetsProtectsOnlyRegisteredDirectorySubtree(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	accountID, projectID, otherProjectID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{projectID, otherProjectID} {
		if _, err := db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, id, accountID, now, now); err != nil {
			t.Fatal(err)
		}
	}
	managedRoot := filepath.Join(root, "projects")
	registered := filepath.Join(managedRoot, projectID, string(domain.AssetMixDraft), "draft")
	keep := filepath.Join(registered, "nested", "keep.dat")
	orphan := filepath.Join(managedRoot, projectID, string(domain.AssetMixDraft), "orphan.dat")
	prefixSibling := filepath.Join(managedRoot, projectID, string(domain.AssetMixDraft), "draft-evil", "remove.dat")
	otherProjectFile := filepath.Join(managedRoot, otherProjectID, string(domain.AssetMixDraft), "foreign", "remove.dat")
	for _, path := range []string{keep, orphan, prefixSibling, otherProjectFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo := store.NewAssetRepository(db)
	first, err := repo.AddVersion(ctx, store.AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: registered, Filename: "draft", MIMEType: "application/x-directory", Size: 1, SHA256: "one"})
	if err != nil {
		t.Fatal(err)
	}
	// Malicious or corrupt directory rows must not protect the managed root or another project's subtree.
	typeRoot := filepath.Join(managedRoot, projectID, string(domain.AssetMixDraft))
	if _, err := repo.AddVersion(ctx, store.AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: typeRoot, Filename: "mix_draft", MIMEType: "application/x-directory", Size: 1, SHA256: "type-root"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddVersion(ctx, store.AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: managedRoot, Filename: "projects", MIMEType: "application/x-directory", Size: 1, SHA256: "two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddVersion(ctx, store.AddAssetVersion{LogicalAssetID: first.AssetID, ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: filepath.Dir(otherProjectFile), Filename: "foreign", MIMEType: "application/x-directory", Size: 1, SHA256: "three"}); err != nil {
		t.Fatal(err)
	}

	if err := NewService(root).ReconcileProjectAssets(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("registered directory content removed: %v", err)
	}
	for _, path := range []string{orphan, prefixSibling, otherProjectFile} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unprotected file %q remains, stat=%v", path, err)
		}
	}
}

func TestReconcileProjectAssetsDoesNotProtectRegisteredSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	accountID, projectID, otherProjectID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, accountID, "a", now, now)
	for _, id := range []string{projectID, otherProjectID} {
		_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','script',?,?)`, id, accountID, now, now)
	}
	target := filepath.Join(t.TempDir(), "outside-target")
	file := filepath.Join(target, "remove.dat")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "projects", projectID, string(domain.AssetMixDraft), "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if _, err := store.NewAssetRepository(db).AddVersion(ctx, store.AddAssetVersion{ProjectID: &projectID, AccountID: accountID, Type: domain.AssetMixDraft, StorageKind: domain.StorageDirectory, Path: link, Filename: "linked", MIMEType: "application/x-directory", Size: 1, SHA256: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := NewService(root).ReconcileProjectAssets(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("registered symlink was not removed: %v", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "x" {
		t.Fatalf("outside target changed: data=%q err=%v", data, err)
	}
}

func TestSaveProjectAssetValidatesContentAndUsesControlledPath(t *testing.T) {
	root := t.TempDir()
	projectID := "f02addf5-275c-4456-a51f-3ebeb9c730ef"
	svc := NewService(root)
	text, err := svc.SaveProjectAsset(projectID, domain.AssetContinuousScript, "script.md", bytes.NewReader([]byte("# 文案\n")))
	if err != nil {
		t.Fatalf("text upload: %v", err)
	}
	if text.MIMEType != "text/markdown; charset=utf-8" {
		t.Fatalf("mime = %q", text.MIMEType)
	}
	wantDir := filepath.Join(root, "projects", projectID, string(domain.AssetContinuousScript))
	if filepath.Dir(text.Path) != wantDir {
		t.Fatalf("path = %q, want directory %q", text.Path, wantDir)
	}
	for _, filename := range []string{"source.txt", "source.md"} {
		source, err := svc.SaveProjectAsset(projectID, domain.AssetSourceScript, filename, bytes.NewReader([]byte("同行原文\n")))
		if err != nil {
			t.Fatalf("source script %q upload: %v", filename, err)
		}
		if filepath.Ext(source.Path) != filepath.Ext(filename) {
			t.Fatalf("source script path=%q, want extension %q", source.Path, filepath.Ext(filename))
		}
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetSourceScript, "source.srt", bytes.NewReader([]byte("同行原文\n"))); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("source script wrong extension error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetSourceScript, "source.txt", bytes.NewReader([]byte(" \n\t"))); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("source script blank content error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetSourceScript, "source.txt", bytes.NewReader(bytes.Repeat([]byte("x"), int(MaxTextAssetSize+1)))); !errors.Is(err, ErrProjectAssetTooBig) {
		t.Fatalf("source script oversized error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetSubtitle, "bad.srt", bytes.NewReader([]byte{0xff, 0xfe})); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetAudio, "fake.mp3", bytes.NewReader([]byte("not mp3"))); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("fake MP3 error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetFinalVideo, "video.txt", bytes.NewReader(minimalMP4())); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("wrong extension error = %v", err)
	}
}

func minimalMP4() []byte {
	return isoFile("vide")
}

func TestSaveProjectAssetParsesISOBaseMediaHandlers(t *testing.T) {
	svc := NewService(t.TempDir())
	id := uuid.NewString()
	for _, tt := range []struct {
		name     string
		typ      domain.AssetType
		filename string
		data     []byte
		ok       bool
	}{
		{"audio m4a", domain.AssetAudio, "voice.m4a", isoFile("soun"), true},
		{"video mp4", domain.AssetFinalVideo, "video.mp4", isoFile("vide"), true},
		{"av mp4", domain.AssetMixDraft, "mix.mp4", isoFile("soun", "vide"), true},
		{"video disguised m4a", domain.AssetAudio, "fake.m4a", isoFile("vide"), false},
		{"av disguised m4a", domain.AssetAudio, "fake-av.m4a", isoFile("soun", "vide"), false},
		{"audio disguised mp4", domain.AssetFinalVideo, "fake.mp4", isoFile("soun"), false},
		{"truncated box", domain.AssetFinalVideo, "bad.mp4", append(isoFile("vide"), 0, 0, 0, 20, 'm', 'o', 'o', 'v'), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved, err := svc.SaveProjectAsset(id, tt.typ, tt.filename, bytes.NewReader(tt.data))
			if tt.ok && err != nil {
				t.Fatalf("error=%v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidProjectAsset) {
				t.Fatalf("error=%v", err)
			}
			if saved.Path != "" {
				_ = os.Remove(saved.Path)
			}
		})
	}
}

func TestProjectAssetSizeLimitsByType(t *testing.T) {
	for _, tt := range []struct {
		typ  domain.AssetType
		want int64
	}{{domain.AssetSourceScript, 5 << 20}, {domain.AssetContinuousScript, 5 << 20}, {domain.AssetSubtitle, 5 << 20}, {domain.AssetAudio, 200 << 20}, {domain.AssetFinalVideo, 500 << 20}} {
		if got := MaxSizeForType(tt.typ); got != tt.want {
			t.Errorf("MaxSizeForType(%s)=%d want %d", tt.typ, got, tt.want)
		}
	}
}

func TestSaveTextVersionAndImportFilePublishAtomically(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root)
	projectID := uuid.NewString()
	saved, err := svc.SaveTextVersion(projectID, domain.AssetContinuousScript, "script.md", "hello\n")
	if err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(saved.Path); readErr != nil || string(data) != "hello\n" {
		t.Fatalf("data=%q err=%v", data, readErr)
	}
	source := filepath.Join(t.TempDir(), "voice.mp3")
	if err := os.WriteFile(source, validMP3(), 0o600); err != nil {
		t.Fatal(err)
	}
	imported, err := svc.ImportFile(projectID, domain.AssetNarration, source)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(imported.Path) != filepath.Join(root, "projects", projectID, string(domain.AssetNarration)) {
		t.Fatalf("path=%q", imported.Path)
	}
	if _, err := svc.ImportFile(projectID, domain.AssetFinalVideo, filepath.Dir(source)); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("directory import error=%v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.mp4")
	if err := os.WriteFile(bad, []byte("not-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportFile(projectID, domain.AssetFinalVideo, bad); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("bad import error=%v", err)
	}
	files := allFiles(t, filepath.Join(root, "projects", projectID, string(domain.AssetFinalVideo)))
	if len(files) != 0 {
		t.Fatalf("failed import left files=%v", files)
	}
}

func TestImportFileRejectsSymlinkSwapBeforeOpen(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root)
	projectID := uuid.NewString()
	source := filepath.Join(t.TempDir(), "voice.mp3")
	outside := filepath.Join(t.TempDir(), "outside.mp3")
	if err := os.WriteFile(source, validMP3(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, append(validMP3(), []byte("outside-secret")...), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.beforeImportOpen = func(string) {
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, source); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if _, err := svc.ImportFile(projectID, domain.AssetNarration, source); !errors.Is(err, ErrAssetPathInvalid) {
		t.Fatalf("swap error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "projects")); !os.IsNotExist(statErr) {
		t.Fatalf("swap created project storage: %v", statErr)
	}
}

func TestHashDirectoryIsDeterministicAndRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root)
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct{ root, name, data string }{{first, "z.txt", "z"}, {first, "nested/a.txt", "a"}, {second, "nested/a.txt", "a"}, {second, "z.txt", "z"}} {
		if err := os.WriteFile(filepath.Join(file.root, filepath.FromSlash(file.name)), []byte(file.data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h1, size1, err := svc.HashDirectory(first)
	if err != nil {
		t.Fatal(err)
	}
	h2, size2, err := svc.HashDirectory(second)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == "" || h1 != h2 || size1 != 2 || size2 != 2 {
		t.Fatalf("first=(%s,%d) second=(%s,%d)", h1, size1, h2, size2)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(first, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := svc.HashDirectory(first); !errors.Is(err, ErrAssetPathInvalid) {
		t.Fatalf("symlink error=%v", err)
	}
}

func TestHashDirectoryRejectsFileSymlinkSwapBeforeOpen(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root)
	directory := filepath.Join(root, "artifact")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(directory, "asset.dat")
	outside := filepath.Join(t.TempDir(), "secret.dat")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.beforeHashFileOpen = func(path string) {
		if path != inside {
			return
		}
		if err := os.Remove(inside); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, inside); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if _, _, err := svc.HashDirectory(directory); !errors.Is(err, ErrAssetPathInvalid) {
		t.Fatalf("swap error=%v", err)
	}
}

func TestAudioStructureValidation(t *testing.T) {
	svc := NewService(t.TempDir())
	id := uuid.NewString()
	for _, tt := range []struct {
		name, file string
		data       []byte
		ok         bool
	}{
		{"valid wav", "voice.wav", validWAV(), true}, {"truncated wav", "voice.wav", validWAV()[:20], false}, {"fake riff", "voice.wav", []byte("RIFF\x20\x00\x00\x00WAVE"), false},
		{"valid mp3", "voice.mp3", validMP3(), true}, {"one mp3 frame", "voice.mp3", validMP3()[:417], false}, {"fake id3", "voice.mp3", []byte("ID3\x04\x00\x00\x00\x00\x00\x00fake"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.SaveProjectAsset(id, domain.AssetAudio, tt.file, bytes.NewReader(tt.data))
			if tt.ok && err != nil {
				t.Fatal(err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidProjectAsset) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestISOBoxExtendedAndToEndSizes(t *testing.T) {
	svc := NewService(t.TempDir())
	id := uuid.NewString()
	for _, data := range [][]byte{isoFileWithTopSize("extended"), isoFileWithTopSize("to-end")} {
		if _, err := svc.SaveProjectAsset(id, domain.AssetFinalVideo, "video.mp4", bytes.NewReader(data)); err != nil {
			t.Fatalf("valid sized box: %v", err)
		}
	}
	bad := isoFile("vide")
	bad[0] = 0
	bad[1] = 0
	bad[2] = 0
	bad[3] = 1
	if _, err := svc.SaveProjectAsset(id, domain.AssetFinalVideo, "bad.mp4", bytes.NewReader(bad)); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("overflow error=%v", err)
	}
}

func validWAV() []byte {
	b := make([]byte, 46)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 38)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], 1)
	binary.LittleEndian.PutUint32(b[24:28], 8000)
	binary.LittleEndian.PutUint32(b[28:32], 8000)
	binary.LittleEndian.PutUint16(b[32:34], 1)
	binary.LittleEndian.PutUint16(b[34:36], 8)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:44], 1)
	b[44] = 128
	return b
}
func validMP3() []byte {
	frame := make([]byte, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x64})
	return append(append([]byte{}, frame...), frame...)
}
func isoFileWithTopSize(mode string) []byte {
	b := isoFile("vide")
	ftypSize := int(binary.BigEndian.Uint32(b[:4]))
	rest := b[ftypSize:]
	if mode == "extended" {
		ext := make([]byte, 16+ftypSize-8)
		binary.BigEndian.PutUint32(ext[:4], 1)
		copy(ext[4:8], "ftyp")
		binary.BigEndian.PutUint64(ext[8:16], uint64(len(ext)))
		copy(ext[16:], b[8:ftypSize])
		return append(ext, rest...)
	}
	binary.BigEndian.PutUint32(rest[:4], 0)
	return append(b[:ftypSize], rest...)
}

func isoFile(handlers ...string) []byte {
	box := func(kind string, payload []byte) []byte {
		size := 8 + len(payload)
		out := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
		out = append(out, kind...)
		return append(out, payload...)
	}
	ftyp := box("ftyp", []byte("isom\x00\x00\x00\x00isommp42"))
	var tracks []byte
	for _, handler := range handlers {
		hdlrPayload := make([]byte, 24)
		copy(hdlrPayload[8:12], handler)
		tracks = append(tracks, box("trak", box("mdia", box("hdlr", hdlrPayload)))...)
	}
	return append(ftyp, box("moov", tracks)...)
}

func malformedWebP() []byte {
	return []byte{
		'R', 'I', 'F', 'F', 12, 0, 0, 0,
		'W', 'E', 'B', 'P', 'V', 'P', '8', 'X',
		0, 0, 0, 0,
	}
}

func encodeImage(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 0xe0, G: 0x80, B: 0x20, A: 0xff})
	var buffer bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buffer, img)
	case "jpeg":
		err = jpeg.Encode(&buffer, img, nil)
	default:
		t.Fatalf("unsupported test image format %q", format)
	}
	if err != nil {
		t.Fatalf("encode test image: %v", err)
	}
	return buffer.Bytes()
}

func encodeSizedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], uint32(width))
	binary.BigEndian.PutUint32(data[4:8], uint32(height))
	data[8], data[9], data[10], data[11], data[12] = 8, 2, 0, 0, 0
	var buffer bytes.Buffer
	buffer.Write([]byte("\x89PNG\r\n\x1a\n"))
	binary.Write(&buffer, binary.BigEndian, uint32(len(data)))
	buffer.WriteString("IHDR")
	buffer.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(data)
	binary.Write(&buffer, binary.BigEndian, crc.Sum32())
	return buffer.Bytes()
}

func allFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, strings.TrimPrefix(path, root))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk data root: %v", err)
	}
	return files
}
