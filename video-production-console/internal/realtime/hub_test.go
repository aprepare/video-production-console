package realtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   bool
	}{
		{"", true}, {"http://localhost:2030", true}, {"http://127.0.0.1:2030", true},
		{"https://evil.example", false}, {"not a url", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", tc.origin)
		if got := localOrigin(r); got != tc.want {
			t.Errorf("origin %q = %v, want %v", tc.origin, got, tc.want)
		}
	}
}

func TestParseAfter(t *testing.T) {
	if got, _ := parseAfter(""); got != 0 {
		t.Fatal(got)
	}
	if got, _ := parseAfter("12"); got != 12 {
		t.Fatal(got)
	}
	if _, err := parseAfter("-1"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parseAfter("x"); err == nil {
		t.Fatal("expected error")
	}
}
