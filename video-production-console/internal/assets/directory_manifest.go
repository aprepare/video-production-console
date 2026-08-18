package assets

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrDirectoryPathInvalid = errors.New("directory asset path is invalid")
	ErrDirectoryTooLarge    = errors.New("directory asset contains too many entries")
	ErrDirectoryTooDeep     = errors.New("directory asset exceeds maximum depth")
)

const MaxDirectoryManifestEntries = 5000
const MaxDirectoryManifestDepth = 64
const MaxDirectoryRelativeBytes = 1 << 20
const MaxDirectoryManifestBytes = 4 << 20

type DirectoryEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

// BuildDirectoryManifest validates the trusted root and registered directory
// without following links, junctions, or reparse points. Only relative entry
// names and metadata leave this package.
func BuildDirectoryManifest(root, target string) (string, []DirectoryEntry, error) {
	handle, canonicalTarget, err := OpenVerifiedDirectory(root, target)
	if err != nil {
		return "", nil, ErrDirectoryPathInvalid
	}
	defer handle.Close()
	entries := make([]DirectoryEntry, 0)
	relativeBytes, outputBytes := 0, 0
	err = filepath.WalkDir(canonicalTarget, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == canonicalTarget {
			return nil
		}
		if len(entries) >= MaxDirectoryManifestEntries {
			return ErrDirectoryTooLarge
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entryHandle, err := openPathNoFollow(path, canonicalTarget, info.IsDir())
		if err != nil {
			return ErrDirectoryPathInvalid
		}
		if err := entryHandle.Close(); err != nil {
			return err
		}
		kind, size := "file", info.Size()
		if info.IsDir() {
			kind, size = "directory", 0
		}
		relative, err := filepath.Rel(canonicalTarget, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrDirectoryPathInvalid
		}
		relative = filepath.ToSlash(relative)
		if !utf8.ValidString(relative) {
			return ErrDirectoryPathInvalid
		}
		depth := strings.Count(relative, "/") + 1
		if depth > MaxDirectoryManifestDepth {
			return ErrDirectoryTooDeep
		}
		relativeBytes += len(relative)
		// JSON can expand control bytes to six-byte escape sequences. Budget the
		// worst case so the encoded response remains bounded as well.
		outputBytes += len(relative)*6 + len(kind) + 128
		if relativeBytes > MaxDirectoryRelativeBytes || outputBytes > MaxDirectoryManifestBytes {
			return ErrDirectoryTooLarge
		}
		entries = append(entries, DirectoryEntry{Path: relative, Kind: kind, Size: size})
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	current, err := filepath.Abs(canonicalTarget)
	pathInfo, statErr := os.Stat(current)
	handleInfo, handleErr := handle.Stat()
	if err != nil || statErr != nil || handleErr != nil || !os.SameFile(pathInfo, handleInfo) {
		return "", nil, ErrDirectoryPathInvalid
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Path == entries[j].Path {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Path < entries[j].Path
	})
	return canonicalTarget, entries, nil
}

func OpenVerifiedDirectory(root, target string) (*os.File, string, error) {
	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, "", ErrDirectoryPathInvalid
	}
	rootHandle, err := openPathNoFollow(canonicalRoot, "", true)
	if err != nil {
		return nil, "", ErrDirectoryPathInvalid
	}
	defer rootHandle.Close()
	canonicalTarget, err := filepath.Abs(target)
	if err != nil || !directoryWithin(canonicalRoot, canonicalTarget) {
		return nil, "", ErrDirectoryPathInvalid
	}
	targetHandle, err := openPathNoFollow(canonicalTarget, canonicalRoot, true)
	if err != nil {
		return nil, "", ErrDirectoryPathInvalid
	}
	return targetHandle, filepath.Clean(canonicalTarget), nil
}

func directoryWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != "." && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
