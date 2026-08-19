package partnergateway

import (
	"strings"
	"testing"
)

func TestPolicyValidatesExactModelsAndImageShape(t *testing.T) {
	p := DefaultPolicy()
	tests := []struct {
		name    string
		kind    RequestKind
		body    string
		wantErr bool
	}{
		{"sol medium", ChatRequest, `{"model":"gpt-5.6-sol","reasoning_effort":"medium","messages":[]}`, false},
		{"grok ultra", ChatRequest, `{"model":"grok-4.6","reasoning_effort":"ultra","messages":[]}`, false},
		{"omitted effort", ChatRequest, `{"model":"gpt-5.6-sol","messages":[]}`, false},
		{"missing model", ChatRequest, `{"messages":[]}`, true},
		{"unknown text", ChatRequest, `{"model":"gpt-4o","messages":[]}`, true},
		{"unknown effort", ChatRequest, `{"model":"gpt-5.6-sol","reasoning_effort":"extreme","messages":[]}`, true},
		{"one image", ImageRequest, `{"model":"gpt-image-2","n":1,"size":"1024x1024","prompt":"x"}`, false},
		{"one image decimal", ImageRequest, `{"model":"gpt-image-2","n":1.0,"size":"1024x1024","prompt":"x"}`, false},
		{"one image exponent", ImageRequest, `{"model":"gpt-image-2","n":1e0,"size":"1024x1024","prompt":"x"}`, false},
		{"fractional image count", ImageRequest, `{"model":"gpt-image-2","n":1.1,"size":"1024x1024","prompt":"x"}`, true},
		{"two images", ImageRequest, `{"model":"gpt-image-2","n":2,"size":"1024x1024","prompt":"x"}`, true},
		{"wrong image model", ImageRequest, `{"model":"gpt-image-1","n":1,"size":"1024x1024","prompt":"x"}`, true},
		{"missing image model", ImageRequest, `{"n":1,"size":"1024x1024","prompt":"x"}`, true},
		{"unknown image size", ImageRequest, `{"model":"gpt-image-2","n":1,"size":"512x512","prompt":"x"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(tc.kind, []byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestPolicyRejectsClientControlledSecretFieldsAtAnyDepth(t *testing.T) {
	p := DefaultPolicy()
	tests := []struct {
		name string
		body string
	}{
		{"api key", `{"model":"gpt-5.6-sol","api_key":"client-secret","messages":[]}`},
		{"base URL mixed case", `{"model":"gpt-5.6-sol","Base_URL":"https://client.invalid","messages":[]}`},
		{"nested authorization", `{"model":"gpt-5.6-sol","messages":[{"role":"user","metadata":{"AUTHORIZATION":"Bearer client"}}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.Validate(ChatRequest, []byte(tc.body)); err == nil {
				t.Fatal("expected client-controlled secret field to be rejected")
			}
		})
	}
}

func TestPolicyRejectsBodiesOverConfiguredLimit(t *testing.T) {
	p := DefaultPolicy()
	p.maxRequestBytes = 64
	body := `{"model":"gpt-5.6-sol","messages":[],"padding":"` + strings.Repeat("x", 64) + `"}`
	if err := p.Validate(ChatRequest, []byte(body)); err == nil {
		t.Fatal("expected oversized request body to be rejected")
	}
}

func TestPolicyDefaultAllowlistsAreIsolated(t *testing.T) {
	first := DefaultPolicy()
	second := DefaultPolicy()
	delete(first.textModels, "gpt-5.6-sol")

	body := []byte(`{"model":"gpt-5.6-sol","messages":[]}`)
	if err := second.Validate(ChatRequest, body); err != nil {
		t.Fatalf("mutating one policy changed another policy: %v", err)
	}
}
