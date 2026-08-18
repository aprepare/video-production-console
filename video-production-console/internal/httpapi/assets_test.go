package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/store"
)

type stubAssetContentStore struct {
	asset domain.Asset
	err   error
}

func (s stubAssetContentStore) GetAsset(context.Context, string) (domain.Asset, error) {
	return s.asset, s.err
}

func TestAssetContentServesFullAndRangeResponses(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "projects", uuid.NewString(), "audio", "sample.mp3")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("0123456789")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	asset := domain.Asset{ID: id, Path: path, Filename: "sample.mp3", MIMEType: "audio/mpeg", SHA256: "abc123"}
	handler := newAssetsHandler(stubAssetContentStore{asset: asset}, assets.NewService(root))

	full := httptest.NewRecorder()
	handler.ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/api/assets/"+id+"/content", nil))
	if full.Code != http.StatusOK || full.Body.String() != string(content) {
		t.Fatalf("full response status=%d body=%q", full.Code, full.Body.String())
	}
	if got := full.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("Content-Type=%q, want audio/mpeg", got)
	}
	if got := full.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges=%q, want bytes", got)
	}
	if got := full.Header().Get("ETag"); got != `"abc123"` {
		t.Fatalf("ETag=%q", got)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/assets/"+id+"/content", nil)
	request.Header.Set("Range", "bytes=2-5")
	partial := httptest.NewRecorder()
	handler.ServeHTTP(partial, request)
	if partial.Code != http.StatusPartialContent || partial.Body.String() != "2345" {
		t.Fatalf("range response status=%d body=%q", partial.Code, partial.Body.String())
	}
	if got := partial.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range=%q", got)
	}
}

func TestAssetContentSupportsHead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	handler := newAssetsHandler(stubAssetContentStore{asset: domain.Asset{
		ID: id, Path: path, Filename: "asset.txt", MIMEType: "text/plain; charset=utf-8",
	}}, assets.NewService(root))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/api/assets/"+id+"/content", nil))
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("HEAD response status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Length") != "5" {
		t.Fatalf("Content-Length=%q", recorder.Header().Get("Content-Length"))
	}
}

func TestAssetContentRejectsUnknownAndUnsafeAssets(t *testing.T) {
	root := t.TempDir()
	id := uuid.NewString()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		path  string
		store stubAssetContentStore
		want  int
	}{
		{"malformed ID", "/api/assets/not-a-uuid/content", stubAssetContentStore{}, http.StatusNotFound},
		{"unknown ID", "/api/assets/" + id + "/content", stubAssetContentStore{err: store.ErrAssetNotFound}, http.StatusNotFound},
		{"outside data root", "/api/assets/" + id + "/content", stubAssetContentStore{asset: domain.Asset{ID: id, Path: outside, Filename: "secret.txt", MIMEType: "text/plain"}}, http.StatusNotFound},
		{"database failure", "/api/assets/" + id + "/content", stubAssetContentStore{err: errors.New("db unavailable")}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newAssetsHandler(tt.store, assets.NewService(root))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if recorder.Code != tt.want {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}
