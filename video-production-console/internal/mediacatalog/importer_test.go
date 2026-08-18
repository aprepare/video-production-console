package mediacatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-production-console/internal/imageproject"
)

func newImporterFixture(t *testing.T) (string, *Repository, *Importer) {
	t.Helper()
	root := t.TempDir()
	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return root, repo, NewImporter(repo)
}

func testPNGBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		canvas.Set(x, 0, color.RGBA{R: uint8(x % 256), A: 255})
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// 最小 mp4 头：boxSize=16、"ftyp"、brand "mp42"，http.DetectContentType 识别为 video/mp4。
func testMP4Bytes() []byte {
	header := []byte{0x00, 0x00, 0x00, 0x10, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00}
	return append(header, bytes.Repeat([]byte{0x00}, 64)...)
}

func countRegularFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() && entry.Name() != CatalogFileName {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestImporterLocalImageCreatesReadySourceAndDegenerateShot(t *testing.T) {
	root, repo, importer := newImporterFixture(t)
	ctx := context.Background()
	payload := testPNGBytes(t, 32, 16)
	source, created, err := importer.Import(ctx, ImportRequest{
		Kind:          SourceKindImage,
		Bytes:         payload,
		SuggestedName: "家庭账本.PNG",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected a new source")
	}
	if source.Kind != SourceKindImage || source.Subtype != SourceSubtypePhoto || source.Origin != SourceOriginLocal {
		t.Fatalf("source=%+v", source)
	}
	if source.Status != SourceStatusReady || source.MIMEType != "image/png" {
		t.Fatalf("source status/mime=%+v", source)
	}
	if source.Width != 32 || source.Height != 16 {
		t.Fatalf("source dimensions=%dx%d", source.Width, source.Height)
	}
	if !strings.HasPrefix(source.RelativePath, "originals/images/") {
		t.Fatalf("relative path=%q", source.RelativePath)
	}
	digest := sha256.Sum256(payload)
	if source.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("sha mismatch: %s", source.SHA256)
	}
	resolved, err := repo.ResolvePath(source.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(resolved)
	if err != nil || !bytes.Equal(written, payload) {
		t.Fatalf("written bytes mismatch err=%v", err)
	}
	shots, err := repo.ShotsBySource(ctx, source.ID)
	if err != nil || len(shots) != 1 || shots[0].SourceInMS != 0 || shots[0].SourceOutMS != 0 {
		t.Fatalf("image shots=%+v err=%v", shots, err)
	}
	// 导入完成后不留 .part 中间文件。
	entries, err := os.ReadDir(filepath.Dir(resolved))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".part") {
			t.Fatalf("leftover part file %q", entry.Name())
		}
	}
	if countRegularFiles(t, root) != 1 {
		t.Fatalf("expected exactly one media file, got %d", countRegularFiles(t, root))
	}
}

func TestImporterDeduplicatesIdenticalBytes(t *testing.T) {
	root, _, importer := newImporterFixture(t)
	ctx := context.Background()
	payload := testPNGBytes(t, 8, 8)
	first, created, err := importer.Import(ctx, ImportRequest{Kind: SourceKindImage, Bytes: payload, SuggestedName: "one.png"})
	if err != nil || !created {
		t.Fatalf("first import created=%t err=%v", created, err)
	}
	second, createdAgain, err := importer.Import(ctx, ImportRequest{Kind: SourceKindImage, Bytes: payload, SuggestedName: "two.png"})
	if err != nil {
		t.Fatal(err)
	}
	if createdAgain {
		t.Fatal("duplicate content must not create a second source")
	}
	if second.ID != first.ID || second.RelativePath != first.RelativePath {
		t.Fatalf("duplicate returned different source: %+v vs %+v", second, first)
	}
	if files := countRegularFiles(t, root); files != 1 {
		t.Fatalf("duplicate content must not write a second file, got %d", files)
	}
}

func TestImporterRejectsUnsupportedMIME(t *testing.T) {
	_, _, importer := newImporterFixture(t)
	_, _, err := importer.Import(context.Background(), ImportRequest{
		Kind:  SourceKindImage,
		Bytes: []byte("plain text is not media"),
	})
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("err=%v", err)
	}
	// 视频字节不允许落成 image 源。
	_, _, err = importer.Import(context.Background(), ImportRequest{Kind: SourceKindImage, Bytes: testMP4Bytes()})
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("video bytes for image kind err=%v", err)
	}
}

func TestImporterGeneratedImageRecordsProvenanceWithoutSecrets(t *testing.T) {
	_, repo, importer := newImporterFixture(t)
	ctx := context.Background()
	result := imageproject.GenerateResult{Bytes: testPNGBytes(t, 24, 24), MIMEType: "image/png", Width: 24, Height: 24}
	prompt := "深夜家庭餐桌上的账本与空钱包，冷色调"
	source, created, err := importer.ImportGeneratedImage(ctx, GeneratedImage{
		Provider: "openai",
		Model:    "gpt-image-1",
		Prompt:   prompt,
		Bytes:    result.Bytes,
		MIMEType: result.MIMEType,
		Width:    result.Width,
		Height:   result.Height,
	})
	if err != nil || !created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	if source.Origin != SourceOriginGenerated || source.Subtype != SourceSubtypeGenerated || source.Kind != SourceKindImage {
		t.Fatalf("source=%+v", source)
	}
	if !strings.HasPrefix(source.RelativePath, "derived/generated/openai/") {
		t.Fatalf("relative path=%q", source.RelativePath)
	}
	rights, err := repo.RightsBySource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	promptDigest := sha256.Sum256([]byte(prompt))
	notes := rights.RightsNotes
	if !strings.Contains(notes, "provider=openai") || !strings.Contains(notes, "model=gpt-image-1") {
		t.Fatalf("notes missing provenance: %q", notes)
	}
	if !strings.Contains(notes, hex.EncodeToString(promptDigest[:])) {
		t.Fatalf("notes missing prompt hash: %q", notes)
	}
	if strings.Contains(notes, prompt) {
		t.Fatalf("notes must not contain the raw prompt: %q", notes)
	}
	if rights.RetrievedAt.IsZero() {
		t.Fatal("generated rights must record the generation time")
	}
	if Publishability(rights) != PublishabilityLocalDraftOnly {
		t.Fatalf("generated media must stay local_draft_only, got %q", Publishability(rights))
	}
	if strings.Contains(notes, "sk-") || strings.Contains(notes, "api_key") {
		t.Fatalf("notes leak credentials: %q", notes)
	}
	shots, err := repo.ShotsBySource(ctx, source.ID)
	if err != nil || len(shots) != 1 {
		t.Fatalf("generated image shot missing: %+v err=%v", shots, err)
	}
}

func TestImporterExternalRequiresRightsMetadata(t *testing.T) {
	_, _, importer := newImporterFixture(t)
	_, _, err := importer.Import(context.Background(), ImportRequest{
		Kind:   SourceKindImage,
		Origin: SourceOriginPexels,
		Bytes:  testPNGBytes(t, 4, 4),
		Rights: Rights{Creator: "someone"},
	})
	if !errors.Is(err, ErrRightsMetadataIncomplete) {
		t.Fatalf("err=%v", err)
	}
}

func TestImporterExternalUnknownLicenseStaysLocalDraftOnly(t *testing.T) {
	_, repo, importer := newImporterFixture(t)
	ctx := context.Background()
	source, _, err := importer.Import(ctx, ImportRequest{
		Kind:   SourceKindImage,
		Origin: SourceOriginPixabay,
		Bytes:  testPNGBytes(t, 6, 6),
		Rights: Rights{
			SourceURL:   "https://pixabay.com/photos/example-1/",
			Creator:     "photographer",
			RetrievedAt: time.Now(),
			RightsNotes: "retrieved via search",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rights, err := repo.RightsBySource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if Publishability(rights) != PublishabilityLocalDraftOnly {
		t.Fatalf("unknown license must be local_draft_only, notes=%q", rights.RightsNotes)
	}
	if strings.Contains(rights.RightsNotes, "publishability=publishable") {
		t.Fatalf("importer must never mark media publishable: %q", rights.RightsNotes)
	}
	if !strings.Contains(rights.RightsNotes, "retrieved via search") {
		t.Fatalf("caller notes must be preserved: %q", rights.RightsNotes)
	}
}

func TestImporterExternalKnownLicenseKeepsMetadata(t *testing.T) {
	_, repo, importer := newImporterFixture(t)
	ctx := context.Background()
	retrieved := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	source, _, err := importer.Import(ctx, ImportRequest{
		Kind:   SourceKindBroll,
		Origin: SourceOriginPexels,
		Bytes:  testMP4Bytes(),
		Rights: Rights{
			SourceURL:   "https://www.pexels.com/video/example-2/",
			Creator:     "videographer",
			LicenseCode: "pexels",
			LicenseURL:  "https://www.pexels.com/license/",
			Attribution: "Video by videographer on Pexels",
			RetrievedAt: retrieved,
			RightsNotes: "downloadable is not a blanket license",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.Status != SourceStatusPendingProbe || source.Subtype != SourceSubtypeVideo {
		t.Fatalf("external video source=%+v", source)
	}
	if !strings.HasPrefix(source.RelativePath, "originals/broll/") {
		t.Fatalf("relative path=%q", source.RelativePath)
	}
	probe, err := repo.JobBySourcePhase(ctx, source.ID, PhaseProbe)
	if err != nil || probe.Status != JobPending {
		t.Fatalf("probe job=%+v err=%v", probe, err)
	}
	rights, err := repo.RightsBySource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rights.SourceURL == "" || rights.Creator == "" || rights.LicenseCode != "pexels" ||
		rights.LicenseURL == "" || rights.Attribution == "" || rights.RetrievedAt.IsZero() {
		t.Fatalf("rights metadata incomplete: %+v", rights)
	}
	if Publishability(rights) == "publishable" {
		t.Fatal("importer must never mark media publishable")
	}
}

func TestImporterGeneratedRequiresProvider(t *testing.T) {
	_, _, importer := newImporterFixture(t)
	_, _, err := importer.Import(context.Background(), ImportRequest{
		Kind:   SourceKindImage,
		Origin: SourceOriginGenerated,
		Bytes:  testPNGBytes(t, 4, 4),
	})
	if err == nil {
		t.Fatal("generated imports must name their provider")
	}
}

func TestImporterServerGeneratesSafeFileNames(t *testing.T) {
	_, _, importer := newImporterFixture(t)
	source, _, err := importer.Import(context.Background(), ImportRequest{
		Kind:          SourceKindImage,
		Bytes:         testPNGBytes(t, 5, 5),
		SuggestedName: "../../evil/../name?.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRelativePath(source.RelativePath); err != nil {
		t.Fatalf("stored path unsafe: %v", err)
	}
	if strings.Contains(source.RelativePath, "..") || strings.Contains(source.RelativePath, "?") {
		t.Fatalf("suggested name leaked into path: %q", source.RelativePath)
	}
}
