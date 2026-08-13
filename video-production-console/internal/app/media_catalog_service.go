package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/httpapi"
	"video-production-console/internal/mediacatalog"
	consoleSettings "video-production-console/internal/settings"
)

// mediaCatalogService adapts the mediacatalog Repository/Indexer surface to
// the narrow httpapi.CatalogService interface. It is the only place outside
// internal/mediacatalog that opens catalog.db, and it enforces the single
// global indexing job the API promises.
type mediaCatalogService struct {
	settings *consoleSettings.Service

	mu           sync.Mutex
	repo         *mediacatalog.Repository
	repoRoot     string
	activeJob    *httpapi.CatalogActiveJob
	lastRunError string
}

func newMediaCatalogService(settings *consoleSettings.Service) *mediaCatalogService {
	return &mediaCatalogService{settings: settings}
}

// repository resolves the configured media root and returns the (cached)
// catalog repository. Every configuration problem collapses into
// ErrCatalogNotConfigured so the handler can answer with one actionable code.
func (s *mediaCatalogService) repository(ctx context.Context) (*mediacatalog.Repository, error) {
	runtime, err := s.settings.Runtime(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: settings unavailable", httpapi.ErrCatalogNotConfigured)
	}
	root := strings.TrimSpace(runtime.MediaRoot)
	if strings.TrimSpace(runtime.MediaCatalogPath) == "" || root == "" {
		return nil, fmt.Errorf("%w: media_catalog_path or media_root is empty", httpapi.ErrCatalogNotConfigured)
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: media root is not an existing directory", httpapi.ErrCatalogNotConfigured)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repo != nil && strings.EqualFold(s.repoRoot, root) {
		return s.repo, nil
	}
	repo, err := mediacatalog.Open(root)
	if err != nil {
		return nil, fmt.Errorf("%w: catalog database could not be opened", httpapi.ErrCatalogNotConfigured)
	}
	if s.repo != nil {
		_ = s.repo.Close()
	}
	s.repo, s.repoRoot = repo, root
	return repo, nil
}

func (s *mediaCatalogService) Status(ctx context.Context) (httpapi.CatalogStatus, error) {
	repo, err := s.repository(ctx)
	if err != nil {
		return httpapi.CatalogStatus{}, err
	}
	return s.snapshot(ctx, repo)
}

func (s *mediaCatalogService) snapshot(ctx context.Context, repo *mediacatalog.Repository) (httpapi.CatalogStatus, error) {
	sources, err := repo.ListSources(ctx)
	if err != nil {
		return httpapi.CatalogStatus{}, err
	}
	status := httpapi.CatalogStatus{Warnings: []httpapi.CatalogWarning{}}
	status.Counts.Sources = len(sources)
	warningCounts := map[string]int{}
	failedSources := 0
	for _, source := range sources {
		if source.Status == mediacatalog.SourceStatusFailed {
			failedSources++
			code := source.ErrorCode
			if code == "" {
				code = "source_failed"
			}
			warningCounts[code]++
		}
		shots, err := repo.ShotsBySource(ctx, source.ID)
		if err != nil {
			return httpapi.CatalogStatus{}, err
		}
		status.Counts.Shots += len(shots)
		for _, shot := range shots {
			switch shot.AnalysisStatus {
			case mediacatalog.AnalysisCompleted:
				status.Counts.ReadyShots++
			case mediacatalog.AnalysisFailed:
				status.Counts.FailedShots++
			}
		}
	}
	s.mu.Lock()
	activeJob := s.activeJob
	lastRunError := s.lastRunError
	s.mu.Unlock()
	if lastRunError != "" {
		warningCounts[lastRunError]++
	}
	for _, code := range sortedWarningCodes(warningCounts) {
		status.Warnings = append(status.Warnings, httpapi.CatalogWarning{Code: code, Count: warningCounts[code]})
	}
	switch {
	case activeJob != nil:
		job := *activeJob
		status.ActiveJob = &job
		status.State = catalogPhaseState(job.Phase)
	case len(sources) == 0:
		status.State = "idle"
	case failedSources == len(sources):
		status.State = "failed"
	case lastRunError != "" || failedSources > 0 || status.Counts.FailedShots > 0:
		status.State = "degraded"
	default:
		status.State = "ready"
	}
	return status, nil
}

func sortedWarningCodes(counts map[string]int) []string {
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// catalogPhaseState maps indexing phases onto the coarse status states the
// panel understands.
func catalogPhaseState(phase string) string {
	switch phase {
	case mediacatalog.PhaseProbe:
		return "extracting"
	case "analysis", "analyzing":
		return "analyzing"
	default:
		return "scanning"
	}
}

func (s *mediaCatalogService) StartIndex(ctx context.Context) (httpapi.CatalogStatus, error) {
	repo, err := s.repository(ctx)
	if err != nil {
		return httpapi.CatalogStatus{}, err
	}
	s.mu.Lock()
	if s.activeJob != nil {
		s.mu.Unlock()
		return httpapi.CatalogStatus{}, httpapi.ErrCatalogJobActive
	}
	job := &httpapi.CatalogActiveJob{ID: uuid.NewString(), Phase: mediacatalog.PhaseIngest}
	s.activeJob = job
	s.mu.Unlock()

	go s.runIndex(repo)
	return s.snapshot(ctx, repo)
}

// runIndex executes the whole build outside the request. The request context
// must not cancel a library build that other clients can already observe.
func (s *mediaCatalogService) runIndex(repo *mediacatalog.Repository) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	_, err := mediacatalog.NewIndexer(repo).Run(ctx)
	s.mu.Lock()
	s.activeJob = nil
	if err != nil {
		s.lastRunError = "index_failed"
	} else {
		s.lastRunError = ""
	}
	s.mu.Unlock()
}

func (s *mediaCatalogService) Sources(ctx context.Context, filter httpapi.CatalogSourceFilter) (httpapi.CatalogSourcesPage, error) {
	repo, err := s.repository(ctx)
	if err != nil {
		return httpapi.CatalogSourcesPage{}, err
	}
	sources, err := repo.ListSources(ctx)
	if err != nil {
		return httpapi.CatalogSourcesPage{}, err
	}
	page := httpapi.CatalogSourcesPage{Sources: []httpapi.CatalogSource{}}
	// The cursor is the last source ID of the previous page; entries up to and
	// including it are skipped. ListSources orders by relative path, so the
	// position is found by ID rather than assumed.
	skipping := filter.Cursor != ""
	for _, source := range sources {
		if skipping {
			if source.ID == filter.Cursor {
				skipping = false
			}
			continue
		}
		if filter.Kind != "" && string(source.Kind) != filter.Kind {
			continue
		}
		if filter.Status != "" && string(source.Status) != filter.Status {
			continue
		}
		page.Sources = append(page.Sources, catalogSourceView(source))
		if len(page.Sources) == catalogSourcesPageSize {
			page.NextCursor = source.ID
			break
		}
	}
	return page, nil
}

const catalogSourcesPageSize = 200

func (s *mediaCatalogService) RetrySource(ctx context.Context, id string) (httpapi.CatalogStatus, error) {
	repo, err := s.repository(ctx)
	if err != nil {
		return httpapi.CatalogStatus{}, err
	}
	if _, err := repo.SourceByID(ctx, id); errors.Is(err, mediacatalog.ErrSourceNotFound) {
		return httpapi.CatalogStatus{}, httpapi.ErrCatalogSourceNotFound
	} else if err != nil {
		return httpapi.CatalogStatus{}, err
	}
	// Requeue the source for the probe/analysis pipeline; clearing the error
	// code also removes it from the warning aggregation.
	if err := repo.UpdateSourceStatus(ctx, id, mediacatalog.SourceStatusPendingProbe, ""); err != nil {
		return httpapi.CatalogStatus{}, err
	}
	return s.snapshot(ctx, repo)
}

func (s *mediaCatalogService) Providers(ctx context.Context) (httpapi.CatalogProviders, error) {
	payload := httpapi.CatalogProviders{Providers: []httpapi.CatalogProviderStatus{
		{Name: "pexels"},
		{Name: "pixabay"},
	}}
	runtime, err := s.settings.Runtime(ctx)
	if err != nil {
		return payload, nil
	}
	payload.Providers[0].Configured = strings.TrimSpace(runtime.PexelsAPIKey) != ""
	payload.Providers[1].Configured = strings.TrimSpace(runtime.PixabayAPIKey) != ""
	return payload, nil
}

func (s *mediaCatalogService) Search(ctx context.Context, query httpapi.CatalogSearchQuery) (httpapi.CatalogSearchPage, error) {
	if strings.TrimSpace(query.Query) == "" {
		return httpapi.CatalogSearchPage{}, httpapi.ErrCatalogSearchInvalid
	}
	provider, err := s.externalProvider(ctx, query.Provider)
	if err != nil {
		return httpapi.CatalogSearchPage{}, err
	}
	assets, err := provider.Search(ctx, query.Query, query.Limit)
	if err != nil {
		return httpapi.CatalogSearchPage{}, mapExternalCatalogError(err, httpapi.ErrCatalogSearchInvalid)
	}
	page := httpapi.CatalogSearchPage{Assets: make([]httpapi.CatalogRemoteAsset, 0, len(assets))}
	for _, asset := range assets {
		page.Assets = append(page.Assets, catalogRemoteAssetView(asset))
	}
	return page, nil
}

func (s *mediaCatalogService) Import(ctx context.Context, request httpapi.CatalogImportRequest) (httpapi.CatalogImportResult, error) {
	repo, err := s.repository(ctx)
	if err != nil {
		return httpapi.CatalogImportResult{}, err
	}
	switch strings.ToLower(strings.TrimSpace(request.Source)) {
	case "local":
		return s.importBytes(ctx, repo, request, mediacatalog.SourceOriginLocal, request.Bytes)
	case "remote":
		return s.importRemote(ctx, repo, request)
	default:
		return httpapi.CatalogImportResult{}, httpapi.ErrCatalogImportInvalid
	}
}

func (s *mediaCatalogService) importRemote(ctx context.Context, repo *mediacatalog.Repository, request httpapi.CatalogImportRequest) (httpapi.CatalogImportResult, error) {
	providerName := strings.TrimSpace(request.Provider)
	if providerName == "" {
		providerName = request.Remote.Provider
	}
	provider, err := s.externalProvider(ctx, providerName)
	if err != nil {
		if errors.Is(err, httpapi.ErrCatalogSearchInvalid) {
			return httpapi.CatalogImportResult{}, httpapi.ErrCatalogImportInvalid
		}
		return httpapi.CatalogImportResult{}, err
	}
	asset := remoteAssetFrom(request.Remote)
	if strings.TrimSpace(asset.DownloadURL) == "" || strings.TrimSpace(asset.ID) == "" {
		return httpapi.CatalogImportResult{}, httpapi.ErrCatalogImportInvalid
	}
	body, err := provider.Fetch(ctx, asset)
	if err != nil {
		return httpapi.CatalogImportResult{}, mapExternalCatalogError(err, httpapi.ErrCatalogImportInvalid)
	}
	defer body.Close()
	payload, err := io.ReadAll(io.LimitReader(body, 200<<20+1))
	if err != nil || len(payload) == 0 || len(payload) > 200<<20 {
		return httpapi.CatalogImportResult{}, httpapi.ErrCatalogImportInvalid
	}
	origin := mediacatalog.SourceOrigin(providerName)
	if origin != mediacatalog.SourceOriginPexels && origin != mediacatalog.SourceOriginPixabay {
		return httpapi.CatalogImportResult{}, httpapi.ErrCatalogImportInvalid
	}
	if strings.TrimSpace(request.Kind) == "" {
		request.Kind = string(asset.Kind)
	}
	if request.Kind == "" {
		request.Kind = string(mediacatalog.SourceKindImage)
	}
	if strings.TrimSpace(request.SuggestedName) == "" {
		request.SuggestedName = asset.ID
	}
	return s.importBytes(ctx, repo, request, origin, payload)
}

func (s *mediaCatalogService) importBytes(ctx context.Context, repo *mediacatalog.Repository, request httpapi.CatalogImportRequest, origin mediacatalog.SourceOrigin, payload []byte) (httpapi.CatalogImportResult, error) {
	kind := mediacatalog.SourceKind(strings.TrimSpace(request.Kind))
	if kind == "" {
		kind = mediacatalog.SourceKindImage
	}
	rights := mediacatalog.Rights{}
	if origin == mediacatalog.SourceOriginPexels || origin == mediacatalog.SourceOriginPixabay {
		rights = mediacatalog.Rights{
			SourceURL:   request.Remote.PageURL,
			Creator:     request.Remote.Creator,
			LicenseCode: request.Remote.LicenseCode,
			LicenseURL:  request.Remote.LicenseURL,
			Attribution: request.Remote.Creator,
			RetrievedAt: time.Now().UTC(),
		}
	}
	source, created, err := mediacatalog.NewImporter(repo).Import(ctx, mediacatalog.ImportRequest{
		Kind:          kind,
		Origin:        origin,
		Bytes:         payload,
		SuggestedName: request.SuggestedName,
		Rights:        rights,
	})
	if err != nil {
		return httpapi.CatalogImportResult{}, mapImportError(err)
	}
	publishability := ""
	if stored, rightsErr := repo.RightsBySource(ctx, source.ID); rightsErr == nil {
		publishability = mediacatalog.Publishability(stored)
	} else if !errors.Is(rightsErr, mediacatalog.ErrRightsNotFound) {
		return httpapi.CatalogImportResult{}, errors.New("catalog import failed")
	}
	return httpapi.CatalogImportResult{
		Source:         catalogSourceView(source),
		Created:        created,
		Publishability: publishability,
	}, nil
}

func (s *mediaCatalogService) externalProvider(ctx context.Context, name string) (mediacatalog.Provider, error) {
	runtime, err := s.settings.Runtime(ctx)
	if err != nil {
		return nil, httpapi.ErrProviderNotConfigured
	}
	config := mediacatalog.ProviderConfig{MaxResultsPerQuery: runtime.MaxExternalResultsPerQuery}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "pexels":
		config.APIKey = runtime.PexelsAPIKey
		config.BaseURL = runtime.PexelsAPIBaseURL
		provider, err := mediacatalog.NewPexelsProvider(config)
		if err != nil {
			return nil, httpapi.ErrCatalogSearchInvalid
		}
		if !provider.Configured() {
			return nil, httpapi.ErrProviderNotConfigured
		}
		return provider, nil
	case "pixabay":
		config.APIKey = runtime.PixabayAPIKey
		config.BaseURL = runtime.PixabayAPIBaseURL
		provider, err := mediacatalog.NewPixabayProvider(config)
		if err != nil {
			return nil, httpapi.ErrCatalogSearchInvalid
		}
		if !provider.Configured() {
			return nil, httpapi.ErrProviderNotConfigured
		}
		return provider, nil
	default:
		return nil, httpapi.ErrCatalogSearchInvalid
	}
}

func catalogSourceView(source mediacatalog.Source) httpapi.CatalogSource {
	return httpapi.CatalogSource{
		ID:           source.ID,
		Kind:         string(source.Kind),
		Subtype:      string(source.Subtype),
		Origin:       string(source.Origin),
		RelativePath: source.RelativePath,
		Status:       string(source.Status),
		ErrorCode:    source.ErrorCode,
		SizeBytes:    source.SizeBytes,
		DurationMS:   source.DurationMS,
		UpdatedAt:    source.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func catalogRemoteAssetView(asset mediacatalog.RemoteAsset) httpapi.CatalogRemoteAsset {
	return httpapi.CatalogRemoteAsset{
		Provider:    asset.Provider,
		ID:          asset.ID,
		Kind:        string(asset.Kind),
		DownloadURL: asset.DownloadURL,
		PageURL:     asset.PageURL,
		Creator:     asset.Creator,
		LicenseCode: asset.LicenseCode,
		LicenseURL:  asset.LicenseURL,
		Width:       asset.Width,
		Height:      asset.Height,
	}
}

func remoteAssetFrom(asset httpapi.CatalogRemoteAsset) mediacatalog.RemoteAsset {
	kind := mediacatalog.SourceKind(strings.TrimSpace(asset.Kind))
	if kind == "" {
		kind = mediacatalog.SourceKindImage
	}
	return mediacatalog.RemoteAsset{
		Provider:    strings.TrimSpace(asset.Provider),
		ID:          strings.TrimSpace(asset.ID),
		Kind:        kind,
		DownloadURL: strings.TrimSpace(asset.DownloadURL),
		PageURL:     strings.TrimSpace(asset.PageURL),
		Creator:     strings.TrimSpace(asset.Creator),
		LicenseCode: strings.TrimSpace(asset.LicenseCode),
		LicenseURL:  strings.TrimSpace(asset.LicenseURL),
		Width:       asset.Width,
		Height:      asset.Height,
	}
}

func mapExternalCatalogError(err error, invalid error) error {
	if errors.Is(err, mediacatalog.ErrProviderNotConfigured) {
		return httpapi.ErrProviderNotConfigured
	}
	if errors.Is(err, mediacatalog.ErrInvalidValue) {
		return invalid
	}
	return errors.New("external catalog request failed")
}

func mapImportError(err error) error {
	if errors.Is(err, mediacatalog.ErrInvalidValue) ||
		errors.Is(err, mediacatalog.ErrUnsupportedMediaType) ||
		errors.Is(err, mediacatalog.ErrRightsMetadataIncomplete) {
		return httpapi.ErrCatalogImportInvalid
	}
	return errors.New("catalog import failed")
}
