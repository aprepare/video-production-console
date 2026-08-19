package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-production-console/internal/partnerclient"
)

type fakePartnerHandlerManager struct {
	snapshot      partnerclient.Snapshot
	activationKey string
	activateErr   error
}

func (m *fakePartnerHandlerManager) Snapshot() partnerclient.Snapshot {
	return m.snapshot
}

func (m *fakePartnerHandlerManager) Activate(_ context.Context, activationKey string) error {
	m.activationKey = activationKey
	return m.activateErr
}

func TestPartnerStatusReturnsSanitizedSnapshot(t *testing.T) {
	manager := &fakePartnerHandlerManager{snapshot: partnerclient.Snapshot{
		State:        partnerclient.StateNeedsActivation,
		PartnerName:  "合作伙伴",
		DeviceSecret: "must-not-leak",
		AuraAPIKey:   "must-not-leak",
		SessionToken: "must-not-leak",
	}}
	handler := NewPartnerHandler(manager)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/partner/status", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"device_secret", "aura_api_key", "session_token", "must-not-leak"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("status response leaked %q: %s", forbidden, response.Body.String())
		}
	}
	var snapshot partnerclient.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != partnerclient.StateNeedsActivation || snapshot.PartnerName != "合作伙伴" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPartnerActivateUsesOneTimeKeyAndDoesNotEchoIt(t *testing.T) {
	manager := &fakePartnerHandlerManager{snapshot: partnerclient.Snapshot{State: partnerclient.StateReady}}
	handler := NewPartnerHandler(manager)
	response := httptest.NewRecorder()
	const activationKey = "one-time-secret"

	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/api/partner/activate",
		strings.NewReader(`{"activation_key":"`+activationKey+`"}`),
	))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if manager.activationKey != activationKey {
		t.Fatalf("activation key=%q", manager.activationKey)
	}
	if strings.Contains(response.Body.String(), activationKey) {
		t.Fatalf("activation response echoed the one-time key: %s", response.Body.String())
	}
}

func TestPartnerActivateRejectsInvalidInputAndSanitizesManagerErrors(t *testing.T) {
	manager := &fakePartnerHandlerManager{
		snapshot:    partnerclient.Snapshot{State: partnerclient.StateLocked, ErrorCode: "gateway_unavailable"},
		activateErr: errors.New("dial 23.138.12.112 with secret one-time-key"),
	}
	handler := NewPartnerHandler(manager)

	for name, body := range map[string]string{
		"unknown field": `{"activation_key":"key","base_url":"http://attacker"}`,
		"empty key":     `{"activation_key":"   "}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/partner/activate", strings.NewReader(body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/partner/activate", strings.NewReader(`{"activation_key":"one-time-key"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"23.138.12.112", "one-time-key", "dial"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("activation error leaked %q: %s", forbidden, response.Body.String())
		}
	}
}
