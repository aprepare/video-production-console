# Partner Portable Delivery M2 Package Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the M1 prototype into a signed, path-clean, size-efficient `.vpcdraft` with referenced-media clipping, a seven-day auditable download lifecycle, and strict importer verification.

**Architecture:** M2 extends the fixed M1 packages instead of creating a second format. `internal/portablepackage` produces a canonical manifest, clips only used video intervals with three-second handles, signs the manifest/file-hash payload with Ed25519, and exposes a delivery gate; `internal/store` owns package metadata and expiry, while a transport-neutral HTTP handler streams authorized package bytes with Range support. M3 will supply partner identity and the automated stage runner through the explicit owner/service interfaces defined here.

**Tech Stack:** Go 1.25, SQLite migrations, SHA-256, Ed25519, ZIP, existing `internal/mediacatalog.FFmpeg`, `net/http`, PowerShell release scripts, Windows importer.

---

## File map and M2 interface lock

| Path | Action | Responsibility |
|---|---|---|
| `internal/portablepackage/model.go` | Modify | Formal package and signature metadata |
| `internal/portablepackage/canonical.go` | Create | Deterministic manifest/file-hash signing payload |
| `internal/portablepackage/references.go` | Create | Jianying reference/range extraction and role assignment |
| `internal/portablepackage/clip.go` | Create | Three-second handle calculation and clipped-asset metadata |
| `internal/mediacatalog/ffmpeg.go` | Modify | Export controlled `ClipRange` using configured binaries |
| `internal/portablepackage/sign.go` | Create | Ed25519 signer, verifier, public keyring |
| `internal/portablepackage/formal.go` | Create | Atomic formal ZIP build and zero-path scan |
| `internal/portablepackage/gate.go` | Create | Ready gate and stable failure codes |
| `internal/portablepackage/*_test.go` | Create/Modify | Canonical, clipping, signing, packaging, gate tests |
| `internal/store/migrations.go` | Modify | Package/download tables and indexes |
| `internal/store/portable_packages.go` | Create | Owner-scoped lifecycle repository |
| `internal/store/portable_packages_test.go` | Create | Scope, expiry, renewal, purge, audit tests |
| `internal/httpapi/portable_packages.go` | Create | Metadata, renewal, Range download handler |
| `internal/httpapi/portable_packages_test.go` | Create | 200/206/416/404/410 and leak tests |
| `internal/draftimport/validate.go` | Modify | Formal signature/version verification |
| `internal/draftimport/config.go` | Create | Local public-key/config persistence |
| `internal/draftimport/import_test.go` | Modify | Tamper, version, key rotation, formal import tests |
| `internal/portablepackage/service.go` | Create | M3-facing `BuildService` and `FormalBuildRequest/Result` |
| `scripts/release.ps1` | Modify | Keyring, compatibility matrix, importer packaging |
| `scripts/m2-acceptance.ps1` | Create | Focused formal-package, lifecycle, importer and regression acceptance |
| `docs/portable-package-format.md` | Create | Versioned format and compatibility contract |

Fixed M2 public types are `FormalBuildRequest`, `PreparedBuild`, `UnsignedBuild`, `FormalBuildResult`, `BuildService`, `Signer`, `Verifier`, `Keyring`, `DeliveryGate`, `store.PackageOwnerScope`, `store.PortablePackage`, and `httpapi.NewPortablePackageHandler`. M3 consumes these exact names.

`PackageOwnerScope.OwnerID` is reserved for the future `partner_users.id`; `AccountID` is the business account that owns the project. M2 treats both as opaque mandatory IDs. M3 resolves them through `partner_project_owners` plus an active `partner_account_grants` row before calling the M2 handler, so grant revocation blocks downloads immediately.

### Task 1: Canonicalize the formal manifest and file list

**Files:** Modify `internal/portablepackage/model.go`; create `internal/portablepackage/canonical.go`, `internal/portablepackage/canonical_test.go`.

- [ ] **Step 1: Write a failing determinism test** with assets and files presented in opposite orders.

```go
func TestCanonicalPayloadIsOrderIndependent(t *testing.T) {
	a := formalManifestFixture(); b := formalManifestFixture()
	slices.Reverse(b.Assets); slices.Reverse(b.Files)
	one, err := CanonicalPayload(a); if err != nil { t.Fatal(err) }
	two, err := CanonicalPayload(b); if err != nil { t.Fatal(err) }
	if !bytes.Equal(one, two) { t.Fatalf("canonical payload differs\n%s\n%s", one, two) }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestCanonicalPayload -count=1`; expect `undefined: CanonicalPayload`.

- [ ] **Step 3: Extend the M1 model with formal metadata.**

```go
const FormalPackageVersion = "1.0.0"
const FormalImporterMinVersion = "1.0.0"
type FileHash struct { Path string `json:"path"`; SHA256 string `json:"sha256"`; SizeBytes int64 `json:"size_bytes"` }
type MediaInfo struct { DurationMS int64 `json:"duration_ms"`; VideoCodec string `json:"video_codec,omitempty"`; AudioCodec string `json:"audio_codec,omitempty"`; Width int `json:"width,omitempty"`; Height int `json:"height,omitempty"`; SampleRate int `json:"sample_rate,omitempty"` }
type Asset struct {
	AssetID string `json:"asset_id"`; Role string `json:"role"`; Path string `json:"path"`; SHA256 string `json:"sha256"`
	SizeBytes int64 `json:"size_bytes"`; MIME string `json:"mime"`; Media MediaInfo `json:"media"`; SourceTimeRange *SourceTimeRange `json:"source_timerange,omitempty"`
}
type SignatureEnvelope struct {
	Mode string `json:"mode"`; Algorithm string `json:"algorithm,omitempty"`; KeyID string `json:"key_id,omitempty"`
}
type Manifest struct {
	SchemaVersion string `json:"schema_version"`; PackageVersion string `json:"package_version"`
	ImporterMinVersion string `json:"importer_min_version"`; DraftVersion string `json:"draft_version"`; ProjectID string `json:"project_id"`
	ContentSetSHA256 string `json:"content_set_sha256"`; Assets []Asset `json:"assets"`; Files []FileHash `json:"files"`; Signature SignatureEnvelope `json:"signature"`
}
```

`content_set_sha256` is the SHA-256 of the sorted `Files` records excluding `manifest.json` and `SIGNATURE.ed25519`. The final ZIP SHA-256 cannot be embedded in the ZIP without self-reference, so `FormalBuildResult.SHA256`, `portable_packages.package_sha256`, download ETag, release checksums, and import receipts are the authoritative final-package hash. The manifest still carries complete signature metadata and the non-recursive content-set hash.

- [ ] **Step 4: Implement canonicalization with copied slices, stable sorting, HTML escaping disabled, UTF-8, and one LF.**

```go
func CanonicalPayload(in Manifest) ([]byte, error) {
	m := in; m.Assets = append([]Asset(nil), in.Assets...); m.Files = append([]FileHash(nil), in.Files...)
	sort.Slice(m.Assets, func(i, j int) bool { return m.Assets[i].AssetID < m.Assets[j].AssetID })
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	var buf bytes.Buffer; enc := json.NewEncoder(&buf); enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil { return nil, err }; return buf.Bytes(), nil
}
```

- [ ] **Step 5: Add duplicate asset/path, non-forward-slash, content-set hash, duration/codec JSON, and final-ZIP-hash-outside-manifest tests; run** `go test ./internal/portablepackage -run 'TestCanonical|TestManifest' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/portablepackage/model.go internal/portablepackage/canonical.go internal/portablepackage/canonical_test.go
git commit -m "feat: canonicalize formal portable manifests"
```

### Task 2: Extract real Jianying references and union used ranges

**Files:** Create `internal/portablepackage/references.go`, `internal/portablepackage/references_test.go`, `internal/portablepackage/testdata/draft_content_ranges.json`.

- [ ] **Step 1: Add a failing fixture test** for two overlapping uses of one video, narration, BGM, SFX, an unused local file, and a generated image.

```go
func TestCollectReferencesUnionsVideoUse(t *testing.T) {
	refs, err := CollectReferences("testdata/draft_content_ranges.json"); if err != nil { t.Fatal(err) }
	video := findRef(t, refs, "video")
	if video.UsedStartMS != 5000 || video.UsedEndMS != 17000 { t.Fatalf("video=%#v", video) }
	if containsSource(refs, "unused.mp4") { t.Fatal("unused file was collected") }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestCollectReferences -count=1`; expect `undefined: CollectReferences`.

- [ ] **Step 3: Define and implement the extraction result** against the sanitized real draft fixture.

```go
type Reference struct {
	SourcePath string; Role string; UsedStartMS int64; UsedEndMS int64; DraftPointers []string
}
func CollectReferences(path string) ([]Reference, error) {
	data, err := os.ReadFile(path); if err != nil { return nil, err }
	var draft jianyingDraft; if err = json.Unmarshal(data, &draft); err != nil { return nil, err }
	byMaterial := indexMaterials(draft.Materials)
	refs := map[string]Reference{}
	for _, track := range draft.Tracks { for _, segment := range track.Segments {
		material, ok := byMaterial[segment.MaterialID]; if !ok || material.LocalPath == "" { continue }
		mergeReference(refs, material.LocalPath, roleFor(track.Type, material.Type), segment.SourceTimerange)
	} }
	return sortedReferences(refs), nil
}
```

- [ ] **Step 4: Add invalid negative/overflow timerange tests and reject unknown local material types; run** `go test ./internal/portablepackage -run TestCollectReferences -count=1`; expect PASS.

- [ ] **Step 5: Commit.**

```powershell
git add internal/portablepackage/references.go internal/portablepackage/references_test.go internal/portablepackage/testdata/draft_content_ranges.json
git commit -m "feat: collect used Jianying media ranges"
```

### Task 3: Clip videos with three-second editing handles

**Files:** Create `internal/portablepackage/clip.go`, `internal/portablepackage/clip_test.go`; modify `internal/mediacatalog/ffmpeg.go`, `internal/mediacatalog/ffmpeg_test.go`.

- [ ] **Step 1: Write failing range tests** for a middle clip, source start, source end, and short source.

```go
func TestExpandRangeAddsThreeSecondHandles(t *testing.T) {
	got := ExpandRange(5000, 12000, 30000)
	want := ClipRange{InputStartMS:2000, InputDurationMS:18000, DraftStartMS:3000, DraftDurationMS:12000}
	if got != want { t.Fatalf("got=%#v want=%#v", got, want) }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestExpandRange -count=1`; expect `undefined: ExpandRange`.

- [ ] **Step 3: Implement handle arithmetic and metadata rewrite.**

```go
type ClipRange struct { InputStartMS, InputDurationMS, DraftStartMS, DraftDurationMS int64 }
func ExpandRange(usedStart, usedEnd, sourceDuration int64) ClipRange {
	start := max(int64(0), usedStart-3000); end := min(sourceDuration, usedEnd+3000)
	return ClipRange{InputStartMS:start, InputDurationMS:end-start, DraftStartMS:usedStart-start, DraftDurationMS:usedEnd-usedStart}
}
```

- [ ] **Step 4: Add an exported controlled clip method to the existing FFmpeg wrapper.**

```go
// Extend the existing Probe type in internal/mediacatalog/ffmpeg.go.
type Probe struct { DurationMS int64; Width,Height int; FPS float64; HasAudio bool; VideoCodec,AudioCodec string; SampleRate int }
func (f *FFmpeg) ClipRange(ctx context.Context, input, output string, startMS, durationMS int64) error {
	args := []string{"-hide_banner","-nostdin","-y","-ss",formatSeconds(startMS),"-i",input,"-t",formatSeconds(durationMS),"-map","0:v:0","-map","0:a?","-c:v","libx264","-preset","medium","-crf","18","-c:a","aac","-movflags","+faststart",output}
	_, stderr, err := f.run(ctx, f.ffmpegPath, args...); if err != nil { return fmt.Errorf("clip media: %w (%s)", err, truncateCommandOutput(stderr)) }; return nil
}
```

- [ ] **Step 4a: Extend the existing ffprobe JSON parser** to read `codec_name` and audio `sample_rate`; copy those values plus clipped duration/width/height into each formal manifest `Asset.Media`. Reject a video with missing codec/dimensions and audio with an invalid sample rate.

```go
type probeStream struct { CodecType string `json:"codec_type"`; CodecName string `json:"codec_name"`; SampleRate string `json:"sample_rate"`; Width int `json:"width"`; Height int `json:"height"`; AvgFrameRate string `json:"avg_frame_rate"`; RFrameRate string `json:"r_frame_rate"` }
for _,stream:=range payload.Streams { switch stream.CodecType {
case "video": if probe.Width==0 { probe.Width=stream.Width; probe.Height=stream.Height; probe.VideoCodec=stream.CodecName }
case "audio": probe.HasAudio=true; probe.AudioCodec=stream.CodecName; probe.SampleRate,_=strconv.Atoi(stream.SampleRate)
} }
```

- [ ] **Step 5: Test exact argv with a fake `mediacatalog.CommandRunner`; run** `go test ./internal/mediacatalog ./internal/portablepackage -run 'TestClip|TestExpandRange' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/portablepackage/clip.go internal/portablepackage/clip_test.go internal/mediacatalog/ffmpeg.go internal/mediacatalog/ffmpeg_test.go
git commit -m "feat: clip referenced video with editing handles"
```

### Task 4: Sign and verify packages with an Ed25519 keyring

**Files:** Create `internal/portablepackage/sign.go`, `internal/portablepackage/sign_test.go`.

- [ ] **Step 1: Add failing tests** for valid signature, changed manifest, changed file hash, unknown key ID, and rotation where old and new public keys both verify.

```go
func TestKeyringSupportsRotation(t *testing.T) {
	oldPub, oldPriv, _ := ed25519.GenerateKey(rand.Reader); newPub, _, _ := ed25519.GenerateKey(rand.Reader)
	signer := NewEd25519Signer("key-2026-08", oldPriv); payload := []byte("canonical")
	sig, _ := signer.Sign(payload)
	if err := (Keyring{"key-2026-08":oldPub, "key-2026-09":newPub}).Verify("key-2026-08", payload, sig); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run 'TestKeyring|TestSignature' -count=1`; expect `undefined: NewEd25519Signer`.

- [ ] **Step 3: Define fixed signing interfaces and implementation.**

```go
const SignatureEntryName = "SIGNATURE.ed25519"
type Signer interface { KeyID() string; Sign([]byte) ([]byte, error) }
type Verifier interface { Verify(keyID string, payload, signature []byte) error }
type Ed25519Signer struct { id string; private ed25519.PrivateKey }
func NewEd25519Signer(id string, private ed25519.PrivateKey) *Ed25519Signer { return &Ed25519Signer{id:id, private:private} }
func (s *Ed25519Signer) KeyID() string { return s.id }
func (s *Ed25519Signer) Sign(payload []byte) ([]byte, error) { return ed25519.Sign(s.private, payload), nil }
type Keyring map[string]ed25519.PublicKey
func (k Keyring) Verify(id string, payload, signature []byte) error {
	pub, ok := k[id]; if !ok { return ErrUnknownSigningKey }; if !ed25519.Verify(pub, payload, signature) { return ErrInvalidSignature }; return nil
}
```

- [ ] **Step 4: Reject malformed key sizes and empty key IDs; run** `go test ./internal/portablepackage -run 'TestKeyring|TestSignature' -count=1`; expect PASS.

- [ ] **Step 5: Commit.**

```powershell
git add internal/portablepackage/sign.go internal/portablepackage/sign_test.go
git commit -m "feat: sign portable packages with Ed25519"
```

### Task 5: Build the formal atomic ZIP and enforce the ready gate

**Files:** Create `internal/portablepackage/formal.go`, `internal/portablepackage/gate.go`, `internal/portablepackage/formal_test.go`, `internal/portablepackage/gate_test.go`, `internal/portablepackage/service.go`.

- [ ] **Step 1: Add a failing full-builder test** that verifies clipped content, rewritten `source_timerange`, `vpcasset://`, `SIGNATURE.ed25519`, and zero drive/UNC/temp/workspace paths.

```go
func TestFormalBuilderProducesSignedPathCleanPackage(t *testing.T) {
	result, err := formalFixture(t).Builder.Build(context.Background(), formalFixture(t).Request)
	if err != nil { t.Fatal(err) }
	if result.Manifest.Signature.Mode != "ed25519" || result.Manifest.Signature.KeyID == "" { t.Fatal("missing signature metadata") }
	assertZipEntry(t, result.Path, "SIGNATURE.ed25519"); assertNoServerPath(t, result.Path)
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run 'TestFormalBuilder|TestDeliveryGate' -count=1`; expect missing `PackageBuilder`.

- [ ] **Step 3: Define the service boundary consumed by M3.**

```go
type FormalBuildRequest struct { ProjectID, SourceScriptPath, NarrationPath, DraftRoot, OutputPath string }
type PreparedBuild struct { Request FormalBuildRequest; StageRoot, SHA256 string; Assets []Asset }
type UnsignedBuild struct { Prepared PreparedBuild; Manifest Manifest; CanonicalSHA256 string }
type FormalBuildResult struct { BuildResult; SignatureKeyID string; ManifestSHA256 string }
type BuildService interface {
	Prepare(context.Context, FormalBuildRequest) (PreparedBuild, error)
	Package(context.Context, PreparedBuild) (UnsignedBuild, error)
	Sign(context.Context, UnsignedBuild) (FormalBuildResult, error)
	Build(context.Context, FormalBuildRequest) (FormalBuildResult, error)
}
type PackageBuilder struct { Media *mediacatalog.FFmpeg; Signer Signer; TempRoot string }
```

- [ ] **Step 4: Implement build order:** validate inputs and disk reserve, collect references, clip videos/copy distributable BGM/SFX/audio/images, hash final bytes for stable filenames, rewrite draft URIs and source ranges, scan JSON, write canonical manifest, sign payload, write signature, create sorted ZIP `.partial`, fsync/close, rename, hash final ZIP.

```go
type DiskSpace interface { FreeBytes(string) (int64,error) }
type DeliveryGate struct { Verifier Verifier; Disk DiskSpace; MinFreeBytes int64 }
func (g DeliveryGate) Validate(ctx context.Context, r FormalBuildResult) error {
	if r.Path == "" || r.SHA256 == "" || r.SignatureKeyID == "" { return gateError("package_incomplete") }
	free,err:=g.Disk.FreeBytes(filepath.Dir(r.Path));if err!=nil||free<g.MinFreeBytes{return gateError("disk_low")}
	if err := verifyFormalArchive(r.Path, g.Verifier); err != nil { return gateError("package_verification_failed") }
	if paths, _ := ScanArchiveForbiddenPaths(r.Path); len(paths) != 0 { return gateError("server_path_leak") }
	return nil
}
```

- [ ] **Step 5: Add gate failures** for missing source script, undecodable narration, duration mismatch, missing asset, `DiskSpace.FreeBytes < MinFreeBytes`, bad hash/signature, and path leak; run `go test ./internal/portablepackage -run 'TestFormalBuilder|TestDeliveryGate' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/portablepackage/formal.go internal/portablepackage/gate.go internal/portablepackage/service.go internal/portablepackage/formal_test.go internal/portablepackage/gate_test.go
git commit -m "feat: build and gate formal portable packages"
```

### Task 6: Persist owner-scoped package and download records

**Files:** Modify `internal/store/migrations.go`; create `internal/store/portable_packages.go`, `internal/store/portable_packages_test.go`.

- [ ] **Step 1: Add a failing migration/repository test** for owner A/B isolation and old databases.

```go
func TestPortablePackageOwnerScopeReturnsNotFound(t *testing.T) {
	repo := fixturePortablePackageRepository(t); pkg := repo.InsertFixture(t, "owner-a")
	_, err := repo.Get(context.Background(), PackageOwnerScope{OwnerID:"owner-b",AccountID:"account-b"}, pkg.ID)
	if !errors.Is(err, ErrPortablePackageNotFound) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'TestPortablePackage|TestPortableMigration' -count=1`; expect missing table/type.

- [ ] **Step 3: Append the concrete migration.**

```sql
CREATE TABLE portable_packages (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  owner_id TEXT NOT NULL, account_id TEXT NOT NULL REFERENCES accounts(id), package_path TEXT NOT NULL, package_sha256 TEXT NOT NULL,
  signature_key_id TEXT NOT NULL, manifest_version TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
  status TEXT NOT NULL CHECK(status IN ('ready','expired','purged')), expires_at DATETIME NOT NULL,
  marked_expired_at DATETIME, created_at DATETIME NOT NULL, version INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX portable_packages_owner_status_idx ON portable_packages(owner_id,account_id,status,expires_at);
CREATE TABLE partner_download_events (
  id TEXT PRIMARY KEY, package_id TEXT NOT NULL REFERENCES portable_packages(id) ON DELETE CASCADE,
  owner_id TEXT NOT NULL, event_type TEXT NOT NULL CHECK(event_type IN ('issued','range','complete','expired','renewed','failed')),
  request_id TEXT NOT NULL, bytes_sent INTEGER NOT NULL DEFAULT 0, client_digest TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL
);
CREATE INDEX partner_download_events_package_created_idx ON partner_download_events(package_id,created_at);
```

- [ ] **Step 4: Implement the fixed store types and scope every statement by `owner_id`.**

```go
type PackageOwnerScope struct { OwnerID,AccountID string }
type PortablePackage struct { ID, ProjectID, OwnerID, AccountID, Path, SHA256, SignatureKeyID, ManifestVersion, Status string; SizeBytes int64; ExpiresAt time.Time; Version int64 }
type DownloadEvent struct { PackageID, OwnerID, EventType, RequestID, ClientDigest string; BytesSent int64; CreatedAt time.Time }
func (r *PortablePackageRepository) Get(ctx context.Context, scope PackageOwnerScope, id string) (PortablePackage, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,project_id,owner_id,account_id,package_path,package_sha256,signature_key_id,manifest_version,size_bytes,status,expires_at,version FROM portable_packages WHERE id=? AND owner_id=? AND account_id=?`, id, scope.OwnerID, scope.AccountID)
	return scanPortablePackage(row)
}
```

- [ ] **Step 5: Run** `go test ./internal/store -run 'TestPortablePackage|TestPortableMigration' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/store/migrations.go internal/store/portable_packages.go internal/store/portable_packages_test.go
git commit -m "feat: persist portable package delivery records"
```

### Task 7: Implement seven-day expiry, renewal, and recoverable cleanup

**Files:** Modify `internal/store/portable_packages.go`; create `internal/portablepackage/cleanup.go`, `internal/portablepackage/cleanup_test.go`.

- [ ] **Step 1: Add failing clock-controlled tests** for default seven days, owner-only renewal, atomic expiry marking, deletion after mark, retained manifest summary, and orphan `.partial` cleanup.

```go
func TestCleanupMarksBeforeDeleting(t *testing.T) {
	fx := cleanupFixture(t); fx.Clock.Advance(8*24*time.Hour)
	if _,err := fx.Cleaner.Run(context.Background(),fx.Clock.Now(),100); err != nil { t.Fatal(err) }
	pkg := fx.Repo.MustGetAny(t, fx.PackageID)
	if pkg.Status != "purged" || fileExists(pkg.Path) { t.Fatalf("package=%#v", pkg) }
	if fx.Repo.EventCount(t, "expired") != 1 { t.Fatal("missing expiry audit") }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage ./internal/store -run 'TestCleanup|TestRenew' -count=1`; expect missing lifecycle methods.

- [ ] **Step 3: Implement conditional lifecycle updates.**

```go
type CleanupResult struct { PackagesExpired,PackagesPurged,StagingPurged int }
type Cleaner struct { Repo *store.PortablePackageRepository; StagingRoot string }
func (c *Cleaner) Run(ctx context.Context,now time.Time,limit int)(CleanupResult,error){ expired,err:=c.Repo.MarkExpiredLimit(ctx,now,limit);if err!=nil{return CleanupResult{},err};return c.deleteMarkedAndOldStaging(ctx,expired,now) }
func (r *PortablePackageRepository) Renew(ctx context.Context, scope PackageOwnerScope, id string, now time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE portable_packages SET expires_at=?,status='ready',marked_expired_at=NULL,version=version+1 WHERE id=? AND owner_id=? AND account_id=? AND status!='purged'`, now.Add(7*24*time.Hour), id, scope.OwnerID, scope.AccountID)
	if err != nil { return err }; return requireOneRow(res, ErrPortablePackageNotFound)
}
func (r *PortablePackageRepository) MarkExpired(ctx context.Context, now time.Time) ([]PortablePackage, error) {
	return r.markAndList(ctx, `UPDATE portable_packages SET status='expired',marked_expired_at=?,version=version+1 WHERE status='ready' AND expires_at<=? RETURNING id,project_id,owner_id,package_path,package_sha256,signature_key_id,manifest_version,size_bytes,status,expires_at,version`, now, now)
}
```

- [ ] **Step 4: Delete package bytes only after committed `expired`; set `purged` only after successful delete or verified absence; retain row and events. Run** `go test ./internal/portablepackage ./internal/store -run 'TestCleanup|TestRenew' -count=1`; expect PASS.

- [ ] **Step 5: Commit.**

```powershell
git add internal/store/portable_packages.go internal/portablepackage/cleanup.go internal/portablepackage/cleanup_test.go
git commit -m "feat: expire and purge portable packages safely"
```

### Task 8: Stream packages with Range and no path disclosure

**Files:** Create `internal/httpapi/portable_packages.go`, `internal/httpapi/portable_packages_test.go`.

- [ ] **Step 1: Add HTTP tests** for full download, `bytes=10-19` 206, unsatisfiable 416, expired 410, owner mismatch 404, ETag, and audit bytes.

```go
func TestPortableDownloadSupportsRange(t *testing.T) {
	h := portableHandlerFixture(t, "owner-a"); req := httptest.NewRequest(http.MethodGet, "/api/portable-packages/pkg/download", nil)
	req.Header.Set("Range", "bytes=10-19"); req = req.WithContext(WithPackageOwner(req.Context(), store.PackageOwnerScope{OwnerID:"owner-a",AccountID:"account-a"}))
	rr := httptest.NewRecorder(); h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent || rr.Header().Get("Content-Range") != "bytes 10-19/100" || rr.Body.Len() != 10 { t.Fatalf("code=%d headers=%v", rr.Code, rr.Header()) }
}
```

- [ ] **Step 2: Run** `go test ./internal/httpapi -run TestPortable -count=1`; expect `undefined: NewPortablePackageHandler`.

- [ ] **Step 3: Define the transport boundary and use `ServeContent` only after scoped lookup.**

```go
type PortablePackageStore interface { Get(context.Context, store.PackageOwnerScope, string) (store.PortablePackage,error); Renew(context.Context, store.PackageOwnerScope,string,time.Time) error; RecordDownload(context.Context,store.DownloadEvent) error }
func NewPortablePackageHandler(repo PortablePackageStore, packageRoot string, now func() time.Time) http.Handler { return &portablePackageHandler{repo:repo, packageRoot:mustCanonicalDirectory(packageRoot), now:now} }
func (h *portablePackageHandler) download(w http.ResponseWriter, r *http.Request, id string) {
	scope, ok := PackageOwnerFromContext(r.Context()); if !ok { http.NotFound(w,r); return }
	pkg, err := h.repo.Get(r.Context(), scope, id); if err != nil { http.NotFound(w,r); return }
	if !pkg.ExpiresAt.After(h.now()) || pkg.Status != "ready" { http.Error(w,"download expired",http.StatusGone); return }
	path,err:=canonicalExistingFile(pkg.Path);if err!=nil||!pathWithin(path,h.packageRoot){http.NotFound(w,r);return};f, err := os.Open(path); if err != nil { http.NotFound(w,r); return }; defer f.Close()
	w.Header().Set("ETag", `"`+pkg.SHA256+`"`); w.Header().Set("Content-Disposition", `attachment; filename="project.vpcdraft"`)
	http.ServeContent(w,r,"project.vpcdraft",pkg.ExpiresAt,f)
}
```

- [ ] **Step 4: Add a database-path-outside-`packageRoot` 404 test, bounded Range-count/rate middleware, and logs containing only package ID/request ID/bytes; run** `go test ./internal/httpapi -run TestPortable -count=1`; expect PASS and no response containing `package_path`.

- [ ] **Step 5: Commit.**

```powershell
git add internal/httpapi/portable_packages.go internal/httpapi/portable_packages_test.go
git commit -m "feat: stream portable packages with range support"
```

### Task 9: Upgrade the M1 importer to formal signature enforcement

**Files:** Modify `internal/draftimport/validate.go`, `internal/draftimport/import_test.go`, `cmd/draft-importer/main.go`; create `internal/draftimport/config.go`.

- [ ] **Step 1: Add failing importer tests** for formal success, unsigned formal rejection, changed file, invalid signature, unknown/retired key, importer version too old, unknown schema, and old-key rotation.

```go
func TestFormalPackageRequiresValidSignature(t *testing.T) {
	pkg, keys := signedPackageFixture(t); mutateZipEntry(t, pkg, "draft/draft_content.json")
	_, err := ValidateFormalPackage(pkg, DefaultLimits(), keys, "1.0.0")
	assertCode(t, err, "signature_invalid")
}
```

- [ ] **Step 2: Run** `go test ./internal/draftimport -run TestFormal -count=1`; expect `undefined: ValidateFormalPackage`.

- [ ] **Step 3: Load the release public keyring, check semver before extraction, recompute all hashes, reconstruct canonical payload, and verify `SIGNATURE.ed25519`.**

```go
type Config struct { ImporterVersion string; PublicKeys portablepackage.Keyring; MediaRoot string }
func ValidateFormalPackage(path string, limits Limits, keys portablepackage.Keyring, importerVersion string) (ValidatedPackage,error) {
	v, err := ValidatePackage(path, limits); if err != nil { return ValidatedPackage{}, err }
	if v.Manifest.Signature.Mode != "ed25519" { return ValidatedPackage{}, code("signature_required","formal package must be signed") }
	if versionLess(importerVersion, v.Manifest.ImporterMinVersion) { return ValidatedPackage{}, code("importer_too_old","update the importer") }
	payload, err := portablepackage.CanonicalPayload(v.Manifest); if err != nil { return ValidatedPackage{}, code("invalid_package","manifest is invalid") }
	if err = keys.Verify(v.Manifest.Signature.KeyID, payload, v.SignatureBytes); err != nil { return ValidatedPackage{}, code("signature_invalid","package signature is invalid") }
	return v, nil
}
```

- [ ] **Step 4: Make production CLI reject `prototype-unsigned` unless explicit `--allow-m1-prototype` is present; run** `go test ./internal/draftimport ./cmd/draft-importer -run 'TestFormal|TestPrototypeFlag' -count=1`; expect PASS.

- [ ] **Step 5: Commit.**

```powershell
git add internal/draftimport/validate.go internal/draftimport/config.go internal/draftimport/import_test.go cmd/draft-importer/main.go
git commit -m "feat: verify formal portable package signatures"
```

### Task 10: Release M2 and prove compatibility, rotation, expiry, and regression

**Files:** Modify `scripts/release.ps1`; create `scripts/m2-acceptance.ps1`, `docs/portable-package-format.md`, `docs/acceptance/m2-package-delivery.md`, `internal/portablepackage/release_test.go`.

- [ ] **Step 1: Add a failing release-layout test** requiring server/importer binaries, public keyring, compatibility matrix, checksums, and excluding private keys.

```go
func TestReleaseFixtureContainsPublicVerificationMaterialOnly(t *testing.T) {
	files := releaseFixtureNames(t)
	requireNames(t, files, "video-production-console.exe", "jianying-draft-importer.exe", "portable-public-keys.json", "portable-compatibility.json", "SHA256SUMS.txt")
	for _, name := range files { if strings.Contains(strings.ToLower(name), "private") { t.Fatalf("private material shipped: %s", name) } }
}
```

- [ ] **Step 2: Run** `go test ./internal/portablepackage -run TestReleaseFixture -count=1`; expect missing verification files.

- [ ] **Step 3: Emit public keyring and compatibility matrix in release staging.**

```powershell
$compatibility = @{ schema_version='1'; package_versions=@('1.0.0'); importer_min_version='1.0.0' } | ConvertTo-Json -Depth 4
Set-Content -LiteralPath (Join-Path $releaseDirectory 'portable-compatibility.json') -Value $compatibility -Encoding utf8NoBOM
Copy-Item -LiteralPath $PortablePublicKeysPath -Destination (Join-Path $releaseDirectory 'portable-public-keys.json')
if (Get-ChildItem -LiteralPath $releaseDirectory -Recurse | Where-Object Name -Match 'private|\.key$') { throw 'private signing material entered release staging' }
```

- [ ] **Step 4: Run complete verification:** `go test ./... -count=1`; `go build ./cmd/console ./cmd/draft-importer`; `pwsh -NoProfile -File scripts/release.ps1`; expect PASS.

- [ ] **Step 5: Execute M2 acceptance:** run M1 regression; formal signed cross-machine import; new key signs/new+old keyring verifies; expiry→renew→Range resume→purge; tamper rejection; source path scan; local `local_jianying` project still registers through the existing montage coordinator.

```powershell
$ErrorActionPreference='Stop'
go test ./internal/portablepackage -run 'Test(FormalBuilder|DeliveryGate|Keyring|Cleanup)' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'formal package acceptance failed' }
go test ./internal/store ./internal/httpapi -run 'Test(PortablePackage|PortableDownload|Renew)' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'download lifecycle acceptance failed' }
go test ./internal/draftimport -run 'Test(FormalPackage|Import|RegisterRollback)' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'formal importer acceptance failed' }
go test ./internal/montage -run 'TestCoordinator.*Local' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'local Jianying regression failed' }
Write-Output 'PASS m2-automated-acceptance'
```

- [ ] **Step 5a: On receiver computer B, run the formal importer with release keyring and resume the package download.**

```powershell
$download = Read-Host '请输入续传完成的正式vpcdraft路径'; $jianyingRoot = Read-Host '请输入剪映草稿根目录'; $mediaRoot = Read-Host '请输入长期素材目录'
.\jianying-draft-importer.exe --package $download --jianying-root $jianyingRoot --media-root $mediaRoot 2>&1 | Tee-Object .\m2-import.txt
if ($LASTEXITCODE -ne 0 -or (Get-Content .\m2-import.txt -Raw) -notmatch 'receipt=written') { throw 'formal cross-machine import failed' }
```

Expected: the package opens as an editable Jianying draft; its receipt hash equals `portable_packages.package_sha256`; the package key ID exists in the released public keyring.

- [ ] **Step 6: Record package hash, key ID, verification result, receipt, screenshot, lifecycle events, and regression result in `docs/acceptance/m2-package-delivery.md`; never record private keys or machine paths.**

- [ ] **Step 7: Commit.**

```powershell
git add scripts/release.ps1 scripts/m2-acceptance.ps1 docs/portable-package-format.md docs/acceptance/m2-package-delivery.md internal/portablepackage/release_test.go
git commit -m "release: verify formal portable package delivery"
```

## M2 exit gate

- [ ] Every packaged video is the union of actual uses plus available three-second handles; narration/BGM/SFX/images are referenced-only.
- [ ] Manifest/file ordering is deterministic; stable filenames use final byte SHA-256; signature verifies under active and retained public keys.
- [ ] Draft JSON and ZIP contain no server drive, UNC, workspace, or temp path.
- [ ] Download lifecycle proves 7-day expiry, renewal, Range resume, audit, mark-before-delete, and retained metadata.
- [ ] Formal importer rejects unsigned/tampered/too-new packages and remains independent of Python/FFmpeg.
- [ ] Existing `local_jianying` behavior passes regression unchanged.

## Self-review

- [ ] Map each M2 design requirement to one task and one automated test above.
- [ ] Confirm M1 names remain unchanged and every M3-facing public type matches the interface lock.
- [ ] Confirm every task contains a red test, concrete implementation, green command, and exact commit.
- [ ] Confirm M2 does not introduce partner login/pages, a 2032 server, or Cloudflare configuration.
