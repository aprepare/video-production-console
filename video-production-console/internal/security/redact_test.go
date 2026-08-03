package security

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRedactorMasksRegisteredSecretsAndSensitivePrefixes(t *testing.T) {
	if Mask != "***" {
		t.Fatalf("mask = %q, want %q", Mask, "***")
	}
	r := NewRedactor()
	r.Register("long-secret-value")
	r.Register("secret")
	got := r.Redact(`long-secret-value secret Authorization: Bearer unregistered-auth API_KEY=unregistered-env {"github_api_key":"unregistered-json"}`)
	if strings.Contains(got, "long-secret-value") || strings.Contains(got, "secret") {
		t.Fatalf("redacted output leaked secret: %s", got)
	}
	for _, value := range []string{"unregistered-auth", "unregistered-env", "unregistered-json"} {
		if strings.Contains(got, value) {
			t.Fatalf("redacted output leaked prefixed value %q: %s", value, got)
		}
	}
	if strings.Count(got, "***") < 3 {
		t.Fatalf("redacted output = %s", got)
	}
}

func TestRedactorConcurrentRegisterAndRedact(t *testing.T) {
	r := NewRedactor()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := fmt.Sprintf("secret-%03d", i)
			r.Register(value)
			_ = r.Redact("Authorization: Bearer " + value)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 50; i++ {
		value := fmt.Sprintf("secret-%03d", i)
		if strings.Contains(r.Redact(value), value) {
			t.Fatalf("leaked %s", value)
		}
	}
}

func TestRedactorMasksEntireAuthorizationValueForAnyScheme(t *testing.T) {
	r := NewRedactor()
	got := r.Redact("Authorization: Basic dXNlcjpwYXNz\nAuthorization: Digest username=admin, response=abcdef\nSafe: visible")
	for _, leaked := range []string{"Basic", "dXNlcjpwYXNz", "Digest", "username=admin", "response=abcdef"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("authorization value leaked %q: %s", leaked, got)
		}
	}
	if strings.Count(got, "Authorization: ***") != 2 {
		t.Fatalf("redacted authorization headers = %q", got)
	}
	if !strings.Contains(got, "Safe: visible") {
		t.Fatalf("safe following header was altered: %q", got)
	}
}

func TestRedactorMasksQuotedEnvironmentAndEscapedJSONValues(t *testing.T) {
	r := NewRedactor()
	got := r.Redact("API_KEY=\"alpha beta \\\"gamma\\\" tail\" SAFE=visible\nSECRET='delta epsilon'\n{\"github_api_key\":\"json alpha \\\"quoted\\\" tail\",\"safe\":\"visible\"}")
	for _, leaked := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "json alpha", "quoted", "tail"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("quoted secret leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "SAFE=visible") || !strings.Contains(got, `"safe":"visible"`) {
		t.Fatalf("safe values were altered: %s", got)
	}
}
