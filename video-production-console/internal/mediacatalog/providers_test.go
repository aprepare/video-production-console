package mediacatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type recordedRequest struct {
	URL    string
	Header http.Header
}

// fakeTransport 按完整 URL（不含顺序敏感的 query 时用前缀）分发响应，绝不发真实网络请求。
type fakeTransport struct {
	responses map[string]*http.Response
	requests  []recordedRequest
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, recordedRequest{URL: request.URL.String(), Header: request.Header.Clone()})
	for key, response := range f.responses {
		if request.URL.String() == key || strings.HasPrefix(request.URL.String(), key) {
			clone := *response
			if response.Body == nil {
				clone.Body = io.NopCloser(strings.NewReader(""))
			}
			clone.Request = request
			return &clone, nil
		}
	}
	return nil, fmt.Errorf("unexpected request %s", request.URL)
}

func newFakeResponse(status int, contentType, body string, extra map[string]string) *http.Response {
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	for key, value := range extra {
		header.Set(key, value)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

type staticResolver struct {
	ips []net.IP
	err error
}

func (r staticResolver) LookupIP(context.Context, string, string) ([]net.IP, error) {
	return r.ips, r.err
}

func publicResolver() staticResolver {
	return staticResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}
}

func newTestPexels(t *testing.T, transport *fakeTransport, key string, maxResults int) *PexelsProvider {
	t.Helper()
	provider, err := NewPexelsProvider(ProviderConfig{
		APIKey:             key,
		BaseURL:            "https://api.pexels.com",
		MaxResultsPerQuery: maxResults,
		HTTPClient:         &http.Client{Transport: transport},
		Resolver:           publicResolver(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func newTestPixabay(t *testing.T, transport *fakeTransport, key string, maxResults int) *PixabayProvider {
	t.Helper()
	provider, err := NewPixabayProvider(ProviderConfig{
		APIKey:             key,
		BaseURL:            "https://pixabay.com",
		MaxResultsPerQuery: maxResults,
		HTTPClient:         &http.Client{Transport: transport},
		Resolver:           publicResolver(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestProvidersWithoutKeyReturnNotConfigured(t *testing.T) {
	transport := &fakeTransport{}
	pexels := newTestPexels(t, transport, "", 20)
	if _, err := pexels.Search(context.Background(), "city", 5); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("pexels search err=%v", err)
	}
	pixabay := newTestPixabay(t, transport, "", 20)
	if _, err := pixabay.Search(context.Background(), "city", 5); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("pixabay search err=%v", err)
	}
	if len(transport.requests) != 0 {
		t.Fatalf("unconfigured providers must not send requests, sent %d", len(transport.requests))
	}
}

func TestProviderRejectsForeignOrInsecureBaseURL(t *testing.T) {
	cases := []struct {
		name string
		base string
	}{
		{"plain http", "http://api.pexels.com"},
		{"foreign host", "https://evil.example.test"},
		{"suffix trick", "https://api.pexels.com.evil.test"},
	}
	for _, testCase := range cases {
		if _, err := NewPexelsProvider(ProviderConfig{APIKey: "k", BaseURL: testCase.base}); err == nil {
			t.Fatalf("%s: base URL %q must be rejected", testCase.name, testCase.base)
		}
	}
	if _, err := NewPixabayProvider(ProviderConfig{APIKey: "k", BaseURL: "https://api.pixabay.com"}); err == nil {
		t.Fatal("pixabay subdomain base must be rejected")
	}
}

func TestPexelsSearchSendsAuthAndClampsLimit(t *testing.T) {
	body := `{"photos":[{"id":101,"width":4000,"height":3000,"url":"https://www.pexels.com/photo/101/","photographer":"Ann Lee","src":{"large2x":"https://images.pexels.com/photos/101/download.jpg"}}]}`
	transport := &fakeTransport{responses: map[string]*http.Response{
		"https://api.pexels.com/v1/search": newFakeResponse(200, "application/json", body, nil),
	}}
	provider := newTestPexels(t, transport, "pexels-key", 20)
	assets, err := provider.Search(context.Background(), "family finance", 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("requests=%d", len(transport.requests))
	}
	request := transport.requests[0]
	if !strings.HasPrefix(request.URL, "https://api.pexels.com/v1/search?") {
		t.Fatalf("url=%q", request.URL)
	}
	if !strings.Contains(request.URL, "per_page=20") {
		t.Fatalf("limit not clamped to configured max: %q", request.URL)
	}
	if got := request.Header.Get("Authorization"); got != "pexels-key" {
		t.Fatalf("authorization=%q", got)
	}
	if len(assets) != 1 {
		t.Fatalf("assets=%d", len(assets))
	}
	asset := assets[0]
	if asset.Provider != "pexels" || asset.ID != "101" || asset.Kind != SourceKindImage {
		t.Fatalf("asset=%+v", asset)
	}
	if asset.PageURL != "https://www.pexels.com/photo/101/" || asset.Creator != "Ann Lee" {
		t.Fatalf("asset rights fields=%+v", asset)
	}
	if asset.DownloadURL != "https://images.pexels.com/photos/101/download.jpg" {
		t.Fatalf("download url=%q", asset.DownloadURL)
	}
	if asset.LicenseCode == "" || asset.LicenseURL == "" {
		t.Fatalf("license snapshot missing: %+v", asset)
	}
}

func TestPixabaySearchUsesKeyParamAndConfiguredCap(t *testing.T) {
	body := `{"hits":[{"id":7,"pageURL":"https://pixabay.com/photos/example-7/","user":"maker","imageWidth":1920,"imageHeight":1080,"largeImageURL":"https://pixabay.com/get/large7.jpg"}]}`
	transport := &fakeTransport{responses: map[string]*http.Response{
		"https://pixabay.com/api/": newFakeResponse(200, "application/json", body, nil),
	}}
	provider := newTestPixabay(t, transport, "pixabay-key", 30)
	assets, err := provider.Search(context.Background(), "ledger", 40)
	if err != nil {
		t.Fatal(err)
	}
	request := transport.requests[0]
	if !strings.Contains(request.URL, "key=pixabay-key") || !strings.Contains(request.URL, "q=ledger") {
		t.Fatalf("url=%q", request.URL)
	}
	if !strings.Contains(request.URL, "per_page=30") {
		t.Fatalf("limit above config must clamp to 30: %q", request.URL)
	}
	if len(assets) != 1 || assets[0].Provider != "pixabay" || assets[0].Creator != "maker" {
		t.Fatalf("assets=%+v", assets)
	}
}

func TestSearchLimitNeverExceedsHardCap(t *testing.T) {
	transport := &fakeTransport{responses: map[string]*http.Response{
		"https://api.pexels.com/v1/search": newFakeResponse(200, "application/json", `{"photos":[]}`, nil),
	}}
	provider := newTestPexels(t, transport, "key", 500)
	if _, err := provider.Search(context.Background(), "city", 500); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transport.requests[0].URL, "per_page=50") {
		t.Fatalf("hard cap 50 not applied: %q", transport.requests[0].URL)
	}
}

func TestFetchRejectsPlainHTTPPrivateAddressesAndForeignHosts(t *testing.T) {
	transport := &fakeTransport{}
	provider := newTestPexels(t, transport, "key", 20)
	ctx := context.Background()
	if _, err := provider.Fetch(ctx, RemoteAsset{DownloadURL: "http://images.pexels.com/a.jpg"}); err == nil {
		t.Fatal("plain http download must fail")
	}
	if _, err := provider.Fetch(ctx, RemoteAsset{DownloadURL: "https://evil.example.test/a.jpg"}); err == nil {
		t.Fatal("off-whitelist download host must fail")
	}
	private, err := NewPexelsProvider(ProviderConfig{
		APIKey:     "key",
		BaseURL:    "https://api.pexels.com",
		HTTPClient: &http.Client{Transport: transport},
		Resolver:   staticResolver{ips: []net.IP{net.ParseIP("127.0.0.1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := private.Fetch(ctx, RemoteAsset{DownloadURL: "https://images.pexels.com/a.jpg"}); err == nil {
		t.Fatal("loopback download target must fail")
	}
	if len(transport.requests) != 0 {
		t.Fatalf("guard failures must not reach the transport, sent %d", len(transport.requests))
	}
}

func TestFetchFollowsAtMostThreeSameDomainRedirects(t *testing.T) {
	transport := &fakeTransport{responses: map[string]*http.Response{
		"https://images.pexels.com/start.jpg": newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/hop1.jpg"}),
		"https://images.pexels.com/hop1.jpg":  newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/hop2.jpg"}),
		"https://images.pexels.com/hop2.jpg":  newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/final.jpg"}),
		"https://images.pexels.com/final.jpg": newFakeResponse(200, "image/jpeg", "jpeg-bytes", nil),
	}}
	provider := newTestPexels(t, transport, "key", 20)
	reader, err := provider.Fetch(context.Background(), RemoteAsset{DownloadURL: "https://images.pexels.com/start.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(payload) != "jpeg-bytes" {
		t.Fatalf("payload=%q err=%v", payload, err)
	}

	tooMany := &fakeTransport{responses: map[string]*http.Response{
		"https://images.pexels.com/a.jpg": newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/b.jpg"}),
		"https://images.pexels.com/b.jpg": newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/c.jpg"}),
		"https://images.pexels.com/c.jpg": newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/d.jpg"}),
		"https://images.pexels.com/d.jpg": newFakeResponse(302, "", "", map[string]string{"Location": "https://images.pexels.com/e.jpg"}),
	}}
	provider = newTestPexels(t, tooMany, "key", 20)
	if _, err := provider.Fetch(context.Background(), RemoteAsset{DownloadURL: "https://images.pexels.com/a.jpg"}); err == nil {
		t.Fatal("more than three redirects must fail")
	}

	offDomain := &fakeTransport{responses: map[string]*http.Response{
		"https://images.pexels.com/x.jpg": newFakeResponse(302, "", "", map[string]string{"Location": "https://evil.example.test/x.jpg"}),
	}}
	provider = newTestPexels(t, offDomain, "key", 20)
	if _, err := provider.Fetch(context.Background(), RemoteAsset{DownloadURL: "https://images.pexels.com/x.jpg"}); err == nil {
		t.Fatal("redirect escaping the whitelist domain must fail")
	}
}

func TestFetchEnforcesMIMEWhitelistAndSizeLimit(t *testing.T) {
	transport := &fakeTransport{responses: map[string]*http.Response{
		"https://images.pexels.com/page.html": newFakeResponse(200, "text/html", "<html></html>", nil),
	}}
	provider := newTestPexels(t, transport, "key", 20)
	if _, err := provider.Fetch(context.Background(), RemoteAsset{DownloadURL: "https://images.pexels.com/page.html"}); err == nil {
		t.Fatal("non-media content type must fail")
	}

	huge := newFakeResponse(200, "video/mp4", "", nil)
	huge.ContentLength = 201 << 20
	huge.Header.Set("Content-Length", fmt.Sprintf("%d", int64(201<<20)))
	transport = &fakeTransport{responses: map[string]*http.Response{
		"https://images.pexels.com/huge.mp4": huge,
	}}
	provider = newTestPexels(t, transport, "key", 20)
	if _, err := provider.Fetch(context.Background(), RemoteAsset{DownloadURL: "https://images.pexels.com/huge.mp4"}); err == nil {
		t.Fatal("downloads above 200 MiB must fail")
	}
}
