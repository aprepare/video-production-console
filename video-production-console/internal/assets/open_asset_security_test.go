package assets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

func TestOpenAssetRejectsSymlinkOrReparseAlias(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	alias := filepath.Join(root, "alias.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	file, _, err := NewService(root).OpenAsset(domain.Asset{ID: uuid.NewString(), Path: alias})
	if file != nil {
		_ = file.Close()
	}
	if !errors.Is(err, ErrAssetPathInvalid) {
		t.Fatalf("err=%v", err)
	}
}
