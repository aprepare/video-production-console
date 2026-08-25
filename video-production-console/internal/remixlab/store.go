package remixlab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store persists the adopted global remix prompt under data_root.
type Store struct {
	DataRoot string
}

func (s Store) activePath() string {
	return filepath.Join(filepath.Clean(s.DataRoot), "remix_lab", "active_prompt.json")
}

// GetActive returns the adopted prompt, or ok=false when using built-in defaults.
func (s Store) GetActive() (ActivePrompt, bool, error) {
	path := s.activePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ActivePrompt{}, false, nil
		}
		return ActivePrompt{}, false, fmt.Errorf("read active prompt: %w", err)
	}
	var active ActivePrompt
	if err := json.Unmarshal(raw, &active); err != nil {
		return ActivePrompt{}, false, fmt.Errorf("decode active prompt: %w", err)
	}
	if strings.TrimSpace(active.System) == "" && strings.TrimSpace(active.User) == "" {
		return ActivePrompt{}, false, nil
	}
	return active, true, nil
}

// SetActive writes the adopted prompt. Empty System+User clears the file (builtin).
func (s Store) SetActive(active ActivePrompt) error {
	dir := filepath.Join(filepath.Clean(s.DataRoot), "remix_lab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create remix_lab dir: %w", err)
	}
	path := s.activePath()
	if strings.TrimSpace(active.System) == "" && strings.TrimSpace(active.User) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clear active prompt: %w", err)
		}
		return nil
	}
	raw, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write active prompt: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish active prompt: %w", err)
	}
	return nil
}

// AdoptFromTemplate resolves a catalog id (with optional edits) and stores it.
func (s Store) AdoptFromTemplate(id, systemOverride, userOverride string) (ActivePrompt, error) {
	resolved := ResolvePrompt(id, systemOverride, userOverride)
	// Builtin empty-system styles stay on code path: only store when custom text exists.
	if resolved.ID == "elder_stable" || resolved.ID == "wash" {
		if strings.TrimSpace(systemOverride) == "" && strings.TrimSpace(userOverride) == "" {
			if err := s.SetActive(ActivePrompt{}); err != nil {
				return ActivePrompt{}, err
			}
			return ActivePrompt{
				ID:    resolved.ID,
				Name:  resolved.Name,
				Stamp: resolved.Stamp,
				Style: resolved.Style,
			}, nil
		}
	}
	active := ActivePrompt{
		ID:     resolved.ID,
		Name:   resolved.Name,
		Stamp:  resolved.Stamp,
		Style:  resolved.Style,
		System: resolved.System,
		User:   resolved.User,
	}
	if err := s.SetActive(active); err != nil {
		return ActivePrompt{}, err
	}
	return active, nil
}
