package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
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
)

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
	entries, err := s.readDir("")
	if err != nil {
		return Tree{}, err
	}
	return Tree{RootName: DirName, Entries: entries}, nil
}

func (s Store) readDir(rel string) ([]Entry, error) {
	dir := s.Root
	if rel != "" {
		dir = filepath.Join(s.Root, filepath.FromSlash(rel))
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var dirs, files []Entry
	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") {
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
			children, childErr := s.readDir(childRel)
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
	abs, err := s.Resolve(rel)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return File{}, ErrNotFound
		}
		return File{}, err
	}
	if info.IsDir() || info.Size() > maxBytes {
		if info.Size() > maxBytes {
			return File{}, ErrTooLarge
		}
		return File{}, ErrKind
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return File{}, err
	}
	if !utf8.Valid(raw) {
		return File{}, ErrKind
	}
	content := string(raw)
	return File{
		Path:       filepath.ToSlash(rel),
		Content:    content,
		MTime:      info.ModTime().UTC().Format(time.RFC3339),
		SpokenBody: spokenBody(content),
	}, nil
}

func (s Store) Write(rel, content string) error {
	if len(content) > maxBytes {
		return ErrTooLarge
	}
	if !utf8.ValidString(content) {
		return ErrKind
	}
	abs, err := s.Resolve(rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
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
