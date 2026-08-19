package partnerclient

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

type State string

const (
	StateNeedsActivation State = "needs_activation"
	StateVerifying       State = "verifying"
	StateReady           State = "ready"
	StateLocked          State = "locked"
)

type Gateway interface {
	Activate(context.Context, ActivateRequest) (AuthResponse, error)
	Verify(context.Context, VerifyRequest) (AuthResponse, error)
}

type CredentialsStore interface {
	Load() (Credentials, error)
	Save(Credentials) error
}

type ManagerOptions struct {
	Gateway         Gateway
	CredentialStore CredentialsStore
	DeviceHash      func() (string, error)
	Clock           func() time.Time
	AppVersion      string
	Edition         string
}

type Snapshot struct {
	State            State        `json:"state"`
	PartnerName      string       `json:"partner_name,omitempty"`
	Capabilities     Capabilities `json:"capabilities,omitempty"`
	SessionExpiresAt time.Time    `json:"session_expires_at,omitempty"`
	ErrorCode        string       `json:"error_code,omitempty"`

	DeviceSecret string `json:"-"`
	AuraAPIKey   string `json:"-"`
	SessionToken string `json:"-"`
}

type RuntimeSnapshot struct {
	State            State
	PartnerID        string
	PartnerName      string
	Capabilities     Capabilities
	SessionExpiresAt time.Time
	ErrorCode        string
	DeviceSecret     string
	Aura             AuraRuntime
	SessionToken     string
}

type Manager struct {
	mu              sync.RWMutex
	gateway         Gateway
	credentialStore CredentialsStore
	deviceHash      func() (string, error)
	clock           func() time.Time
	appVersion      string
	edition         string

	state            State
	partnerID        string
	partnerName      string
	capabilities     Capabilities
	sessionExpiresAt time.Time
	errorCode        string
	deviceSecret     string
	aura             AuraRuntime
	sessionToken     string
	verifyInProgress bool
	timer            *time.Timer
	closed           bool
}

func NewManager(options ManagerOptions) *Manager {
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	deviceHash := options.DeviceHash
	if deviceHash == nil {
		deviceHash = CurrentDeviceHash
	}
	return &Manager{
		gateway:         options.Gateway,
		credentialStore: options.CredentialStore,
		deviceHash:      deviceHash,
		clock:           clock,
		appVersion:      options.AppVersion,
		edition:         options.Edition,
		state:           StateNeedsActivation,
	}
}

func (m *Manager) Start(ctx context.Context) error {
	deviceHash, err := m.deviceHash()
	if err != nil {
		m.lockWithError(err)
		return err
	}
	if m.gateway == nil || m.credentialStore == nil {
		err := errors.New("partner manager dependencies are required")
		m.lockWithError(err)
		return err
	}

	credentials, err := m.credentialStore.Load()
	if errors.Is(err, os.ErrNotExist) {
		m.setNeedsActivation()
		return nil
	}
	if err != nil {
		m.lockWithError(err)
		return err
	}
	return m.verify(ctx, deviceHash, credentials)
}

func (m *Manager) Activate(ctx context.Context, activationKey string) error {
	deviceHash, err := m.deviceHash()
	if err != nil {
		m.lockWithError(err)
		return err
	}
	if m.gateway == nil || m.credentialStore == nil {
		err := errors.New("partner manager dependencies are required")
		m.lockWithError(err)
		return err
	}

	m.beginVerification()
	response, err := m.gateway.Activate(ctx, ActivateRequest{
		ActivationKey: activationKey,
		DeviceHash:    deviceHash,
		AppVersion:    m.appVersion,
		Edition:       m.edition,
	})
	if err != nil {
		m.lockWithError(err)
		return err
	}
	if err := m.validateAuthResponse(response, "", true); err != nil {
		m.lockWithError(err)
		return err
	}
	credentials := Credentials{
		PartnerID:    response.PartnerID,
		DeviceSecret: response.DeviceSecret,
		AuraAPIKey:   response.Aura.APIKey,
	}
	if err := m.credentialStore.Save(credentials); err != nil {
		m.lockWithError(err)
		return err
	}
	m.acceptAuth(credentials, response)
	return nil
}

func (m *Manager) Ready() bool {
	m.mu.RLock()
	ready := m.state == StateReady && m.clock().Before(m.sessionExpiresAt)
	m.mu.RUnlock()
	return ready
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{
		State:            m.state,
		PartnerName:      m.partnerName,
		Capabilities:     cloneCapabilities(m.capabilities),
		SessionExpiresAt: m.sessionExpiresAt,
		ErrorCode:        m.errorCode,
	}
}

func (m *Manager) RuntimeSnapshot() RuntimeSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return RuntimeSnapshot{
		State:            m.state,
		PartnerID:        m.partnerID,
		PartnerName:      m.partnerName,
		Capabilities:     cloneCapabilities(m.capabilities),
		SessionExpiresAt: m.sessionExpiresAt,
		ErrorCode:        m.errorCode,
		DeviceSecret:     m.deviceSecret,
		Aura:             m.aura,
		SessionToken:     m.sessionToken,
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.sessionToken = ""
	m.deviceSecret = ""
	m.aura.APIKey = ""
}

func (m *Manager) verify(ctx context.Context, deviceHash string, credentials Credentials) error {
	m.beginVerification()
	response, err := m.gateway.Verify(ctx, VerifyRequest{
		PartnerID:    credentials.PartnerID,
		DeviceSecret: credentials.DeviceSecret,
		DeviceHash:   deviceHash,
		AppVersion:   m.appVersion,
	})
	if err != nil {
		m.lockWithError(err)
		return err
	}
	if err := m.validateAuthResponse(response, credentials.PartnerID, false); err != nil {
		m.lockWithError(err)
		return err
	}
	if response.PartnerID != "" {
		credentials.PartnerID = response.PartnerID
	}
	if response.Aura.APIKey != "" {
		credentials.AuraAPIKey = response.Aura.APIKey
	} else {
		response.Aura.APIKey = credentials.AuraAPIKey
	}
	if err := m.credentialStore.Save(credentials); err != nil {
		m.lockWithError(err)
		return err
	}
	m.acceptAuth(credentials, response)
	return nil
}

func (m *Manager) reverify() {
	m.mu.Lock()
	if m.closed || m.verifyInProgress || m.state != StateReady {
		m.mu.Unlock()
		return
	}
	credentials := Credentials{
		PartnerID:    m.partnerID,
		DeviceSecret: m.deviceSecret,
		AuraAPIKey:   m.aura.APIKey,
	}
	m.verifyInProgress = true
	m.mu.Unlock()

	deviceHash, err := m.deviceHash()
	if err != nil {
		m.lockWithError(err)
		return
	}
	_ = m.verify(context.Background(), deviceHash, credentials)
}

func (m *Manager) beginVerification() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.state = StateVerifying
	m.errorCode = ""
	m.sessionToken = ""
	m.sessionExpiresAt = time.Time{}
	m.verifyInProgress = true
}

func (m *Manager) acceptAuth(credentials Credentials, response AuthResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.state = StateReady
	m.partnerID = credentials.PartnerID
	m.partnerName = response.DisplayName
	m.capabilities = cloneCapabilities(response.Capabilities)
	m.sessionExpiresAt = response.SessionExpiresAt
	m.errorCode = ""
	m.deviceSecret = credentials.DeviceSecret
	m.aura = response.Aura
	m.aura.APIKey = credentials.AuraAPIKey
	m.sessionToken = response.SessionToken
	m.verifyInProgress = false
	m.scheduleReverifyLocked()
}

func (m *Manager) validateAuthResponse(response AuthResponse, expectedPartnerID string, activation bool) error {
	now := m.clock()
	if response.SessionToken == "" || !response.SessionExpiresAt.After(now) {
		return ErrInvalidGatewayResponse
	}
	if activation && (response.PartnerID == "" || response.DeviceSecret == "" || response.Aura.APIKey == "") {
		return ErrInvalidGatewayResponse
	}
	if expectedPartnerID != "" && response.PartnerID != "" && response.PartnerID != expectedPartnerID {
		return ErrInvalidGatewayResponse
	}
	return nil
}

func (m *Manager) scheduleReverifyLocked() {
	now := m.clock()
	reverifyAt := m.sessionExpiresAt.Add(-5 * time.Minute)
	maximum := now.Add(11 * time.Hour)
	if maximum.Before(reverifyAt) {
		reverifyAt = maximum
	}
	delay := reverifyAt.Sub(now)
	if delay < 0 {
		delay = 0
	}
	m.timer = time.AfterFunc(delay, m.reverify)
}

func (m *Manager) lockWithError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.state = StateLocked
	m.partnerID = ""
	m.partnerName = ""
	m.capabilities = Capabilities{}
	m.sessionExpiresAt = time.Time{}
	m.errorCode = sanitizedErrorCode(err)
	m.deviceSecret = ""
	m.aura = AuraRuntime{}
	m.sessionToken = ""
	m.verifyInProgress = false
}

func (m *Manager) setNeedsActivation() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.state = StateNeedsActivation
	m.partnerID = ""
	m.partnerName = ""
	m.capabilities = Capabilities{}
	m.sessionExpiresAt = time.Time{}
	m.errorCode = ""
	m.deviceSecret = ""
	m.aura = AuraRuntime{}
	m.sessionToken = ""
	m.verifyInProgress = false
}

func sanitizedErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAuthorizationFailed):
		return "authorization_failed"
	case errors.Is(err, ErrDeviceMismatch):
		return "device_mismatch"
	case errors.Is(err, ErrGatewayUnavailable):
		return "gateway_unavailable"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrInvalidGatewayResponse):
		return "invalid_gateway_response"
	default:
		return "gateway_error"
	}
}

func cloneCapabilities(capabilities Capabilities) Capabilities {
	capabilities.Features = append([]string(nil), capabilities.Features...)
	capabilities.TextModels = append([]string(nil), capabilities.TextModels...)
	capabilities.ReasoningEfforts = append([]string(nil), capabilities.ReasoningEfforts...)
	return capabilities
}
