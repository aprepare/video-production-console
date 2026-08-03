package skillregistry

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

const (
	MaxSkillFileSize   int64 = 16 << 20
	MaxSkillBundleSize int64 = 128 << 20
)

var (
	ErrSkillPathEscape        = errors.New("skill entry resolves outside its root")
	ErrSkillNotFound          = errors.New("skill is not registered")
	ErrSkillFileTooLarge      = errors.New("skill file exceeds size limit")
	ErrSkillBundleTooLarge    = errors.New("skill bundle exceeds size limit")
	ErrSkillChangedDuringScan = errors.New("skill file changed during scan")
)

type Repository interface {
	Save(context.Context, domain.SkillSnapshot) error
	Get(context.Context, string) (domain.SkillSnapshot, error)
	Latest(context.Context, string) (domain.SkillSnapshot, error)
	ListLatest(context.Context) ([]domain.SkillSnapshot, error)
}

type Root struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Options struct {
	Roots []Root
	Now   func() time.Time
}

type Service struct {
	repo  Repository
	roots []Root
	now   func() time.Time
}

func NewService(repo Repository, optionValues ...Options) *Service {
	options := Options{}
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Roots == nil {
		if home, err := os.UserHomeDir(); err == nil {
			options.Roots = DefaultRoots(filepath.Join(home, ".codex", "skills"))
		}
	}
	return &Service{repo: repo, roots: append([]Root(nil), options.Roots...), now: options.Now}
}

func DefaultRoots(skillsBase string) []Root {
	names := []string{"finance-topic-selector", "finance-viral-remix", "jianying-montage-draft"}
	roots := make([]Root, 0, len(names))
	for _, name := range names {
		roots = append(roots, Root{Name: name, Path: filepath.Join(skillsBase, name)})
	}
	return roots
}

func (s *Service) Roots() []Root { return append([]Root(nil), s.roots...) }

func (s *Service) Scan(name, root string) (domain.SkillSnapshot, error) {
	return s.ScanContext(context.Background(), name, root)
}

func (s *Service) ScanContext(ctx context.Context, name, root string) (domain.SkillSnapshot, error) {
	name = strings.TrimSpace(name)
	if name == "" || root == "" || !filepath.IsAbs(root) {
		return domain.SkillSnapshot{}, ErrSkillNotFound
	}
	root = filepath.Clean(root)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return domain.SkillSnapshot{}, ErrSkillNotFound
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil || !info.IsDir() {
		return domain.SkillSnapshot{}, ErrSkillNotFound
	}
	type scannedFile struct {
		snapshot domain.SkillFileSnapshot
		path     string
		identity os.FileInfo
		modified time.Time
	}
	var scanned []scannedFile
	var totalSize int64
	err = filepath.WalkDir(canonicalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == canonicalRoot {
			return nil
		}
		relative, err := filepath.Rel(canonicalRoot, path)
		if err != nil || filepath.IsAbs(relative) {
			return ErrSkillPathEscape
		}
		if excludedSkillEntry(relative, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve skill entry: %w", err)
		}
		if !pathWithin(canonicalRoot, resolved) {
			return ErrSkillPathEscape
		}
		resolvedInfo, err := os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("inspect skill entry: %w", err)
		}
		if resolvedInfo.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("skill directory links are unsupported")
			}
			return nil
		}
		if !resolvedInfo.Mode().IsRegular() {
			return nil
		}
		if resolvedInfo.Size() > MaxSkillFileSize {
			return ErrSkillFileTooLarge
		}
		if resolvedInfo.Size() < 0 || totalSize > MaxSkillBundleSize-resolvedInfo.Size() {
			return ErrSkillBundleTooLarge
		}
		totalSize += resolvedInfo.Size()
		scanned = append(scanned, scannedFile{
			snapshot: domain.SkillFileSnapshot{Path: filepath.ToSlash(relative), Size: resolvedInfo.Size()},
			path:     resolved,
			identity: resolvedInfo,
			modified: resolvedInfo.ModTime().UTC(),
		})
		return nil
	})
	if err != nil {
		return domain.SkillSnapshot{}, err
	}
	sort.Slice(scanned, func(i, j int) bool { return scanned[i].snapshot.Path < scanned[j].snapshot.Path })
	aggregate := sha256.New()
	files := make([]domain.SkillFileSnapshot, 0, len(scanned))
	modifiedAt := info.ModTime().UTC()
	buffer := make([]byte, 32<<10)
	defer clear(buffer)
	for _, file := range scanned {
		if err := ctx.Err(); err != nil {
			return domain.SkillSnapshot{}, err
		}
		opened, err := openRegularNoFollow(file.path)
		if err != nil {
			return domain.SkillSnapshot{}, fmt.Errorf("open skill file: %w", err)
		}
		openedInfo, err := opened.Stat()
		if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(file.identity, openedInfo) || openedInfo.Size() != file.snapshot.Size {
			_ = opened.Close()
			return domain.SkillSnapshot{}, ErrSkillChangedDuringScan
		}
		writeHashBoundary(aggregate, []byte(file.snapshot.Path))
		writeHashLength(aggregate, uint64(file.snapshot.Size))
		fileHash := sha256.New()
		copied, copyErr := io.CopyBuffer(io.MultiWriter(aggregate, fileHash), io.LimitReader(contextReader{ctx: ctx, reader: opened}, file.snapshot.Size+1), buffer)
		afterInfo, statErr := opened.Stat()
		closeErr := opened.Close()
		if copyErr != nil {
			return domain.SkillSnapshot{}, copyErr
		}
		if statErr != nil || closeErr != nil || copied != file.snapshot.Size || afterInfo.Size() != file.snapshot.Size || !afterInfo.ModTime().Equal(file.identity.ModTime()) {
			return domain.SkillSnapshot{}, ErrSkillChangedDuringScan
		}
		file.snapshot.SHA256 = hex.EncodeToString(fileHash.Sum(nil))
		files = append(files, file.snapshot)
		if file.modified.After(modifiedAt) {
			modifiedAt = file.modified
		}
	}
	now := s.now().UTC()
	snapshot := domain.SkillSnapshot{
		ID: uuid.NewString(), Name: name, Path: root,
		SHA256: hex.EncodeToString(aggregate.Sum(nil)), Files: files,
		ModifiedAt: modifiedAt, CreatedAt: now,
	}
	if s.repo != nil {
		if err := s.repo.Save(ctx, snapshot); err != nil {
			return domain.SkillSnapshot{}, err
		}
	}
	return snapshot, nil
}

func (s *Service) ScanAll(ctx context.Context) ([]domain.SkillSnapshot, error) {
	snapshots := make([]domain.SkillSnapshot, 0, len(s.roots))
	for _, root := range s.roots {
		snapshot, err := s.ScanContext(ctx, root.Name, root.Path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (s *Service) List(ctx context.Context) ([]domain.SkillSnapshot, error) {
	if s.repo == nil {
		return []domain.SkillSnapshot{}, nil
	}
	return s.repo.ListLatest(ctx)
}

func (s *Service) Latest(ctx context.Context, name string) (domain.SkillSnapshot, error) {
	if s.repo == nil {
		return domain.SkillSnapshot{}, ErrSkillNotFound
	}
	snapshot, err := s.repo.Latest(ctx, name)
	if errors.Is(err, store.ErrSkillSnapshotNotFound) {
		return domain.SkillSnapshot{}, ErrSkillNotFound
	}
	return snapshot, err
}

type byteWriter interface{ Write([]byte) (int, error) }

func writeHashBoundary(writer byteWriter, value []byte) {
	writeHashLength(writer, uint64(len(value)))
	_, _ = writer.Write(value)
}

func writeHashLength(writer byteWriter, size uint64) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], size)
	_, _ = writer.Write(length[:])
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func excludedSkillEntry(relative string, directory bool) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, part := range parts {
		lower := strings.ToLower(part)
		if lower == ".git" || lower == "__pycache__" || (directory && (lower == "tmp" || lower == "temp")) {
			return true
		}
	}
	name := strings.ToLower(parts[len(parts)-1])
	return strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".temp") ||
		strings.HasSuffix(name, ".swp") || strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, "~") ||
		strings.HasPrefix(name, ".~") || strings.HasPrefix(name, "~$")
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
