package mediacatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVacuumIntoWritesReopenableCopyAndRejectsOverwrite(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	if _, _, err := repo.UpsertSource(ctx, testVideoSource("snap")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "copy", "catalog.db")
	if err := repo.VacuumInto(dest); err != nil {
		t.Fatal(err)
	}
	copyRoot := filepath.Dir(dest)
	copied, err := Open(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = copied.Close() })
	source, err := copied.SourceBySHA256(ctx, testDigest("snap"))
	if err != nil || source.RelativePath != "originals/movies/snap.mp4" {
		t.Fatalf("copied source=%+v err=%v", source, err)
	}
	if err := repo.VacuumInto(dest); err == nil {
		t.Fatal("overwrite of existing snapshot was accepted")
	}
	if err := repo.VacuumInto("relative.db"); err == nil {
		t.Fatal("relative snapshot destination was accepted")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("first snapshot disappeared: %v", err)
	}
}
