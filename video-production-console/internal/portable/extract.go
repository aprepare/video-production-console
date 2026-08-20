package portable

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const stagingDirName = ".staging"

type Extractor struct {
	SpaceCheck func(dir string, needed int64) error
}

func ExtractToStaging(ctx context.Context, overlay *Overlay, appRoot string) error {
	return Extractor{}.ExtractToStaging(ctx, overlay, appRoot)
}

func (e Extractor) ExtractToStaging(ctx context.Context, overlay *Overlay, appRoot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if overlay == nil || overlay.Zip == nil {
		return fmt.Errorf("%w: missing overlay", ErrInvalidOverlay)
	}
	if err := overlay.Manifest.Validate(); err != nil {
		return err
	}
	appRoot, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	version := overlay.Manifest.AppVersion
	dest := filepath.Join(appRoot, version)
	if filepath.Base(dest) != version || !samePath(filepath.Dir(dest), appRoot) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, version)
	}
	if _, err := os.Lstat(dest); err == nil {
		if err := assertSafeVersionDir(appRoot, version); err != nil {
			return err
		}
		return verifyExistingVersion(dest, overlay.Manifest)
	} else if !os.IsNotExist(err) {
		return err
	}

	var needed int64
	for _, entry := range overlay.Manifest.Entries {
		needed += entry.Size
	}
	if e.SpaceCheck != nil {
		if err := e.SpaceCheck(appRoot, needed); err != nil {
			return err
		}
	}

	stagingRoot := filepath.Join(appRoot, stagingDirName)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return err
	}
	if err := assertDirectChildDir(appRoot, stagingRoot, false); err != nil {
		return err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	staging := filepath.Join(stagingRoot, version+"-"+hex.EncodeToString(suffix))
	if err := os.Mkdir(staging, 0o700); err != nil {
		return err
	}
	if err := assertDirectChildDir(stagingRoot, staging, false); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}

	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	wanted := make(map[string]Entry, len(overlay.Manifest.Entries))
	for _, entry := range overlay.Manifest.Entries {
		wanted[entry.Path] = entry
	}
	seen := make(map[string]struct{}, len(wanted))
	for _, file := range overlay.Zip.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateZipHeader(file); err != nil {
			return err
		}
		entry, ok := wanted[file.Name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnexpectedEntry, file.Name)
		}
		if _, dup := seen[file.Name]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateEntry, file.Name)
		}
		seen[file.Name] = struct{}{}
		target, err := safeExtractTarget(staging, entry.Path)
		if err != nil {
			return err
		}
		if err := mkdirAllNoFollow(staging, filepath.Dir(target)); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		copyErr := copyVerifiedFile(src, target, entry)
		_ = src.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	for _, entry := range overlay.Manifest.Entries {
		if _, ok := seen[entry.Path]; !ok {
			return fmt.Errorf("%w: %q", ErrMissingEntry, entry.Path)
		}
	}
	if err := syncTree(staging); err != nil {
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		if _, statErr := os.Lstat(dest); statErr == nil {
			if verifyErr := verifyExistingVersion(dest, overlay.Manifest); verifyErr == nil {
				return nil
			}
		}
		return err
	}
	if err := assertSafeVersionDir(appRoot, version); err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	committed = true
	return nil
}

func safeExtractTarget(staging, relPath string) (string, error) {
	if err := validateRelPath(relPath); err != nil {
		return "", err
	}
	target := filepath.Join(staging, filepath.FromSlash(relPath))
	rel, err := filepath.Rel(staging, target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: absolute rel %q", ErrUnsafePath, rel)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, relPath)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("%w: %q", ErrUnsafePath, relPath)
		}
	}
	return target, nil
}

func mkdirAllNoFollow(root, dir string) error {
	if samePath(dir, root) {
		return nil
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return fmt.Errorf("%w: %q", ErrUnsafePath, dir)
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: %q", ErrUnsafePath, dir)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if isSymlinkOrReparse(info) {
			return fmt.Errorf("%w: %q", ErrSymlink, current)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: %q is not a directory", ErrUnsafePath, current)
		}
	}
	return nil
}

func copyVerifiedFile(src io.Reader, target string, entry Entry) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), src)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return err
	}
	if written != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		_ = file.Close()
		_ = os.Remove(target)
		return fmt.Errorf("%w: %q", ErrEntryHash, entry.Path)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		_ = os.Remove(target)
		return err
	}
	if isSymlinkOrReparse(info) || !info.Mode().IsRegular() {
		_ = os.Remove(target)
		return fmt.Errorf("%w: %q", ErrUnsafePath, entry.Path)
	}
	return nil
}

func verifyExistingVersion(dest string, manifest Manifest) error {
	for _, entry := range manifest.Entries {
		target, err := safeExtractTarget(dest, entry.Path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if isSymlinkOrReparse(info) {
			return fmt.Errorf("%w: %q", ErrSymlink, entry.Path)
		}
		if !info.Mode().IsRegular() || info.Size() != entry.Size {
			return fmt.Errorf("%w: %q", ErrEntryHash, entry.Path)
		}
		file, err := openRegularNoFollow(target)
		if err != nil {
			return err
		}
		hash := sha256.New()
		written, err := io.Copy(hash, file)
		_ = file.Close()
		if err != nil {
			return err
		}
		if written != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("%w: %q", ErrEntryHash, entry.Path)
		}
	}
	return nil
}

func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if isSymlinkOrReparse(info) {
			return fmt.Errorf("%w: %q", ErrSymlink, path)
		}
		flag := os.O_RDONLY
		if !d.IsDir() {
			flag = os.O_WRONLY
		}
		file, openErr := os.OpenFile(path, flag, 0)
		if openErr != nil {
			return openErr
		}
		syncErr := file.Sync()
		_ = file.Close()
		if d.IsDir() && syncErr != nil {
			return nil
		}
		return syncErr
	})
}
