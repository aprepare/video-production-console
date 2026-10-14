package remixlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrInvalidStyle  = errors.New("invalid style")
	ErrStyleNotFound = errors.New("style not found")
	ErrBuiltinStyle  = errors.New("builtin style cannot be deleted")
	stylesMu         sync.Mutex
)

type StyleTemplate struct {
	ID          string   `json:"id"`
	BaseID      string   `json:"base_id,omitempty"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	BestFor     []string `json:"best_for"`
	Planner     string   `json:"planner"`
	Writer      string   `json:"writer"`
	WriterUser  string   `json:"writer_user"`
	Reviewer    string   `json:"reviewer"`
	Rules       string   `json:"rules"`
	Finance     bool     `json:"finance"`
	Publish     string   `json:"publish,omitempty"`
	Builtin     bool     `json:"builtin"`
}

type stylesFile struct {
	Styles []StyleTemplate `json:"styles"`
}

func (s Store) stylesPath() string {
	return filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "styles.json")
}

func builtinStyleMap() map[string]StyleTemplate {
	out := make(map[string]StyleTemplate, len(BuiltinStyles()))
	for _, style := range BuiltinStyles() {
		out[style.ID] = style
	}
	return out
}

func normalizeStyle(in StyleTemplate) StyleTemplate {
	in.ID = strings.TrimSpace(in.ID)
	in.BaseID = strings.TrimSpace(in.BaseID)
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(in.Category)
	in.Description = strings.TrimSpace(in.Description)
	in.Planner = strings.TrimSpace(in.Planner)
	in.Writer = strings.TrimSpace(in.Writer)
	in.WriterUser = strings.TrimSpace(in.WriterUser)
	in.Reviewer = strings.TrimSpace(in.Reviewer)
	in.Rules = strings.TrimSpace(in.Rules)
	in.Publish = strings.TrimSpace(in.Publish)
	for i := range in.BestFor {
		in.BestFor[i] = strings.TrimSpace(in.BestFor[i])
	}
	return in
}

func validateStyle(in StyleTemplate) error {
	if in.Name == "" || in.Writer == "" {
		return fmt.Errorf("%w: name and writer are required", ErrInvalidStyle)
	}
	for field, value := range map[string]string{
		"id": in.ID, "base_id": in.BaseID, "name": in.Name, "category": in.Category,
		"description": in.Description, "planner": in.Planner, "writer": in.Writer,
		"writer_user": in.WriterUser, "reviewer": in.Reviewer, "rules": in.Rules, "publish": in.Publish,
	} {
		if utf8.RuneCountInString(value) > 50000 {
			return fmt.Errorf("%w: %s exceeds 50000 runes", ErrInvalidStyle, field)
		}
	}
	bestForRunes := 0
	for _, value := range in.BestFor {
		bestForRunes += utf8.RuneCountInString(value)
	}
	if bestForRunes > 50000 {
		return fmt.Errorf("%w: best_for exceeds 50000 runes", ErrInvalidStyle)
	}
	return nil
}

func (s Store) loadStylesLocked() (stylesFile, error) {
	raw, err := os.ReadFile(s.stylesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return stylesFile{}, nil
		}
		return stylesFile{}, fmt.Errorf("read styles: %w", err)
	}
	var file stylesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return stylesFile{}, fmt.Errorf("decode styles: %w", err)
	}
	return file, nil
}

func (s Store) saveStylesLocked(file stylesFile) error {
	dir := filepath.Dir(s.stylesPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create remix_lab dir: %w", err)
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode styles: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "styles-*.tmp")
	if err != nil {
		return fmt.Errorf("create styles temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect styles temp file: %w", err)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write styles: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync styles: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close styles: %w", err)
	}
	if err := os.Rename(tmpPath, s.stylesPath()); err != nil {
		return fmt.Errorf("publish styles: %w", err)
	}
	return nil
}

func (s Store) listStylesLocked() ([]StyleTemplate, error) {
	file, err := s.loadStylesLocked()
	if err != nil {
		return nil, err
	}
	builtins := BuiltinStyles()
	out := make([]StyleTemplate, 0, len(builtins)+len(file.Styles))
	out = append(out, builtins...)
	for _, style := range file.Styles {
		style.Builtin = false
		out = append(out, style)
	}
	return out, nil
}

func (s Store) ListStyles() ([]StyleTemplate, error) {
	stylesMu.Lock()
	defer stylesMu.Unlock()
	return s.listStylesLocked()
}

func (s Store) GetStyle(id string) (StyleTemplate, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return StyleTemplate{}, false, nil
	}
	stylesMu.Lock()
	defer stylesMu.Unlock()
	styles, err := s.listStylesLocked()
	if err != nil {
		return StyleTemplate{}, false, err
	}
	for _, style := range styles {
		if style.ID == id {
			return style, true, nil
		}
	}
	return StyleTemplate{}, false, nil
}

func (s Store) UpsertStyle(in StyleTemplate) (StyleTemplate, error) {
	stylesMu.Lock()
	defer stylesMu.Unlock()
	in = normalizeStyle(in)
	builtins := builtinStyleMap()
	if _, editingBuiltin := builtins[in.ID]; editingBuiltin {
		in.BaseID = in.ID
		in.ID = "custom-" + uuid.NewString()
	} else if in.ID == "" {
		in.ID = "custom-" + uuid.NewString()
	}
	in.Builtin = false
	if err := validateStyle(in); err != nil {
		return StyleTemplate{}, err
	}
	file, err := s.loadStylesLocked()
	if err != nil {
		return StyleTemplate{}, err
	}
	replaced := false
	for i := range file.Styles {
		if file.Styles[i].ID == in.ID {
			file.Styles[i] = in
			replaced = true
			break
		}
	}
	if !replaced {
		file.Styles = append(file.Styles, in)
	}
	if err := s.saveStylesLocked(file); err != nil {
		return StyleTemplate{}, err
	}
	return in, nil
}

func (s Store) DeleteStyle(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidStyle)
	}
	stylesMu.Lock()
	defer stylesMu.Unlock()
	if _, ok := builtinStyleMap()[id]; ok {
		return fmt.Errorf("%w: %s", ErrBuiltinStyle, id)
	}
	file, err := s.loadStylesLocked()
	if err != nil {
		return err
	}
	for i := range file.Styles {
		if file.Styles[i].ID == id {
			file.Styles = append(file.Styles[:i], file.Styles[i+1:]...)
			return s.saveStylesLocked(file)
		}
	}
	return fmt.Errorf("%w: %s", ErrStyleNotFound, id)
}
