package skillregistry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

func TestSkillFingerprintIsDeterministicRootIndependentAndMtimeIndependent(t *testing.T) {
	service := NewService(nil)
	rootA := t.TempDir()
	rootB := t.TempDir()
	for _, root := range []string{rootA, rootB} {
		writeSkillFile(t, filepath.Join(root, "SKILL.md"), "skill body")
		writeSkillFile(t, filepath.Join(root, "references", "contract.md"), "contract")
		writeSkillFile(t, filepath.Join(root, ".git", "config"), "ignored git")
		writeSkillFile(t, filepath.Join(root, "__pycache__", "cache.pyc"), "ignored cache")
		writeSkillFile(t, filepath.Join(root, "scratch.tmp"), "ignored temporary")
	}

	first, err := service.Scan("finance-viral-remix", rootA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Scan("finance-viral-remix", rootB)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("root-dependent hashes: %s != %s", first.SHA256, second.SHA256)
	}
	wantPaths := []string{"SKILL.md", "references/contract.md"}
	gotPaths := []string{first.Files[0].Path, first.Files[1].Path}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("file paths=%v, want %v", gotPaths, wantPaths)
	}

	mtime := time.Now().Add(12 * time.Hour)
	if err := os.Chtimes(filepath.Join(rootA, "SKILL.md"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	mtimeOnly, err := service.Scan("finance-viral-remix", rootA)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != mtimeOnly.SHA256 {
		t.Fatalf("mtime changed fingerprint: %s != %s", first.SHA256, mtimeOnly.SHA256)
	}

	writeSkillFile(t, filepath.Join(rootA, "references", "contract.md"), "changed contract")
	changed, err := service.Scan("finance-viral-remix", rootA)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SHA256 == first.SHA256 {
		t.Fatal("content change did not change fingerprint")
	}
}

func TestSkillFingerprintUsesCollisionSafePathAndContentBoundaries(t *testing.T) {
	service := NewService(nil)
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeSkillFile(t, filepath.Join(rootA, "a"), "bc")
	writeSkillFile(t, filepath.Join(rootB, "ab"), "c")
	a, err := service.Scan("collision-a", rootA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Scan("collision-b", rootB)
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == b.SHA256 {
		t.Fatalf("ambiguous path/content concatenation collided: %s", a.SHA256)
	}
}

func TestSkillScanRejectsSingleFileAndAggregateSizeLimits(t *testing.T) {
	service := NewService(nil)
	single := t.TempDir()
	large := filepath.Join(single, "large.bin")
	writeSparseSkillFile(t, large, MaxSkillFileSize+1)
	if _, err := service.Scan("too-large", single); !errors.Is(err, ErrSkillFileTooLarge) {
		t.Fatalf("single-file limit error=%v", err)
	}

	aggregate := t.TempDir()
	for index := 0; index < int(MaxSkillBundleSize/MaxSkillFileSize)+1; index++ {
		writeSparseSkillFile(t, filepath.Join(aggregate, fmt.Sprintf("%02d.bin", index)), MaxSkillFileSize)
	}
	if _, err := service.Scan("too-large-total", aggregate); !errors.Is(err, ErrSkillBundleTooLarge) {
		t.Fatalf("aggregate limit error=%v", err)
	}
}

func TestSkillScanHonorsCanceledContextBeforeReading(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "SKILL.md"), "body")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewService(nil).ScanContext(ctx, "canceled", root); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanContext() error=%v, want context.Canceled", err)
	}
}

func TestOpenRegularNoFollowRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	writeSkillFile(t, target, "body")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	file, err := openRegularNoFollow(link)
	if file != nil {
		_ = file.Close()
	}
	if !errors.Is(err, ErrSkillPathEscape) {
		t.Fatalf("openRegularNoFollow() error=%v", err)
	}
}

func TestSkillScanFollowsConfiguredSymlinkRootWithoutChangingFingerprint(t *testing.T) {
	service := NewService(nil)
	target := t.TempDir()
	writeSkillFile(t, filepath.Join(target, "SKILL.md"), "skill body")
	writeSkillFile(t, filepath.Join(target, "references", "contract.md"), "contract")
	link := filepath.Join(t.TempDir(), "linked-skill")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlink creation unavailable: %v", err)
	}

	direct, err := service.Scan("finance-viral-remix", target)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := service.Scan("finance-viral-remix", link)
	if err != nil {
		t.Fatal(err)
	}
	if linked.SHA256 != direct.SHA256 || !reflect.DeepEqual(linked.Files, direct.Files) {
		t.Fatalf("symlink root fingerprint=%s files=%v, direct fingerprint=%s files=%v", linked.SHA256, linked.Files, direct.SHA256, direct.Files)
	}
}

func TestSkillScanRejectsSymlinkEscapeAndPersistsSnapshot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := NewService(store.NewSkillRepository(db))
	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "SKILL.md"), "body")
	snapshot, err := service.Scan("finance-topic-selector", root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.NewSkillRepository(db).Get(t.Context(), snapshot.ID)
	if err != nil || got.SHA256 != snapshot.SHA256 || len(got.Files) != 1 {
		t.Fatalf("persisted snapshot=%+v err=%v", got, err)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeSkillFile(t, outside, "outside")
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := service.Scan("finance-topic-selector", root); !errors.Is(err, ErrSkillPathEscape) {
		t.Fatalf("symlink escape error=%v", err)
	}
}

func TestPartnerRootsMatchDefaultProductionSkills(t *testing.T) {
	base := filepath.Join(t.TempDir(), "skills")
	got := PartnerRoots(base)
	want := DefaultRoots(base)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partner roots=%+v want %+v", got, want)
	}
}

func TestDefaultRootsRegisterProductionSkills(t *testing.T) {
	base := filepath.Join(t.TempDir(), "skills")
	got := DefaultRoots(base)
	wantNames := []string{"finance-topic-selector", "finance-viral-remix", "jianying-montage-draft", "jianying-movie-montage"}
	if len(got) != len(wantNames) {
		t.Fatalf("roots=%+v", got)
	}
	for i, want := range wantNames {
		if got[i].Name != want || got[i].Path != filepath.Join(base, want) {
			t.Fatalf("root[%d]=%+v", i, got[i])
		}
	}
}

type failingSkillRepository struct{ err error }

func (f failingSkillRepository) Save(context.Context, domain.SkillSnapshot) error { return f.err }
func (f failingSkillRepository) Get(context.Context, string) (domain.SkillSnapshot, error) {
	return domain.SkillSnapshot{}, f.err
}
func (f failingSkillRepository) Latest(context.Context, string) (domain.SkillSnapshot, error) {
	return domain.SkillSnapshot{}, f.err
}
func (f failingSkillRepository) ListLatest(context.Context) ([]domain.SkillSnapshot, error) {
	return nil, f.err
}

func TestScanAllSkipsMissingMovieMontageSkill(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"finance-topic-selector", "finance-viral-remix", "jianying-montage-draft"} {
		writeSkillFile(t, filepath.Join(base, name, "SKILL.md"), "skill")
	}
	service := NewService(nil, Options{Roots: DefaultRoots(base)})
	snapshots, err := service.ScanAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 3 {
		t.Fatalf("snapshots=%d, want 3 required skills", len(snapshots))
	}
}

func TestScanAllRegistersMovieMontageWhenPresent(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"finance-topic-selector", "finance-viral-remix", "jianying-montage-draft", "jianying-movie-montage"} {
		writeSkillFile(t, filepath.Join(base, name, "SKILL.md"), "skill")
	}
	service := NewService(nil, Options{Roots: DefaultRoots(base)})
	snapshots, err := service.ScanAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 4 {
		t.Fatalf("snapshots=%d, want all production skills", len(snapshots))
	}
}

func TestSkillLatestPreservesRepositoryFailures(t *testing.T) {
	want := errors.New("database unavailable")
	_, err := NewService(failingSkillRepository{err: want}).Latest(t.Context(), "skill")
	if !errors.Is(err, want) {
		t.Fatalf("Latest() error=%v, want repository error", err)
	}
}

func writeSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSparseSkillFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
