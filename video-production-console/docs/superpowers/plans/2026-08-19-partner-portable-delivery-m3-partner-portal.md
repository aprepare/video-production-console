# Partner Portable Delivery M3 Partner Portal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give approved partners an owner-isolated website on `127.0.0.1:2032` that accepts source copy, runs the complete portable workflow without administrator approval, and exposes friendly progress and signed-package downloads.

**Architecture:** M3 adds partner identity/session boundaries, owner-scoped stores, and a durable lease-based stage runner beside the current admin application. `internal/app.New` and port 2030 remain admin-only; a new `internal/app.NewPartnerApp` is the only handler served on 2032. Cloudflare JWT cryptography is an injected `partnerauth.Verifier`: tests inject a deterministic verifier, while production refuses to enable the partner listener until M4 supplies the real verifier.

**Tech Stack:** Go 1.25, SQLite, `net/http`, existing rewrite/narration/montage services, M2 `portablepackage.BuildService`, React 19, TypeScript 6, TanStack Query, Vitest, Playwright.

---

## File map and fixed boundaries

| Path | Action | Responsibility |
|---|---|---|
| `internal/domain/models.go` | Modify | Add project `DeliveryMode` without reusing conversation delivery mode |
| `internal/store/migrations.go` | Modify | Partner users, grants, owners, sessions, replay, workflow records |
| `internal/store/partners.go` | Create | Partner/session and owner-scoped project/task/package queries |
| `internal/store/partners_test.go` | Create | Migration, grant, IDOR and compatibility tests |
| `internal/partnerauth/types.go` | Create | Assertion, verifier, session, owner scope, errors |
| `internal/partnerauth/service.go` | Create | Assertion-to-session exchange, cookie authentication, revocation |
| `internal/partnerauth/service_test.go` | Create | Audience/email/expiry/replay/session-version tests with injected verifier |
| `internal/partnerworkflow/types.go` | Create | Nine stages, run record, executor/result/error contracts |
| `internal/partnerworkflow/runner.go` | Create | Lease claim, idempotency, retry, stage advance |
| `internal/partnerworkflow/runner_test.go` | Create | Crash/retry/concurrency and friendly-status tests |
| `internal/partnerworkflow/executors.go` | Create | Existing pipeline/M2 adapters; portable never registers on host Jianying |
| `internal/partnerworkflow/executors_test.go` | Create | Stage call order, artifact hashes, local/portable split |
| `internal/httpapi/partner.go` | Create | Partner DTOs and owner-scoped APIs |
| `internal/httpapi/partner_test.go` | Create | Create/list/progress/download/renew/CSRF/404 tests |
| `internal/app/partner.go` | Create | Partner-only mux and middleware |
| `internal/app/partner_test.go` | Create | 2030/2032 route isolation tests |
| `internal/config/config.go` | Modify | Loopback-only partner listener setting |
| `cmd/console/main.go` | Modify | Optional 2032 server and partner worker lifecycle |
| `web/src/partner/*` | Create | Four partner pages, typed API and styles |
| `web/src/main.tsx` | Modify | Select partner entry for `/partner/*` |
| `web/e2e/partner-portal.spec.ts` | Create | Browser workflow test |
| `internal/integration/partner_delivery_test.go` | Create | Source-to-ready and retry integration |

M3 consumes the M2 names `portablepackage.BuildService`, `FormalBuildRequest`, `FormalBuildResult`, `DeliveryGate`, `store.PackageOwnerScope`, and `httpapi.NewPortablePackageHandler`. Partner-facing code never receives `package_path`.

### Task 1: Add compatible partner and workflow migrations

**Files:** Modify `internal/domain/models.go`, `internal/store/migrations.go`; create `internal/store/partners_test.go`.

- [ ] **Step 1: Add a failing upgrade test** that creates an old project, migrates, and checks `local_jianying`; also inspect every new table/index.

```go
func TestPartnerMigrationPreservesExistingProjects(t *testing.T) {
	db := openDatabaseAtMigration(t, latestMigration()-1); insertLegacyProject(t, db, "old-project")
	applyAllMigrations(t, db)
	var mode string; if err := db.QueryRow(`SELECT delivery_mode FROM projects WHERE id='old-project'`).Scan(&mode); err != nil { t.Fatal(err) }
	if mode != "local_jianying" { t.Fatalf("mode=%q", mode) }
	assertTables(t, db, "partner_users", "partner_account_grants", "partner_project_owners", "partner_sessions", "partner_access_replays", "partner_workflow_runs")
}
```

- [ ] **Step 2: Run** `go test ./internal/store -run TestPartnerMigration -count=1`; expect `no such column: delivery_mode`.

- [ ] **Step 3: Add a project-specific delivery mode type** in `internal/domain/models.go` and append the migration.

```go
type ProjectDeliveryMode string
const (
	ProjectDeliveryLocalJianying ProjectDeliveryMode = "local_jianying"
	ProjectDeliveryPortable ProjectDeliveryMode = "portable"
)
```

```sql
ALTER TABLE projects ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT 'local_jianying'
  CHECK(delivery_mode IN ('local_jianying','portable'));
CREATE TABLE partner_users (id TEXT PRIMARY KEY,email TEXT NOT NULL COLLATE NOCASE UNIQUE,status TEXT NOT NULL CHECK(status IN ('active','disabled')),access_subject TEXT,session_version INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL,last_login_at DATETIME);
CREATE TABLE partner_account_grants (partner_user_id TEXT NOT NULL REFERENCES partner_users(id),account_id TEXT NOT NULL REFERENCES accounts(id),scope TEXT NOT NULL DEFAULT 'produce',active INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,PRIMARY KEY(partner_user_id,account_id));
CREATE TABLE partner_project_owners (project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,partner_user_id TEXT NOT NULL REFERENCES partner_users(id),account_id TEXT NOT NULL REFERENCES accounts(id),created_at DATETIME NOT NULL,PRIMARY KEY(project_id,partner_user_id));
CREATE TABLE partner_sessions (id TEXT PRIMARY KEY,partner_user_id TEXT NOT NULL REFERENCES partner_users(id) ON DELETE CASCADE,token_hash TEXT NOT NULL UNIQUE,csrf_hash TEXT NOT NULL,session_version INTEGER NOT NULL,expires_at DATETIME NOT NULL,created_at DATETIME NOT NULL,last_seen_at DATETIME NOT NULL);
CREATE TABLE partner_access_replays (jti_hash TEXT PRIMARY KEY,expires_at DATETIME NOT NULL,created_at DATETIME NOT NULL);
CREATE TABLE partner_workflow_runs (project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,stage TEXT NOT NULL,attempt INTEGER NOT NULL DEFAULT 0,idempotency_key TEXT NOT NULL DEFAULT '',output_hash TEXT NOT NULL DEFAULT '',lease_owner TEXT,lease_until DATETIME,next_retry_at DATETIME,started_at DATETIME,finished_at DATETIME,last_error_code TEXT,last_error_message TEXT,version INTEGER NOT NULL DEFAULT 1,updated_at DATETIME NOT NULL);
CREATE INDEX partner_project_owners_user_idx ON partner_project_owners(partner_user_id,created_at);
CREATE INDEX partner_workflow_claim_idx ON partner_workflow_runs(stage,next_retry_at,lease_until);
```

One partner submission always creates one new `projects.id`, so `partner_workflow_runs.project_id PRIMARY KEY` means exactly one durable run per submitted project. Retrying reuses that run; submitting revised source copy creates a new project instead of overwriting audit history. Add this assertion to the migration/repository tests.

- [ ] **Step 4: Add `delivery_mode` to `projectColumns`, every scan, create input, and SQL insert in `internal/store/projects.go`; existing callers pass `ProjectDeliveryLocalJianying`. Add a migration test that inserting `A@Example.com` and `a@example.com` cannot create two partner users.**

- [ ] **Step 5: Run** `go test ./internal/store -run 'TestPartnerMigration|TestProject' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/domain/models.go internal/store/migrations.go internal/store/projects.go internal/store/partners_test.go
git commit -m "feat: add partner delivery schema"
```

### Task 2: Enforce owner scope in the repository

**Files:** Create `internal/store/partners.go`; modify `internal/store/partners_test.go`.

- [ ] **Step 1: Write a failing two-owner IDOR test** for project, task, asset, and M2 package lookups.

```go
func TestPartnerQueriesHideOtherOwners(t *testing.T) {
	r := partnerFixture(t); projectID := r.CreateOwnedProject(t, "user-a", "account-a")
	for _, check := range []func() error{
		func() error { _, err := r.Repo.GetProject(context.Background(), PartnerOwnerScope{UserID:"user-b",AccountID:"account-b"}, projectID); return err },
		func() error { _, err := r.Repo.GetLatestTask(context.Background(), PartnerOwnerScope{UserID:"user-b",AccountID:"account-b"}, projectID); return err },
		func() error { _, err := r.Repo.ListPackages(context.Background(), PartnerOwnerScope{UserID:"user-b",AccountID:"account-b"}, projectID); return err },
	} { if err := check(); !errors.Is(err, ErrPartnerResourceNotFound) { t.Fatalf("err=%v", err) } }
}
```

- [ ] **Step 2: Run** `go test ./internal/store -run TestPartnerQueriesHideOtherOwners -count=1`; expect missing `PartnerOwnerScope`.

- [ ] **Step 3: Define repository inputs and perform ownership joins in every query.**

```go
type PartnerOwnerScope struct { UserID,AccountID string }
type CreatePartnerProjectInput struct { AccountID, Title, SourceScript string }
func (r *PartnerRepository) GetProject(ctx context.Context, scope PartnerOwnerScope, id string) (domain.Project,error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects p JOIN partner_project_owners o ON o.project_id=p.id AND o.partner_user_id=? AND o.account_id=? JOIN partner_account_grants g ON g.partner_user_id=o.partner_user_id AND g.account_id=o.account_id AND g.active=1 WHERE p.id=? AND p.delivery_mode='portable'`, scope.UserID, scope.AccountID, id)
	p, err := scanProject(row); if errors.Is(err,sql.ErrNoRows) { return domain.Project{}, ErrPartnerResourceNotFound }; return p, err
}
```

- [ ] **Step 4: Implement `CreateProject` as one transaction:** verify active grant, insert portable project/source asset/owner/workflow row; rollback all on any error.

```go
func (r *PartnerRepository) CreateProject(ctx context.Context, scope PartnerOwnerScope, in CreatePartnerProjectInput) (domain.Project,error) {
	tx, err := r.db.BeginTx(ctx,nil); if err != nil { return domain.Project{},err }; defer tx.Rollback()
	if err = requireActiveGrant(ctx,tx,scope.UserID,in.AccountID); err != nil { return domain.Project{},ErrPartnerResourceNotFound }
	p, err := insertPortableProject(ctx,tx,in); if err != nil { return domain.Project{},err }
	if err = insertSourceAndOwnerAndRun(ctx,tx,p.ID,scope.UserID,in); err != nil { return domain.Project{},err }
	if err = tx.Commit(); err != nil { return domain.Project{},err }; return p,nil
}
```

- [ ] **Step 5: Add list/package/download-event scope tests plus a revoked-grant test that turns `active=0` and immediately makes project/progress/package/download return not found; run** `go test ./internal/store -run 'TestPartnerQueries|TestCreatePartnerProject|TestRevokedGrant' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/store/partners.go internal/store/partners_test.go
git commit -m "feat: enforce partner resource ownership"
```

### Task 3: Exchange verified Access assertions for short application sessions

**Files:** Create `internal/partnerauth/types.go`, `internal/partnerauth/service.go`, `internal/partnerauth/service_test.go`.

- [ ] **Step 1: Add failing tests** for audience/email/subject/expiry, duplicate JTI, disabled user, secure cookie, 15-minute expiry, and `session_version` revocation. The verifier is an injected Go fake, never an HTTP header bypass.

```go
func TestAuthenticateRejectsReplay(t *testing.T) {
	fx := authFixture(t, Assertion{Email:"A@Example.com",Subject:"sub-1",Audience:"partner-aud",JTI:"jti-1",ExpiresAt:time.Now().Add(time.Minute)})
	if _, err := fx.Service.Authenticate(context.Background(), "signed-token"); err != nil { t.Fatal(err) }
	if _, err := fx.Service.Authenticate(context.Background(), "signed-token"); !errors.Is(err,ErrAssertionReplay) { t.Fatalf("err=%v",err) }
}
```

- [ ] **Step 2: Run** `go test ./internal/partnerauth -count=1`; expect package/type missing.

- [ ] **Step 3: Define the production boundary and session shape.**

```go
type Assertion struct { Email, Subject, Audience, JTI string; ExpiresAt time.Time }
type Verifier interface { Verify(context.Context,string) (Assertion,error) }
type OwnerScope struct { UserID string }
type Session struct { ID, UserID, Token, CSRFToken string; Version int64; ExpiresAt time.Time }
type Store interface {
	ConsumeAssertion(context.Context,Assertion,string) (string,int64,error)
	CreateSession(context.Context,string,int64,string,string,time.Time) error
	GetSessionByHash(context.Context,string,time.Time) (Session,error)
	RevokeSessions(context.Context,string) error
}
type Service struct { verifier Verifier; store Store; audience string; now func() time.Time; random io.Reader }
func NewService(v Verifier, s Store, audience string) (*Service,error) { if v==nil { return nil,ErrVerifierRequired }; return &Service{verifier:v,store:s,audience:audience,now:time.Now,random:rand.Reader},nil }
```

- [ ] **Step 4: Implement `Authenticate`, `RequireSession`, `Revoke`, token hashing, normalized lowercase email, and cookie helpers.**

```go
func SessionCookie(s Session) *http.Cookie {
	return &http.Cookie{Name:"partner_session",Value:s.Token,Path:"/",Expires:s.ExpiresAt,MaxAge:int(time.Until(s.ExpiresAt).Seconds()),Secure:true,HttpOnly:true,SameSite:http.SameSiteLaxMode}
}
func (s *Service) RequireSession(ctx context.Context, raw string) (OwnerScope,error) {
	session, err := s.store.GetSessionByHash(ctx,sha256Hex(raw),s.now()); if err != nil { return OwnerScope{},ErrUnauthenticated }
	return OwnerScope{UserID:session.UserID},nil
}
```

- [ ] **Step 5: Run** `go test ./internal/partnerauth -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/partnerauth
git commit -m "feat: add partner assertion sessions"
```

### Task 4: Implement the durable nine-stage runner

**Files:** Create `internal/partnerworkflow/types.go`, `internal/partnerworkflow/runner.go`, `internal/partnerworkflow/runner_test.go`.

- [ ] **Step 1: Add failing tests** for exact order, output-hash reuse, lease contention/reclaim, retry backoff, permanent failure, restart at failed stage, and optimistic-version conflict.

```go
func TestRetryStartsAtFailedStage(t *testing.T) {
	fx := runnerFixture(t); fx.Executor.FailOnce(StagePackaging, Retryable("disk_busy","temporary storage failure"))
	fx.RunUntilBlocked(t); fx.Clock.Advance(time.Minute); fx.RunUntilReady(t)
	if fx.Executor.Calls(StageRewriting) != 1 || fx.Executor.Calls(StagePackaging) != 2 || fx.Executor.Calls(StageSigning) != 1 { t.Fatal(fx.Executor.AllCalls()) }
}
```

- [ ] **Step 2: Run** `go test ./internal/partnerworkflow -count=1`; expect package missing.

- [ ] **Step 3: Define all stage and runner contracts.**

```go
type Stage string
const (
	StageQueued Stage="queued"; StageRewriting Stage="rewriting"; StageNarrating Stage="narrating"; StageMontaging Stage="montaging"; StageValidating Stage="validating"; StageClippingMedia Stage="clipping_media"; StagePackaging Stage="packaging"; StageSigning Stage="signing"; StageReady Stage="ready"
)
var Order=[]Stage{StageQueued,StageRewriting,StageNarrating,StageMontaging,StageValidating,StageClippingMedia,StagePackaging,StageSigning,StageReady}
type StageRun struct { ProjectID string; Stage Stage; Attempt int; IdempotencyKey string }
type StageOutput struct { ArtifactID, SHA256 string }
type StageError struct { Code, SafeMessage string; Retryable bool }
func (e *StageError) Error() string { return e.Code+": "+e.SafeMessage }
type Progress struct { ProjectID string; Stage Stage; FriendlyStatus, FriendlyMessage string; Attempt int; DownloadReady bool }
type StageExecutor interface { Execute(context.Context,StageRun) (StageOutput,error) }
type Store interface { Claim(context.Context,string,string,time.Time,time.Duration) (StageRun,error); Complete(context.Context,StageRun,StageOutput) error; Fail(context.Context,StageRun,*StageError,time.Time) error }
```

- [ ] **Step 4: Implement one-stage `RunOnce`; claim uses `version` and expired lease, completion transaction stores artifact/hash before stage advance, retry uses capped exponential backoff.**

```go
func (r *Runner) RunOnce(ctx context.Context, projectID string) error {
	run, err := r.store.Claim(ctx,projectID,r.workerID,r.clock.Now(),r.lease); if err != nil { return err }
	out, err := r.executor.Execute(ctx,run); if err == nil { return r.store.Complete(ctx,run,out) }
	stageErr := NormalizeError(err); return r.store.Fail(ctx,run,stageErr,r.clock.Now().Add(backoff(run.Attempt,stageErr.Retryable)))
}
```

- [ ] **Step 5: Map internal states only to `排队中`, `处理中`, `准备下载`, `需要重试`, `下载已过期`; run** `go test ./internal/partnerworkflow -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/partnerworkflow/types.go internal/partnerworkflow/runner.go internal/partnerworkflow/runner_test.go
git commit -m "feat: persist partner workflow stages"
```

### Task 5: Adapt the existing pipeline without host Jianying registration

**Files:** Create `internal/partnerworkflow/executors.go`, `internal/partnerworkflow/executors_test.go`; modify `internal/montage/coordinator.go`, `internal/montage/coordinator_test.go` at `Coordinator.HandleCompleted`.

- [ ] **Step 1: Add a failing portable/local split test.**

```go
func TestPortableMontageNeverCallsRegistrar(t *testing.T) {
	fx := executorFixture(t, domain.ProjectDeliveryPortable)
	for _, stage := range Order[1:] { _, _ = fx.Executor.Execute(context.Background(), StageRun{ProjectID:"p1",Stage:stage}) }
	if fx.Registrar.Calls != 0 { t.Fatalf("registrar calls=%d",fx.Registrar.Calls) }
	if fx.PackageService.SignCalls != 1 { t.Fatalf("sign calls=%d",fx.PackageService.SignCalls) }
}
```

- [ ] **Step 2: Run** `go test ./internal/partnerworkflow ./internal/montage -run 'TestPortable|TestLocalJianying' -count=1`; expect missing executor adapter and portable delivery branch.

- [ ] **Step 3: Define explicit service ports; adapters call current rewrite/narration/montage generation, but portable montage stops after validated plaintext workspace.**

```go
type ContentPipeline interface { Rewrite(context.Context,string) (StageOutput,error); Narrate(context.Context,string) (StageOutput,error); MontagePlaintext(context.Context,string) (StageOutput,error); ValidatePlaintext(context.Context,string) (StageOutput,error) }
type PortablePipeline interface { Prepare(context.Context,portablepackage.FormalBuildRequest) (portablepackage.PreparedBuild,error); Package(context.Context,portablepackage.PreparedBuild) (portablepackage.UnsignedBuild,error); Sign(context.Context,portablepackage.UnsignedBuild) (portablepackage.FormalBuildResult,error) }
type BuildInputResolver interface { ResolveFormalBuild(context.Context,string)(portablepackage.FormalBuildRequest,error) }
type Executors struct { Projects ProjectReader; Content ContentPipeline; Inputs BuildInputResolver; Portable PortablePipeline; Packages PackageRecorder }
```

`ResolveFormalBuild` reads the current owner-scoped `source_script`, `narration`, and `mix_draft/plaintext_workspace` artifacts, derives the package output under the server data root, and returns the M2 `FormalBuildRequest`. Missing/stale artifacts stop at the current stage with a stable error; they are never guessed from filesystem paths.

- [ ] **Step 4: Implement exact stage dispatch and require output SHA-256 at every successful stage.**

```go
func (e *Executors) Execute(ctx context.Context, run StageRun) (StageOutput,error) {
	switch run.Stage {
	case StageRewriting: return e.Content.Rewrite(ctx,run.ProjectID)
	case StageNarrating: return e.Content.Narrate(ctx,run.ProjectID)
	case StageMontaging: return e.Content.MontagePlaintext(ctx,run.ProjectID)
	case StageValidating: return e.Content.ValidatePlaintext(ctx,run.ProjectID)
	case StageClippingMedia: return e.prepare(ctx,run)
	case StagePackaging: return e.packageBuild(ctx,run)
	case StageSigning: return e.signAndRecord(ctx,run)
	default: return StageOutput{},fmt.Errorf("unsupported stage %s",run.Stage)
	}
}
```

- [ ] **Step 5: In `montage.Coordinator.HandleCompleted`, load `input.Task.ProjectID`; return `(false,nil)` for `ProjectDeliveryPortable` so normal task completion retains the plaintext artifact without starting registration, and retain the current `CompleteAndBegin` path for `ProjectDeliveryLocalJianying`. Run** `go test ./internal/partnerworkflow ./internal/montage -run 'TestPortable|TestLocalJianying' -count=1`; expect PASS.

```go
project, err := store.NewProjectRepository(c.tasks.DB()).GetProject(ctx,input.Task.ProjectID)
if err != nil { return true,err }
if project.DeliveryMode == domain.ProjectDeliveryPortable { return false,nil }
```

- [ ] **Step 6: Commit.**

```powershell
git add internal/partnerworkflow/executors.go internal/partnerworkflow/executors_test.go internal/montage/coordinator.go internal/montage/coordinator_test.go
git commit -m "feat: route portable projects around local registration"
```

### Task 6: Expose owner-scoped partner APIs

**Files:** Create `internal/httpapi/partner.go`, `internal/httpapi/partner_test.go`.

- [ ] **Step 1: Add failing HTTP tests** for login exchange, create/list/progress/download/renew, CSRF, request size, owner B→A 404, expired 410, and response leak scan.

```go
func TestPartnerProjectIDORReturns404(t *testing.T) {
	h := partnerHandlerFixture(t); req := ownerRequest(t,"user-b",http.MethodGet,"/api/partner/projects/owner-a-project/progress",nil)
	rr := httptest.NewRecorder(); h.ServeHTTP(rr,req)
	if rr.Code != http.StatusNotFound { t.Fatalf("code=%d body=%s",rr.Code,rr.Body.String()) }
	if strings.Contains(rr.Body.String(),"owner-a") { t.Fatal("resource existence leaked") }
}
```

- [ ] **Step 2: Run** `go test ./internal/httpapi -run TestPartner -count=1`; expect `undefined: NewPartnerHandler`.

- [ ] **Step 3: Define friendly DTOs and handler dependencies.**

```go
type PartnerProjectDTO struct { ID, Title, Status, CreatedAt string }
type PartnerProgressDTO struct { ProjectID, Status, Message string; Attempt int; DownloadReady bool }
type PartnerPackageDTO struct { ID, ProjectID, Status, ExpiresAt, DownloadURL string; SizeBytes int64 }
type PartnerStore interface {
	CreateProject(context.Context,store.PartnerOwnerScope,store.CreatePartnerProjectInput)(domain.Project,error)
	ListProjects(context.Context,string)([]domain.Project,error)
	ResolveProjectScope(context.Context,string,string)(store.PartnerOwnerScope,error)
	ResolvePackageScope(context.Context,string,string)(store.PartnerOwnerScope,error)
	GetProgress(context.Context,store.PartnerOwnerScope,string)(partnerworkflow.Progress,error)
	ListPackages(context.Context,store.PartnerOwnerScope,string)([]store.PortablePackage,error)
}
func NewPartnerHandler(auth *partnerauth.Service, repo PartnerStore, packages http.Handler) http.Handler { return &partnerHandler{auth:auth,repo:repo,packages:packages} }
```

- [ ] **Step 4: Mount only** `POST /api/partner/session`, `DELETE /api/partner/session`, `POST/GET /api/partner/projects`, `GET /api/partner/projects/{id}/progress`, `GET /api/partner/packages`, `GET /api/partner/packages/{id}/download`, and `POST /api/partner/packages/{id}/renew`. Before progress/package/renew/download, call `ResolveProjectScope` or `ResolvePackageScope`; those methods join active grant and owner rows. Adapt the resolved scope to M2 at the package-handler boundary.

```go
func packageScope(owner store.PartnerOwnerScope) store.PackageOwnerScope { return store.PackageOwnerScope{OwnerID:owner.UserID,AccountID:owner.AccountID} }
```

- [ ] **Step 5: Run** `go test ./internal/httpapi -run TestPartner -count=1`; expect PASS and response JSON free of `path`, `error_stack`, `worker_id`, and tokens.

- [ ] **Step 6: Commit.**

```powershell
git add internal/httpapi/partner.go internal/httpapi/partner_test.go
git commit -m "feat: expose partner production APIs"
```

### Task 7: Run an isolated loopback 2032 application

**Files:** Create `internal/app/partner.go`, `internal/app/partner_test.go`; modify `internal/config/config.go`, `internal/config/config_test.go`, `cmd/console/main.go`, `cmd/console/main_test.go`.

- [ ] **Step 1: Add failing isolation tests:** partner routes absent on admin handler; admin routes absent on partner handler; default is `127.0.0.1:2032`; non-loopback addresses rejected; nil verifier prevents partner startup.

```go
func TestAdminAndPartnerRoutesAreDisjoint(t *testing.T) {
	admin := New(adminFixtureOptions(t)); partner := NewPartnerApp(partnerFixtureOptions(t))
	assertStatus(t,admin.Handler(),"/api/partner/projects",http.StatusNotFound)
	assertStatus(t,partner.Handler(),"/api/settings",http.StatusNotFound)
}
```

- [ ] **Step 2: Run** `go test ./internal/app ./internal/config ./cmd/console -run 'TestPartner|TestRouteIsolation' -count=1`; expect missing `NewPartnerApp` and config field.

- [ ] **Step 3: Add config validation and a separate mux constructor; do not modify `internal/app.New` routing.**

```go
type Config struct { ListenAddr, PartnerListenAddr, DataRoot, DatabasePath, BaokuanBaseURL, CodexBinaryPath, ObsidianVault, MachineProfilePath string }
func ValidatePartnerListen(addr string) error { host,_,err:=net.SplitHostPort(addr); if err!=nil{return err}; if host!="127.0.0.1"&&host!="::1"&&host!="localhost"{return errors.New("partner listener must be loopback")}; return nil }
type PartnerOptions struct { Handler http.Handler; Static http.Handler }
func NewPartnerApp(options PartnerOptions) *App {
	static:=options.Static;if static==nil{static=webui.Handler()};mux:=http.NewServeMux();mux.Handle("/api/partner/",options.Handler);mux.Handle("/partner/",static);return &App{handler:logging.RequestID(mux)}
}
```

- [ ] **Step 4: In `cmd/console/main.go`, create a second `http.Server` and worker only when partner runtime config is enabled and verifier construction succeeds; share DB/services, never the admin mux; shut both down from the existing signal context.**

```go
partnerServer := &http.Server{Addr:settings.PartnerListenAddr,Handler:partnerApp.Handler(),ReadHeaderTimeout:10*time.Second}
go func(){ if err:=partnerServer.ListenAndServe(); err!=nil&&!errors.Is(err,http.ErrServerClosed){ partnerErr<-err } }()
defer partnerServer.Shutdown(context.Background())
```

- [ ] **Step 5: Run** `go test ./internal/app ./internal/config ./cmd/console -run 'TestPartner|TestRouteIsolation' -count=1`; expect PASS.

- [ ] **Step 6: Commit.**

```powershell
git add internal/app/partner.go internal/app/partner_test.go internal/config/config.go internal/config/config_test.go cmd/console/main.go cmd/console/main_test.go
git commit -m "feat: isolate partner service on loopback 2032"
```

### Task 8: Build the four partner pages

**Files:** Create `web/src/partner/types.ts`, `api.ts`, `PartnerApp.tsx`, `PartnerApp.test.tsx`, `partner.css`, `web/e2e/partner-portal.spec.ts`; modify `web/src/main.tsx`.

- [ ] **Step 1: Add a failing component test** for the four labels, source submission, progress polling, ready download, renewal, and friendly error.

```tsx
it("creates a project and shows progress", async () => {
  mockPartnerAPI({create:{id:"p1"},progress:{project_id:"p1",status:"处理中",message:"正在生成",attempt:1,download_ready:false}})
  render(<PartnerApp />)
  await userEvent.click(screen.getByRole("link",{name:"新建项目"}))
  await userEvent.type(screen.getByLabelText("原稿"),"测试原稿")
  await userEvent.click(screen.getByRole("button",{name:"开始制作"}))
  expect(await screen.findByText("处理中")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run** `cd web; npm test -- src/partner/PartnerApp.test.tsx`; expect module missing.

- [ ] **Step 3: Define typed API responses and a single safe request helper.**

```ts
export type PartnerProgress={project_id:string;status:"排队中"|"处理中"|"准备下载"|"需要重试"|"下载已过期";message:string;attempt:number;download_ready:boolean}
export type PartnerPackage={id:string;project_id:string;status:string;expires_at:string;size_bytes:number;download_url:string}
async function request<T>(url:string,init:RequestInit={}):Promise<T>{const response=await fetch(url,{...init,credentials:"same-origin",headers:{"Content-Type":"application/json",...init.headers}});if(!response.ok)throw new Error(response.status===404?"项目不存在":"请求失败，请重试");return response.json() as Promise<T>}
```

- [ ] **Step 4: Implement `/partner/new`, `/partner/projects`, `/partner/projects/:id`, `/partner/downloads`; poll active progress every two seconds and stop at ready/error; render no internal stage/error/path.**

```tsx
export function PartnerApp(){const path=window.location.pathname;if(path==="/partner/new")return <NewProjectPage/>;if(path==="/partner/downloads")return <DownloadCenter/>;if(/^\/partner\/projects\/[^/]+$/.test(path))return <ProgressPage projectId={path.split("/").at(-1)!}/>;return <ProjectListPage/>}
```

- [ ] **Step 5: In `web/src/main.tsx`, render `PartnerApp` only for `/partner`; retain the existing admin root for all other paths.**

- [ ] **Step 6: Run** `cd web; npm test -- src/partner/PartnerApp.test.tsx; npm run typecheck; npm run build:verify`; expect PASS.

- [ ] **Step 7: Commit.**

```powershell
git add web/src/partner web/src/main.tsx web/e2e/partner-portal.spec.ts
git commit -m "feat: add partner production portal"
```

### Task 9: Verify source-to-ready, restart retry, and local regression

**Files:** Create `internal/integration/partner_delivery_test.go`, `docs/operations/partner-portal-m3.md`; modify `web/e2e/partner-portal.spec.ts`.

- [ ] **Step 1: Add a failing integration test** with temporary SQLite, injected verifier, fake existing pipeline, real M2 signing/package services, and 2032 `httptest.Server`.

```go
func TestPartnerSourceToReadyRetriesOnlyFailedStage(t *testing.T) {
	fx:=newPartnerIntegration(t); project:=fx.CreateProject(t,"source copy"); fx.FailOnce(partnerworkflow.StagePackaging)
	fx.RunUntilBlocked(t,project.ID); fx.RestartWorker(); fx.AdvanceRetry(); fx.RunUntilReady(t,project.ID)
	if fx.Calls(partnerworkflow.StageRewriting)!=1||fx.Calls(partnerworkflow.StagePackaging)!=2{t.Fatal(fx.CallLog())}
	if fx.RegistrarCalls()!=0{t.Fatal("portable project registered on host Jianying")}
}
```

- [ ] **Step 2: Run** `go test ./internal/integration -run TestPartnerSourceToReady -count=1`; expect compile failure `undefined: newPartnerIntegration` before the integration fixture and production wiring are added.

- [ ] **Step 3: Build the integration fixture with the actual partner mux and owner session; assert final M2 signature, package row, friendly progress, owner B 404, Range download, and audit event.**

```go
server:=httptest.NewServer(app.NewPartnerApp(app.PartnerOptions{Handler:handler}).Handler());defer server.Close()
client:=authenticatedPartnerClient(t,server.URL,fixtureVerifier);createProjectAndWaitReady(t,client)
```

- [ ] **Step 4: Add a local regression** creating an existing admin project and asserting its default `local_jianying` path still calls the montage coordinator exactly once.

- [ ] **Step 5: Run** `go test ./... -count=1`; then `cd web; npm test; npm run typecheck; npm run build:verify; npm run test:e2e -- partner-portal.spec.ts`; expect PASS.

- [ ] **Step 6: Document M3 operator facts:** 2030 admin only, 2032 partner only, no administrator approval, friendly states, session revocation command, stage retry inspection, and M4 production-verifier dependency.

```markdown
# Partner portal M3 operations
- Admin: `127.0.0.1:2030`; partner: `127.0.0.1:2032`; neither listener may bind a non-loopback address.
- Partner jobs have no approval gate. Stable public states are 排队中、处理中、准备下载、需要重试、下载已过期.
- Revoke access by incrementing `partner_users.session_version`; every prior `partner_session` is rejected on its next request.
- Inspect retries from `partner_workflow_runs` using project ID, stage, attempt, next_retry_at, and stable error code; never expose last_error_message to the partner.
- Production 2032 remains disabled until M4 supplies a real Cloudflare Access verifier. Test fixture verifiers are not runtime configuration.
```

- [ ] **Step 6a: Verify the document contains all five statements** with `rg -n '127\.0\.0\.1:2030|127\.0\.0\.1:2032|session_version|partner_workflow_runs|Cloudflare Access verifier' docs/operations/partner-portal-m3.md`; expect five matched topics.

- [ ] **Step 7: Commit.**

```powershell
git add internal/integration/partner_delivery_test.go docs/operations/partner-portal-m3.md web/e2e/partner-portal.spec.ts
git commit -m "test: verify partner delivery from source to ready"
```

## M3 exit gate

- [ ] Owner A cannot infer or access owner B project, task, asset, package, progress, renewal, or download.
- [ ] All nine stages survive restart; a transient failure reruns only its failed stage.
- [ ] Portable jobs never register on host Jianying; existing local jobs still do.
- [ ] 2030 and 2032 route sets are disjoint and both listeners are loopback-only.
- [ ] Four partner pages show only friendly status and download metadata; no administrator approval exists.
- [ ] Production partner startup still requires the M4 verifier and cannot fall back to a trusted header.

## Self-review

- [ ] Confirm all migrations, type names, method signatures, API fields, and stage names match later tasks.
- [ ] Confirm every repository lookup starts from authenticated owner scope and converts misses to 404.
- [ ] Confirm commands match the scripts present in `web/package.json`.
- [ ] Confirm M3 does not implement Cloudflare JWKS/Tunnel or application OTP.
