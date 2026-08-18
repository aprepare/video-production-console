package mediacatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VacuumInto writes a consistent copy of catalog.db to dest. The destination
// must be an absolute path that does not already exist so an interrupted pack
// cannot silently overwrite another catalog.
func (r *Repository) VacuumInto(dest string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("%w: catalog is not open", ErrInvalidValue)
	}
	if strings.TrimSpace(dest) == "" || !filepath.IsAbs(dest) {
		return fmt.Errorf("%w: snapshot destination must be an absolute path", ErrInvalidValue)
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%w: snapshot destination already exists", ErrInvalidValue)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect snapshot destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if _, err := r.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("snapshot catalog: %w", err)
	}
	return nil
}
