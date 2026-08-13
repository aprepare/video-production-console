package mediacatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

// PublishabilityLocalDraftOnly marks media whose license is unknown or
// non-transferable: it may appear in local drafts but is never publishable.
const PublishabilityLocalDraftOnly = "local_draft_only"

const publishabilityNoteMarker = "publishability=" + PublishabilityLocalDraftOnly

var (
	ErrUnsupportedMediaType     = errors.New("unsupported media type")
	ErrRightsMetadataIncomplete = errors.New("rights metadata is incomplete")
)

// License codes whose terms we recorded verbatim from the provider. Anything
// else (including empty) is treated as unknown and stays local_draft_only.
// Known licenses are still never marked "publishable" by the importer.
var knownLicenseCodes = map[string]struct{}{
	"pexels": {}, "pixabay": {}, "cc0": {}, "cc-by": {}, "cc-by-sa": {}, "public-domain": {},
}

var supportedImageImports = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

var supportedVideoImports = map[string]string{
	"video/mp4":  ".mp4",
	"video/webm": ".webm",
}

// ImportRequest is the single entry point shared by local files, generated
// images, and external downloads. Bytes are validated (MIME + dimensions),
// deduplicated by SHA-256, written atomically below the media root, and
// registered through Repository.UpsertSource.
type ImportRequest struct {
	Kind SourceKind
	// Subtype defaults to photo for images (generated for generated origin)
	// and video otherwise.
	Subtype SourceSubtype
	// Origin defaults to local.
	Origin SourceOrigin
	// Provider names the derived/generated/{provider} directory and is
	// required when Origin is generated.
	Provider      string
	Bytes         []byte
	SuggestedName string
	Prompt        string
	Rights        Rights
}

// GeneratedImage adapts an image-generation result (for example
// imageproject.GenerateResult) into an ImportRequest with provenance notes.
type GeneratedImage struct {
	Provider string
	Model    string
	Prompt   string
	Bytes    []byte
	MIMEType string
	Width    int
	Height   int
}

// Importer serializes writes so concurrent imports cannot interleave disk and
// catalog updates within this process.
type Importer struct {
	repo *Repository
	mu   sync.Mutex
	now  func() time.Time
}

func NewImporter(repo *Repository) *Importer {
	return &Importer{repo: repo, now: time.Now}
}

// ImportGeneratedImage funnels a generated picture through Import. Rights
// notes record provider, model, prompt hash, and generation time; the raw
// prompt and any API credentials are never persisted.
func (im *Importer) ImportGeneratedImage(ctx context.Context, generated GeneratedImage) (Source, bool, error) {
	provider := strings.TrimSpace(generated.Provider)
	if provider == "" {
		return Source{}, false, fmt.Errorf("%w: generated imports require a provider name", ErrInvalidValue)
	}
	promptDigest := sha256.Sum256([]byte(generated.Prompt))
	generatedAt := im.now().UTC()
	notes := fmt.Sprintf("provider=%s; model=%s; prompt_sha256=%s; generated_at=%s",
		provider, strings.TrimSpace(generated.Model), hex.EncodeToString(promptDigest[:]),
		generatedAt.Format(time.RFC3339))
	return im.Import(ctx, ImportRequest{
		Kind:     SourceKindImage,
		Subtype:  SourceSubtypeGenerated,
		Origin:   SourceOriginGenerated,
		Provider: provider,
		Bytes:    generated.Bytes,
		Prompt:   generated.Prompt,
		Rights:   Rights{RetrievedAt: generatedAt, RightsNotes: notes},
	})
}

// Import validates, deduplicates, writes, and registers one media payload.
// The returned bool reports whether a new source row was created; duplicate
// content returns the already-stored source without writing a second file.
func (im *Importer) Import(ctx context.Context, request ImportRequest) (Source, bool, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	normalized, extension, err := normalizeImportRequest(request)
	if err != nil {
		return Source{}, false, err
	}

	digest := sha256.Sum256(normalized.Bytes)
	sha := hex.EncodeToString(digest[:])
	if existing, err := im.repo.SourceBySHA256(ctx, sha); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrSourceNotFound) {
		return Source{}, false, err
	}

	relative := importTargetPath(normalized, sha, extension)
	if err := im.writeAtomically(relative, normalized.Bytes); err != nil {
		return Source{}, false, err
	}

	source, created, err := im.repo.UpsertSource(ctx, Source{
		Kind:         normalized.Kind,
		Subtype:      normalized.Subtype,
		Origin:       normalized.Origin,
		RelativePath: relative,
		SHA256:       sha,
		SizeBytes:    int64(len(normalized.Bytes)),
		MIMEType:     normalized.mimeType,
		Width:        normalized.width,
		Height:       normalized.height,
		Status:       normalized.status,
	})
	if err != nil {
		return Source{}, false, err
	}
	if err := im.registerRights(ctx, source, normalized); err != nil {
		return Source{}, false, err
	}
	if err := im.registerJobs(ctx, source); err != nil {
		return Source{}, false, err
	}
	return source, created, nil
}

type normalizedImport struct {
	ImportRequest
	mimeType string
	width    int
	height   int
	status   SourceStatus
}

func normalizeImportRequest(request ImportRequest) (normalizedImport, string, error) {
	normalized := normalizedImport{ImportRequest: request}
	if len(request.Bytes) == 0 {
		return normalized, "", fmt.Errorf("%w: import payload is empty", ErrInvalidValue)
	}
	if normalized.Origin == "" {
		normalized.Origin = SourceOriginLocal
	}
	switch normalized.Kind {
	case SourceKindImage, SourceKindBroll, SourceKindMovie:
	default:
		return normalized, "", fmt.Errorf("%w: kind %q", ErrInvalidValue, normalized.Kind)
	}
	if normalized.Origin == SourceOriginGenerated && strings.TrimSpace(normalized.Provider) == "" {
		return normalized, "", fmt.Errorf("%w: generated imports require a provider name", ErrInvalidValue)
	}
	if err := validateImportRights(normalized.Origin, normalized.Rights); err != nil {
		return normalized, "", err
	}

	sniffed := http.DetectContentType(request.Bytes)
	sniffed = strings.ToLower(strings.TrimSpace(strings.SplitN(sniffed, ";", 2)[0]))
	if normalized.Kind == SourceKindImage {
		extension, ok := supportedImageImports[sniffed]
		if !ok {
			return normalized, "", fmt.Errorf("%w: %q is not a supported image type", ErrUnsupportedMediaType, sniffed)
		}
		configuration, _, err := image.DecodeConfig(bytes.NewReader(request.Bytes))
		if err != nil || configuration.Width < 1 || configuration.Height < 1 {
			return normalized, "", fmt.Errorf("%w: image payload could not be decoded", ErrUnsupportedMediaType)
		}
		normalized.width, normalized.height = configuration.Width, configuration.Height
		if normalized.Subtype == "" {
			normalized.Subtype = SourceSubtypePhoto
			if normalized.Origin == SourceOriginGenerated {
				normalized.Subtype = SourceSubtypeGenerated
			}
		}
		normalized.mimeType = sniffed
		normalized.status = SourceStatusReady
		return normalized, extension, nil
	}
	extension, ok := supportedVideoImports[sniffed]
	if !ok {
		return normalized, "", fmt.Errorf("%w: %q is not a supported video type", ErrUnsupportedMediaType, sniffed)
	}
	if normalized.Subtype == "" {
		normalized.Subtype = SourceSubtypeVideo
	}
	normalized.mimeType = sniffed
	normalized.status = SourceStatusPendingProbe
	return normalized, extension, nil
}

func validateImportRights(origin SourceOrigin, rights Rights) error {
	if origin != SourceOriginPexels && origin != SourceOriginPixabay {
		return nil
	}
	if strings.TrimSpace(rights.SourceURL) == "" || strings.TrimSpace(rights.Creator) == "" || rights.RetrievedAt.IsZero() {
		return fmt.Errorf("%w: external imports require source_url, creator, and retrieved_at", ErrRightsMetadataIncomplete)
	}
	return nil
}

// importTargetPath builds a server-generated relative path. Suggested names
// only contribute a sanitized stem; URL paths and user input never become
// filesystem structure.
func importTargetPath(normalized normalizedImport, sha, extension string) string {
	name := sanitizedStem(normalized.SuggestedName) + "-" + sha[:12] + extension
	if normalized.Origin == SourceOriginGenerated {
		return "derived/generated/" + sanitizedStem(normalized.Provider) + "/" + name
	}
	switch normalized.Kind {
	case SourceKindMovie:
		return "originals/movies/" + name
	case SourceKindBroll:
		return "originals/broll/" + name
	default:
		return "originals/images/" + name
	}
}

func sanitizedStem(value string) string {
	value = strings.TrimSpace(value)
	if extension := filepath.Ext(value); extension != "" {
		value = strings.TrimSuffix(value, extension)
	}
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-', char == '_':
			builder.WriteRune(char)
		case unicode.IsSpace(char):
			builder.WriteRune('-')
		}
	}
	stem := strings.Trim(builder.String(), "-_")
	if stem == "" {
		return "asset"
	}
	if len(stem) > 40 {
		stem = stem[:40]
	}
	return stem
}

// writeAtomically writes the payload to a unique .part file and renames it
// into place so interrupted imports never leave half-written media behind.
func (im *Importer) writeAtomically(relative string, payload []byte) error {
	if err := ValidateRelativePath(relative); err != nil {
		return err
	}
	target := filepath.Join(im.repo.Root(), filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create import directory: %w", err)
	}
	if _, err := os.Lstat(target); err == nil {
		// The SHA-named target already exists (for example after a crash
		// between file write and catalog registration); its content is
		// identical by construction.
		return nil
	}
	part := target + "." + uuid.NewString() + ".part"
	if err := os.WriteFile(part, payload, 0o600); err != nil {
		return fmt.Errorf("write import payload: %w", err)
	}
	if err := os.Rename(part, target); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("finalize import payload: %w", err)
	}
	return nil
}

func (im *Importer) registerRights(ctx context.Context, source Source, normalized normalizedImport) error {
	rights := normalized.Rights
	external := normalized.Origin == SourceOriginPexels || normalized.Origin == SourceOriginPixabay
	generated := normalized.Origin == SourceOriginGenerated
	if !external && !generated && rightsIsEmpty(rights) {
		return nil
	}
	rights.SourceID = source.ID
	if _, known := knownLicenseCodes[strings.ToLower(strings.TrimSpace(rights.LicenseCode))]; !known || generated {
		if !strings.Contains(rights.RightsNotes, publishabilityNoteMarker) {
			if rights.RightsNotes != "" {
				rights.RightsNotes += "; "
			}
			rights.RightsNotes += publishabilityNoteMarker
		}
	}
	return im.repo.UpsertRights(ctx, rights)
}

func rightsIsEmpty(rights Rights) bool {
	return rights.SourceURL == "" && rights.Creator == "" && rights.LicenseCode == "" &&
		rights.LicenseURL == "" && rights.Attribution == "" && rights.RetrievedAt.IsZero() &&
		rights.RightsNotes == ""
}

// registerJobs mirrors the indexer's per-source state machine: ingest
// completes immediately, images get their degenerate shot, and videos park a
// pending probe job for the FFmpeg pipeline.
func (im *Importer) registerJobs(ctx context.Context, source Source) error {
	ingest, err := im.repo.EnsureJob(ctx, source.ID, PhaseIngest, 1)
	if err != nil {
		return err
	}
	if ingest.Status != JobCompleted {
		if err := im.repo.CompleteJob(ctx, ingest.ID, 1); err != nil {
			return err
		}
	}
	if source.Kind != SourceKindImage {
		_, err := im.repo.EnsureJob(ctx, source.ID, PhaseProbe, 1)
		return err
	}
	imageShot, err := im.repo.EnsureJob(ctx, source.ID, PhaseImageShot, 1)
	if err != nil {
		return err
	}
	if imageShot.Status == JobCompleted {
		return nil
	}
	if err := im.repo.StartJob(ctx, imageShot.ID); err != nil {
		return err
	}
	if _, err := im.repo.EnsureImageShot(ctx, source.ID); err != nil {
		if failErr := im.repo.FailJob(ctx, imageShot.ID, "image_shot_failed", err.Error()); failErr != nil {
			return failErr
		}
		return err
	}
	return im.repo.CompleteJob(ctx, imageShot.ID, 1)
}

// Publishability reports the publishability marker recorded in rights notes.
// The importer only ever records local_draft_only; it never claims media is
// publishable.
func Publishability(rights Rights) string {
	if strings.Contains(rights.RightsNotes, publishabilityNoteMarker) {
		return PublishabilityLocalDraftOnly
	}
	return ""
}
