# Partner Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an independent, device-bound gateway that authenticates four partners and proxies only approved text and image models without exposing the upstream credential.

**Architecture:** A new `internal/partnergateway` package owns its SQLite schema, authentication service, policy, limiter and HTTP server. A single `cmd/partner-gateway` binary serves pinned-TLS traffic and exposes local-only administration subcommands against the same database; upstream tests use `httptest.Server` and never call the real API.

**Tech Stack:** Go 1.25, `net/http`, modernc SQLite, bcrypt, SHA-256 opaque-secret hashes, JSON/SSE pass-through, Docker-compatible file secrets.

---

## File map

Create:

- `internal/partnergateway/model.go` — protocol/domain structs and stable errors.
- `internal/partnergateway/store.go` — independent SQLite schema and transaction methods.
- `internal/partnergateway/store_test.go` — schema, WAL, atomic device binding and counters.
- `internal/partnergateway/auth.go` — partner keys, device secrets, sessions and admin actions.
- `internal/partnergateway/auth_test.go` — activation/verify/disable/rotate/unbind behavior.
- `internal/partnergateway/policy.go` — model, reasoning and image request allowlists.
- `internal/partnergateway/policy_test.go` — exact accepted/rejected payloads.
- `internal/partnergateway/limiter.go` — per-partner minute and concurrency limits.
- `internal/partnergateway/limiter_test.go` — isolation and release behavior.
- `internal/partnergateway/upstream.go` — header-safe upstream forwarding.
- `internal/partnergateway/server.go` — routes, middleware and stable JSON errors.
- `internal/partnergateway/server_test.go` — mock-upstream integration and leakage tests.
- `cmd/partner-gateway/main.go` — `serve` and local administration subcommands.
- `cmd/partner-gateway/main_test.go` — configuration/CLI tests.

Do not reuse `internal/store` migrations or persist any gateway row in `video-console-data/console.db`.

### Task 1: Lock the protocol and standalone SQLite schema

**Files:**
- Create: `internal/partnergateway/model.go`
- Create: `internal/partnergateway/store.go`
- Create: `internal/partnergateway/store_test.go`

- [ ] **Step 1: Write the failing store tests**

Create tests that open a temporary database, assert WAL mode, create a partner, and prove the plaintext activation key is not a schema column:

```go
func TestOpenStoreCreatesIndependentWALSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenStore(dbPath)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = store.Close() })

	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil { t.Fatal(err) }
	if !strings.EqualFold(mode, "wal") { t.Fatalf("journal_mode=%q", mode) }

	partner := Partner{ID: "p1", DisplayName: "天中观局", KeyPrefix: "vpc_abcd", KeyHash: []byte("hash"), Status: PartnerActive, SessionVersion: 1}
	if err := store.CreatePartner(context.Background(), partner); err != nil { t.Fatal(err) }
	got, err := store.PartnerByID(context.Background(), "p1")
	if err != nil { t.Fatal(err) }
	if got.DisplayName != "天中观局" || string(got.KeyHash) != "hash" { t.Fatalf("got=%+v", got) }

	rows, err := store.db.Query(`PRAGMA table_info(partners)`)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil { t.Fatal(err) }
		if name == "activation_key" || name == "device_secret" || name == "session_token" { t.Fatalf("plaintext column %q", name) }
	}
}
```

- [ ] **Step 2: Run the test and verify the package does not exist**

Run:

```powershell
go test ./internal/partnergateway -run TestOpenStoreCreatesIndependentWALSchema -v
```

Expected: FAIL because `OpenStore`, `Partner`, and `PartnerActive` are undefined.

- [ ] **Step 3: Define the exact domain types and schema**

Implement these public contracts in `model.go`:

```go
type PartnerStatus string
const (
	PartnerActive PartnerStatus = "active"
	PartnerDisabled PartnerStatus = "disabled"
)

type Partner struct {
	ID, DisplayName, KeyPrefix string
	KeyHash []byte
	Status PartnerStatus
	DeviceHash string
	DeviceSecretHash []byte
	SessionVersion int64
	TextCalls, ImageCalls, VerifyFailures, RateLimited int64
	CreatedAt, UpdatedAt, LastVerifiedAt time.Time
}

type Session struct {
	TokenHash []byte
	PartnerID string
	SessionVersion int64
	ExpiresAt, CreatedAt time.Time
}

type Capabilities struct {
	Features []string `json:"features"`
	TextModels []string `json:"text_models"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	ImageModel string `json:"image_model"`
}

type AuraRuntime struct {
	BaseURL string `json:"base_url"`
	APIKey string `json:"api_key"`
	Model string `json:"model"`
	VoiceID string `json:"voice_id"`
	Speed float64 `json:"speed"`
	Volume float64 `json:"volume"`
}
```

Implement `OpenStore` with a dedicated schema containing `partners`, `sessions`, and `schema_migrations`. Use `_pragma=foreign_keys(ON)`, `_pragma=busy_timeout(5000)`, `SetMaxOpenConns(1)`, `PRAGMA journal_mode=WAL`, a status check constraint, unique display name/key prefix, and foreign-key cascade from sessions to partners. Store timestamps as RFC3339Nano UTC strings. Do not import `internal/store`.

- [ ] **Step 4: Run schema tests**

Run:

```powershell
go test ./internal/partnergateway -run 'TestOpenStore|TestCreatePartner|TestSessionCascade' -v
```

Expected: PASS.

- [ ] **Step 5: Commit the standalone store**

```powershell
git add internal/partnergateway/model.go internal/partnergateway/store.go internal/partnergateway/store_test.go
git commit -m "feat: add partner gateway store"
```

### Task 2: Implement activation, single-device binding and sessions

**Files:**
- Create: `internal/partnergateway/auth.go`
- Create: `internal/partnergateway/auth_test.go`
- Modify: `internal/partnergateway/store.go`

- [ ] **Step 1: Write the failing authentication matrix**

Use a fixed clock and deterministic random reader. Cover first activation, wrong key, second device, verification, disable, unbind and expiration:

```go
func TestActivateBindsExactlyOneDeviceAndVerifyIssuesSession(t *testing.T) {
	svc, store := newAuthFixture(t)
	created, err := svc.CreatePartner(context.Background(), "天中观局")
	if err != nil { t.Fatal(err) }
	if created.ActivationKey == "" { t.Fatal("missing one-time key") }

	first, err := svc.Activate(context.Background(), ActivateRequest{ActivationKey: created.ActivationKey, DeviceHash: "device-a", AppVersion: "0.1.0", Edition: "partner"})
	if err != nil { t.Fatal(err) }
	if first.PartnerID == "" || first.DeviceSecret == "" || first.SessionToken == "" { t.Fatalf("response=%+v", first) }

	_, err = svc.Activate(context.Background(), ActivateRequest{ActivationKey: created.ActivationKey, DeviceHash: "device-b", AppVersion: "0.1.0", Edition: "partner"})
	if !errors.Is(err, ErrDeviceMismatch) { t.Fatalf("err=%v", err) }

	verified, err := svc.Verify(context.Background(), VerifyRequest{PartnerID: first.PartnerID, DeviceSecret: first.DeviceSecret, DeviceHash: "device-a", AppVersion: "0.1.0"})
	if err != nil { t.Fatal(err) }
	if verified.SessionToken == first.SessionToken { t.Fatal("verify must rotate the process session") }
	if _, err := store.SessionPartner(context.Background(), verified.SessionToken); err != nil { t.Fatal(err) }
}
```

Add separate tests asserting `ErrInvalidCredential`, `ErrPartnerDisabled`, `ErrSessionExpired`, rotate invalidates sessions, and unbind clears the device secret.

- [ ] **Step 2: Run the authentication tests and observe undefined contracts**

Run:

```powershell
go test ./internal/partnergateway -run 'TestActivate|TestVerify|TestDisable|TestRotate|TestUnbind' -v
```

Expected: FAIL because `AuthService` and the request/response/error types are undefined.

- [ ] **Step 3: Implement the service with explicit security primitives**

Use these request/response contracts:

```go
type ActivateRequest struct {
	ActivationKey string `json:"activation_key"`
	DeviceHash string `json:"device_hash"`
	AppVersion string `json:"app_version"`
	Edition string `json:"edition"`
}
type VerifyRequest struct {
	PartnerID string `json:"partner_id"`
	DeviceSecret string `json:"device_secret"`
	DeviceHash string `json:"device_hash"`
	AppVersion string `json:"app_version"`
}
type AuthResponse struct {
	PartnerID string `json:"partner_id"`
	DisplayName string `json:"display_name"`
	DeviceSecret string `json:"device_secret,omitempty"`
	SessionToken string `json:"session_token"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	Capabilities Capabilities `json:"capabilities"`
	Aura AuraRuntime `json:"aura"`
}
type CreatedPartner struct { PartnerID, DisplayName, ActivationKey string }
```

Implement `NewAuthService(store, random, clock, sessionTTL, minVersion, capabilities, aura)`. Generate activation keys as `vpc_` plus 32 random base64url bytes, store the first 12 characters as prefix, and store `bcrypt.GenerateFromPassword([]byte(key), bcrypt.DefaultCost)`. Generate device/session secrets with `security.NewSecret`; persist only their SHA-256 hashes. `Activate` must use one `BEGIN IMMEDIATE` transaction to compare the key and bind an empty device; a non-empty different device returns `ErrDeviceMismatch`. `Verify` compares partner ID, device hash and hashed device secret. Each successful call creates a new 12-hour opaque session; `SessionPartner` checks expiry, session version and partner status on every proxy request.

Admin actions must be explicit methods:

```go
CreatePartner(ctx, displayName string) (CreatedPartner, error)
SetPartnerStatus(ctx, partnerID string, status PartnerStatus) error
RotateKey(ctx, partnerID string) (string, error)
UnbindDevice(ctx, partnerID string) error
ListPartners(ctx context.Context) ([]Partner, error)
```

Disable, rotate and unbind increment `session_version` and delete active sessions in the same transaction.

- [ ] **Step 4: Run authentication and race tests**

Run:

```powershell
go test ./internal/partnergateway -run 'TestActivate|TestVerify|TestDisable|TestRotate|TestUnbind' -v
go test -race ./internal/partnergateway -run TestConcurrentActivationBindsOneDevice -v
```

Expected: PASS; the concurrency test reports one success and one `ErrDeviceMismatch`, with no race.

- [ ] **Step 5: Commit authentication**

```powershell
git add internal/partnergateway/auth.go internal/partnergateway/auth_test.go internal/partnergateway/store.go
git commit -m "feat: add device-bound partner authentication"
```

### Task 3: Enforce model policy and partner isolation limits

**Files:**
- Create: `internal/partnergateway/policy.go`
- Create: `internal/partnergateway/policy_test.go`
- Create: `internal/partnergateway/limiter.go`
- Create: `internal/partnergateway/limiter_test.go`

- [ ] **Step 1: Write failing table-driven policy tests**

```go
func TestPolicyValidatesExactModelsAndImageShape(t *testing.T) {
	p := DefaultPolicy()
	tests := []struct{ name string; kind RequestKind; body string; wantErr bool }{
		{"sol medium", ChatRequest, `{"model":"gpt-5.6-sol","reasoning_effort":"medium","messages":[]}`, false},
		{"grok ultra", ChatRequest, `{"model":"grok-4.6","reasoning_effort":"ultra","messages":[]}`, false},
		{"unknown text", ChatRequest, `{"model":"gpt-4o","messages":[]}`, true},
		{"one image", ImageRequest, `{"model":"gpt-image-2","n":1,"size":"1024x1024","prompt":"x"}`, false},
		{"two images", ImageRequest, `{"model":"gpt-image-2","n":2,"size":"1024x1024","prompt":"x"}`, true},
		{"wrong image model", ImageRequest, `{"model":"gpt-image-1","n":1,"size":"1024x1024","prompt":"x"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(tc.kind, []byte(tc.body))
			if (err != nil) != tc.wantErr { t.Fatalf("err=%v", err) }
		})
	}
}
```

Add limiter tests proving partner A cannot consume partner B's quota and concurrency is released after cancellation.

- [ ] **Step 2: Run tests and verify failure**

```powershell
go test ./internal/partnergateway -run 'TestPolicy|TestLimiter' -v
```

Expected: FAIL because `DefaultPolicy` and `NewLimiter` are undefined.

- [ ] **Step 3: Implement immutable defaults**

Define:

```go
var defaultTextModels = map[string]struct{}{"gpt-5.6-sol": {}, "grok-4.6": {}}
var defaultEfforts = map[string]struct{}{"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}, "ultra": {}}
var defaultImageSizes = map[string]struct{}{"1024x1024": {}, "1536x1024": {}, "1024x1536": {}}
```

`Policy.Validate` must reject missing/unknown model, unknown effort, client fields named `api_key`, `base_url`, `authorization`, image `n != 1`, unknown size, and request bodies over the configured limit. It must not log the body.

Implement a mutex-protected limiter keyed by partner ID with a UTC minute bucket, `requestsPerMinute=60`, `maxConcurrent=3`, `Acquire(partnerID) (release func(), err error)`, and `RetryAfter`. Remove idle entries after two minutes. Limits are configuration values with these defaults, not hard-coded in handlers.

- [ ] **Step 4: Run policy and limiter tests**

```powershell
go test ./internal/partnergateway -run 'TestPolicy|TestLimiter' -v
go test -race ./internal/partnergateway -run TestLimiter -v
```

Expected: PASS.

- [ ] **Step 5: Commit policy and limits**

```powershell
git add internal/partnergateway/policy.go internal/partnergateway/policy_test.go internal/partnergateway/limiter.go internal/partnergateway/limiter_test.go
git commit -m "feat: enforce partner gateway policy"
```

### Task 4: Build the header-safe upstream transport and HTTP server

**Files:**
- Create: `internal/partnergateway/upstream.go`
- Create: `internal/partnergateway/server.go`
- Create: `internal/partnergateway/server_test.go`
- Modify: `internal/partnergateway/store.go`

- [ ] **Step 1: Write failing end-to-end handler tests with a mock upstream**

The upstream handler must capture headers and body without logging them:

```go
func TestChatProxyReplacesAuthorizationAndPreservesStreaming(t *testing.T) {
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"ok\":true}\n\n")
	}))
	defer upstream.Close()

	server, session := newHTTPFixture(t, upstream.URL+"/v1", "server-upstream-key")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.6-sol","reasoning_effort":"high","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+session)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
	if gotPath != "/v1/chat/completions" { t.Fatalf("path=%q", gotPath) }
	if gotAuth != "Bearer server-upstream-key" { t.Fatalf("auth=%q", gotAuth) }
	if !strings.Contains(w.Body.String(), `data: {"ok":true}`) { t.Fatalf("body=%q", w.Body.String()) }
}
```

Add tests for activate/verify JSON, `/v1/models`, image proxy, missing/expired session, disabled partner, oversized body, upstream timeout, rate limit, stable error JSON, and a log buffer that must not contain activation key, device secret, session, upstream key, prompt or base64 image.

- [ ] **Step 2: Run server tests and verify failure**

```powershell
go test ./internal/partnergateway -run 'TestChatProxy|TestImageProxy|TestActivateHandler|TestVerifyHandler|TestModels|TestServerDoesNotLogSecrets' -v
```

Expected: FAIL because `NewServer` and `Upstream` are undefined.

- [ ] **Step 3: Implement the server boundary**

Define configuration and dependencies:

```go
type ServerOptions struct {
	Auth *AuthService
	Store *Store
	Policy Policy
	Limiter *Limiter
	UpstreamBaseURL *url.URL
	UpstreamAPIKey string
	HTTPClient *http.Client
	Logger *slog.Logger
	MaxRequestBytes int64
	MaxResponseBytes int64
}
```

Register only:

```go
mux.HandleFunc("GET /healthz", server.health)
mux.HandleFunc("POST /auth/activate", server.activate)
mux.HandleFunc("POST /auth/verify", server.verify)
mux.HandleFunc("GET /v1/models", server.withSession(server.models))
mux.HandleFunc("POST /v1/chat/completions", server.withSession(server.proxyChat))
mux.HandleFunc("POST /v1/images/generations", server.withSession(server.proxyImage))
```

Accept the opaque session only in `Authorization: Bearer <session>` from the partner client; never forward that header. Create a new upstream request, copy only `Content-Type`, `Accept` and request cancellation context, set `Authorization: Bearer <server key>`, and join the configured URL with the fixed route. Use `http.MaxBytesReader` for input, a client timeout for non-stream setup, `io.LimitReader` for response protection, and forward only safe response headers (`Content-Type`, `Cache-Control`, request id). Do not decode/re-encode successful upstream bodies.

All errors use:

```go
type ErrorResponse struct {
	Error struct {
		Code, Message, RequestID string
	} `json:"error"`
}
```

Use fixed messages such as `authorization_failed`, `device_mismatch`, `model_not_allowed`, `rate_limited`, `upstream_unavailable`; never include upstream URL/body/header. Structured logs contain request ID, partner ID, route, model, status, duration and byte counts only.

- [ ] **Step 4: Run integration and race tests**

```powershell
go test ./internal/partnergateway -v
go test -race ./internal/partnergateway -run 'TestChatProxy|TestLimiter|TestConcurrentActivation' -v
```

Expected: PASS; leakage test reports no forbidden string.

- [ ] **Step 5: Commit the HTTP gateway**

```powershell
git add internal/partnergateway/upstream.go internal/partnergateway/server.go internal/partnergateway/server_test.go internal/partnergateway/store.go
git commit -m "feat: proxy approved partner model requests"
```

### Task 5: Add the gateway command and local-only administration

**Files:**
- Create: `cmd/partner-gateway/main.go`
- Create: `cmd/partner-gateway/main_test.go`

- [ ] **Step 1: Write failing CLI configuration tests**

```go
func TestLoadServeConfigRequiresSecretFilesAndTLS(t *testing.T) {
	t.Setenv("PARTNER_GATEWAY_DB", filepath.Join(t.TempDir(), "gateway.db"))
	t.Setenv("PARTNER_GATEWAY_LISTEN", ":2443")
	t.Setenv("PARTNER_GATEWAY_UPSTREAM", "http://127.0.0.1:2001/v1")
	_, err := loadServeConfig()
	if err == nil || !strings.Contains(err.Error(), "UPSTREAM_KEY_FILE") { t.Fatalf("err=%v", err) }
}

func TestRunPartnerCreatePrintsKeyOnce(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"partner", "create", "--db", filepath.Join(t.TempDir(), "g.db"), "--name", "天中观局"}, &out, io.Discard)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(out.String(), "vpc_") { t.Fatalf("out=%q", out.String()) }
}
```

- [ ] **Step 2: Run command tests and verify failure**

```powershell
go test ./cmd/partner-gateway -v
```

Expected: FAIL because `run` and `loadServeConfig` are undefined.

- [ ] **Step 3: Implement exact subcommands and file-secret loading**

`main` delegates to `run(ctx, os.Args[1:], os.Stdout, os.Stderr)`. Support:

```text
serve
partner create --db <path> --name <display-name>
partner list --db <path>
partner enable --db <path> --id <partner-id>
partner disable --db <path> --id <partner-id>
partner rotate-key --db <path> --id <partner-id>
partner unbind --db <path> --id <partner-id>
```

`serve` reads these required variables:

```text
PARTNER_GATEWAY_LISTEN
PARTNER_GATEWAY_DB
PARTNER_GATEWAY_TLS_CERT
PARTNER_GATEWAY_TLS_KEY
PARTNER_GATEWAY_UPSTREAM
PARTNER_GATEWAY_UPSTREAM_KEY_FILE
PARTNER_GATEWAY_AURA_KEY_FILE
PARTNER_GATEWAY_AURA_BASE_URL
PARTNER_GATEWAY_AURA_MODEL
PARTNER_GATEWAY_AURA_VOICE_ID
```

Trim one trailing newline from secret files, reject empty/world-readable secret files on Linux, never accept secret values as command-line flags, and never print them. Serve with `http.Server{ReadHeaderTimeout: 10*time.Second, IdleTimeout: 90*time.Second}` and `ListenAndServeTLS`. Handle SIGINT/SIGTERM with a 15-second graceful shutdown.

- [ ] **Step 4: Run command and package tests**

```powershell
go test ./cmd/partner-gateway ./internal/partnergateway -v
go vet ./cmd/partner-gateway ./internal/partnergateway
```

Expected: PASS.

- [ ] **Step 5: Commit the command**

```powershell
git add cmd/partner-gateway/main.go cmd/partner-gateway/main_test.go
git commit -m "feat: add partner gateway command"
```

### Task 6: Complete the M1 verification gate

**Files:**
- Modify only files already owned by M1 when a test exposes a defect.

- [ ] **Step 1: Run focused tests**

```powershell
go test ./internal/partnergateway ./cmd/partner-gateway
go test -race ./internal/partnergateway
go vet ./internal/partnergateway ./cmd/partner-gateway
```

Expected: PASS.

- [ ] **Step 2: Prove no real network dependency**

Run:

```powershell
go test ./internal/partnergateway -run 'TestChatProxy|TestImageProxy' -count=20
```

Expected: PASS using only loopback `httptest.Server`; no request reaches `23.138.12.112` or Aura.

- [ ] **Step 3: Inspect the staged diff for secret-bearing literals**

```powershell
git diff --check
rg -n "23\.138\.12\.112:2001|Authorization: Bearer [A-Za-z0-9]" internal/partnergateway cmd/partner-gateway
```

Expected: `git diff --check` is clean and `rg` returns no embedded real credential or direct partner use of port 2001.

- [ ] **Step 4: Commit any verification-only fixes**

```powershell
git add internal/partnergateway cmd/partner-gateway
git commit -m "test: harden partner gateway verification"
```

Expected: create this commit only if Step 1-3 required a code/test correction; otherwise leave history unchanged.
