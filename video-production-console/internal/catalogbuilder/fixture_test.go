package catalogbuilder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/mediacatalog"
)

func writeTestJPEG(t *testing.T, path string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for x := 0; x < 32; x++ {
		for y := 0; y < 24; y++ {
			img.Set(x, y, color.RGBA{R: 40, G: 80, B: uint8(x + y), A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func seedAnalyzedMovie(t *testing.T, root, seed, embedModel string) (mediacatalog.Source, mediacatalog.Shot) {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(root, "originals", "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("movie-" + seed)
	if err := os.WriteFile(filepath.Join(root, "originals", "movies", seed+".mp4"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := mediacatalog.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	source, _, err := repo.UpsertSource(ctx, mediacatalog.Source{
		Kind: mediacatalog.SourceKindMovie, Subtype: mediacatalog.SourceSubtypeVideo,
		Origin: mediacatalog.SourceOriginLocal, RelativePath: "originals/movies/" + seed + ".mp4",
		SHA256: digestOf(original), SizeBytes: int64(len(original)), MIMEType: "video/mp4",
		Width: 1920, Height: 1080, DurationMS: 10_000, FPS: 24,
		Status: mediacatalog.SourceStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	shot, err := repo.InsertShot(ctx, mediacatalog.Shot{
		SourceID: source.ID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 4000,
		AnalysisStatus: mediacatalog.AnalysisPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	rel := "derived/keyframes/" + source.SHA256 + "/" + shot.ID + "/01.jpg"
	jpegBytes := writeTestJPEG(t, filepath.Join(root, filepath.FromSlash(rel)))
	if _, err := repo.InsertKeyframe(ctx, mediacatalog.Keyframe{
		ShotID: shot.ID, Ordinal: 0, RelativePath: rel, AtMS: 800,
		Width: 32, Height: 24, SHA256: digestOf(jpegBytes),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetShotAnalysis(ctx, shot.ID, mediacatalog.ShotAnalysis{
		Summary: "a person reviews documents at a desk", Mood: "calm", Setting: "office",
		PeopleCount: 1, MotionLevel: "low",
		Tags: []mediacatalog.TagScore{{Value: "desk", Confidence: 0.9}},
	}, mediacatalog.AnalysisVersion); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertTags(ctx, shot.ID, []mediacatalog.Tag{{
		ShotID: shot.ID, Namespace: "vision", Value: "desk", Confidence: 0.9,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetShotEmbedding(ctx, shot.ID, embedModel, []float32{0.1, 0.2, 0.3}, mediacatalog.AnalysisVersion); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertRights(ctx, mediacatalog.Rights{SourceID: source.ID, LicenseCode: "local"}); err != nil {
		t.Fatal(err)
	}
	return source, shot
}

func seedPendingMovie(t *testing.T, root, seed string) mediacatalog.Source {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(root, "originals", "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("movie-" + seed)
	if err := os.WriteFile(filepath.Join(root, "originals", "movies", seed+".mp4"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := mediacatalog.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	source, _, err := repo.UpsertSource(ctx, mediacatalog.Source{
		Kind: mediacatalog.SourceKindMovie, Subtype: mediacatalog.SourceSubtypeVideo,
		Origin: mediacatalog.SourceOriginLocal, RelativePath: "originals/movies/" + seed + ".mp4",
		SHA256: digestOf(original), SizeBytes: int64(len(original)), MIMEType: "video/mp4",
		Width: 1920, Height: 1080, DurationMS: 10_000, FPS: 24,
		Status: mediacatalog.SourceStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	shot, err := repo.InsertShot(ctx, mediacatalog.Shot{
		SourceID: source.ID, Ordinal: 0, SourceInMS: 0, SourceOutMS: 4000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rel := fmt.Sprintf("derived/keyframes/%s/%s/%02d.jpg", source.SHA256, shot.ID, i+1)
		jpegBytes := writeTestJPEG(t, filepath.Join(root, filepath.FromSlash(rel)))
		if _, err := repo.InsertKeyframe(ctx, mediacatalog.Keyframe{
			ShotID: shot.ID, Ordinal: i, RelativePath: rel, AtMS: int64(800 * (i + 1)),
			Width: 32, Height: 24, SHA256: digestOf(jpegBytes),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range []string{mediacatalog.PhaseIngest, mediacatalog.PhaseProbe} {
		job, err := repo.EnsureJob(ctx, source.ID, phase, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.CompleteJob(ctx, job.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	return source
}
