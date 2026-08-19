package partnergateway

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActivateBindsExactlyOneDeviceAndVerifyIssuesSession(t *testing.T) {
	svc, store := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}
	if created.ActivationKey == "" {
		t.Fatal("missing one-time key")
	}

	first, err := svc.Activate(context.Background(), ActivateRequest{ActivationKey: created.ActivationKey, DeviceHash: "device-a", AppVersion: "0.1.0", Edition: "partner"})
	if err != nil {
		t.Fatal(err)
	}
	if first.PartnerID == "" || first.DeviceSecret == "" || first.SessionToken == "" {
		t.Fatalf("response=%+v", first)
	}

	_, err = svc.Activate(context.Background(), ActivateRequest{ActivationKey: created.ActivationKey, DeviceHash: "device-b", AppVersion: "0.1.0", Edition: "partner"})
	if !errors.Is(err, ErrDeviceMismatch) {
		t.Fatalf("err=%v", err)
	}

	verified, err := svc.Verify(context.Background(), VerifyRequest{PartnerID: first.PartnerID, DeviceSecret: first.DeviceSecret, DeviceHash: "device-a", AppVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if verified.SessionToken == first.SessionToken {
		t.Fatal("verify must rotate the process session")
	}
	if _, err := store.SessionPartner(context.Background(), verified.SessionToken); err != nil {
		t.Fatal(err)
	}
}

func TestActivateAndVerifyRejectInvalidCredentials(t *testing.T) {
	svc, _ := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}

	wrongKey := created.ActivationKey[:len(created.ActivationKey)-1] + "!"
	if _, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: wrongKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong activation key err=%v", err)
	}

	first, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(context.Background(), VerifyRequest{
		PartnerID:    first.PartnerID,
		DeviceSecret: first.DeviceSecret + "-wrong",
		DeviceHash:   "device-a",
		AppVersion:   "0.1.0",
	}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong device secret err=%v", err)
	}
}

func TestDisableRejectsVerificationAndInvalidatesSessions(t *testing.T) {
	svc, store := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.SetPartnerStatus(context.Background(), first.PartnerID, PartnerDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(context.Background(), VerifyRequest{
		PartnerID:    first.PartnerID,
		DeviceSecret: first.DeviceSecret,
		DeviceHash:   "device-a",
		AppVersion:   "0.1.0",
	}); !errors.Is(err, ErrPartnerDisabled) {
		t.Fatalf("verify disabled partner err=%v", err)
	}
	if _, err := store.SessionPartner(context.Background(), first.SessionToken); err == nil {
		t.Fatal("disabled partner session remains valid")
	}
}

func TestVerifySessionExpires(t *testing.T) {
	svc, store, clock := newAuthFixtureWithTTL(t, time.Hour)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Hour)
	if _, err := store.SessionPartner(context.Background(), first.SessionToken); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired session err=%v", err)
	}
}

func TestRotateKeyInvalidatesSessionsAndOldKey(t *testing.T) {
	svc, store := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}

	newKey, err := svc.RotateKey(context.Background(), first.PartnerID)
	if err != nil {
		t.Fatal(err)
	}
	if newKey == "" || newKey == created.ActivationKey {
		t.Fatalf("rotated key=%q", newKey)
	}
	if _, err := store.SessionPartner(context.Background(), first.SessionToken); err == nil {
		t.Fatal("rotated partner session remains valid")
	}
	if _, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old activation key err=%v", err)
	}
	if _, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: newKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	}); err != nil {
		t.Fatalf("new activation key err=%v", err)
	}
}

func TestUnbindDeviceClearsSecretAndAllowsNewDevice(t *testing.T) {
	svc, _ := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.UnbindDevice(context.Background(), first.PartnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(context.Background(), VerifyRequest{
		PartnerID:    first.PartnerID,
		DeviceSecret: first.DeviceSecret,
		DeviceHash:   "device-a",
		AppVersion:   "0.1.0",
	}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old device secret err=%v", err)
	}
	second, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-b",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.DeviceSecret == "" || second.DeviceSecret == first.DeviceSecret {
		t.Fatalf("new device secret=%q", second.DeviceSecret)
	}
}

func TestActivateAndVerifyRejectOldAppVersion(t *testing.T) {
	svc, _ := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.0.9",
		Edition:       "partner",
	}); !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("old activation version err=%v", err)
	}
	first, err := svc.Activate(context.Background(), ActivateRequest{
		ActivationKey: created.ActivationKey,
		DeviceHash:    "device-a",
		AppVersion:    "0.1.0",
		Edition:       "partner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(context.Background(), VerifyRequest{
		PartnerID:    first.PartnerID,
		DeviceSecret: first.DeviceSecret,
		DeviceHash:   "device-a",
		AppVersion:   "0.0.9",
	}); !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("old verification version err=%v", err)
	}
}

func TestConcurrentActivationBindsOneDevice(t *testing.T) {
	svc, _ := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, deviceHash := range []string{"device-a", "device-b"} {
		deviceHash := deviceHash
		go func() {
			<-start
			_, err := svc.Activate(context.Background(), ActivateRequest{
				ActivationKey: created.ActivationKey,
				DeviceHash:    deviceHash,
				AppVersion:    "0.1.0",
				Edition:       "partner",
			})
			results <- err
		}()
	}
	close(start)

	var successes, mismatches int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrDeviceMismatch):
			mismatches++
		default:
			t.Fatalf("unexpected activation err=%v", err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Fatalf("successes=%d mismatches=%d", successes, mismatches)
	}
}

func newAuthFixture(t *testing.T) (*AuthService, *Store) {
	t.Helper()
	svc, store, _ := newAuthFixtureWithTTL(t, 12*time.Hour)
	return svc, store
}

func newAuthFixtureWithTTL(t *testing.T, ttl time.Duration) (*AuthService, *Store, *testClock) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	clock := &testClock{now: time.Date(2035, time.August, 19, 12, 0, 0, 0, time.UTC)}
	random := &deterministicReader{}
	capabilities := Capabilities{Features: []string{"text"}, TextModels: []string{"gpt-test"}}
	aura := AuraRuntime{BaseURL: "https://aura.test", APIKey: "test-key", Model: "aura-test"}
	svc := NewAuthService(store, random, clock.Now, ttl, "0.1.0", capabilities, aura)
	return svc, store, clock
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type deterministicReader struct {
	mu   sync.Mutex
	next byte
}

func (r *deterministicReader) Read(buffer []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range buffer {
		r.next++
		buffer[index] = r.next
	}
	return len(buffer), nil
}
