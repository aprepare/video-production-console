# Partner Client and Edition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Windows partner edition that activates against the M1 gateway, binds to one device, hides all upstream configuration, and exposes only scenery montage and image-text production.

**Architecture:** A new `internal/partnerclient` manager owns pinned-TLS communication, DPAPI-protected device credentials and the in-memory session. `internal/partneredition` provides immutable build metadata; the existing settings/runtime and HTTP app receive partner-specific adapters so owner mode stays unchanged.

**Tech Stack:** Go 1.25, Windows DPAPI, custom `x509.CertPool`, React 19, TanStack Query, Vitest, existing OpenAI-compatible/image/Aura clients.

---

## File map

Create:

- `internal/partneredition/edition.go`, `edition_test.go` — immutable owner/partner build flavor and gateway metadata.
- `internal/partnerclient/protocol.go` — M1 JSON contracts mirrored without importing server internals.
- `internal/partnerclient/device_windows.go`, `device_other.go`, `device_test.go` — one-way MachineGuid device hash.
- `internal/partnerclient/credentials.go`, `credentials_test.go` — DPAPI-protected credential file.
- `internal/partnerclient/client.go`, `client_test.go` — pinned-CA activate/verify client.
- `internal/partnerclient/manager.go`, `manager_test.go` — locked/ready state, session and silent re-verification.
- `internal/httpapi/partner.go`, `partner_test.go` — local activation/status endpoints.
- `internal/httpapi/partner_settings.go`, `partner_settings_test.go` — curated partner settings response/update.
- `web/src/partner/PartnerGate.tsx`, `PartnerGate.test.tsx`, `api.ts`, `types.ts` — activation/error gate.
- `web/src/settings/PartnerSettingsPanel.tsx`, `PartnerSettingsPanel.test.tsx` — non-sensitive partner settings.

Modify:

- `cmd/console/main.go`, `cmd/console/main_test.go` — partner startup without external Codex CLI and manager wiring.
- `internal/app/app.go`, `internal/app/app_test.go` — partner routes/middleware/capabilities and Aura producer selection.
- `internal/settings/service.go`, `service_test.go` — in-memory partner runtime override.
- `internal/httpapi/settings.go`, `settings_test.go` — edition-aware settings handler selection.
- `web/src/App.tsx`, `web/src/production-modes/catalog.ts`, `web/src/production-modes/ModeHome.tsx`, `web/src/settings/SettingsPanel.tsx` — gate and capability-limited UI.
- `web/src/taskModel.ts`, `web/src/ModelSelect.tsx` — accept server-provided partner allowlists.

### Task 1: Add immutable edition metadata

**Files:**
- Create: `internal/partneredition/edition.go`
- Create: `internal/partneredition/edition_test.go`

- [ ] **Step 1: Write failing edition tests**

```go
func TestCurrentRejectsInvalidBuildMetadata(t *testing.T) {
	oldEdition, oldGateway := builtEdition, builtGatewayURL
	t.Cleanup(func() { builtEdition, builtGatewayURL = oldEdition, oldGateway })
	builtEdition, builtGatewayURL = "partner", "https://23.138.12.112:2443"
	got, err := Current()
	if err != nil { t.Fatal(err) }
	if !got.IsPartner() || got.GatewayURL != "https://23.138.12.112:2443" { t.Fatalf("got=%+v", got) }

	builtEdition = "unexpected"
	if _, err := Current(); !errors.Is(err, ErrInvalidEdition) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/partneredition -v
```

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement the fixed build contract**

```go
type Name string
const (
	Owner Name = "owner"
	Partner Name = "partner"
)

var builtEdition = string(Owner)
var builtGatewayURL = ""

type Config struct {
	Name Name
	GatewayURL string
}

func Current() (Config, error) {
	name := Name(strings.TrimSpace(builtEdition))
	switch name {
	case Owner:
		return Config{Name: Owner}, nil
	case Partner:
		u, err := url.Parse(strings.TrimSpace(builtGatewayURL))
		if err != nil || u.Scheme != "https" || u.Host != "23.138.12.112:2443" || u.Path != "" { return Config{}, ErrInvalidGateway }
		return Config{Name: Partner, GatewayURL: u.String()}, nil
	default:
		return Config{}, ErrInvalidEdition
	}
}

func (c Config) IsPartner() bool { return c.Name == Partner }
```

The partner build script later sets both variables with `-ldflags -X`. Do not add an environment-variable or front-end override.

- [ ] **Step 4: Run tests**

```powershell
go test ./internal/partneredition -v
```

Expected: PASS.

- [ ] **Step 5: Commit edition metadata**

```powershell
git add internal/partneredition
git commit -m "feat: add immutable partner edition"
```

### Task 2: Implement device identity and DPAPI credential storage

**Files:**
- Create: `internal/partnerclient/protocol.go`
- Create: `internal/partnerclient/device_windows.go`
- Create: `internal/partnerclient/device_other.go`
- Create: `internal/partnerclient/device_test.go`
- Create: `internal/partnerclient/credentials.go`
- Create: `internal/partnerclient/credentials_test.go`

- [ ] **Step 1: Write failing device and credential tests**

```go
func TestHashMachineGUIDNeverReturnsRawValue(t *testing.T) {
	raw := "01234567-89ab-cdef-0123-456789abcdef"
	got := hashMachineGUID(raw)
	if got == raw || len(got) != 64 { t.Fatalf("hash=%q", got) }
	if got != hashMachineGUID(strings.ToUpper(raw)) { t.Fatalf("hash must normalize GUID") }
}

func TestCredentialStoreEncryptsSecretsAndRoundTrips(t *testing.T) {
	protector := &fakeProtector{prefix: []byte("cipher:")}
	path := filepath.Join(t.TempDir(), "partner-credentials.json")
	store := NewCredentialStore(path, protector)
	want := Credentials{PartnerID: "p1", DeviceSecret: "device-secret", AuraAPIKey: "aura-secret"}
	if err := store.Save(want); err != nil { t.Fatal(err) }
	raw, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if bytes.Contains(raw, []byte("device-secret")) || bytes.Contains(raw, []byte("aura-secret")) { t.Fatalf("plaintext leaked: %s", raw) }
	got, err := store.Load()
	if err != nil { t.Fatal(err) }
	if got != want { t.Fatalf("got=%+v want=%+v", got, want) }
}
```

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/partnerclient -run 'TestHashMachineGUID|TestCredentialStore' -v
```

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement exact storage and protocol contracts**

`protocol.go` mirrors the M1 JSON fields:

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
type AuthResponse struct {
	PartnerID string `json:"partner_id"`
	DisplayName string `json:"display_name"`
	DeviceSecret string `json:"device_secret"`
	SessionToken string `json:"session_token"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	Capabilities Capabilities `json:"capabilities"`
	Aura AuraRuntime `json:"aura"`
}
type Credentials struct { PartnerID, DeviceSecret, AuraAPIKey string }
```

On Windows, read `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` with `golang.org/x/sys/windows/registry`, normalize lowercase/trimmed text, and return `hex(SHA-256("video-production-console/partner/v1\x00" + guid))`. Never log or persist the raw GUID. Non-Windows returns `ErrDeviceIdentityUnsupported`.

`CredentialStore.Save` JSON-encodes only `partner_id`, `protected_device_secret`, and `protected_aura_key`; call the existing `security.Protector` separately for each secret, base64-encode ciphertext, write to a same-directory temporary file with mode `0600`, sync, then rename. `Load` rejects unknown version, empty ciphertext and DPAPI errors. Do not store session tokens.

- [ ] **Step 4: Run portable and Windows tests**

```powershell
go test ./internal/partnerclient -v
go test ./internal/security -run Secret -v
```

Expected: PASS on Windows. Non-Windows build tests assert the unsupported error without reading a registry.

- [ ] **Step 5: Commit credentials**

```powershell
git add internal/partnerclient internal/security
git commit -m "feat: protect partner device credentials"
```

### Task 3: Add pinned-TLS gateway client and startup manager

**Files:**
- Create: `internal/partnerclient/client.go`
- Create: `internal/partnerclient/client_test.go`
- Create: `internal/partnerclient/manager.go`
- Create: `internal/partnerclient/manager_test.go`

- [ ] **Step 1: Write failing TLS and state tests**

```go
func TestNewClientTrustsOnlyPinnedCA(t *testing.T) {
	goodServer, caPEM := newTLSServerForIP(t, "23.138.12.112")
	badServer, _ := newTLSServerForIP(t, "23.138.12.112")
	client, err := NewClient(ClientOptions{BaseURL: goodServer.URL, CAPEM: caPEM, DialContext: dialTestServer(goodServer)})
	if err != nil { t.Fatal(err) }
	if _, err := client.Models(context.Background(), "session"); err != nil { t.Fatal(err) }
	client.baseURL = badServer.URL
	client.httpClient.Transport = transportWithPinnedCA(caPEM, dialTestServer(badServer))
	if _, err := client.Models(context.Background(), "session"); err == nil { t.Fatal("untrusted CA accepted") }
}

func TestManagerRequiresOnlineVerifyEveryStart(t *testing.T) {
	manager := newManagerFixture(t, fakeGateway{verifyErr: ErrGatewayUnavailable}, savedCredentials())
	if err := manager.Start(context.Background()); !errors.Is(err, ErrGatewayUnavailable) { t.Fatalf("err=%v", err) }
	if got := manager.Snapshot(); got.State != StateLocked || got.SessionToken != "" { t.Fatalf("snapshot=%+v", got) }
}
```

Add tests for wrong IP SAN, expired certificate, successful activation saves credentials, verify response refreshes Aura key, session kept only in memory, session expiry triggers reverify, and authorization failure returns to locked state.

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/partnerclient -run 'TestNewClient|TestManager' -v
```

Expected: FAIL because client and manager types are undefined.

- [ ] **Step 3: Implement the strict client and manager**

`NewClient` must:

```go
pool := x509.NewCertPool()
if !pool.AppendCertsFromPEM(options.CAPEM) { return nil, ErrInvalidPinnedCA }
transport := http.DefaultTransport.(*http.Transport).Clone()
transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: "23.138.12.112"}
transport.Proxy = http.ProxyFromEnvironment
```

Use fixed paths `/auth/activate`, `/auth/verify`, `/v1/models`; cap JSON responses at 1 MiB; require `Content-Type: application/json`; map stable gateway error codes without returning raw bodies. For authenticated model calls, expose `RoundTripper(session func() string)` that always replaces Authorization with the current opaque session.

Manager states are:

```go
type State string
const (
	StateNeedsActivation State = "needs_activation"
	StateVerifying State = "verifying"
	StateReady State = "ready"
	StateLocked State = "locked"
)
```

`Start` always reads device hash and calls verify when credentials exist; no cached-ready or offline branch. `Activate` sends the one-time key, saves device/Aura secrets through DPAPI, and keeps the new session only in memory. `Snapshot` returns status, partner name, capabilities, session expiry and sanitized error code but omits device secret, Aura key and session token from JSON. `RuntimeSnapshot` is an internal method that supplies those values to settings/runtime consumers. Reverify at `min(expiresAt-5m, now+11h)` and lock immediately on authorization failure.

- [ ] **Step 4: Run TLS/state/race tests**

```powershell
go test ./internal/partnerclient -v
go test -race ./internal/partnerclient -run TestManager -v
```

Expected: PASS; tests use only local TLS servers.

- [ ] **Step 5: Commit the partner client**

```powershell
git add internal/partnerclient
git commit -m "feat: add pinned partner gateway client"
```

### Task 4: Gate the local HTTP app and sanitize settings

**Files:**
- Create: `internal/httpapi/partner.go`
- Create: `internal/httpapi/partner_test.go`
- Create: `internal/httpapi/partner_settings.go`
- Create: `internal/httpapi/partner_settings_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/httpapi/settings.go`
- Modify: `internal/httpapi/settings_test.go`

- [ ] **Step 1: Write failing middleware and settings tests**

```go
func TestPartnerModeBlocksBusinessAPIUntilVerified(t *testing.T) {
	manager := newFakePartnerManager(partnerclient.StateNeedsActivation)
	app := New(Options{Partner: manager})
	for _, path := range []string{"/api/projects", "/api/image-projects", "/api/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized { t.Fatalf("%s status=%d", path, w.Code) }
	}
	for _, path := range []string{"/api/health", "/api/partner/status"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK { t.Fatalf("%s status=%d", path, w.Code) }
	}
}

func TestPartnerSettingsNeverReturnOrAcceptServiceFields(t *testing.T) {
	h := NewPartnerSettingsHandler(fakeSettings())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	for _, forbidden := range []string{"base_url", "api_key", "23.138.12.112", "aurastd_base_url"} {
		if strings.Contains(strings.ToLower(w.Body.String()), forbidden) { t.Fatalf("leaked %q: %s", forbidden, w.Body.String()) }
	}
	bad := `{"remix_base_url":"http://attacker","media_root":"D:\\\\Media"}`
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(bad)))
	if w.Code != http.StatusBadRequest { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
}
```

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/httpapi ./internal/app -run 'TestPartnerMode|TestPartnerSettings' -v
```

Expected: FAIL because partner options/handlers do not exist.

- [ ] **Step 3: Add partner routes and defense-in-depth**

Extend `app.Options` with an interface that exposes `Snapshot`, `Activate`, `Ready`, and `RuntimeSnapshot`. Register:

```go
mux.Handle("GET /api/partner/status", httpapi.NewPartnerHandler(options.Partner))
mux.Handle("POST /api/partner/activate", httpapi.NewPartnerHandler(options.Partner))
```

In partner mode, allow static files, `/api/health` and `/api/partner/*` before readiness; every other `/api/*` receives a stable `401 partner_verification_required`. When ready, block unsupported API prefixes such as ideas, skills, dependencies and movie/image-to-video endpoints with `404` even if a caller bypasses the UI. Preserve the existing owner `AuthService` middleware byte-for-byte for owner mode.

Define a curated response:

```go
type PartnerSettingsView struct {
	TextModels []string `json:"text_models,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
	ImageModel string `json:"image_model,omitempty"`
	AuraModel string `json:"aura_model,omitempty"`
	AuraVoiceID string `json:"aura_voice_id,omitempty"`
	MediaRoot string `json:"media_root,omitempty"`
	JianyingRoot string `json:"jianying_root,omitempty"`
	MachineProfilePath string `json:"machine_profile_path,omitempty"`
	DefaultImageRatio string `json:"default_image_ratio,omitempty"`
	DefaultImageStyle string `json:"default_image_style,omitempty"`
	AuraSpeed float64 `json:"aura_speed,omitempty"`
	AuraVolume float64 `json:"aura_volume,omitempty"`
}
```

Use a separate `PartnerSettingsUpdate` decoder with `DisallowUnknownFields`; allow only path, image presentation, speed and volume fields. Never serialize `domain.PublicSettings` or secret statuses in partner mode.

- [ ] **Step 4: Run app/handler tests**

```powershell
go test ./internal/httpapi ./internal/app -run 'TestPartnerMode|TestPartnerSettings|TestOwner' -v
```

Expected: PASS and existing owner-auth tests remain unchanged.

- [ ] **Step 5: Commit the HTTP gate**

```powershell
git add internal/httpapi/partner.go internal/httpapi/partner_test.go internal/httpapi/partner_settings.go internal/httpapi/partner_settings_test.go internal/httpapi/settings.go internal/httpapi/settings_test.go internal/app/app.go internal/app/app_test.go
git commit -m "feat: gate partner console and sanitize settings"
```

### Task 5: Feed the in-memory session into text, image and Aura runtimes

**Files:**
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/agentruntime/router.go`
- Modify: `internal/agentruntime/router_test.go`
- Modify: `internal/imageproject/client_test.go`
- Modify: `internal/httpapi/narration.go`
- Modify: `internal/httpapi/narration_test.go`

- [ ] **Step 1: Write failing runtime override tests**

```go
func TestPartnerRuntimeOverridesPersistedProvidersWithoutPersistingSession(t *testing.T) {
	service := newSettingsFixture(t)
	service.SetPartnerRuntime(PartnerRuntime{
		GatewayBaseURL: "https://23.138.12.112:2443/v1",
		SessionToken: "opaque-session",
		TextModels: []string{"gpt-5.6-sol", "grok-4.6"},
		ImageModel: "gpt-image-2",
		AuraBaseURL: "https://tts.aurastd.com", AuraAPIKey: "aura-key", AuraModel: "speech-2.8-hd", AuraVoiceID: "voice-id",
	})
	runtime, err := service.Runtime(context.Background())
	if err != nil { t.Fatal(err) }
	if runtime.RemixBaseURL != "https://23.138.12.112:2443/v1" || runtime.RemixAPIKey != "opaque-session" { t.Fatalf("runtime=%+v", runtime) }
	if runtime.ImageModel != "gpt-image-2" || runtime.ImageAPIKey != "opaque-session" { t.Fatalf("runtime=%+v", runtime) }
	rawDB := readEncryptedSecrets(t, service)
	if bytes.Contains(rawDB, []byte("opaque-session")) || bytes.Contains(rawDB, []byte("aura-key")) { t.Fatal("partner runtime persisted") }
}
```

Add a narration test proving partner mode constructs Aura while owner mode preserves the current provider; add a draft test proving subtitle tracks are disabled while SRT/word timing files remain.

- [ ] **Step 2: Run targeted runtime tests**

```powershell
go test ./internal/settings ./internal/app ./internal/httpapi ./internal/agentruntime -run 'TestPartnerRuntime|TestPartnerNarration|TestOwnerNarration|TestPartnerSubtitle' -v
```

Expected: FAIL because `PartnerRuntime` and edition-aware producer wiring do not exist.

- [ ] **Step 3: Implement a memory-only override**

Add to the settings service:

```go
type PartnerRuntime struct {
	GatewayBaseURL, SessionToken string
	TextModels []string
	ImageModel string
	AuraBaseURL, AuraAPIKey, AuraModel, AuraVoiceID string
	AuraSpeed, AuraVolume float64
}

func (s *Service) SetPartnerRuntime(value *PartnerRuntime)
```

Protect it with the existing service mutex, clone slices, and never call `store.UpdateAtomic`. In `Runtime`, apply the override only when non-nil: gateway URL/session become Remix, Grok, ImageText and Image Base URL/API key; allowed models remain `gpt-5.6-sol`, `grok-4.6`, and `gpt-image-2`; Aura values come from the activation response. A nil override preserves owner behavior.

Use the gateway URL with `/v1`; existing `chatCompletionsURL` and `generationURL` append the correct endpoint. Do not change their support for streaming or base64/url image responses. Add an edition-aware narration producer factory so partner mode always uses `narration.NewAuraSTDClient` with memory-only runtime, while owner mode keeps its configured provider. Keep narration file creation unchanged and set the partner draft subtitle-insertion option false.

- [ ] **Step 4: Run text/image/Aura tests**

```powershell
go test ./internal/settings ./internal/agentruntime/openaicompat ./internal/imageproject ./internal/narration ./internal/httpapi ./internal/app
```

Expected: PASS. HTTP client tests assert partner session is the gateway Authorization value, and no setting/manifest contains it.

- [ ] **Step 5: Commit runtime integration**

```powershell
git add internal/settings/service.go internal/settings/service_test.go internal/app/app.go internal/app/app_test.go internal/agentruntime internal/imageproject internal/httpapi/narration.go internal/httpapi/narration_test.go internal/narration
git commit -m "feat: route partner media models through runtime session"
```

### Task 6: Wire partner startup without an external Codex CLI

**Files:**
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`

- [ ] **Step 1: Write the failing startup test**

```go
func TestPartnerEditionStartsWithoutCodexBinary(t *testing.T) {
	deps := newMainTestDeps(t)
	deps.Edition = partneredition.Config{Name: partneredition.Partner, GatewayURL: "https://23.138.12.112:2443"}
	deps.CodexBinaryPath = filepath.Join(t.TempDir(), "missing-codex.exe")
	deps.PartnerManager = readyPartnerManager()
	if err := runConsole(context.Background(), deps); err != nil { t.Fatalf("partner startup: %v", err) }
}
```

Add an owner test proving a missing configured Codex binary still produces the existing error.

- [ ] **Step 2: Run and verify the current hard dependency fails**

```powershell
go test ./cmd/console -run 'TestPartnerEditionStartsWithoutCodex|TestOwnerEditionStillRequiresCodex' -v
```

Expected: partner test FAIL at the current Codex binary validation.

- [ ] **Step 3: Branch only the dependency and scheduler wiring**

Load `partneredition.Current()` before validating external tools. In partner mode:

- construct the credential store under `<data-root>/partner/credentials.json` using `security.NewSecretProtector()`;
- load the pinned CA from `<app-root>/resources/tls/partner-ca.crt`;
- create/start `partnerclient.Manager` before `app.New`;
- allow startup to continue in locked state so the activation page is reachable;
- do not start the Codex app-server or Codex-only engineering features;
- preserve the internal OpenAI-compatible task path needed by scenery rewrite and image planning;
- pass the manager into `app.Options` and refresh `settings.SetPartnerRuntime` whenever manager state changes.

Owner mode follows the existing startup sequence without new requirements.

- [ ] **Step 4: Run command and owner regression tests**

```powershell
go test ./cmd/console -v
go test ./internal/app ./internal/settings ./internal/partnerclient
```

Expected: PASS.

- [ ] **Step 5: Commit startup wiring**

```powershell
git add cmd/console/main.go cmd/console/main_test.go
git commit -m "feat: start partner console without codex cli"
```

### Task 7: Build the activation and capability-limited frontend

**Files:**
- Create: `web/src/partner/PartnerGate.tsx`
- Create: `web/src/partner/PartnerGate.test.tsx`
- Create: `web/src/partner/api.ts`
- Create: `web/src/partner/types.ts`
- Create: `web/src/settings/PartnerSettingsPanel.tsx`
- Create: `web/src/settings/PartnerSettingsPanel.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/production-modes/catalog.ts`
- Modify: `web/src/production-modes/ModeHome.tsx`
- Modify: `web/src/settings/SettingsPanel.tsx`
- Modify: `web/src/taskModel.ts`
- Modify: `web/src/ModelSelect.tsx`

- [ ] **Step 1: Write failing UI tests**

```tsx
it("shows only activation until the partner is ready", async () => {
  server.use(http.get("/api/partner/status", () => HttpResponse.json({state: "needs_activation"})))
  render(<PartnerGate><div>business-ui</div></PartnerGate>)
  expect(await screen.findByLabelText("伙伴密钥")).toBeVisible()
  expect(screen.queryByText("business-ui")).not.toBeInTheDocument()
})

it("never renders provider URLs or API key fields", async () => {
  render(<PartnerSettingsPanel settings={partnerSettingsFixture()} onSave={vi.fn()} />)
  expect(screen.getByText("gpt-5.6-sol")).toBeVisible()
  expect(screen.getByText("gpt-image-2")).toBeVisible()
  expect(screen.queryByText(/Base URL|API Key|接口地址/i)).not.toBeInTheDocument()
  expect(screen.queryByRole("textbox", {name: /模型/})).not.toBeInTheDocument()
})
```

Add a ModeHome test whose capabilities are `scenery_montage,image_text` and assert movie/image-to-video entries are absent.

- [ ] **Step 2: Run and verify failure**

```powershell
npm --prefix web test -- PartnerGate PartnerSettingsPanel ModeHome
```

Expected: FAIL because the components and capability filtering do not exist.

- [ ] **Step 3: Implement the gate and separate partner settings UI**

`PartnerGate` fetches `/api/partner/status` before any settings/project query. It renders one of: activation form, verifying spinner, ready children, or a locked error with retry. Submit the key only to `/api/partner/activate`; clear the input immediately after the request settles. Do not store it in React Query cache, localStorage, sessionStorage or URL state.

Wrap the existing app body in `PartnerGate` only when `/api/runtime` identifies edition `partner`; alternatively expose edition in the public status response before App settings load. Build mode tiles from server capabilities. Partner settings use the curated endpoint and fixed model labels; owner settings keep the existing `SettingsPanel` unchanged.

Display only sanitized codes:

```ts
const messages = {
  gateway_unavailable: "授权服务暂时不可用，请稍后重试。",
  invalid_credential: "伙伴密钥不正确。",
  device_mismatch: "该密钥已绑定其他电脑。",
  partner_disabled: "当前账号已停用。",
  client_too_old: "软件版本过低，请获取新版。",
} as const
```

Never render raw server error bodies or gateway URL.

- [ ] **Step 4: Run frontend checks**

```powershell
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build:verify
```

Expected: PASS.

- [ ] **Step 5: Commit the partner UI**

```powershell
git add web/src/partner web/src/settings/PartnerSettingsPanel.tsx web/src/settings/PartnerSettingsPanel.test.tsx web/src/App.tsx web/src/production-modes web/src/settings/SettingsPanel.tsx web/src/taskModel.ts web/src/ModelSelect.tsx
git commit -m "feat: add partner activation interface"
```

### Task 8: Complete the M2 verification gate

**Files:**
- Modify only M2-owned files when verification finds a defect.

- [ ] **Step 1: Run complete M2 tests**

```powershell
go test ./internal/partneredition ./internal/partnerclient ./internal/settings ./internal/httpapi ./internal/app ./cmd/console
npm --prefix web test
npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 2: Run the explicit leakage scan**

```powershell
rg -n "remix_base_url|image_base_url|aurastd_base_url|api_key|23\.138\.12\.112:2001" web/src/partner web/src/settings/PartnerSettingsPanel.tsx internal/httpapi/partner* internal/partnerclient
```

Expected: only protocol tests or rejection lists mention forbidden field names; no real secret or direct port-2001 URL appears.

- [ ] **Step 3: Re-run owner regressions**

```powershell
go test ./internal/app ./internal/settings ./internal/httpapi ./cmd/console -run 'Owner|Settings|Auth|Narration' -v
```

Expected: PASS; owner authentication, settings and provider selection remain unchanged.

- [ ] **Step 4: Commit verification fixes only when required**

```powershell
git add cmd/console internal/partneredition internal/partnerclient internal/settings internal/httpapi internal/app web/src
git commit -m "test: verify partner client isolation"
```

Expected: skip this commit if no file changed.
