package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalOpenRequestRequiresDirectLoopbackBrowserRequest(t *testing.T) {
	tests := []struct {
		name                        string
		remote, local, host, origin string
		fetchSite, fetchMode        string
		xForwardedFor               string
		want                        bool
	}{
		{name: "IPv4 browser", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "cors", want: true},
		{name: "IPv6 browser", remote: "[::1]:5000", local: "[::1]:2030", host: "[::1]:2030", origin: "http://[::1]:2030", fetchSite: "same-origin", fetchMode: "same-origin", want: true},
		{name: "XFF is ignored", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "localhost:2030", origin: "http://localhost:2030", fetchSite: "same-origin", fetchMode: "cors", xForwardedFor: "198.51.100.10", want: true},
		{name: "empty Origin", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
		{name: "spoofed Host", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1.evil:2030", origin: "http://127.0.0.1.evil:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
		{name: "missing Fetch Site", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchMode: "cors", want: false},
		{name: "missing Fetch Mode", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "same-origin", want: false},
		{name: "cross-site Fetch Site", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "cross-site", fetchMode: "cors", want: false},
		{name: "navigate Fetch Mode", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "navigate", want: false},
		{name: "remote proxy", remote: "192.168.1.2:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
		{name: "nonloopback listener", remote: "127.0.0.1:5000", local: "192.168.1.2:2030", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
		{name: "missing listener address", remote: "127.0.0.1:5000", host: "127.0.0.1:2030", origin: "http://127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
		{name: "different origin host", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "http://localhost:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
		{name: "different origin scheme", remote: "127.0.0.1:5000", local: "127.0.0.1:2030", host: "127.0.0.1:2030", origin: "https://127.0.0.1:2030", fetchSite: "same-origin", fetchMode: "cors", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := localOpenTestRequest(t, test.remote, test.local, test.host, test.origin, test.fetchSite, test.fetchMode)
			if test.xForwardedFor != "" {
				r.Header.Set("X-Forwarded-For", test.xForwardedFor)
			}
			if got := localOpenRequest(r); got != test.want {
				t.Fatalf("localOpenRequest()=%v, want %v", got, test.want)
			}
		})
	}
}

func TestOpenDirectoryRejectsInvalidLocalBrowserMetadataWithStableForbidden(t *testing.T) {
	tests := []struct {
		name, origin, fetchSite, fetchMode string
	}{
		{name: "empty Origin", fetchSite: "same-origin", fetchMode: "cors"},
		{name: "missing Fetch Site", origin: "http://127.0.0.1:2030", fetchMode: "cors"},
		{name: "missing Fetch Mode", origin: "http://127.0.0.1:2030", fetchSite: "same-origin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := localOpenTestRequest(t, "127.0.0.1:5000", "127.0.0.1:2030", "127.0.0.1:2030", test.origin, test.fetchSite, test.fetchMode)
			w := httptest.NewRecorder()
			(&assetContentHandler{}).openDirectory(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d, want %d", w.Code, http.StatusForbidden)
			}
			if !strings.Contains(w.Body.String(), `"code":"local_same_origin_required"`) {
				t.Fatalf("body=%q, want stable local_same_origin_required error", w.Body.String())
			}
		})
	}
}

func localOpenTestRequest(t *testing.T, remote, local, host, origin, fetchSite, fetchMode string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/assets/id/open-directory", nil)
	r.RemoteAddr, r.Host = remote, host
	if local != "" {
		localAddr, err := net.ResolveTCPAddr("tcp", local)
		if err != nil {
			t.Fatalf("resolve local address %q: %v", local, err)
		}
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, localAddr))
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	if fetchMode != "" {
		r.Header.Set("Sec-Fetch-Mode", fetchMode)
	}
	return r
}
