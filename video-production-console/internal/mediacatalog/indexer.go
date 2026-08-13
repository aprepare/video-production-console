package mediacatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// Indexing phases recorded in media_jobs. Re-runs only execute phases whose
// job has not completed, which makes interrupted builds resumable.
const (
	// PhaseIngest covers discovery, hashing, and the source row itself.
	PhaseIngest = "ingest"
	// PhaseImageShot creates the degenerate whole-image shot for images.
	PhaseImageShot = "image_shot"
	// PhaseProbe stays pending here; Task 7 wires FFmpeg probing/scene cuts.
	PhaseProbe = "probe"
)

// Indexer discovers original media files below media_root/originals and
// drives the per-source job state machine in the catalog.
type Indexer struct {
	repo *Repository
}

func NewIndexer(repo *Repository) *Indexer { return &Indexer{repo: repo} }

type IndexSummary struct {
	DiscoveredFiles int
	NewSources      int
	DuplicateFiles  int
	ExecutedPhases  int
	ReadyImages     int
	PendingProbe    int
	SkippedEntries  int
}

var originalsLayout = []struct {
	dir     string
	kind    SourceKind
	subtype SourceSubtype
}{
	{"movies", SourceKindMovie, SourceSubtypeVideo},
	{"broll", SourceKindBroll, SourceSubtypeVideo},
	{"images", SourceKindImage, SourceSubtypePhoto},
}

// Run walks originals/{movies,broll,images}, hashes every regular file, and
// upserts sources idempotently (SHA-256 keyed). Symlinks are never followed.
// Video sources are parked at pending_probe with a pending probe job for
// Task 7; image sources get their degenerate shot and become ready.
func (ix *Indexer) Run(ctx context.Context) (IndexSummary, error) {
	summary := IndexSummary{}
	for _, layout := range originalsLayout {
		base := filepath.Join(ix.repo.Root(), "originals", layout.dir)
		info, err := os.Stat(base)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() {
			return summary, fmt.Errorf("inspect originals directory %q: %w", layout.dir, err)
		}
		walkErr := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				summary.SkippedEntries++
				return nil
			}
			return ix.indexFile(ctx, layout.kind, layout.subtype, path, &summary)
		})
		if walkErr != nil {
			return summary, fmt.Errorf("walk originals directory %q: %w", layout.dir, walkErr)
		}
	}
	return summary, nil
}

func (ix *Indexer) indexFile(ctx context.Context, kind SourceKind, subtype SourceSubtype, path string, summary *IndexSummary) error {
	relative, err := filepath.Rel(ix.repo.Root(), path)
	if err != nil {
		return fmt.Errorf("relativize %q: %w", path, err)
	}
	relative = filepath.ToSlash(relative)
	if err := ValidateRelativePath(relative); err != nil {
		return err
	}
	summary.DiscoveredFiles++
	digest, size, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash %q: %w", relative, err)
	}
	status := SourceStatusPendingProbe
	if kind == SourceKindImage {
		status = SourceStatusReady
	}
	source, created, err := ix.repo.UpsertSource(ctx, Source{
		Kind: kind, Subtype: subtype, Origin: SourceOriginLocal,
		RelativePath: relative, SHA256: digest, SizeBytes: size,
		MIMEType: mimeTypeByExtension(path), Status: status,
	})
	if err != nil {
		return err
	}
	if created {
		summary.NewSources++
	} else if source.RelativePath != relative {
		summary.DuplicateFiles++
	}
	ingest, err := ix.repo.EnsureJob(ctx, source.ID, PhaseIngest, 1)
	if err != nil {
		return err
	}
	if ingest.Status != JobCompleted {
		if err := ix.repo.CompleteJob(ctx, ingest.ID, 1); err != nil {
			return err
		}
		summary.ExecutedPhases++
	}
	if source.Kind == SourceKindImage {
		imageShot, err := ix.repo.EnsureJob(ctx, source.ID, PhaseImageShot, 1)
		if err != nil {
			return err
		}
		if imageShot.Status != JobCompleted {
			if err := ix.repo.StartJob(ctx, imageShot.ID); err != nil {
				return err
			}
			if _, err := ix.repo.EnsureImageShot(ctx, source.ID); err != nil {
				failErr := ix.repo.FailJob(ctx, imageShot.ID, "image_shot_failed", err.Error())
				if failErr != nil {
					return failErr
				}
				return err
			}
			if err := ix.repo.UpdateSourceStatus(ctx, source.ID, SourceStatusReady, ""); err != nil {
				return err
			}
			if err := ix.repo.CompleteJob(ctx, imageShot.ID, 1); err != nil {
				return err
			}
			summary.ExecutedPhases++
		}
		summary.ReadyImages++
		return nil
	}
	if _, err := ix.repo.EnsureJob(ctx, source.ID, PhaseProbe, 1); err != nil {
		return err
	}
	summary.PendingProbe++
	return nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func mimeTypeByExtension(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	if value := mime.TypeByExtension(extension); value != "" {
		return value
	}
	return "application/octet-stream"
}
