package mediacatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newIndexerFixture(t *testing.T) (string, *Repository, *Indexer) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"movies", "broll", "images"} {
		if err := os.MkdirAll(filepath.Join(root, "originals", dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeOriginal(t, root, "movies", "movie.mp4", "movie-bytes")
	writeOriginal(t, root, "broll", "broll.mp4", "broll-bytes")
	writeOriginal(t, root, "images", "chart.png", "image-bytes")
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return root, repo, NewIndexer(repo)
}

func writeOriginal(t *testing.T, root, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "originals", dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIndexerDiscoversTypedSourcesAndDegenerateImageShots(t *testing.T) {
	_, repo, indexer := newIndexerFixture(t)
	ctx := context.Background()
	summary, err := indexer.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DiscoveredFiles != 3 || summary.NewSources != 3 {
		t.Fatalf("summary=%+v", summary)
	}
	sources, err := repo.ListSources(ctx)
	if err != nil || len(sources) != 3 {
		t.Fatalf("sources=%d err=%v", len(sources), err)
	}
	byKind := map[SourceKind]Source{}
	for _, source := range sources {
		byKind[source.Kind] = source
	}
	movie, broll, image := byKind[SourceKindMovie], byKind[SourceKindBroll], byKind[SourceKindImage]
	if movie.Subtype != SourceSubtypeVideo || movie.Status != SourceStatusPendingProbe {
		t.Fatalf("movie=%+v", movie)
	}
	if broll.Subtype != SourceSubtypeVideo || broll.Status != SourceStatusPendingProbe {
		t.Fatalf("broll=%+v", broll)
	}
	if image.Subtype != SourceSubtypePhoto || image.Status != SourceStatusReady || image.Origin != SourceOriginLocal {
		t.Fatalf("image=%+v", image)
	}
	if movie.SHA256 == "" || movie.SizeBytes == 0 {
		t.Fatalf("movie hash/size missing: %+v", movie)
	}
	// 每个源的相对路径必须能安全解析回真实文件。
	for _, source := range sources {
		if _, err := repo.ResolvePath(source.RelativePath); err != nil {
			t.Fatalf("source %q relative path unresolvable: %v", source.RelativePath, err)
		}
	}
	shots, err := repo.ShotsBySource(ctx, image.ID)
	if err != nil || len(shots) != 1 || shots[0].SourceInMS != 0 || shots[0].SourceOutMS != 0 {
		t.Fatalf("image shots=%+v err=%v", shots, err)
	}
	// 视频源留 pending 的 probe job 给 Task 7。
	probe, err := repo.JobBySourcePhase(ctx, movie.ID, PhaseProbe)
	if err != nil || probe.Status != JobPending {
		t.Fatalf("movie probe job=%+v err=%v", probe, err)
	}
	imageJob, err := repo.JobBySourcePhase(ctx, image.ID, PhaseImageShot)
	if err != nil || imageJob.Status != JobCompleted {
		t.Fatalf("image shot job=%+v err=%v", imageJob, err)
	}
}

func TestIndexerRerunOnlyProcessesUnfinishedPhases(t *testing.T) {
	_, repo, indexer := newIndexerFixture(t)
	ctx := context.Background()
	if _, err := indexer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	repeat, err := indexer.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.ExecutedPhases != 0 || repeat.NewSources != 0 {
		t.Fatalf("idempotent rerun still executed work: %+v", repeat)
	}
	sources, err := repo.ListSources(ctx)
	if err != nil || len(sources) != 3 {
		t.Fatalf("sources=%d err=%v", len(sources), err)
	}

	// 模拟中断：image_shot phase 未完成且退化 shot 丢失。
	var imageID string
	for _, source := range sources {
		if source.Kind == SourceKindImage {
			imageID = source.ID
		}
	}
	if _, err := repo.db.Exec(`UPDATE media_jobs SET status='pending', finished_at=NULL WHERE source_id=? AND phase=?`, imageID, PhaseImageShot); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`DELETE FROM media_shots WHERE source_id=?`, imageID); err != nil {
		t.Fatal(err)
	}
	resumed, err := indexer.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ExecutedPhases != 1 || resumed.NewSources != 0 {
		t.Fatalf("resume executed=%d new=%d, want exactly the unfinished phase", resumed.ExecutedPhases, resumed.NewSources)
	}
	shots, err := repo.ShotsBySource(ctx, imageID)
	if err != nil || len(shots) != 1 {
		t.Fatalf("degenerate shot not rebuilt: %+v err=%v", shots, err)
	}
	job, err := repo.JobBySourcePhase(ctx, imageID, PhaseImageShot)
	if err != nil || job.Status != JobCompleted {
		t.Fatalf("resumed job=%+v err=%v", job, err)
	}
}

func TestIndexerDeduplicatesIdenticalContent(t *testing.T) {
	root, repo, indexer := newIndexerFixture(t)
	ctx := context.Background()
	writeOriginal(t, root, "broll", "copy-of-broll.mp4", "broll-bytes")
	summary, err := indexer.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DiscoveredFiles != 4 || summary.NewSources != 3 || summary.DuplicateFiles != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	sources, err := repo.ListSources(ctx)
	if err != nil || len(sources) != 3 {
		t.Fatalf("duplicate content created extra sources: %d err=%v", len(sources), err)
	}
}

func TestIndexerSkipsSymlinkedOriginals(t *testing.T) {
	root, repo, indexer := newIndexerFixture(t)
	ctx := context.Background()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.mp4")
	if err := os.WriteFile(secret, []byte("outside-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "originals", "movies", "escape.mp4")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	summary, err := indexer.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkippedEntries != 1 {
		t.Fatalf("summary=%+v, want symlink skipped", summary)
	}
	sources, err := repo.ListSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.RelativePath == "originals/movies/escape.mp4" {
			t.Fatalf("symlinked file was indexed: %+v", source)
		}
	}
}
