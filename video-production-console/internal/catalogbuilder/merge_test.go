package catalogbuilder

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/mediacatalog"
)

func TestMergePackImportsThenSkipsAndWarnsMissingOriginal(t *testing.T) {
	ctx := context.Background()
	remoteRoot := t.TempDir()
	remoteSource, _ := seedAnalyzedMovie(t, remoteRoot, "merge-movie", "embed-a")
	zipPath := filepath.Join(t.TempDir(), "pack.zip")
	if err := ExportPack(ctx, remoteRoot, zipPath, "vision-x"); err != nil {
		t.Fatal(err)
	}

	hostRoot := t.TempDir()
	first, err := MergePack(ctx, hostRoot, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImportedSources != 1 || first.ImportedShots != 1 || first.CopiedKeyframes != 1 {
		t.Fatalf("first merge=%+v", first)
	}
	if len(first.Warnings) != 1 || first.Warnings[0] != "original_missing" {
		t.Fatalf("expected missing original warning, got %+v", first)
	}

	host, err := mediacatalog.Open(hostRoot)
	if err != nil {
		t.Fatal(err)
	}
	local, err := host.SourceBySHA256(ctx, remoteSource.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if local.ID == remoteSource.ID {
		t.Fatal("merged source kept the remote id")
	}
	shots, err := host.ShotsBySource(ctx, local.ID)
	if err != nil || len(shots) != 1 || shots[0].Summary == "" || shots[0].AnalysisStatus != mediacatalog.AnalysisCompleted {
		t.Fatalf("shots=%+v err=%v", shots, err)
	}
	if shots[0].ID == "" {
		t.Fatal("local shot id missing")
	}
	frames, err := host.KeyframesByShot(ctx, shots[0].ID)
	if err != nil || len(frames) != 1 {
		t.Fatalf("frames=%+v err=%v", frames, err)
	}
	if _, err := os.Stat(filepath.Join(hostRoot, filepath.FromSlash(frames[0].RelativePath))); err != nil {
		t.Fatalf("keyframe file missing: %v", err)
	}
	embedding, err := host.ShotEmbedding(ctx, shots[0].ID)
	if err != nil || embedding.Model != "embed-a" || embedding.Dimension != 3 {
		t.Fatalf("embedding=%+v err=%v", embedding, err)
	}
	rights, err := host.RightsBySource(ctx, local.ID)
	if err != nil || rights.LicenseCode != "local" {
		t.Fatalf("rights=%+v err=%v", rights, err)
	}
	host.Close()

	second, err := MergePack(ctx, hostRoot, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.ImportedSources != 0 || second.SkippedSources != 1 || second.ImportedShots != 0 {
		t.Fatalf("second merge should be idempotent, got %+v", second)
	}
}

func TestMergeFillsIngestOnlyHostSource(t *testing.T) {
	ctx := context.Background()
	remoteRoot := t.TempDir()
	remoteSource, _ := seedAnalyzedMovie(t, remoteRoot, "ingest-only", "embed-a")
	zipPath := filepath.Join(t.TempDir(), "pack.zip")
	if err := ExportPack(ctx, remoteRoot, zipPath, ""); err != nil {
		t.Fatal(err)
	}
	hostRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostRoot, "originals", "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "originals", "movies", "ingest-only.mp4"), []byte("movie-ingest-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := mediacatalog.Open(hostRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.UpsertSource(ctx, mediacatalog.Source{
		Kind: mediacatalog.SourceKindMovie, Subtype: mediacatalog.SourceSubtypeVideo,
		Origin: mediacatalog.SourceOriginLocal, RelativePath: remoteSource.RelativePath,
		SHA256: remoteSource.SHA256, SizeBytes: remoteSource.SizeBytes, MIMEType: "video/mp4",
		Status: mediacatalog.SourceStatusPendingProbe,
	}); err != nil {
		t.Fatal(err)
	}
	host.Close()

	summary, err := MergePack(ctx, hostRoot, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ImportedShots != 1 || summary.SkippedSources != 0 {
		t.Fatalf("ingest-only fill=%+v", summary)
	}
	if len(summary.Warnings) != 0 {
		t.Fatalf("original was present, warnings=%v", summary.Warnings)
	}
}

func TestMergeRejectsEmbeddingMismatch(t *testing.T) {
	ctx := context.Background()
	hostRoot := t.TempDir()
	seedAnalyzedMovie(t, hostRoot, "host-movie", "embed-host")
	remoteRoot := t.TempDir()
	seedAnalyzedMovie(t, remoteRoot, "other-movie", "embed-cloud")
	zipPath := filepath.Join(t.TempDir(), "pack.zip")
	if err := ExportPack(ctx, remoteRoot, zipPath, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := MergePack(ctx, hostRoot, zipPath); err == nil {
		t.Fatal("mismatched embedding model was accepted")
	}
}
