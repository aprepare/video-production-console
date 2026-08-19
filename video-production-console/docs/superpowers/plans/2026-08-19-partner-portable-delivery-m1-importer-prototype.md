# Partner Portable Delivery M1 Importer Prototype Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a minimal `.vpcdraft` package from the existing “天中观局” draft and import it safely on a second Windows computer that has only Jianying and the standalone Go importer.

**Architecture:** M1 deliberately proves portability before website work. A server-side Go packager copies only files referenced by the plaintext draft, replaces machine paths with `vpcasset://<sha256>`, and writes a hash-verifiable unsigned ZIP; a separate Go importer validates and stages that ZIP, materializes media, rewrites virtual paths, and registers the draft through an exclusive lock, backup, atomic index replacement, receipt, and rollback.

**Tech Stack:** Go 1.25 standard library, ZIP/JSON/SHA-256, Windows NTFS, PowerShell 7, Jianying plaintext draft files. No Python, Codex, FFmpeg, Cloudflare, or complete media library is required on the receiving computer.

---

## File map and fixed interfaces

| Path | Action | Responsibility |
|---|---|---|
| `internal/portablepackage/model.go` | Create | M1 manifest, assets, constants, build request/result |
| `internal/portablepackage/paths.go` | Create | JSON media discovery, SHA-256 asset IDs, virtual-path rewrite, residual-path scan |
| `internal/portablepackage/package.go` | Create | Deterministic staging and ZIP production |
| `internal/portablepackage/package_test.go` | Create | Contract, referenced-only packaging, path and hash tests |
| `cmd/portable-packager/main.go` | Create | Explicit M1 packager CLI used for “天中观局” |
| `internal/draftimport/model.go` | Create | Import request/result, limits, stable error codes |
| `internal/draftimport/validate.go` | Create | Hostile ZIP and manifest validation |
| `internal/draftimport/import.go` | Create | Extraction, materialization, draft rewrite, receipt idempotency |
| `internal/draftimport/register_windows.go` | Create | Jianying lock, backup, collision handling, atomic index update, rollback |
| `internal/draftimport/import_test.go` | Create | Validator/import/register failure-injection tests |
| `internal/draftimport/testdata/root_meta_info.json` | Create | Sanitized real Jianying index shape used by registration tests |
| `cmd/draft-importer/main.go` | Create | Standalone Windows CLI |
| `scripts/package-m1.ps1` | Create | Reproducible packager invocation |
| `scripts/m1-cross-machine.ps1` | Create | Evidence collection without server paths |
| `scripts/release.ps1` | Modify | Build and checksum importer |
| `M1-IMPORTER-README.md` | Create | Receiver instructions and evidence checklist |

M1 owns the canonical package names `internal/portablepackage`, `internal/draftimport`, `cmd/portable-packager`, and `cmd/draft-importer`. Later milestones extend these packages and do not rename them.

### Task 1: Freeze the M1 manifest and error contract

**Files:** Create `internal/portablepackage/model.go`, `internal/draftimport/model.go`, `internal/portablepackage/package_test.go`.

- [ ] **Step 1: Write the failing contract test.**

```go
func TestM1Contract(t *testing.T) {
	m := NewPrototypeManifest("project-1", "draft-v1")
	if m.SchemaVersion != "1" || m.PackageVersion != "0.1.0" || m.ImporterMinVersion != "0.1.0" {
		t.Fatalf("unexpected versions: %#v", m)
	}
	if m.Signature.Mode != "prototype-unsigned" { t.Fatalf("mode=%q", m.Signature.Mode) }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestM1Contract -count=1`; expect `undefined: NewPrototypeManifest`.

- [ ] **Step 3: Add the minimal package contract.**

```go
package portablepackage

const SchemaVersion = "1"
const PrototypePackageVersion = "0.1.0"
const PrototypeImporterMinVersion = "0.1.0"

type SourceTimeRange struct { StartMS int64 `json:"start_ms"`; DurationMS int64 `json:"duration_ms"` }
type Asset struct {
	AssetID string `json:"asset_id"`; Role string `json:"role"`; Path string `json:"path"`
	SHA256 string `json:"sha256"`; SizeBytes int64 `json:"size_bytes"`; MIME string `json:"mime"`
	SourceTimeRange *SourceTimeRange `json:"source_timerange,omitempty"`
}
type SignatureEnvelope struct { Mode string `json:"mode"`; KeyID string `json:"key_id,omitempty"` }
type Manifest struct {
	SchemaVersion string `json:"schema_version"`; PackageVersion string `json:"package_version"`
	ImporterMinVersion string `json:"importer_min_version"`; DraftVersion string `json:"draft_version"`
	ProjectID string `json:"project_id"`; Assets []Asset `json:"assets"`; Signature SignatureEnvelope `json:"signature"`
}
func NewPrototypeManifest(projectID, draftVersion string) Manifest {
	return Manifest{SchemaVersion: SchemaVersion, PackageVersion: PrototypePackageVersion,
		ImporterMinVersion: PrototypeImporterMinVersion, DraftVersion: draftVersion, ProjectID: projectID,
		Assets: []Asset{}, Signature: SignatureEnvelope{Mode: "prototype-unsigned"}}
}
type BuildRequest struct { ProjectID, DraftRoot, OutputPath, DraftVersion string }
type BuildResult struct { Path, SHA256 string; SizeBytes int64; Manifest Manifest }
```

- [ ] **Step 4: Add `internal/draftimport/model.go`** with the importer types.

```go
type Limits struct { MaxFiles int; MaxTotalBytes, MaxFileBytes int64; MaxPathDepth int }
func DefaultLimits() Limits { return Limits{MaxFiles: 10000, MaxTotalBytes: 4 << 30, MaxFileBytes: 512 << 20, MaxPathDepth: 16} }
type ImportRequest struct { PackagePath, JianyingRoot, MediaRoot string; DryRun, CreateCopy bool }
type ImportResult struct { DraftID, ReceiptPath, PackageSHA256 string; AlreadyImported bool }
type Receipt struct { PackageSHA256, DraftID, ImporterVersion, Status string; ImportedAt time.Time }
type CodeError struct { Code, SafeMessage string }
func (e *CodeError) Error() string { return e.Code + ": " + e.SafeMessage }
const (CodeInvalidPackage="invalid_package"; CodeUnsafePath="unsafe_path"; CodeHashMismatch="hash_mismatch"; CodeLockBusy="lock_busy"; CodeCommitFailed="commit_failed"; CodeRollbackFailed="rollback_failed")
func code(value,message string) error { return &CodeError{Code:value,SafeMessage:message} }
```

- [ ] **Step 5: Run** `go test ./internal/portablepackage ./internal/draftimport -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/portablepackage/model.go internal/portablepackage/package_test.go internal/draftimport/model.go
git commit -m "feat: define portable draft M1 contract"
```

### Task 2: Discover referenced assets and remove machine paths

**Files:** Create `internal/portablepackage/paths.go`; modify `internal/portablepackage/package_test.go`.

- [ ] **Step 1: Add a failing test using one referenced file.**

```go
func TestRewriteDraftUsesContentHashURI(t *testing.T) {
	root := t.TempDir(); clip := filepath.Join(root, "clip.mp4")
	os.WriteFile(clip, []byte("clip-bytes"), 0o600)
	in, _ := json.Marshal(map[string]any{"path": clip, "name": "C drive is prose"})
	out, assets, err := RewriteDraftJSON(in)
	if err != nil { t.Fatal(err) }
	wantID := fmt.Sprintf("%x", sha256.Sum256([]byte("clip-bytes")))
	if len(assets) != 1 || !bytes.Contains(out, []byte("vpcasset://"+wantID)) { t.Fatalf("assets=%#v json=%s", assets, out) }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestRewriteDraftUsesContentHashURI -count=1`; expect `undefined: RewriteDraftJSON`.

- [ ] **Step 3: Implement exact JSON-string traversal and content hashing.**

```go
type SourceAsset struct { SourcePath, AssetID string; SizeBytes int64 }
func RewriteDraftJSON(input []byte) ([]byte, []SourceAsset, error) {
	var node any
	if err := json.Unmarshal(input, &node); err != nil { return nil, nil, err }
	byPath := map[string]SourceAsset{}
	var visit func(any) (any, error)
	visit = func(v any) (any, error) {
		switch x := v.(type) {
		case string:
			info, err := os.Stat(x); if err != nil || !info.Mode().IsRegular() { return x, nil }
			data, err := os.ReadFile(x); if err != nil { return nil, err }
			id := fmt.Sprintf("%x", sha256.Sum256(data)); byPath[x] = SourceAsset{SourcePath:x, AssetID:id, SizeBytes:info.Size()}
			return "vpcasset://" + id, nil
		case []any:
			for i := range x { y, err := visit(x[i]); if err != nil { return nil, err }; x[i] = y }; return x, nil
		case map[string]any:
			for k := range x { y, err := visit(x[k]); if err != nil { return nil, err }; x[k] = y }; return x, nil
		default: return v, nil
		}
	}
	rewritten, err := visit(node); if err != nil { return nil, nil, err }
	out, err := json.Marshal(rewritten); if err != nil { return nil, nil, err }
	assets := make([]SourceAsset, 0, len(byPath)); for _, a := range byPath { assets = append(assets, a) }
	sort.Slice(assets, func(i, j int) bool { return assets[i].AssetID < assets[j].AssetID })
	return append(out, '\n'), assets, nil
}
```

- [ ] **Step 4: Add `ScanForbiddenPaths` tests** for drive paths, UNC paths, `..`, workspace/temp roots, while allowing `vpcasset://`; implement the scanner over parsed JSON string values rather than raw prose.

- [ ] **Step 5: Run** `go test ./internal/portablepackage -run 'TestRewrite|TestScanForbidden' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/portablepackage/paths.go internal/portablepackage/package_test.go
git commit -m "feat: rewrite draft assets to virtual paths"
```

### Task 3: Build a deterministic, referenced-only M1 package

**Files:** Create `internal/portablepackage/package.go`, `cmd/portable-packager/main.go`, `scripts/package-m1.ps1`; modify `internal/portablepackage/package_test.go`.

- [ ] **Step 1: Add `TestBuildPrototypePackage`** and assert only referenced content appears.

```go
result, err := Build(context.Background(), BuildRequest{ProjectID:"tianzhong", DraftRoot:draft, OutputPath:out, DraftVersion:"fixture"})
if err != nil { t.Fatal(err) }
names := zipNames(t, result.Path)
assertNames(t, names, []string{"assets/"+clipHash+".mp4", "draft/draft_content.json", "draft/draft_meta_info.json", "manifest.json"})
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestBuildPrototypePackage -count=1`; expect `undefined: Build`.

- [ ] **Step 3: Implement `Build` with same-directory `.partial`, sorted entries, close-before-rename, and final ZIP hash.**

```go
func Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	stage, err := os.MkdirTemp(filepath.Dir(req.OutputPath), ".vpcbuild-"); if err != nil { return BuildResult{}, err }; defer os.RemoveAll(stage)
	manifest := NewPrototypeManifest(req.ProjectID, req.DraftVersion)
	assets, err := rewriteDraftFiles(req.DraftRoot, filepath.Join(stage, "draft")); if err != nil { return BuildResult{}, err }
	manifest.Assets, err = copyReferencedAssets(assets, filepath.Join(stage, "assets")); if err != nil { return BuildResult{}, err }
	if err = ScanForbiddenPaths(filepath.Join(stage, "draft")); err != nil { return BuildResult{}, err }
	if err = writeManifest(stage, manifest); err != nil { return BuildResult{}, err }
	partial := req.OutputPath + ".partial"; if err = writeSortedZip(stage, partial); err != nil { return BuildResult{}, err }
	if err = os.Rename(partial, req.OutputPath); err != nil { return BuildResult{}, err }
	hash, size, err := hashFile(req.OutputPath); return BuildResult{Path:req.OutputPath, SHA256:hash, SizeBytes:size, Manifest:manifest}, err
}
```

- [ ] **Step 4: Implement the fixed CLI and script.**

```powershell
param([Parameter(Mandatory)][string]$DraftRoot,[Parameter(Mandatory)][string]$OutputPath,[Parameter(Mandatory)][string]$ProjectId)
$ErrorActionPreference = 'Stop'
go run ./cmd/portable-packager --draft-root $DraftRoot --output $OutputPath --project-id $ProjectId --draft-version m1
if ($LASTEXITCODE -ne 0) { throw "portable packager failed with exit code $LASTEXITCODE" }
Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256
```

- [ ] **Step 5: Run** `go test ./internal/portablepackage -count=1`; run the script on its fixture; expect one `.vpcdraft` plus SHA-256.

- [ ] **Step 6: Commit.**

```powershell
git add internal/portablepackage/package.go internal/portablepackage/package_test.go cmd/portable-packager/main.go scripts/package-m1.ps1
git commit -m "feat: build referenced-only M1 packages"
```

### Task 4: Reject hostile ZIPs before extraction

**Files:** Create `internal/draftimport/validate.go`, `internal/draftimport/import_test.go`.

- [ ] **Step 1: Table-test unsafe ZIP entries.**

```go
func TestValidateRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", `C:\escape`, `\\host\share`} {
		t.Run(name, func(t *testing.T) { p := maliciousZip(t, name); _, err := ValidatePackage(p, DefaultLimits()); assertCode(t, err, "unsafe_path") })
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/draftimport -run TestValidate -count=1`; expect `undefined: ValidatePackage`.

- [ ] **Step 3: Implement name and size validation before extraction.**

```go
type ValidatedPackage struct { Manifest portablepackage.Manifest; PackageSHA256 string; Entries map[string]*zip.File; SignatureBytes []byte }
var drivePath = regexp.MustCompile(`^[A-Za-z]:`)
func ValidatePackage(path string, limits Limits) (ValidatedPackage, error) {
	r, err := zip.OpenReader(path); if err != nil { return ValidatedPackage{}, code("invalid_package", "package cannot be opened") }; defer r.Close()
	seen := map[string]struct{}{}; var total int64
	for _, f := range r.File {
		clean := filepath.ToSlash(pathpkg.Clean(f.Name))
		if clean != f.Name || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) || drivePath.MatchString(clean) || f.Mode()&os.ModeSymlink != 0 { return ValidatedPackage{}, code("unsafe_path", "package contains an unsafe entry") }
		if _, ok := seen[clean]; ok { return ValidatedPackage{}, code("invalid_package", "package contains duplicate entries") }; seen[clean] = struct{}{}
		if f.UncompressedSize64 > uint64(limits.MaxFileBytes) { return ValidatedPackage{}, code("invalid_package", "package file limit exceeded") }
		total += int64(f.UncompressedSize64); if total > limits.MaxTotalBytes || len(seen) > limits.MaxFiles { return ValidatedPackage{}, code("invalid_package", "package limit exceeded") }
	}
	return verifyManifestAndHashes(&r.Reader, path)
}
```

- [ ] **Step 4: Add malformed manifest, unsupported schema/mode, missing asset, and changed hash cases; run** `go test ./internal/draftimport -run TestValidate -count=1`; expect PASS.

- [ ] **Step 5: Commit.**

```powershell
git add internal/draftimport/validate.go internal/draftimport/import_test.go
git commit -m "feat: validate portable draft archives"
```

### Task 5: Materialize assets and make repeated imports idempotent

**Files:** Create `internal/draftimport/import.go`; modify `internal/draftimport/import_test.go`.

- [ ] **Step 1: Add a failing receipt test.**

```go
func TestImportReusesMatchingReceipt(t *testing.T) {
	req := fixtureImportRequest(t); first, err := Import(context.Background(), req); if err != nil { t.Fatal(err) }
	second, err := Import(context.Background(), req); if err != nil { t.Fatal(err) }
	if !second.AlreadyImported || second.DraftID != first.DraftID { t.Fatalf("first=%#v second=%#v", first, second) }
}
```

- [ ] **Step 2: Run** `go test ./internal/draftimport -run 'TestImport|TestMaterialize' -count=1`; expect `undefined: Import`.

- [ ] **Step 3: Implement validation-first staging and receipt lookup.**

```go
func Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	validated, err := ValidatePackage(req.PackagePath, DefaultLimits()); if err != nil { return ImportResult{}, err }
	if prior, ok := readReceipt(req.MediaRoot, validated.PackageSHA256); ok && !req.CreateCopy { return ImportResult{DraftID:prior.DraftID, ReceiptPath:receiptPath(req.MediaRoot, validated.PackageSHA256), PackageSHA256:validated.PackageSHA256, AlreadyImported:true}, nil }
	stage, err := extractToRandomStage(validated, req.JianyingRoot); if err != nil { return ImportResult{}, err }; defer os.RemoveAll(stage)
	mapping, err := materializeAssets(stage, req.MediaRoot, validated.Manifest.Assets); if err != nil { return ImportResult{}, err }
	if err = rewriteVirtualURIs(filepath.Join(stage, "draft"), mapping); err != nil { return ImportResult{}, err }
	if req.DryRun { return ImportResult{PackageSHA256:validated.PackageSHA256}, nil }
	return registerDraft(ctx, req, validated, stage)
}
```

- [ ] **Step 4: Add missing ID, same-hash destination, conflicting destination, and `CreateCopy` cases; run** `go test ./internal/draftimport -run 'TestImport|TestMaterialize' -count=1`; expect PASS.

- [ ] **Step 5: Commit.**

```powershell
git add internal/draftimport/import.go internal/draftimport/import_test.go
git commit -m "feat: materialize portable draft assets"
```

### Task 6: Register atomically and roll back every partial change

**Files:** Create `internal/draftimport/register_windows.go`, `internal/draftimport/testdata/root_meta_info.json`; modify `internal/draftimport/import_test.go`.

- [ ] **Step 1: Add a rollback test using the sanitized real index fixture.**

```go
func TestRegisterRollbackRestoresIndex(t *testing.T) {
	fx := registrationFixture(t); before, _ := os.ReadFile(fx.IndexPath)
	fx.Hooks.BeforeIndexRename = func() error { return errors.New("injected") }
	_, err := registerDraftWithHooks(context.Background(), fx.Request, fx.Validated, fx.Stage, fx.Hooks)
	assertCode(t, err, "commit_failed"); after, _ := os.ReadFile(fx.IndexPath)
	if !bytes.Equal(before, after) { t.Fatal("root index changed after rollback") }
}
```

- [ ] **Step 2: Run** `go test ./internal/draftimport -run 'TestRegister|TestLock|TestRollback' -count=1`; expect `undefined: registerDraftWithHooks`.

- [ ] **Step 3: Implement exclusive locking and atomic file replacement.**

```go
func withRegistrationLock(root string, fn func() error) error {
	p := filepath.Join(root, ".vpc-import.lock"); f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil { return code("lock_busy", "close Jianying and retry") }
	fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)); f.Sync(); f.Close()
	defer os.Remove(p); return fn()
}
func atomicReplace(path string, data []byte) error {
	tmp := path + ".partial"; f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); if err != nil { return err }
	if _, err = f.Write(data); err == nil { err = f.Sync() }; if closeErr := f.Close(); err == nil { err = closeErr }
	if err != nil { os.Remove(tmp); return err }; return os.Rename(tmp, path)
}
```

- [ ] **Step 4: Commit in this order:** lock, backup index bytes, choose collision-free ID, update staged metadata, sync partial draft, atomically replace index, rename draft, verify index/assets, write receipt last. Roll back in reverse order.

- [ ] **Step 5: Add ID conflict, lock contention, post-write verification failure, and restoration-failure cases; run** `go test ./internal/draftimport -run 'TestRegister|TestLock|TestRollback' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/draftimport/register_windows.go internal/draftimport/import_test.go internal/draftimport/testdata/root_meta_info.json
git commit -m "feat: register portable drafts atomically"
```

### Task 7: Ship the standalone Windows importer

**Files:** Create `cmd/draft-importer/main.go`, `cmd/draft-importer/main_test.go`, `M1-IMPORTER-README.md`; modify `scripts/release.ps1`.

- [ ] **Step 1: Add a failing CLI test.**

```go
func TestCLIRequiresRoots(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run([]string{"--package", "x.vpcdraft"}, &out, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "error_code=invalid_arguments") { t.Fatalf("code=%d stderr=%s", code, stderr.String()) }
}
```

- [ ] **Step 2: Run** `go test ./cmd/draft-importer -count=1`; expect `undefined: run`.

- [ ] **Step 3: Implement CLI and safe exit codes.**

```go
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jianying-draft-importer", flag.ContinueOnError); fs.SetOutput(stderr)
	packagePath := fs.String("package", "", "package path"); jianyingRoot := fs.String("jianying-root", "", "Jianying draft root"); mediaRoot := fs.String("media-root", "", "persistent media root")
	dryRun := fs.Bool("dry-run", false, "validate only"); copyDraft := fs.Bool("copy", false, "create another copy")
	if fs.Parse(args) != nil || *packagePath == "" || *jianyingRoot == "" || *mediaRoot == "" { fmt.Fprintln(stderr, "error_code=invalid_arguments message=required argument missing"); return 2 }
	result, err := draftimport.Import(context.Background(), draftimport.ImportRequest{PackagePath:*packagePath, JianyingRoot:*jianyingRoot, MediaRoot:*mediaRoot, DryRun:*dryRun, CreateCopy:*copyDraft})
	if err != nil { fmt.Fprintf(stderr, "error_code=%s message=%s\n", safeCode(err), safeMessage(err)); return 1 }
	fmt.Fprintf(stdout, "status=ok draft_id=%s receipt=written package_sha256=%s\n", result.DraftID, result.PackageSHA256); return 0
}
```

- [ ] **Step 4: Extend `scripts/release.ps1` after existing builds.**

```powershell
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'
go build -trimpath -o (Join-Path $releaseDirectory 'jianying-draft-importer.exe') ./cmd/draft-importer
if ($LASTEXITCODE -ne 0) { throw 'draft importer build failed' }
Copy-Item -LiteralPath 'M1-IMPORTER-README.md' -Destination $releaseDirectory
```

- [ ] **Step 5: Run** `go test ./... -count=1`, build the Windows binary, and execute `scripts/release.ps1`; expect PASS, checksum entry, and release ZIP.

- [ ] **Step 6: Commit.**

```powershell
git add cmd/draft-importer/main.go cmd/draft-importer/main_test.go scripts/release.ps1 M1-IMPORTER-README.md
git commit -m "release: ship standalone M1 draft importer"
```

### Task 8: Prove “天中观局” on a second computer before M2

**Files:** Create `scripts/m1-cross-machine.ps1`, `docs/acceptance/m1-tianzhong-template.md`.

- [ ] **Step 1: Write the failing evidence smoke check.**

```powershell
param([Parameter(Mandatory)][string]$EvidenceRoot)
$required = @('package.sha256','dry-run.txt','import.txt','receipt.json','jianying-open.png','rollback.txt')
foreach ($name in $required) { if (-not (Test-Path -LiteralPath (Join-Path $EvidenceRoot $name))) { throw "missing evidence: $name" } }
if (Select-String -LiteralPath (Join-Path $EvidenceRoot 'import.txt') -Pattern '[A-Za-z]:\\|\\\\') { throw 'absolute path leaked into evidence' }
Write-Output 'PASS m1-cross-machine'
```

- [ ] **Step 2: Run** the checker on an empty evidence folder; expect `missing evidence: package.sha256`.

- [ ] **Step 3: On machine A**, package the current “天中观局” draft and copy only `.vpcdraft`, importer EXE, README, and `SHA256SUMS.txt` to machine B. Record package hash and dry-run output.

```powershell
$draftRoot = Read-Host '请输入天中观局草稿绝对目录'
$handoffRoot = Read-Host '请输入交付U盘或共享目录'
New-Item -ItemType Directory -Path $handoffRoot -Force | Out-Null
pwsh -NoProfile -File .\scripts\package-m1.ps1 -DraftRoot $draftRoot -OutputPath (Join-Path $handoffRoot '天中观局.vpcdraft') -ProjectId 'tianzhong-m1'
Copy-Item -LiteralPath '.\dist\jianying-draft-importer.exe','.\M1-IMPORTER-README.md','.\dist\SHA256SUMS.txt' -Destination $handoffRoot
Get-FileHash -LiteralPath (Join-Path $handoffRoot '天中观局.vpcdraft') -Algorithm SHA256 | Format-List | Out-File (Join-Path $handoffRoot 'package.sha256') -Encoding utf8
```

- [ ] **Step 4: On machine B**, verify checksum, run dry-run, close Jianying, import into a long-lived media directory, open the editable timeline, rerun for idempotency, then execute the disposable rollback fixture.

```powershell
$handoffRoot = Read-Host '请输入交付目录'
$jianyingRoot = Read-Host '请输入剪映草稿根目录'
$mediaRoot = Read-Host '请输入长期素材目录'
$package = Join-Path $handoffRoot '天中观局.vpcdraft'; $importer = Join-Path $handoffRoot 'jianying-draft-importer.exe'
& $importer --package $package --jianying-root $jianyingRoot --media-root $mediaRoot --dry-run 2>&1 | Tee-Object (Join-Path $handoffRoot 'dry-run.txt')
if ($LASTEXITCODE -ne 0 -or (Get-Content (Join-Path $handoffRoot 'dry-run.txt') -Raw) -notmatch 'status=ok') { throw 'M1 dry-run failed' }
& $importer --package $package --jianying-root $jianyingRoot --media-root $mediaRoot 2>&1 | Tee-Object (Join-Path $handoffRoot 'import.txt')
if ($LASTEXITCODE -ne 0 -or (Get-Content (Join-Path $handoffRoot 'import.txt') -Raw) -notmatch 'receipt=written') { throw 'M1 import failed' }
& $importer --package $package --jianying-root $jianyingRoot --media-root $mediaRoot 2>&1 | Tee-Object (Join-Path $handoffRoot 'idempotent.txt')
if ((Get-Content (Join-Path $handoffRoot 'idempotent.txt') -Raw) -notmatch 'status=ok') { throw 'M1 repeat import failed' }
$receipt = Get-ChildItem -LiteralPath (Join-Path $mediaRoot '.vpc-receipts') -Filter '*.json' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if ($null -eq $receipt) { throw 'M1 receipt missing' }; Copy-Item -LiteralPath $receipt.FullName -Destination (Join-Path $handoffRoot 'receipt.json')
$screenshot = Read-Host '关闭剪映前，请输入已保存的可编辑时间线截图路径'
Copy-Item -LiteralPath $screenshot -Destination (Join-Path $handoffRoot 'jianying-open.png')
```

- [ ] **Step 5: On machine A, run the injected rollback test and the evidence checker.**

```powershell
go test ./internal/draftimport -run TestRegisterRollbackRestoresIndex -count=1 -v 2>&1 | Out-File (Join-Path $handoffRoot 'rollback.txt') -Encoding utf8
if ($LASTEXITCODE -ne 0) { throw 'rollback fixture failed' }
pwsh -NoProfile -File .\scripts\m1-cross-machine.ps1 -EvidenceRoot $handoffRoot
if ($LASTEXITCODE -ne 0) { throw 'M1 evidence verification failed' }
```

Expected: rollback test reports `PASS`; the evidence checker exits 0 and prints `PASS m1-cross-machine` with no sender absolute path.

- [ ] **Step 6: Commit reusable script/template only; never commit partner media or local paths.**

```powershell
git add scripts/m1-cross-machine.ps1 docs/acceptance/m1-tianzhong-template.md
git commit -m "test: define M1 cross-machine acceptance"
```

## M1 exit gate

- [ ] The receiver opens the editable “天中观局” timeline with every used visual, narration, BGM, and SFX, without the sender's library.
- [ ] Re-import is idempotent; `--copy` creates a non-conflicting draft ID.
- [ ] Lock contention and injected index failure leave the original Jianying index recoverable.
- [ ] Evidence includes package hash, validator result, receipt, screenshot, and rollback result, with no source absolute path.
- [ ] M2 does not begin until all four checks pass.

## Self-review

- [ ] Confirm every new symbol is defined before use and package names match the interface table.
- [ ] Confirm every task follows red-test, minimal implementation, green-test, commit order.
- [ ] Confirm M1 contains no Ed25519 private-key handling, partner site, 2032 listener, or Cloudflare configuration.
- [ ] Confirm Markdown fences are paired and PowerShell uses Windows syntax.
