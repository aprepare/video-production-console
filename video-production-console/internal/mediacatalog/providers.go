package mediacatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrProviderNotConfigured signals that the provider's API key is missing.
// Local imports and generated images keep working without external providers.
var ErrProviderNotConfigured = errors.New("provider_not_configured")

const (
	pexelsAPIHost  = "api.pexels.com"
	pixabayAPIHost = "pixabay.com"

	defaultExternalResultsPerQuery = 20
	maxExternalResultsHardCap      = 50
	maxExternalDownloadBytes       = 200 << 20
	maxExternalRedirects           = 3
	maxProviderResponseBytes       = 8 << 20
)

var supportedExternalMIMEs = map[string]struct{}{
	"image/jpeg": {}, "image/png": {}, "image/gif": {}, "image/webp": {},
	"video/mp4": {}, "video/webm": {},
}

// RemoteAsset is one search hit with the license snapshot required by the
// rights rules in the plan (§4.3): keep source URL, creator, and license,
// and never treat "downloadable" as a blanket license.
type RemoteAsset struct {
	Provider    string
	ID          string
	Kind        SourceKind
	DownloadURL string
	PageURL     string
	Creator     string
	LicenseCode string
	LicenseURL  string
	Width       int
	Height      int
}

// Provider searches an external stock-media catalog and fetches one asset
// through the download guard (HTTPS only, whitelisted domain, public IPs,
// bounded redirects and size, media MIME types only).
type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]RemoteAsset, error)
	Fetch(ctx context.Context, asset RemoteAsset) (io.ReadCloser, error)
}

// IPResolver matches net.Resolver's LookupIP so tests can fake DNS.
type IPResolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// ProviderConfig carries caller-injected settings. API keys come from
// settings.Runtime (PexelsAPIKey / PixabayAPIKey); providers never read the
// settings store themselves.
type ProviderConfig struct {
	APIKey  string
	BaseURL string
	// MaxResultsPerQuery mirrors the max_external_results_per_query public
	// setting: default 20, hard cap 50.
	MaxResultsPerQuery int
	HTTPClient         *http.Client
	Resolver           IPResolver
}

type providerCore struct {
	name           string
	apiKey         string
	baseURL        *url.URL
	maxResults     int
	client         *http.Client
	resolver       IPResolver
	downloadDomain string
}

func newProviderCore(name string, config ProviderConfig, apiHost, downloadDomain string) (providerCore, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host != apiHost ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return providerCore{}, fmt.Errorf("%s base URL must be https://%s", name, apiHost)
	}
	maxResults := config.MaxResultsPerQuery
	if maxResults <= 0 {
		maxResults = defaultExternalResultsPerQuery
	}
	if maxResults > maxExternalResultsHardCap {
		maxResults = maxExternalResultsHardCap
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	// Redirects are validated hop by hop in fetch; never follow implicitly.
	guarded := *client
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return providerCore{
		name:           name,
		apiKey:         strings.TrimSpace(config.APIKey),
		baseURL:        parsed,
		maxResults:     maxResults,
		client:         &guarded,
		resolver:       resolver,
		downloadDomain: downloadDomain,
	}, nil
}

func (core providerCore) effectiveLimit(limit int) int {
	if limit <= 0 || limit > core.maxResults {
		return core.maxResults
	}
	return limit
}

func (core providerCore) getJSON(ctx context.Context, endpoint string, header http.Header, dest any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%s search request: %w", core.name, err)
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := core.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s search failed", core.name)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes))
	if err != nil {
		return fmt.Errorf("%s search response: %w", core.name, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s search returned HTTP %d", core.name, response.StatusCode)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("%s search response is not valid JSON", core.name)
	}
	return nil
}

// fetch downloads one asset URL under the shared guard: HTTPS only, host
// inside the provider's whitelist domain, resolved addresses public, at most
// three same-domain redirects, whitelisted media MIME, and a 200 MiB cap.
func (core providerCore) fetch(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	current := rawURL
	for redirects := 0; ; redirects++ {
		parsed, err := core.validateDownloadURL(ctx, current)
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("%s download request: %w", core.name, err)
		}
		response, err := core.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("%s download failed", core.name)
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			_ = response.Body.Close()
			if location == "" {
				return nil, fmt.Errorf("%s download redirect without location", core.name)
			}
			if redirects >= maxExternalRedirects {
				return nil, fmt.Errorf("%s download exceeded %d redirects", core.name, maxExternalRedirects)
			}
			next, err := parsed.Parse(location)
			if err != nil {
				return nil, fmt.Errorf("%s download redirect is invalid", core.name)
			}
			current = next.String()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_ = response.Body.Close()
			return nil, fmt.Errorf("%s download returned HTTP %d", core.name, response.StatusCode)
		}
		mimeType := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
		if _, ok := supportedExternalMIMEs[mimeType]; !ok {
			_ = response.Body.Close()
			return nil, fmt.Errorf("%s download content type %q is not allowed", core.name, mimeType)
		}
		if response.ContentLength > maxExternalDownloadBytes {
			_ = response.Body.Close()
			return nil, fmt.Errorf("%s download exceeds %d bytes", core.name, int64(maxExternalDownloadBytes))
		}
		return &boundedReadCloser{reader: response.Body, remaining: maxExternalDownloadBytes}, nil
	}
}

func (core providerCore) validateDownloadURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%s download URL must be https", core.name)
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname != core.downloadDomain && !strings.HasSuffix(hostname, "."+core.downloadDomain) {
		return nil, fmt.Errorf("%s download host %q is outside the whitelist", core.name, hostname)
	}
	addresses := []net.IP{}
	if literal := net.ParseIP(hostname); literal != nil {
		addresses = append(addresses, literal)
	} else {
		resolved, err := core.resolver.LookupIP(ctx, "ip", hostname)
		if err != nil || len(resolved) == 0 {
			return nil, fmt.Errorf("%s download host could not be resolved", core.name)
		}
		addresses = resolved
	}
	for _, address := range addresses {
		if !isPublicExternalIP(address) {
			return nil, fmt.Errorf("%s download host resolves to a non-public address", core.name)
		}
	}
	return parsed, nil
}

func isPublicExternalIP(address net.IP) bool {
	if address == nil || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	// Carrier-grade NAT (100.64.0.0/10) is not publicly routable either.
	if v4 := address.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 0x40 {
		return false
	}
	return true
}

type boundedReadCloser struct {
	reader    io.ReadCloser
	remaining int64
}

func (b *boundedReadCloser) Read(payload []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, fmt.Errorf("external download exceeds %d bytes", int64(maxExternalDownloadBytes))
	}
	if int64(len(payload)) > b.remaining {
		payload = payload[:b.remaining]
	}
	read, err := b.reader.Read(payload)
	b.remaining -= int64(read)
	return read, err
}

func (b *boundedReadCloser) Close() error { return b.reader.Close() }

// PexelsProvider searches Pexels photos. The API key belongs in the
// Authorization header per Pexels documentation.
type PexelsProvider struct {
	core providerCore
}

func NewPexelsProvider(config ProviderConfig) (*PexelsProvider, error) {
	core, err := newProviderCore("pexels", config, pexelsAPIHost, "pexels.com")
	if err != nil {
		return nil, err
	}
	return &PexelsProvider{core: core}, nil
}

// Configured reports whether the API key is present; unconfigured providers
// stay disabled without blocking local-only imports.
func (p *PexelsProvider) Configured() bool { return p.core.apiKey != "" }

func (p *PexelsProvider) Search(ctx context.Context, query string, limit int) ([]RemoteAsset, error) {
	if !p.Configured() {
		return nil, ErrProviderNotConfigured
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: search query is required", ErrInvalidValue)
	}
	endpoint := *p.core.baseURL
	endpoint.Path = "/v1/search"
	values := url.Values{}
	values.Set("query", query)
	values.Set("per_page", strconv.Itoa(p.core.effectiveLimit(limit)))
	endpoint.RawQuery = values.Encode()
	var payload struct {
		Photos []struct {
			ID           int64  `json:"id"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			URL          string `json:"url"`
			Photographer string `json:"photographer"`
			Src          struct {
				Original string `json:"original"`
				Large2x  string `json:"large2x"`
			} `json:"src"`
		} `json:"photos"`
	}
	header := http.Header{}
	header.Set("Authorization", p.core.apiKey)
	if err := p.core.getJSON(ctx, endpoint.String(), header, &payload); err != nil {
		return nil, err
	}
	assets := make([]RemoteAsset, 0, len(payload.Photos))
	for _, photo := range payload.Photos {
		download := photo.Src.Large2x
		if download == "" {
			download = photo.Src.Original
		}
		assets = append(assets, RemoteAsset{
			Provider:    "pexels",
			ID:          strconv.FormatInt(photo.ID, 10),
			Kind:        SourceKindImage,
			DownloadURL: download,
			PageURL:     photo.URL,
			Creator:     photo.Photographer,
			LicenseCode: "pexels",
			LicenseURL:  "https://www.pexels.com/license/",
			Width:       photo.Width,
			Height:      photo.Height,
		})
	}
	return assets, nil
}

func (p *PexelsProvider) Fetch(ctx context.Context, asset RemoteAsset) (io.ReadCloser, error) {
	if !p.Configured() {
		return nil, ErrProviderNotConfigured
	}
	return p.core.fetch(ctx, asset.DownloadURL)
}

// PixabayProvider searches Pixabay images. Pixabay authenticates through the
// key query parameter; the key must never be logged or persisted.
type PixabayProvider struct {
	core providerCore
}

func NewPixabayProvider(config ProviderConfig) (*PixabayProvider, error) {
	core, err := newProviderCore("pixabay", config, pixabayAPIHost, "pixabay.com")
	if err != nil {
		return nil, err
	}
	return &PixabayProvider{core: core}, nil
}

func (p *PixabayProvider) Configured() bool { return p.core.apiKey != "" }

func (p *PixabayProvider) Search(ctx context.Context, query string, limit int) ([]RemoteAsset, error) {
	if !p.Configured() {
		return nil, ErrProviderNotConfigured
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: search query is required", ErrInvalidValue)
	}
	endpoint := *p.core.baseURL
	endpoint.Path = "/api/"
	values := url.Values{}
	values.Set("key", p.core.apiKey)
	values.Set("q", query)
	values.Set("per_page", strconv.Itoa(p.core.effectiveLimit(limit)))
	endpoint.RawQuery = values.Encode()
	var payload struct {
		Hits []struct {
			ID            int64  `json:"id"`
			PageURL       string `json:"pageURL"`
			User          string `json:"user"`
			ImageWidth    int    `json:"imageWidth"`
			ImageHeight   int    `json:"imageHeight"`
			LargeImageURL string `json:"largeImageURL"`
		} `json:"hits"`
	}
	if err := p.core.getJSON(ctx, endpoint.String(), nil, &payload); err != nil {
		return nil, err
	}
	assets := make([]RemoteAsset, 0, len(payload.Hits))
	for _, hit := range payload.Hits {
		assets = append(assets, RemoteAsset{
			Provider:    "pixabay",
			ID:          strconv.FormatInt(hit.ID, 10),
			Kind:        SourceKindImage,
			DownloadURL: hit.LargeImageURL,
			PageURL:     hit.PageURL,
			Creator:     hit.User,
			LicenseCode: "pixabay",
			LicenseURL:  "https://pixabay.com/service/license-summary/",
			Width:       hit.ImageWidth,
			Height:      hit.ImageHeight,
		})
	}
	return assets, nil
}

func (p *PixabayProvider) Fetch(ctx context.Context, asset RemoteAsset) (io.ReadCloser, error) {
	if !p.Configured() {
		return nil, ErrProviderNotConfigured
	}
	return p.core.fetch(ctx, asset.DownloadURL)
}
