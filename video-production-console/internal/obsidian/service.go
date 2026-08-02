package obsidian

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Service constrains topic-card access to one configured Obsidian vault.
type Service struct{ Vault string }

func New(vault string) Service { return Service{Vault: vault} }
func (s Service) ValidateCardPath(path string) (string, error) {
	if strings.TrimSpace(s.Vault) == "" {
		return "", fmt.Errorf("obsidian vault is not configured")
	}
	v, err := filepath.Abs(s.Vault)
	if err != nil {
		return "", err
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(v, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("card path is outside vault")
	}
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}
func (s Service) RelativeCardPath(path string) (string, error) {
	p, err := s.ValidateCardPath(path)
	if err != nil {
		return "", err
	}
	return filepath.Rel(s.Vault, p)
}
func (s Service) Health() map[string]any {
	if s.Vault == "" {
		return map[string]any{"status": "not_configured"}
	}
	if _, err := os.Stat(s.Vault); err != nil {
		return map[string]any{"status": "offline", "error": err.Error()}
	}
	return map[string]any{"status": "ok", "path": s.Vault}
}
