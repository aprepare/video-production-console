package partnerclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerRequiresOnlineVerifyEveryStart(t *testing.T) {
	manager := newManagerFixture(t, fakeGateway{verifyErr: ErrGatewayUnavailable}, savedCredentials())
	if err := manager.Start(context.Background()); !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if got := manager.Snapshot(); got.State != StateLocked || got.SessionToken != "" {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestManagerSuccessfulActivationSavesCredentials(t *testing.T) {
	now := managerTestNow()
	gatewayState := &fakeGatewayState{}
	gateway := fakeGateway{
		state: gatewayState,
		activateResponse: authResponse(
			now.Add(12*time.Hour),
			"device-from-gateway",
			"aura-from-gateway",
			"session-from-gateway",
		),
	}
	store := &memoryCredentialStore{loadErr: os.ErrNotExist}
	manager := newManagerForTest(t, gateway, store, func() time.Time { return now })

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot().State; got != StateNeedsActivation {
		t.Fatalf("state=%q", got)
	}
	if err := manager.Activate(context.Background(), "one-time-activation-key"); err != nil {
		t.Fatal(err)
	}

	wantCredentials := Credentials{
		PartnerID:    "partner-1",
		DeviceSecret: "device-from-gateway",
		AuraAPIKey:   "aura-from-gateway",
	}
	if got := store.lastSaved(); got != wantCredentials {
		t.Fatalf("saved=%+v want=%+v", got, wantCredentials)
	}
	gatewayState.mu.Lock()
	activationRequest := gatewayState.lastActivate
	gatewayState.mu.Unlock()
	if activationRequest.ActivationKey != "one-time-activation-key" ||
		activationRequest.DeviceHash != "device-hash" ||
		activationRequest.AppVersion != "1.2.3" ||
		activationRequest.Edition != "partner" {
		t.Fatalf("activation request=%+v", activationRequest)
	}
	if got := manager.Snapshot().State; got != StateReady {
		t.Fatalf("state=%q", got)
	}
}

func TestManagerVerifyRefreshesAuraKey(t *testing.T) {
	now := managerTestNow()
	store := &memoryCredentialStore{credentials: savedCredentials()}
	gateway := fakeGateway{verifyResponse: authResponse(
		now.Add(12*time.Hour),
		"",
		"fresh-aura-key",
		"fresh-session",
	)}
	manager := newManagerForTest(t, gateway, store, func() time.Time { return now })

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := savedCredentials()
	want.AuraAPIKey = "fresh-aura-key"
	if got := store.lastSaved(); got != want {
		t.Fatalf("saved=%+v want=%+v", got, want)
	}
	if got := manager.RuntimeSnapshot().Aura.APIKey; got != "fresh-aura-key" {
		t.Fatalf("runtime aura key=%q", got)
	}
}

func TestManagerKeepsSessionOnlyInMemory(t *testing.T) {
	now := managerTestNow()
	path := filepath.Join(t.TempDir(), "partner-credentials.json")
	store := NewCredentialStore(path, &fakeProtector{prefix: []byte("cipher:")})
	manager := newManagerForTest(t, fakeGateway{activateResponse: authResponse(
		now.Add(12*time.Hour),
		"device-secret",
		"aura-secret",
		"memory-only-session",
	)}, store, func() time.Time { return now })

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(context.Background(), "activation-key"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["session_token"]; exists {
		t.Fatalf("credential file contains session token field: %s", raw)
	}
	if strings.Contains(string(raw), "memory-only-session") {
		t.Fatalf("credential file contains session: %s", raw)
	}
	if got := manager.RuntimeSnapshot().SessionToken; got != "memory-only-session" {
		t.Fatalf("runtime session=%q", got)
	}
}

func TestManagerSessionExpiryTriggersReverify(t *testing.T) {
	now := managerTestNow()
	state := &fakeGatewayState{verifyCalled: make(chan struct{}, 4)}
	gateway := fakeGateway{
		state: state,
		verifyResponses: []AuthResponse{
			authResponse(now.Add(4*time.Minute), "", "aura-one", "session-one"),
			authResponse(now.Add(12*time.Hour), "", "aura-two", "session-two"),
		},
	}
	manager := newManagerForTest(
		t,
		gateway,
		&memoryCredentialStore{credentials: savedCredentials()},
		func() time.Time { return now },
	)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForGatewayVerifies(t, state, 2)
	runtime := manager.RuntimeSnapshot()
	if runtime.SessionToken != "session-two" || runtime.Aura.APIKey != "aura-two" {
		t.Fatalf("runtime=%+v", runtime)
	}
}

func TestManagerAuthorizationFailureReturnsToLockedState(t *testing.T) {
	now := managerTestNow()
	state := &fakeGatewayState{verifyCalled: make(chan struct{}, 4)}
	gateway := fakeGateway{
		state: state,
		verifyResponses: []AuthResponse{
			authResponse(now.Add(4*time.Minute), "", "aura-one", "session-one"),
		},
		verifyErrors: []error{nil, ErrAuthorizationFailed},
	}
	manager := newManagerForTest(
		t,
		gateway,
		&memoryCredentialStore{credentials: savedCredentials()},
		func() time.Time { return now },
	)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForGatewayVerifies(t, state, 2)
	got := manager.Snapshot()
	if got.State != StateLocked || got.ErrorCode != "authorization_failed" || got.SessionToken != "" {
		t.Fatalf("snapshot=%+v", got)
	}
	if runtime := manager.RuntimeSnapshot(); runtime.SessionToken != "" {
		t.Fatalf("runtime session=%q", runtime.SessionToken)
	}
}

func TestManagerSnapshotJSONOmitsSecrets(t *testing.T) {
	now := managerTestNow()
	manager := newManagerForTest(
		t,
		fakeGateway{verifyResponse: authResponse(
			now.Add(12*time.Hour),
			"",
			"private-aura-key",
			"private-session-token",
		)},
		&memoryCredentialStore{credentials: savedCredentials()},
		func() time.Time { return now },
	)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot := manager.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"device_secret",
		"aura_api_key",
		"session_token",
		"saved-device-secret",
		"private-aura-key",
		"private-session-token",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("snapshot JSON leaked %q: %s", forbidden, raw)
		}
	}
	if snapshot.SessionToken != "" || snapshot.DeviceSecret != "" || snapshot.AuraAPIKey != "" {
		t.Fatalf("snapshot exposes runtime secrets: %+v", snapshot)
	}
}

func TestManagerRuntimeSnapshotExposesInternals(t *testing.T) {
	now := managerTestNow()
	manager := newManagerForTest(
		t,
		fakeGateway{verifyResponse: authResponse(
			now.Add(12*time.Hour),
			"",
			"runtime-aura-key",
			"runtime-session-token",
		)},
		&memoryCredentialStore{credentials: savedCredentials()},
		func() time.Time { return now },
	)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	runtime := manager.RuntimeSnapshot()
	if runtime.PartnerID != "partner-1" ||
		runtime.DeviceSecret != "saved-device-secret" ||
		runtime.Aura.APIKey != "runtime-aura-key" ||
		runtime.SessionToken != "runtime-session-token" {
		t.Fatalf("runtime=%+v", runtime)
	}
}

func TestManagerSnapshotUsesSanitizedGatewayErrorCode(t *testing.T) {
	const privateError = "dial tcp private-gateway.internal: secret failure"
	manager := newManagerFixture(t, fakeGateway{verifyErr: errors.New(privateError)}, savedCredentials())
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("Start() error=nil")
	}
	snapshot := manager.Snapshot()
	if snapshot.ErrorCode != "gateway_error" {
		t.Fatalf("error code=%q", snapshot.ErrorCode)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), privateError) {
		t.Fatalf("snapshot leaked raw error: %s", raw)
	}
}

type fakeGateway struct {
	state            *fakeGatewayState
	activateResponse AuthResponse
	activateErr      error
	verifyResponse   AuthResponse
	verifyErr        error
	verifyResponses  []AuthResponse
	verifyErrors     []error
}

type fakeGatewayState struct {
	mu           sync.Mutex
	verifyCalls  int
	lastActivate ActivateRequest
	lastVerify   VerifyRequest
	verifyCalled chan struct{}
}

func (g fakeGateway) Activate(_ context.Context, request ActivateRequest) (AuthResponse, error) {
	if g.state != nil {
		g.state.mu.Lock()
		g.state.lastActivate = request
		g.state.mu.Unlock()
	}
	return g.activateResponse, g.activateErr
}

func (g fakeGateway) Verify(_ context.Context, request VerifyRequest) (AuthResponse, error) {
	call := 0
	if g.state != nil {
		g.state.mu.Lock()
		g.state.lastVerify = request
		call = g.state.verifyCalls
		g.state.verifyCalls++
		called := g.state.verifyCalled
		g.state.mu.Unlock()
		if called != nil {
			called <- struct{}{}
		}
	}
	if call < len(g.verifyErrors) && g.verifyErrors[call] != nil {
		return AuthResponse{}, g.verifyErrors[call]
	}
	if call < len(g.verifyResponses) {
		return g.verifyResponses[call], nil
	}
	return g.verifyResponse, g.verifyErr
}

type memoryCredentialStore struct {
	mu          sync.Mutex
	credentials Credentials
	loadErr     error
	saved       []Credentials
}

func (s *memoryCredentialStore) Load() (Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credentials, s.loadErr
}

func (s *memoryCredentialStore) Save(credentials Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials = credentials
	s.loadErr = nil
	s.saved = append(s.saved, credentials)
	return nil
}

func (s *memoryCredentialStore) lastSaved() Credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		return Credentials{}
	}
	return s.saved[len(s.saved)-1]
}

func newManagerFixture(t *testing.T, gateway Gateway, credentials Credentials) *Manager {
	t.Helper()
	return newManagerForTest(
		t,
		gateway,
		&memoryCredentialStore{credentials: credentials},
		managerTestNow,
	)
}

func newManagerForTest(
	t *testing.T,
	gateway Gateway,
	store CredentialsStore,
	clock func() time.Time,
) *Manager {
	t.Helper()
	manager := NewManager(ManagerOptions{
		Gateway:         gateway,
		CredentialStore: store,
		DeviceHash:      func() (string, error) { return "device-hash", nil },
		Clock:           clock,
		AppVersion:      "1.2.3",
		Edition:         "partner",
	})
	t.Cleanup(manager.Close)
	return manager
}

func savedCredentials() Credentials {
	return Credentials{
		PartnerID:    "partner-1",
		DeviceSecret: "saved-device-secret",
		AuraAPIKey:   "saved-aura-key",
	}
}

func authResponse(expiresAt time.Time, deviceSecret, auraKey, session string) AuthResponse {
	return AuthResponse{
		PartnerID:        "partner-1",
		DisplayName:      "Partner One",
		DeviceSecret:     deviceSecret,
		SessionToken:     session,
		SessionExpiresAt: expiresAt,
		Capabilities: Capabilities{
			Features:         []string{"text", "image"},
			TextModels:       []string{"gpt-5.6-sol"},
			ReasoningEfforts: []string{"medium", "high"},
			ImageModel:       "gpt-image-2",
		},
		Aura: AuraRuntime{
			BaseURL: "https://aura.example/v1",
			APIKey:  auraKey,
			Model:   "aura-model",
			VoiceID: "voice-1",
			Speed:   1.1,
			Volume:  0.9,
		},
	}
}

func managerTestNow() time.Time {
	return time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC)
}

func waitForGatewayVerifies(t *testing.T, state *fakeGatewayState, count int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		state.mu.Lock()
		got := state.verifyCalls
		state.mu.Unlock()
		if got >= count {
			return
		}
		select {
		case <-state.verifyCalled:
		case <-deadline.C:
			t.Fatalf("verify calls=%d want>=%d", got, count)
		}
	}
}
