package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DirName   = "二创工作区"
	maxBytes  = 2 << 20
	maxWalkUp = 8
	pathSep   = "/"
)

var (
	ErrOutside  = errors.New("path escapes workspace")
	ErrKind     = errors.New("only markdown or text files")
	ErrNotFound = errors.New("workspace not found")
	ErrTooLarge = errors.New("file too large")
	ErrConflict = errors.New("workspace file changed since it was read")
)

// Handlers resolve fresh Store values; serialize their compare-and-replace writes.
var writeMu sync.Mutex

type Store struct {
	Root string
}

type Entry struct {
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	MTime    string  `json:"mtime,omitempty"`
	Children []Entry `json:"children,omitempty"`
}

type Tree struct {
	RootName string  `json:"root"`
	Entries  []Entry `json:"entries"`
}

type File struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	MTime      string `json:"mtime"`
	SpokenBody string `json:"spoken_body"`
	Revision   string `json:"revision"`
}

func FindRoot(explicit string, starts []string) (string, error) {
	if explicit != "" {
		return absExistingDir(explicit)
	}
	if env := strings.TrimSpace(os.Getenv("VIDEO_CONSOLE_WORKSPACE")); env != "" {
		return absExistingDir(env)
	}
	seen := map[string]bool{}
	for _, start := range starts {
		dir, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		for i := 0; i < maxWalkUp; i++ {
			if seen[dir] {
				break
			}
			seen[dir] = true
			candidate := filepath.Join(dir, DirName)
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				return candidate, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", ErrNotFound
}

func absExistingDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", ErrNotFound
	}
	return abs, nil
}

func DefaultStarts() []string {
	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	return starts
}

func (s Store) Resolve(rel string) (string, error) {
	clean := filepath.ToSlash(strings.TrimSpace(rel))
	clean = strings.TrimPrefix(clean, "./")
	if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", ErrOutside
	}
	for _, part := range strings.Split(clean, pathSep) {
		if part == "" || part == "." || part == ".." {
			return "", ErrOutside
		}
	}
	if !allowedName(clean) {
		return "", ErrKind
	}
	abs := filepath.Join(s.Root, filepath.FromSlash(clean))
	rootAbs, err := filepath.Abs(s.Root)
	if err != nil {
		return "", err
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	relToRoot, err := filepath.Rel(rootAbs, abs)
	if err != nil || strings.HasPrefix(relToRoot, "..") {
		return "", ErrOutside
	}
	return abs, nil
}

func allowedName(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	return ext == ".md" || ext == ".txt"
}

func (s Store) Tree() (Tree, error) {
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return Tree{}, err
	}
	defer root.Close()
	entries, err := s.readDir(root, "")
	if err != nil {
		return Tree{}, err
	}
	return Tree{RootName: DirName, Entries: entries}, nil
}

func (s Store) readDir(root *os.Root, rel string) ([]Entry, error) {
	dir := rel
	if dir == "" {
		dir = "."
	}
	items, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return nil, err
	}
	var dirs, files []Entry
	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") || item.Type()&os.ModeSymlink != 0 {
			continue
		}
		childRel := name
		if rel != "" {
			childRel = rel + pathSep + name
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			continue
		}
		if item.IsDir() {
			children, childErr := s.readDir(root, childRel)
			if childErr != nil {
				continue
			}
			dirs = append(dirs, Entry{Path: childRel, Name: name, Kind: "dir", Children: children})
			continue
		}
		if !allowedName(name) {
			continue
		}
		files = append(files, Entry{Path: childRel, Name: name, Kind: "file", MTime: info.ModTime().UTC().Format(time.RFC3339)})
	}
	return append(dirs, files...), nil
}

func (s Store) Read(rel string) (File, error) {
	_, err := s.Resolve(rel)
	if err != nil {
		return File{}, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return File{}, err
	}
	defer root.Close()
	file, _, err := readFile(root, filepath.FromSlash(rel))
	return file, err
}

func readFile(root *os.Root, rel string) (File, fs.FileMode, error) {
	// Root.Open prevents escaping through links even if paths change after validation.
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return File{}, 0, ErrNotFound
		}
		return File{}, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return File{}, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		if info.Size() > maxBytes {
			return File{}, 0, ErrTooLarge
		}
		return File{}, 0, ErrKind
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return File{}, 0, err
	}
	if len(raw) > maxBytes {
		return File{}, 0, ErrTooLarge
	}
	if !utf8.Valid(raw) {
		return File{}, 0, ErrKind
	}
	content := string(raw)
	digest := sha256.Sum256(raw)
	return File{
		Path:       filepath.ToSlash(rel),
		Content:    content,
		MTime:      info.ModTime().UTC().Format(time.RFC3339Nano),
		SpokenBody: spokenBody(content),
		Revision:   hex.EncodeToString(digest[:]),
	}, info.Mode().Perm(), nil
}

func (s Store) Write(rel, content string, expectedRevision ...string) error {
	if len(content) > maxBytes {
		return ErrTooLarge
	}
	if !utf8.ValidString(content) {
		return ErrKind
	}
	_, err := s.Resolve(rel)
	if err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	rel = filepath.FromSlash(rel)
	// Pin the parent before reading: comparison and replacement must use the same directory.
	parent, err := root.OpenRoot(filepath.Dir(rel))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(rel)
	previous, mode, err := readFile(parent, name)
	if err != nil {
		return err
	}
	if len(expectedRevision) > 0 && expectedRevision[0] != previous.Revision {
		return ErrConflict
	}
	tmp := ".workspace-save-" + uuid.NewString()
	f, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer parent.Remove(tmp)
	_, writeErr := io.WriteString(f, content)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Recheck external edits that happened while staging the replacement.
	latest, _, err := readFile(parent, name)
	if err != nil {
		return err
	}
	if latest.Revision != previous.Revision {
		return ErrConflict
	}
	return parent.Rename(tmp, name)
}

func spokenBody(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return strings.TrimSpace(content)
	}
	rest := strings.TrimPrefix(trimmed, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return strings.TrimSpace(content)
	}
	body := strings.TrimSpace(rest[end+4:])
	if body == "" {
		return strings.TrimSpace(content)
	}
	return body
}
