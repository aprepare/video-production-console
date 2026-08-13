package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"video-production-console/internal/httpapi"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func newCatalogServiceFixture(t *testing.T) *mediaCatalogService {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	settings := consoleSettings.NewService(store.NewSettingsRepository(database), nil, consoleSettings.Options{})
	return newMediaCatalogService(settings)
}

func TestMediaCatalogServiceProvidersDoNotRequireACatalog(t *testing.T) {
	service := newCatalogServiceFixture(t)
	payload, err := service.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Providers) != 2 || payload.Providers[0].Name != "pexels" || payload.Providers[0].Configured || payload.Providers[1].Configured {
		t.Fatalf("providers = %+v", payload.Providers)
	}
}

func TestMediaCatalogServiceSearchWithoutKeyIsNotConfigured(t *testing.T) {
	service := newCatalogServiceFixture(t)
	_, err := service.Search(context.Background(), httpapi.CatalogSearchQuery{Provider: "pexels", Query: "lake"})
	if !errors.Is(err, httpapi.ErrProviderNotConfigured) {
		t.Fatalf("err = %v, want provider_not_configured", err)
	}
}

func TestMediaCatalogServiceSearchRejectsEmptyQuery(t *testing.T) {
	service := newCatalogServiceFixture(t)
	_, err := service.Search(context.Background(), httpapi.CatalogSearchQuery{Provider: "pexels"})
	if !errors.Is(err, httpapi.ErrCatalogSearchInvalid) {
		t.Fatalf("err = %v, want catalog_search_invalid", err)
	}
}

func TestMediaCatalogServiceImportWithoutCatalogStaysLocallyUnblocked(t *testing.T) {
	service := newCatalogServiceFixture(t)
	_, err := service.Import(context.Background(), httpapi.CatalogImportRequest{Source: "local", Kind: "image", Bytes: []byte("png")})
	if !errors.Is(err, httpapi.ErrCatalogNotConfigured) {
		t.Fatalf("err = %v, want catalog_not_configured", err)
	}
	if _, searchErr := service.Providers(context.Background()); searchErr != nil {
		t.Fatalf("providers must keep working without a catalog: %v", searchErr)
	}
}
