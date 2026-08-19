# Partner Portable Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce one safe self-extracting Windows EXE that carries all non-secret runtime dependencies, configures local paths, and registers Jianying drafts without separately installed Codex CLI, Python or FFmpeg.

**Architecture:** A small launcher reads an authenticated-by-hash payload appended as a PE overlay, extracts it into a versioned LocalAppData directory, atomically switches current/previous versions, and starts the partner console. A first-run setup service discovers Jianying, validates the separately delivered scenery library, creates a local media index and writes the machine profile with bundled runtime paths.

**Tech Stack:** Go 1.25, Windows PE overlay, ZIP, SHA-256 manifest, PowerShell packaging, embedded Python/pyJianYingDraft, FFmpeg/FFprobe, React setup wizard.

---

## File map

Create:

- `internal/portable/manifest.go`, `manifest_test.go` — payload manifest and normalized safe paths.
- `internal/portable/overlay.go`, `overlay_test.go` — appended overlay reader/writer contract.
- `internal/portable/extract.go`, `extract_test.go` — staging extraction and hash verification.
- `internal/portable/switch.go`, `switch_test.go` — current/previous state and bounded cleanup.
- `cmd/partner-launcher/main.go`, `main_test.go` — LocalAppData install and console launch.
- `scripts/new-partner-ip-cert.ps1`, `new-partner-ip-cert.tests.ps1` — offline private CA and IP-SAN leaf certificate.
- `scripts/build-partner.ps1` — validated dependency collection and final EXE assembly.
- `scripts/test-partner-package.ps1` — payload structure, hash and leakage checks.
- `internal/partnerprofile/profile.go`, `profile_test.go` — discovery, validation and machine profile generation.
- `internal/httpapi/partner_setup.go`, `partner_setup_test.go` — first-run setup/status endpoints.
- `web/src/partner/SetupWizard.tsx`, `SetupWizard.test.tsx` — Jianying/material path wizard.
- `docs/operations/partner-portable-runbook.md` — install, update, rollback and repair instructions.

Modify:

- `.gitignore` — generated payload/release artifacts.
- `cmd/console/main.go`, `cmd/console/main_test.go` — app/data root environment and automatic setup restart.
- `internal/config/config.go`, `internal/config/config_test.go` — fixed partner app/data roots.
- `internal/domain/settings.go`, `internal/settings/service.go`, tests — setup paths and completion state.
- `internal/app/app.go`, tests — setup gate and restart requester.
- `internal/mediacatalog/ffmpeg.go`, tests — explicit bundled paths.
- `internal/agentruntime/montagescript/run.go`, tests — remove partner fallback to PATH Python.
- `internal/montage/coordinator.go`, tests — consume generated profile without weakening trust checks.
- `web/src/partner/PartnerGate.tsx`, `web/src/App.tsx` — setup gate after activation.

### Task 1: Define the payload manifest and PE overlay contract

**Files:**
- Create: `internal/portable/manifest.go`
- Create: `internal/portable/manifest_test.go`
- Create: `internal/portable/overlay.go`
- Create: `internal/portable/overlay_test.go`

- [ ] **Step 1: Write failing manifest/overlay tests**

```go
func TestValidateManifestRejectsUnsafeAndDuplicatePaths(t *testing.T) {
	base := Manifest{SchemaVersion: 1, AppVersion: "0.1.0", Entries: []Entry{{Path: "bin/video-production-console.exe", Size: 3, SHA256: strings.Repeat("a", 64)}}}
	if err := base.Validate(); err != nil { t.Fatal(err) }
	for _, path := range []string{"../escape", "/absolute", `C:\\escape`, `bin\\..\\escape`, "", "bin//x"} {
		bad := base
		bad.Entries = []Entry{{Path: path, Size: 1, SHA256: strings.Repeat("b", 64)}}
		if err := bad.Validate(); err == nil { t.Fatalf("accepted %q", path) }
	}
}

func TestReadOverlayFindsAppendedPayload(t *testing.T) {
	exe := append([]byte("MZ-fake-stub"), buildTestOverlay(t, "0.1.0", map[string][]byte{"bin/app.exe": []byte("app")})...)
	overlay, err := ReadOverlay(bytes.NewReader(exe), int64(len(exe)))
	if err != nil { t.Fatal(err) }
	if overlay.Manifest.AppVersion != "0.1.0" { t.Fatalf("manifest=%+v", overlay.Manifest) }
	if overlay.PayloadSize == 0 { t.Fatal("missing payload") }
}
```

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/portable -run 'TestValidateManifest|TestReadOverlay' -v
```

Expected: FAIL because the package is absent.

- [ ] **Step 3: Implement the exact overlay format**

Use:

```go
const SchemaVersion = 1
const trailerMagic = "VPCPARTNERPAY01!"
const trailerSize = 16 + 8 + 8

type Entry struct {
	Path string `json:"path"`
	Size int64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion int `json:"schema_version"`
	AppVersion string `json:"app_version"`
	PayloadSHA256 string `json:"payload_sha256"`
	Entries []Entry `json:"entries"`
}
```

The final file layout is `[launcher PE][payload.zip][manifest.json][16-byte magic][zip length uint64 LE][manifest length uint64 LE]`. `ReadOverlay(io.ReaderAt, size)` validates magic, positive lengths, integer overflow, bounds, JSON unknown fields, schema/version, lowercase SHA-256, forward-slash relative paths, unique sorted entries and payload hash before returning a `zip.Reader` section. Reject backslashes, absolute paths, drive letters, empty segments, `.`/`..`, NUL and symlink entries.

- [ ] **Step 4: Run manifest/overlay tests**

```powershell
go test ./internal/portable -run 'TestValidateManifest|TestReadOverlay|TestOverlayRejects' -v
```

Expected: PASS.

- [ ] **Step 5: Commit the overlay contract**

```powershell
git add internal/portable/manifest.go internal/portable/manifest_test.go internal/portable/overlay.go internal/portable/overlay_test.go
git commit -m "feat: define partner payload overlay"
```

### Task 2: Implement safe extraction and bounded version switching

**Files:**
- Create: `internal/portable/extract.go`
- Create: `internal/portable/extract_test.go`
- Create: `internal/portable/switch.go`
- Create: `internal/portable/switch_test.go`

- [ ] **Step 1: Write failing security and rollback tests**

```go
func TestExtractVerifiesEveryEntryBeforeCommit(t *testing.T) {
	overlay := testOverlay(t, Manifest{SchemaVersion: 1, AppVersion: "0.1.0", Entries: []Entry{{Path: "bin/app.exe", Size: 3, SHA256: sha256Hex([]byte("app"))}}}, map[string][]byte{"bin/app.exe": []byte("bad")})
	root := filepath.Join(t.TempDir(), "app")
	err := ExtractToStaging(context.Background(), overlay, root)
	if !errors.Is(err, ErrEntryHash) { t.Fatalf("err=%v", err) }
	if _, err := os.Stat(filepath.Join(root, "0.1.0")); !errors.Is(err, os.ErrNotExist) { t.Fatalf("committed bad version: %v", err) }
}

func TestSwitchKeepsCurrentAndPreviousButNeverTouchesData(t *testing.T) {
	base := t.TempDir()
	appRoot := filepath.Join(base, "app")
	dataRoot := filepath.Join(base, "data")
	mustWrite(t, filepath.Join(dataRoot, "project.txt"), []byte("keep"))
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0"} { mustMkdir(t, filepath.Join(appRoot, v)) }
	if err := SwitchVersion(appRoot, "0.3.0"); err != nil { t.Fatal(err) }
	state := readState(t, appRoot)
	if state.Current != "0.3.0" || state.Previous != "0.2.0" { t.Fatalf("state=%+v", state) }
	if got := string(mustRead(t, filepath.Join(dataRoot, "project.txt"))); got != "keep" { t.Fatalf("data=%q", got) }
}
```

Add tests for ZIP path escape, absolute path, symlink/reparse metadata, unexpected/missing/duplicate file, cancellation, low disk-space callback, failed state write, and cleanup target resolving outside `appRoot`.

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/portable -run 'TestExtract|TestSwitch' -v
```

Expected: FAIL because extraction/switch functions do not exist.

- [ ] **Step 3: Implement staging, fsync and state rules**

Extract only to `<appRoot>/.staging/<version>-<random>`. For each regular ZIP entry, resolve `filepath.Join(staging, filepath.FromSlash(path))`, then prove `filepath.Rel(staging, target)` is neither `..` nor absolute. Create parent directories, copy through SHA-256 and byte counter, sync, compare manifest size/hash, and reject any ZIP entry not present in the manifest. After all entries pass, sync the staging tree and rename it to `<appRoot>/<version>`; if that version already exists, verify it against the manifest and reuse it rather than overwrite.

Persist state atomically:

```go
type VersionState struct {
	Current string `json:"current"`
	Previous string `json:"previous,omitempty"`
}
```

`SwitchVersion` accepts only semver directory names directly below the canonical fixed app root. Update `state.json` by temp-write/sync/rename. Cleanup begins only after successful child startup and keeps state current and previous. Before recursive removal, re-resolve the target with no-follow logic and prove its parent equals canonical `appRoot`; never accept `dataRoot` as an argument.

- [ ] **Step 4: Run extraction/race tests**

```powershell
go test ./internal/portable -v
go test -race ./internal/portable -run 'TestExtract|TestSwitch' -v
```

Expected: PASS.

- [ ] **Step 5: Commit extraction/versioning**

```powershell
git add internal/portable/extract.go internal/portable/extract_test.go internal/portable/switch.go internal/portable/switch_test.go
git commit -m "feat: safely install partner runtime versions"
```

### Task 3: Build the launcher process

**Files:**
- Create: `cmd/partner-launcher/main.go`
- Create: `cmd/partner-launcher/main_test.go`

- [ ] **Step 1: Write failing launcher tests**

```go
func TestInstallAndLaunchUsesLocalAppDataAndSeparateDataRoot(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	launcher := newLauncherFixture(t, "0.1.0")
	var got commandSpec
	launcher.start = func(spec commandSpec) error { got = spec; return nil }
	if err := launcher.Run(context.Background()); err != nil { t.Fatal(err) }
	wantApp := filepath.Join(local, "VideoProductionConsole", "app", "0.1.0")
	if got.Path != filepath.Join(wantApp, "bin", "video-production-console.exe") { t.Fatalf("path=%q", got.Path) }
	assertEnv(t, got.Env, "VIDEO_CONSOLE_APP_ROOT", wantApp)
	assertEnv(t, got.Env, "VIDEO_CONSOLE_DATA_ROOT", filepath.Join(local, "VideoProductionConsole", "data"))
}
```

Add tests for missing LocalAppData, corrupt overlay, payload verification failure, child exit code 75 automatic restart, normal exit propagation, and previous-version preservation after a failed child start.

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./cmd/partner-launcher -v
```

Expected: FAIL because launcher types are undefined.

- [ ] **Step 3: Implement the launcher without shell commands**

Open `os.Executable()`, pass it to `portable.ReadOverlay`, install/switch, and start `<version>/bin/video-production-console.exe` with `os/exec.CommandContext`. Add only these environment values to a copy of `os.Environ()`:

```text
VIDEO_CONSOLE_APP_ROOT=<canonical version directory>
VIDEO_CONSOLE_DATA_ROOT=%LOCALAPPDATA%\VideoProductionConsole\data
VIDEO_CONSOLE_LAUNCHED=1
```

Do not pass an API key, partner key, session, Aura key or gateway override. Use exit code 75 as `restart requested`: restart the same verified child at most twice in 60 seconds to avoid loops. Only after the child reaches its health endpoint once may cleanup remove versions older than current/previous.

- [ ] **Step 4: Run launcher tests and Windows build**

```powershell
go test ./cmd/partner-launcher ./internal/portable -v
go build -trimpath -o .tmp/partner-launcher-stub.exe ./cmd/partner-launcher
```

Expected: PASS and the stub builds without a generated payload file.

- [ ] **Step 5: Commit the launcher**

```powershell
git add cmd/partner-launcher
git commit -m "feat: add partner self-extracting launcher"
```

### Task 4: Generate the offline private CA and IP certificate

**Files:**
- Create: `scripts/new-partner-ip-cert.ps1`
- Create: `scripts/new-partner-ip-cert.tests.ps1`
- Modify: `.gitignore`

- [ ] **Step 1: Write failing certificate-script tests**

Use a temporary output directory and require exactly these files:

```text
partner-ca.key
partner-ca.crt
gateway.key
gateway.crt
```

The test invokes OpenSSL verification and checks that the leaf certificate validates against the CA, contains IP SAN `23.138.12.112`, is not a CA certificate, and that the script refuses an output directory inside the repository.

- [ ] **Step 2: Run and verify failure**

```powershell
pwsh -File scripts/new-partner-ip-cert.tests.ps1
```

Expected: FAIL because the certificate script is absent.

- [ ] **Step 3: Implement offline certificate generation**

Use mandatory `-OutputRoot` and optional `-OpenSSLPath`, with fixed server IP `23.138.12.112`. Resolve the output root and reject it when it is equal to or below the repository root. Refuse to overwrite existing keys. Generate a 4096-bit RSA CA key/certificate valid for 10 years and a 3072-bit RSA leaf valid for 365 days. The leaf extension must be:

```text
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:23.138.12.112
```

Run `openssl verify -CAfile partner-ca.crt gateway.crt` and `openssl x509 -in gateway.crt -checkip 23.138.12.112 -noout`. Delete CSR, serial and temporary config files after success. Remove inherited ACLs from `partner-ca.key` and `gateway.key`, grant only the current Windows user read/write, and never print key content. Add `release/partner-tls/`, `release/partner/` and `*.key` to `.gitignore`; `C:\PartnerBuildDeps\tls` is already outside the repository and must remain there.

- [ ] **Step 4: Generate production certificate material outside Git**

```powershell
pwsh -File scripts/new-partner-ip-cert.ps1 -OutputRoot C:\PartnerBuildDeps\tls
pwsh -File scripts/new-partner-ip-cert.tests.ps1 -ExistingOutputRoot C:\PartnerBuildDeps\tls
```

Expected: PASS; `partner-ca.key` remains only on the build workstation. M3 embeds `partner-ca.crt`; M4 uploads only `gateway.crt` and `gateway.key`.

- [ ] **Step 5: Commit only scripts and ignore rules**

```powershell
git add .gitignore scripts/new-partner-ip-cert.ps1 scripts/new-partner-ip-cert.tests.ps1
git commit -m "build: generate pinned partner ip certificate"
```

### Task 5: Build the reproducible partner payload script

**Files:**
- Create: `scripts/build-partner.ps1`
- Create: `scripts/test-partner-package.ps1`
- Modify: `.gitignore`
- Modify: `scripts/check-embedded-dist.ps1`

- [ ] **Step 1: Write the failing packaging validation script**

`scripts/test-partner-package.ps1` accepts mandatory `-Version` and checks the final EXE plus `.sha256`. It must invoke a launcher inspection subcommand (`--inspect-payload`) and assert this exact payload shape:

```text
bin/video-production-console.exe
runtime/python/python.exe
runtime/python/Lib/site-packages/pyJianYingDraft/
runtime/ffmpeg/bin/ffmpeg.exe
runtime/ffmpeg/bin/ffprobe.exe
resources/tls/partner-ca.crt
resources/montage/
schemas/settings.schema.json
schemas/task-manifest.schema.json
```

It fails on missing path, hash mismatch, private-key extension, `.env`, database, cookie, log, upstream secret, development workspace absolute path or a second unlisted `.exe` outside `bin`/runtime.

- [ ] **Step 2: Run and verify the artifact is absent**

```powershell
pwsh -File scripts/test-partner-package.ps1 -Version 0.1.0
```

Expected: FAIL with `partner package not found`.

- [ ] **Step 3: Implement the build script with explicit dependency inputs**

Parameters are mandatory:

```powershell
param(
  [Parameter(Mandatory=$true)][string]$Version,
  [Parameter(Mandatory=$true)][string]$PythonRuntimeDir,
  [Parameter(Mandatory=$true)][string]$FFmpegDir,
  [Parameter(Mandatory=$true)][string]$MediaResourcesDir,
  [Parameter(Mandatory=$true)][string]$PinnedCAFile
)
```

Validate semver; resolve all sources; require `python.exe`, `Lib/site-packages/pyJianYingDraft`, `ffmpeg.exe`, `ffprobe.exe`, at least one BGM/SFX/transition resource, and a PEM certificate containing `BEGIN CERTIFICATE` but no `PRIVATE KEY`. Build Web embed, build the console using:

```powershell
$ldflags = "-s -w -X video-production-console/internal/buildinfo.Version=$versionValue -X video-production-console/internal/buildinfo.Commit=$commit -X video-production-console/internal/buildinfo.BuildTime=$built -X video-production-console/internal/partneredition.builtEdition=partner -X video-production-console/internal/partneredition.builtGatewayURL=https://23.138.12.112:2443"
```

Assemble a temporary payload directory, generate sorted lowercase SHA-256 entries, ZIP with forward-slash names, serialize canonical UTF-8 JSON manifest, build the launcher stub, and append ZIP + manifest + binary trailer using .NET file streams. Write only:

```text
release/partner/<version>/video-production-console-partner-<version>.exe
release/partner/<version>/video-production-console-partner-<version>.exe.sha256
release/partner/<version>/BUILD-METADATA.json
```

`BUILD-METADATA.json` contains version, commit, UTC build time and dependency hashes, never source paths or secrets. Add `release/partner/` and temporary payload directories to `.gitignore`.

- [ ] **Step 4: Build and validate with known dependency roots**

```powershell
pwsh -File scripts/build-partner.ps1 -Version 0.1.0 -PythonRuntimeDir C:\PartnerBuildDeps\python -FFmpegDir C:\PartnerBuildDeps\ffmpeg -MediaResourcesDir C:\PartnerBuildDeps\montage-resources -PinnedCAFile C:\PartnerBuildDeps\tls\partner-ca.crt
pwsh -File scripts/test-partner-package.ps1 -Version 0.1.0
```

Expected: PASS; the package inspection lists the fixed shape and no secret-bearing file.

- [ ] **Step 5: Commit build tooling, not artifacts**

```powershell
git add .gitignore scripts/build-partner.ps1 scripts/test-partner-package.ps1 scripts/check-embedded-dist.ps1
git commit -m "build: package partner console as one exe"
```

### Task 6: Generate the local machine profile from bundled runtimes

**Files:**
- Create: `internal/partnerprofile/profile.go`
- Create: `internal/partnerprofile/profile_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/settings/service.go`
- Modify: `internal/settings/service_test.go`
- Modify: `internal/mediacatalog/ffmpeg.go`
- Modify: `internal/mediacatalog/ffmpeg_test.go`
- Modify: `internal/agentruntime/montagescript/run.go`
- Modify: `internal/agentruntime/montagescript/run_test.go`

- [ ] **Step 1: Write failing path/profile tests**

```go
func TestBuildProfileUsesOnlyBundledExecutablesAndSelectedRoots(t *testing.T) {
	appRoot := createBundledRuntimeFixture(t)
	dataRoot := filepath.Join(t.TempDir(), "数据")
	mediaRoot := filepath.Join(t.TempDir(), "风景 素材")
	mustMkdir(t, filepath.Join(mediaRoot, "originals"))
	jianyingRoot := filepath.Join(t.TempDir(), "剪映草稿")
	mustMkdir(t, jianyingRoot)
	profile, err := Build(BuildOptions{AppRoot: appRoot, DataRoot: dataRoot, MediaRoot: mediaRoot, JianyingRoot: jianyingRoot})
	if err != nil { t.Fatal(err) }
	if profile.PythonBinary != filepath.Join(appRoot, "runtime", "python", "python.exe") { t.Fatalf("python=%q", profile.PythonBinary) }
	if profile.MediaIndexPath != filepath.Join(dataRoot, "media", "index.json") { t.Fatalf("index=%q", profile.MediaIndexPath) }
	if !filepath.IsAbs(profile.JianyingRoot) { t.Fatalf("jianying=%q", profile.JianyingRoot) }
}

func TestDecodeGBKFallbackUsesConfiguredPython(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "python.exe")
	got := buildDecodeCommand(configured, "input")
	if got.Path != configured { t.Fatalf("path=%q", got.Path) }
}
```

Add cases for Windows/drive root rejection, missing `originals`, unwritable draft directory, Chinese/space path, reparse point, absent pyJianYingDraft, absent FFmpeg and fallback to PATH `python` forbidden in partner mode.

- [ ] **Step 2: Run and verify current path fallbacks fail**

```powershell
go test ./internal/partnerprofile ./internal/mediacatalog ./internal/agentruntime/montagescript -run 'TestBuildProfile|TestDecodeGBKFallback|TestPartner' -v
```

Expected: FAIL because partnerprofile is absent and GBK fallback still uses `python`.

- [ ] **Step 3: Implement discovery and profile creation**

Use these candidates, in order, under `%LOCALAPPDATA%`:

```text
JianyingPro\User Data\Projects\com.lveditor.draft
JianyingPro\User Data\Projects
```

Accept the first existing writable directory that is not a root/reparse target; otherwise require manual selection. Define the serialized profile:

```go
type MachineProfile struct {
	PythonBinary string `json:"python_binary"`
	JianyingRoot string `json:"jianying_root"`
	MediaRoot string `json:"media_root"`
	MediaIndexPath string `json:"media_index_path"`
	MontageResourcesPath string `json:"montage_resources_path"`
}
```

Write it atomically to `<dataRoot>/config/machine-profile.json`. Update public settings with profile path, media root/index, Jianying root, bundled FFmpeg and FFprobe absolute paths. Keep existing `montage.ResolveTrustedRuntime` canonical/hash checks; do not weaken no-follow validation. Change montage script GBK decoding and every Python subprocess to use the explicit profile Python path in partner mode.

- [ ] **Step 4: Run profile/runtime tests**

```powershell
go test ./internal/partnerprofile ./internal/config ./internal/settings ./internal/mediacatalog ./internal/agentruntime/montagescript ./internal/montage
```

Expected: PASS; owner-mode tests still allow their existing configured behavior.

- [ ] **Step 5: Commit profile/runtime integration**

```powershell
git add internal/partnerprofile internal/config internal/domain/settings.go internal/settings internal/mediacatalog internal/agentruntime/montagescript internal/montage
git commit -m "feat: configure bundled partner media runtime"
```

### Task 7: Add the first-run setup API and UI

**Files:**
- Create: `internal/httpapi/partner_setup.go`
- Create: `internal/httpapi/partner_setup_test.go`
- Create: `web/src/partner/SetupWizard.tsx`
- Create: `web/src/partner/SetupWizard.test.tsx`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `cmd/console/main.go`
- Modify: `cmd/console/main_test.go`
- Modify: `web/src/partner/PartnerGate.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Write failing setup workflow tests**

```go
func TestPartnerSetupCreatesProfileIndexesMediaAndRequestsRestart(t *testing.T) {
	fixture := newSetupFixture(t)
	body := fmt.Sprintf(`{"jianying_root":%q,"media_root":%q}`, fixture.JianyingRoot, fixture.MediaRoot)
	w := httptest.NewRecorder()
	fixture.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/partner/setup", strings.NewReader(body)))
	if w.Code != http.StatusAccepted { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
	if !fixture.ProfileExists() || !fixture.MediaIndexExists() { t.Fatal("setup artifacts missing") }
	select { case <-fixture.RestartRequested: default: t.Fatal("restart not requested") }
}
```

Frontend test:

```tsx
it("requires Jianying and scenery roots before opening the app", async () => {
  render(<SetupWizard status={{complete: false, detected_jianying_root: ""}} onComplete={vi.fn()} />)
  await userEvent.type(screen.getByLabelText("剪映草稿目录"), "D:\\剪映草稿")
  await userEvent.type(screen.getByLabelText("风景素材目录"), "E:\\风景素材")
  await userEvent.click(screen.getByRole("button", {name: "检查并继续"}))
  expect(await screen.findByText("正在建立本机素材索引")).toBeVisible()
})
```

- [ ] **Step 2: Run and verify failure**

```powershell
go test ./internal/httpapi ./internal/app ./cmd/console -run 'TestPartnerSetup' -v
npm --prefix web test -- SetupWizard
```

Expected: FAIL because setup endpoints/UI do not exist.

- [ ] **Step 3: Implement setup as an activation-following gate**

Register `GET /api/partner/setup` and `POST /api/partner/setup` as partner-public routes after authorization but before business readiness. GET returns only completion, detected Jianying candidate and sanitized validation codes. POST uses `json.Decoder.DisallowUnknownFields`, validates both paths, creates the machine profile, invokes the existing `mediacatalog.Indexer.Run` against `<mediaRoot>/originals`, updates settings atomically, then sends a non-blocking restart request.

In `cmd/console`, listen for the restart request, gracefully stop HTTP/scheduler/registration services, and exit code 75. The launcher restarts the verified version; the second startup loads the profile and registration coordinator normally.

`PartnerGate` order is: authorization state → setup completion → business UI. The wizard never accepts a catalog database path and never copies material files.

- [ ] **Step 4: Run setup and web tests**

```powershell
go test ./internal/httpapi ./internal/app ./cmd/console ./internal/partnerprofile ./internal/mediacatalog
npm --prefix web test
npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 5: Commit setup workflow**

```powershell
git add internal/httpapi/partner_setup.go internal/httpapi/partner_setup_test.go internal/app cmd/console web/src/partner/SetupWizard.tsx web/src/partner/SetupWizard.test.tsx web/src/partner/PartnerGate.tsx web/src/App.tsx
git commit -m "feat: add partner first-run setup"
```

### Task 8: Document and verify the complete portable artifact

**Files:**
- Create: `docs/operations/partner-portable-runbook.md`
- Modify only M3-owned code/tests when verification exposes a defect.

- [ ] **Step 1: Write the operational runbook**

Document exact user actions: receive EXE + scenery folder, double-click without elevation, enter activation key once, choose Jianying/scenery paths, wait for automatic restart, create project, repair a moved path, update by replacing EXE, and rollback by running the previous EXE. Include SmartScreen unknown-publisher wording and the SHA-256 verification command:

```powershell
(Get-FileHash .\video-production-console-partner-0.1.0.exe -Algorithm SHA256).Hash.ToLowerInvariant()
```

Do not document gateway/upstream URLs or any secret.

- [ ] **Step 2: Run the complete package build**

```powershell
pwsh -File scripts/build-partner.ps1 -Version 0.1.0 -PythonRuntimeDir C:\PartnerBuildDeps\python -FFmpegDir C:\PartnerBuildDeps\ffmpeg -MediaResourcesDir C:\PartnerBuildDeps\montage-resources -PinnedCAFile C:\PartnerBuildDeps\tls\partner-ca.crt
pwsh -File scripts/test-partner-package.ps1 -Version 0.1.0
```

Expected: PASS.

- [ ] **Step 3: Run the M3 regression gate**

```powershell
go test ./internal/portable ./cmd/partner-launcher ./internal/partnerprofile ./internal/mediacatalog ./internal/agentruntime/montagescript ./internal/montage ./internal/httpapi ./internal/app ./cmd/console
npm --prefix web test
npm --prefix web run typecheck
git diff --check
```

Expected: PASS.

- [ ] **Step 4: Scan artifact metadata and source for secret leakage**

```powershell
rg -n "PRIVATE KEY|api_key|23\.138\.12\.112:2001|cookie|session_token" release/partner/0.1.0/BUILD-METADATA.json docs/operations/partner-portable-runbook.md
```

Expected: no match. The public gateway URL may exist only inside the executable build metadata and is not a secret; the runbook intentionally omits it.

- [ ] **Step 5: Commit runbook and verification fixes**

```powershell
git add docs/operations/partner-portable-runbook.md internal/portable cmd/partner-launcher internal/partnerprofile scripts web/src/partner internal/httpapi internal/app cmd/console
git commit -m "docs: add partner portable operations"
```

Expected: generated EXE, payload, dependency runtimes, local databases and build dependency directories remain ignored and unstaged.
