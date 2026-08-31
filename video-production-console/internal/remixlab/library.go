package remixlab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type libraryFile struct {
	Prompts []PromptTemplate `json:"prompts"`
	Deleted []string         `json:"deleted_builtin_ids"`
}

func (s Store) libraryPath() string {
	return filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "library.json")
}

func (s Store) loadLibrary() (libraryFile, error) {
	raw, err := os.ReadFile(s.libraryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return libraryFile{}, nil
		}
		return libraryFile{}, fmt.Errorf("read prompt library: %w", err)
	}
	var file libraryFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return libraryFile{}, fmt.Errorf("decode prompt library: %w", err)
	}
	return file, nil
}

func (s Store) saveLibrary(file libraryFile) error {
	dir := filepath.Join(filepath.Clean(s.DataRoot), "remix_lab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create remix_lab dir: %w", err)
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	path := s.libraryPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write prompt library: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish prompt library: %w", err)
	}
	return nil
}

func builtinByID() map[string]PromptTemplate {
	out := make(map[string]PromptTemplate, 8)
	for _, p := range Catalog() {
		out[p.ID] = p
	}
	return out
}

func deletedSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// ListLibrary returns builtin catalog minus deletions, plus custom/edited overlays.
func (s Store) ListLibrary() ([]PromptTemplate, error) {
	file, err := s.loadLibrary()
	if err != nil {
		return nil, err
	}
	deleted := deletedSet(file.Deleted)
	overlays := make(map[string]PromptTemplate, len(file.Prompts))
	for _, p := range file.Prompts {
		if strings.TrimSpace(p.ID) == "" {
			continue
		}
		overlays[p.ID] = p
	}
	out := make([]PromptTemplate, 0, len(Catalog())+len(file.Prompts))
	seen := make(map[string]bool, len(Catalog())+len(file.Prompts))
	for _, base := range Catalog() {
		if deleted[base.ID] {
			continue
		}
		if overlay, ok := overlays[base.ID]; ok {
			item := ResolvePrompt(base.ID, overlay.System, overlay.User)
			if strings.TrimSpace(overlay.Name) != "" {
				item.Name = overlay.Name
			}
			if strings.TrimSpace(overlay.Description) != "" {
				item.Description = overlay.Description
			}
			if strings.TrimSpace(overlay.Stamp) != "" {
				item.Stamp = overlay.Stamp
			}
			item.Builtin = true
			out = append(out, item)
		} else {
			out = append(out, ResolvePrompt(base.ID, "", ""))
		}
		seen[base.ID] = true
	}
	for _, p := range file.Prompts {
		if seen[p.ID] || deleted[p.ID] {
			continue
		}
		p.Builtin = false
		if strings.TrimSpace(p.System) == "" {
			p.System = ResolvePrompt("elder_stable", "", "").System
		}
		if strings.TrimSpace(p.User) == "" {
			p.User = defaultUserTemplate("rewrite")
		}
		out = append(out, p)
	}
	return out, nil
}

func (s Store) GetPrompt(id string) (PromptTemplate, bool, error) {
	id = strings.TrimSpace(id)
	list, err := s.ListLibrary()
	if err != nil {
		return PromptTemplate{}, false, err
	}
	for _, p := range list {
		if p.ID == id {
			return p, true, nil
		}
	}
	return PromptTemplate{}, false, nil
}

func (s Store) UpsertPrompt(in PromptTemplate) (PromptTemplate, error) {
	name := strings.TrimSpace(in.Name)
	system := strings.TrimSpace(in.System)
	if name == "" || system == "" {
		return PromptTemplate{}, fmt.Errorf("prompt name and system are required")
	}
	file, err := s.loadLibrary()
	if err != nil {
		return PromptTemplate{}, err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "custom-" + uuid.NewString()
	}
	_, isBuiltin := builtinByID()[id]
	deleted := make([]string, 0, len(file.Deleted))
	for _, existing := range file.Deleted {
		if existing != id {
			deleted = append(deleted, existing)
		}
	}
	file.Deleted = deleted
	next := PromptTemplate{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Style:       strings.TrimSpace(in.Style),
		Stamp:       strings.TrimSpace(in.Stamp),
		System:      in.System,
		User:        in.User,
		Builtin:     isBuiltin,
	}
	if next.Style == "" {
		next.Style = "rewrite"
	}
	if next.Stamp == "" {
		next.Stamp = name
	}
	replaced := false
	for i, p := range file.Prompts {
		if p.ID == id {
			file.Prompts[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		file.Prompts = append(file.Prompts, next)
	}
	if err := s.saveLibrary(file); err != nil {
		return PromptTemplate{}, err
	}
	return next, nil
}

func (s Store) DeletePrompt(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("prompt id is required")
	}
	file, err := s.loadLibrary()
	if err != nil {
		return err
	}
	kept := file.Prompts[:0]
	for _, p := range file.Prompts {
		if p.ID != id {
			kept = append(kept, p)
		}
	}
	file.Prompts = kept
	if _, ok := builtinByID()[id]; ok {
		if !deletedSet(file.Deleted)[id] {
			file.Deleted = append(file.Deleted, id)
		}
	}
	if err := s.saveLibrary(file); err != nil {
		return err
	}
	active, ok, err := s.GetActive()
	if err != nil {
		return err
	}
	if ok && active.ID == id {
		return s.SetActive(ActivePrompt{})
	}
	return nil
}
