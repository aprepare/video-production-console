package mediacatalog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()
	root := t.TempDir()
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func testDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func testVideoSource(seed string) Source {
	return Source{
		Kind: SourceKindMovie, Subtype: SourceSubtypeVideo, Origin: SourceOriginLocal,
		RelativePath: "originals/movies/" + seed + ".mp4",
		SHA256:       testDigest(seed), SizeBytes: 100, MIMEType: "video/mp4",
		DurationMS: 10_000, Status: SourceStatusPendingProbe,
	}
}

func testImageSource(seed string) Source {
	return Source{
		Kind: SourceKindImage, Subtype: SourceSubtypePhoto, Origin: SourceOriginLocal,
		RelativePath: "originals/images/" + seed + ".png",
		SHA256:       testDigest(seed), SizeBytes: 10, MIMEType: "image/png",
		Status: SourceStatusReady,
	}
}

func TestCatalogCreatesAllSixTablesInMediaRoot(t *testing.T) {
	root := t.TempDir()
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if _, err := os.Stat(filepath.Join(root, CatalogFileName)); err != nil {
		t.Fatalf("catalog.db missing from media root: %v", err)
	}
	for _, table := range []string{"media_sources", "media_shots", "media_keyframes", "media_tags", "media_rights", "media_jobs"} {
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func TestCatalogRepeatedSHA256ImportIsIdempotent(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	first, created, err := repo.UpsertSource(ctx, testVideoSource("dup"))
	if err != nil || !created {
		t.Fatalf("first upsert created=%t err=%v", created, err)
	}
	second := testVideoSource("dup")
	second.RelativePath = "originals/broll/other-name.mp4"
	got, created, err := repo.UpsertSource(ctx, second)
	if err != nil || created {
		t.Fatalf("duplicate upsert created=%t err=%v", created, err)
	}
	if got.ID != first.ID || got.RelativePath != first.RelativePath {
		t.Fatalf("duplicate import returned new row: %+v vs %+v", got, first)
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM media_sources`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sources=%d err=%v", count, err)
	}
}

func TestCatalogRejectsInvalidSourceEnums(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	tests := []struct {
		name   string
		mutate func(*Source)
	}{
		{"unknown kind", func(s *Source) { s.Kind = "landscape" }},
		{"unknown subtype", func(s *Source) { s.Subtype = "gif" }},
		{"unknown origin", func(s *Source) { s.Origin = "torrent" }},
		{"unknown status", func(s *Source) { s.Status = "maybe" }},
		{"image with video subtype", func(s *Source) { s.Kind = SourceKindImage; s.Subtype = SourceSubtypeVideo }},
		{"movie with photo subtype", func(s *Source) { s.Subtype = SourceSubtypePhoto }},
		{"bad sha", func(s *Source) { s.SHA256 = "zz" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := testVideoSource("enum-" + test.name)
			test.mutate(&source)
			if _, _, err := repo.UpsertSource(ctx, source); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestCatalogRejectsUnsafeRelativePaths(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	for _, unsafe := range []string{
		`C:\outside\movie.mp4`,
		"/outside/movie.mp4",
		"../escape.mp4",
		"originals/../../escape.mp4",
		"",
	} {
		source := testVideoSource("path-" + unsafe)
		source.RelativePath = unsafe
		if _, _, err := repo.UpsertSource(ctx, source); err == nil {
			t.Fatalf("unsafe relative path %q accepted", unsafe)
		}
	}
}

func TestCatalogResolvePathRejectsEscapesAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := os.MkdirAll(filepath.Join(root, "originals", "broll"), 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(root, "originals", "broll", "clip.mp4")
	if err := os.WriteFile(good, []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := repo.ResolvePath("originals/broll/clip.mp4")
	if err != nil {
		t.Fatalf("valid relative path rejected: %v", err)
	}
	if !strings.EqualFold(resolved, good) {
		t.Fatalf("resolved=%q want %q", resolved, good)
	}
	for _, unsafe := range []string{
		filepath.Join(root, "originals", "broll", "clip.mp4"), // absolute
		"../console.db",
		"originals/../../etc/passwd",
	} {
		if _, err := repo.ResolvePath(unsafe); err == nil {
			t.Fatalf("unsafe path %q resolved", unsafe)
		}
	}
	if _, err := repo.ResolvePath("originals/broll"); err == nil {
		t.Fatal("directory resolved as media file")
	}
	if _, err := repo.ResolvePath("originals/broll/missing.mp4"); err == nil {
		t.Fatal("missing file resolved")
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.mp4")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "originals", "broll", "escape.mp4")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := repo.ResolvePath("originals/broll/escape.mp4"); err == nil {
		t.Fatal("symlink escape resolved")
	}
}

func TestCatalogShotRangesMustStayInsideSourceWithoutOverlap(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	source, _, err := repo.UpsertSource(ctx, testVideoSource("shots"))
	if err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name    string
		in, out int64
	}{
		{"negative in", -1, 2000},
		{"in equals out", 2000, 2000},
		{"in after out", 3000, 2000},
		{"out beyond duration", 8000, 10_001},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if _, err := repo.InsertShot(ctx, Shot{SourceID: source.ID, Ordinal: 0, SourceInMS: test.in, SourceOutMS: test.out}); err == nil {
				t.Fatal("invalid shot range accepted")
			}
		})
	}
	first, err := repo.InsertShot(ctx, Shot{SourceID: source.ID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 5000})
	if err != nil {
		t.Fatalf("valid shot rejected: %v", err)
	}
	if first.DurationMS != 5000 {
		t.Fatalf("duration=%d", first.DurationMS)
	}
	if _, err := repo.InsertShot(ctx, Shot{SourceID: source.ID, Ordinal: 1, SourceInMS: 4000, SourceOutMS: 6000}); err == nil {
		t.Fatal("overlapping shot accepted")
	}
	if _, err := repo.InsertShot(ctx, Shot{SourceID: source.ID, Ordinal: 1, SourceInMS: 5000, SourceOutMS: 10_000}); err != nil {
		t.Fatalf("adjacent shot rejected: %v", err)
	}
	if _, err := repo.InsertShot(ctx, Shot{SourceID: "missing-source", Ordinal: 0, SourceInMS: 0, SourceOutMS: 1000}); err == nil {
		t.Fatal("shot for unknown source accepted")
	}
}

// 图片没有镜头概念：每张图片允许且仅允许一行 in=0/out=0 的整图退化 shot，
// 用来承载分析结果与向量（§4.2）。
func TestCatalogImageSourcesUseSingleDegenerateShot(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	image, _, err := repo.UpsertSource(ctx, testImageSource("pic"))
	if err != nil {
		t.Fatal(err)
	}
	shot, err := repo.EnsureImageShot(ctx, image.ID)
	if err != nil {
		t.Fatalf("degenerate image shot rejected: %v", err)
	}
	if shot.SourceInMS != 0 || shot.SourceOutMS != 0 || shot.DurationMS != 0 {
		t.Fatalf("degenerate shot=%+v", shot)
	}
	again, err := repo.EnsureImageShot(ctx, image.ID)
	if err != nil || again.ID != shot.ID {
		t.Fatalf("EnsureImageShot is not idempotent: %+v err=%v", again, err)
	}
	shots, err := repo.ShotsBySource(ctx, image.ID)
	if err != nil || len(shots) != 1 {
		t.Fatalf("shots=%d err=%v", len(shots), err)
	}
	if _, err := repo.InsertShot(ctx, Shot{SourceID: image.ID, Ordinal: 1, SourceInMS: 0, SourceOutMS: 1000}); err == nil {
		t.Fatal("ranged shot accepted for image source")
	}
	video, _, err := repo.UpsertSource(ctx, testVideoSource("video-degenerate"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnsureImageShot(ctx, video.ID); err == nil {
		t.Fatal("degenerate shot accepted for video source")
	}
	if _, err := repo.InsertShot(ctx, Shot{SourceID: video.ID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 0}); err == nil {
		t.Fatal("zero-length shot accepted for video source")
	}
}

func TestCatalogStoresEmbeddingsAsLittleEndianFloat32WithMetadata(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	image, _, err := repo.UpsertSource(ctx, testImageSource("embed"))
	if err != nil {
		t.Fatal(err)
	}
	shot, err := repo.EnsureImageShot(ctx, image.ID)
	if err != nil {
		t.Fatal(err)
	}
	vector := []float32{0.25, -1.5, 3.75}
	if err := repo.SetShotEmbedding(ctx, shot.ID, "text-embedding-x", vector, "vision-v1"); err != nil {
		t.Fatal(err)
	}
	var blob []byte
	var model, version string
	var dimension int
	if err := repo.db.QueryRow(`SELECT embedding_blob, embedding_model, embedding_dimension, analysis_version FROM media_shots WHERE id=?`, shot.ID).
		Scan(&blob, &model, &dimension, &version); err != nil {
		t.Fatal(err)
	}
	if model != "text-embedding-x" || version != "vision-v1" || dimension != len(vector) {
		t.Fatalf("metadata model=%q dimension=%d version=%q", model, dimension, version)
	}
	if len(blob) != 4*len(vector) {
		t.Fatalf("blob length=%d", len(blob))
	}
	for i, want := range vector {
		got := math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
		if got != want {
			t.Fatalf("blob[%d]=%v want %v", i, got, want)
		}
	}
	embedding, err := repo.ShotEmbedding(ctx, shot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if embedding.Model != "text-embedding-x" || embedding.Dimension != 3 || embedding.AnalysisVersion != "vision-v1" {
		t.Fatalf("embedding=%+v", embedding)
	}
	for i, want := range vector {
		if embedding.Vector[i] != want {
			t.Fatalf("vector[%d]=%v", i, embedding.Vector[i])
		}
	}
	if err := repo.SetShotEmbedding(ctx, shot.ID, "", vector, "vision-v1"); err == nil {
		t.Fatal("embedding without model accepted")
	}
	if err := repo.SetShotEmbedding(ctx, shot.ID, "model", nil, "vision-v1"); err == nil {
		t.Fatal("empty embedding accepted")
	}
	if err := repo.SetShotEmbedding(ctx, shot.ID, "model", vector, ""); err == nil {
		t.Fatal("embedding without analysis version accepted")
	}
}

func TestCatalogKeyframesTagsAndRightsEnforceUniqueKeys(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	source, _, err := repo.UpsertSource(ctx, testVideoSource("meta"))
	if err != nil {
		t.Fatal(err)
	}
	shot, err := repo.InsertShot(ctx, Shot{SourceID: source.ID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 4000})
	if err != nil {
		t.Fatal(err)
	}
	keyframe := Keyframe{
		ShotID: shot.ID, Ordinal: 1, RelativePath: "derived/keyframes/" + source.SHA256 + "/" + shot.ID + "/01.jpg",
		AtMS: 800, Width: 512, Height: 288, SHA256: testDigest("kf"),
	}
	if _, err := repo.InsertKeyframe(ctx, keyframe); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InsertKeyframe(ctx, keyframe); err == nil {
		t.Fatal("duplicate keyframe ordinal accepted")
	}
	bad := keyframe
	bad.Ordinal = 2
	bad.RelativePath = `C:\evil\frame.jpg`
	if _, err := repo.InsertKeyframe(ctx, bad); err == nil {
		t.Fatal("absolute keyframe path accepted")
	}
	tags := []Tag{
		{Namespace: "topic", Value: "bank", Confidence: 0.9},
		{Namespace: "topic", Value: "bank", Confidence: 0.4},
		{Namespace: "mood", Value: "tense", Confidence: 0.8},
	}
	if err := repo.UpsertTags(ctx, shot.ID, tags); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.TagsByShot(ctx, shot.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("tags=%+v err=%v", stored, err)
	}
	rights := Rights{
		SourceID: source.ID, SourceURL: "https://example.test/video", Creator: "author",
		LicenseCode: "CC-BY", LicenseURL: "https://example.test/license", Attribution: "author / example",
		RightsNotes: "user supplied",
	}
	if err := repo.UpsertRights(ctx, rights); err != nil {
		t.Fatal(err)
	}
	rights.RightsNotes = "updated"
	if err := repo.UpsertRights(ctx, rights); err != nil {
		t.Fatal(err)
	}
	got, err := repo.RightsBySource(ctx, source.ID)
	if err != nil || got.RightsNotes != "updated" || got.LicenseCode != "CC-BY" {
		t.Fatalf("rights=%+v err=%v", got, err)
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM media_rights WHERE source_id=?`, source.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rights rows=%d err=%v", count, err)
	}
}

func TestCatalogJobsTrackPhasesPerSource(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	source, _, err := repo.UpsertSource(ctx, testVideoSource("jobs"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := repo.EnsureJob(ctx, source.ID, PhaseProbe, 3)
	if err != nil || job.Status != JobPending {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	again, err := repo.EnsureJob(ctx, source.ID, PhaseProbe, 3)
	if err != nil || again.ID != job.ID {
		t.Fatalf("EnsureJob is not idempotent: %+v err=%v", again, err)
	}
	if err := repo.StartJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.FailJob(ctx, job.ID, "probe_failed", "boom"); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
	if err != nil || failed.Status != JobFailed || failed.ErrorCode != "probe_failed" {
		t.Fatalf("failed job=%+v err=%v", failed, err)
	}
	if err := repo.CompleteJob(ctx, job.ID, 3); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
	if err != nil || completed.Status != JobCompleted || completed.CompletedUnits != 3 {
		t.Fatalf("completed job=%+v err=%v", completed, err)
	}
	if _, err := repo.EnsureJob(ctx, "missing-source", PhaseProbe, 1); err == nil {
		t.Fatal("job for unknown source accepted")
	}
	if _, err := repo.JobBySourcePhase(ctx, source.ID, "unknown-phase"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("unknown phase error=%v", err)
	}
}
