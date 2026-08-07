package montage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type workspaceDigestEntry struct {
	rel    string
	isDir  bool
	size   int64
	digest [sha256.Size]byte
}

// hashPlaintextWorkspace intentionally matches codex.HashResultDirectory.
func hashPlaintextWorkspace(ctx context.Context, root string) (string, error) {
	root, err := canonicalNoFollow(root, true)
	if err != nil {
		return "", err
	}
	var entries []workspaceDigestEntry
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		canonical, err := canonicalNoFollow(path, entry.IsDir())
		if err != nil || !samePath(canonical, path) {
			return fmt.Errorf("plaintext workspace contains a reparse or non-regular entry %q", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := workspaceDigestEntry{rel: filepath.ToSlash(rel), isDir: entry.IsDir()}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			item.size = info.Size()
			digest, err := hashFile(path)
			if err != nil {
				return err
			}
			if check, checkErr := canonicalNoFollow(path, false); checkErr != nil || !samePath(check, canonical) {
				return fmt.Errorf("plaintext workspace entry identity changed while hashing %q", path)
			}
			decoded, err := hex.DecodeString(digest)
			if err != nil {
				return err
			}
			copy(item.digest[:], decoded)
		}
		entries = append(entries, item)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	hash := sha256.New()
	var frame [8]byte
	for _, entry := range entries {
		kind := byte('f')
		if entry.isDir {
			kind = 'd'
		}
		_, _ = hash.Write([]byte{kind})
		binary.BigEndian.PutUint64(frame[:], uint64(len(entry.rel)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write([]byte(entry.rel))
		binary.BigEndian.PutUint64(frame[:], uint64(entry.size))
		_, _ = hash.Write(frame[:])
		if !entry.isDir {
			_, _ = hash.Write(entry.digest[:])
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
