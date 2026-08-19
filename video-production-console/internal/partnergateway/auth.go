package partnergateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"video-production-console/internal/security"
)

var (
	ErrInvalidCredential = errors.New("invalid credential")
	ErrDeviceMismatch    = errors.New("device mismatch")
	ErrPartnerDisabled   = errors.New("partner disabled")
	ErrSessionExpired    = errors.New("session expired")
	ErrVersionTooOld     = errors.New("app version is below the minimum supported version")
)

type ActivateRequest struct {
	ActivationKey string `json:"activation_key"`
	DeviceHash    string `json:"device_hash"`
	AppVersion    string `json:"app_version"`
	Edition       string `json:"edition"`
}

type VerifyRequest struct {
	PartnerID    string `json:"partner_id"`
	DeviceSecret string `json:"device_secret"`
	DeviceHash   string `json:"device_hash"`
	AppVersion   string `json:"app_version"`
}

type AuthResponse struct {
	PartnerID        string       `json:"partner_id"`
	DisplayName      string       `json:"display_name"`
	DeviceSecret     string       `json:"device_secret,omitempty"`
	SessionToken     string       `json:"session_token"`
	SessionExpiresAt time.Time    `json:"session_expires_at"`
	Capabilities     Capabilities `json:"capabilities"`
	Aura             AuraRuntime  `json:"aura"`
}

type CreatedPartner struct {
	PartnerID, DisplayName, ActivationKey string
}

type AuthService struct {
	store        *Store
	random       io.Reader
	clock        func() time.Time
	sessionTTL   time.Duration
	minVersion   string
	capabilities Capabilities
	aura         AuraRuntime
}

func NewAuthService(
	store *Store,
	random io.Reader,
	clock func() time.Time,
	sessionTTL time.Duration,
	minVersion string,
	capabilities Capabilities,
	aura AuraRuntime,
) *AuthService {
	if random == nil {
		random = rand.Reader
	}
	if clock == nil {
		clock = time.Now
	}
	if sessionTTL <= 0 {
		sessionTTL = 12 * time.Hour
	}
	if store != nil {
		store.clock = clock
	}
	return &AuthService{
		store:        store,
		random:       random,
		clock:        clock,
		sessionTTL:   sessionTTL,
		minVersion:   minVersion,
		capabilities: capabilities,
		aura:         aura,
	}
}

func (s *AuthService) CreatePartner(ctx context.Context, displayName string) (CreatedPartner, error) {
	if strings.TrimSpace(displayName) == "" {
		return CreatedPartner{}, fmt.Errorf("display name is required")
	}
	activationKey, err := s.newActivationKey()
	if err != nil {
		return CreatedPartner{}, err
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(activationKey), bcrypt.DefaultCost)
	if err != nil {
		return CreatedPartner{}, fmt.Errorf("hash activation key: %w", err)
	}

	now := s.clock().UTC()
	partner := Partner{
		ID:             uuid.NewString(),
		DisplayName:    displayName,
		KeyPrefix:      activationKey[:12],
		KeyHash:        keyHash,
		Status:         PartnerActive,
		SessionVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastVerifiedAt: now,
	}
	if err := s.store.CreatePartner(ctx, partner); err != nil {
		return CreatedPartner{}, err
	}
	return CreatedPartner{
		PartnerID:     partner.ID,
		DisplayName:   partner.DisplayName,
		ActivationKey: activationKey,
	}, nil
}

func (s *AuthService) Activate(ctx context.Context, request ActivateRequest) (AuthResponse, error) {
	if err := s.checkVersion(request.AppVersion); err != nil {
		return AuthResponse{}, err
	}
	if len(request.ActivationKey) < 12 || request.DeviceHash == "" {
		return AuthResponse{}, ErrInvalidCredential
	}

	deviceSecret, deviceSecretHash, err := security.NewSecret(s.random)
	if err != nil {
		return AuthResponse{}, fmt.Errorf("generate device secret: %w", err)
	}
	sessionToken, sessionTokenHash, err := security.NewSecret(s.random)
	if err != nil {
		return AuthResponse{}, fmt.Errorf("generate session token: %w", err)
	}
	now := s.clock().UTC()
	session := Session{
		TokenHash: []byte(sessionTokenHash),
		ExpiresAt: now.Add(s.sessionTTL),
		CreatedAt: now,
	}

	partner, err := s.store.activatePartner(
		ctx,
		request.ActivationKey[:12],
		request.DeviceHash,
		[]byte(deviceSecretHash),
		session,
		now,
		func(keyHash []byte) bool {
			return bcrypt.CompareHashAndPassword(keyHash, []byte(request.ActivationKey)) == nil
		},
	)
	if err != nil {
		return AuthResponse{}, err
	}
	return s.authResponse(partner, deviceSecret, sessionToken, session.ExpiresAt), nil
}

func (s *AuthService) Verify(ctx context.Context, request VerifyRequest) (AuthResponse, error) {
	if err := s.checkVersion(request.AppVersion); err != nil {
		return AuthResponse{}, err
	}
	if request.PartnerID == "" || request.DeviceSecret == "" || request.DeviceHash == "" {
		return AuthResponse{}, ErrInvalidCredential
	}

	sessionToken, sessionTokenHash, err := security.NewSecret(s.random)
	if err != nil {
		return AuthResponse{}, fmt.Errorf("generate session token: %w", err)
	}
	now := s.clock().UTC()
	session := Session{
		TokenHash: []byte(sessionTokenHash),
		ExpiresAt: now.Add(s.sessionTTL),
		CreatedAt: now,
	}
	partner, err := s.store.verifyPartner(
		ctx,
		request.PartnerID,
		request.DeviceHash,
		session,
		now,
		func(deviceSecretHash []byte) bool {
			return security.SecretMatches(string(deviceSecretHash), request.DeviceSecret)
		},
	)
	if err != nil {
		return AuthResponse{}, err
	}
	return s.authResponse(partner, "", sessionToken, session.ExpiresAt), nil
}

func (s *AuthService) SetPartnerStatus(ctx context.Context, partnerID string, status PartnerStatus) error {
	if status != PartnerActive && status != PartnerDisabled {
		return fmt.Errorf("invalid partner status %q", status)
	}
	return s.store.setPartnerStatus(ctx, partnerID, status, s.clock().UTC())
}

func (s *AuthService) RotateKey(ctx context.Context, partnerID string) (string, error) {
	activationKey, err := s.newActivationKey()
	if err != nil {
		return "", err
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(activationKey), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash activation key: %w", err)
	}
	if err := s.store.rotatePartnerKey(ctx, partnerID, activationKey[:12], keyHash, s.clock().UTC()); err != nil {
		return "", err
	}
	return activationKey, nil
}

func (s *AuthService) UnbindDevice(ctx context.Context, partnerID string) error {
	return s.store.unbindDevice(ctx, partnerID, s.clock().UTC())
}

func (s *AuthService) ListPartners(ctx context.Context) ([]Partner, error) {
	return s.store.ListPartners(ctx)
}

func (s *AuthService) newActivationKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", fmt.Errorf("generate activation key: %w", err)
	}
	return "vpc_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *AuthService) authResponse(partner Partner, deviceSecret, sessionToken string, expiresAt time.Time) AuthResponse {
	return AuthResponse{
		PartnerID:        partner.ID,
		DisplayName:      partner.DisplayName,
		DeviceSecret:     deviceSecret,
		SessionToken:     sessionToken,
		SessionExpiresAt: expiresAt,
		Capabilities:     s.capabilities,
		Aura:             s.aura,
	}
}

func (s *AuthService) checkVersion(appVersion string) error {
	if s.minVersion == "" {
		return nil
	}
	comparison, ok := compareVersions(appVersion, s.minVersion)
	if !ok || comparison < 0 {
		return ErrVersionTooOld
	}
	return nil
}

func compareVersions(left, right string) (int, bool) {
	leftParts, leftPrerelease, ok := parseVersion(left)
	if !ok {
		return 0, false
	}
	rightParts, rightPrerelease, ok := parseVersion(right)
	if !ok {
		return 0, false
	}
	for index := range leftParts {
		switch {
		case leftParts[index] < rightParts[index]:
			return -1, true
		case leftParts[index] > rightParts[index]:
			return 1, true
		}
	}
	switch {
	case leftPrerelease == rightPrerelease:
		return 0, true
	case leftPrerelease == "":
		return 1, true
	case rightPrerelease == "":
		return -1, true
	case leftPrerelease < rightPrerelease:
		return -1, true
	default:
		return 1, true
	}
}

func parseVersion(value string) ([3]int, string, bool) {
	var parsed [3]int
	value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
	if buildIndex := strings.IndexByte(value, '+'); buildIndex >= 0 {
		value = value[:buildIndex]
	}
	var prerelease string
	if prereleaseIndex := strings.IndexByte(value, '-'); prereleaseIndex >= 0 {
		prerelease = value[prereleaseIndex+1:]
		value = value[:prereleaseIndex]
	}
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > len(parsed) {
		return parsed, "", false
	}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return parsed, "", false
		}
		parsed[index] = number
	}
	return parsed, prerelease, true
}
