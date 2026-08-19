# M4 Partner Portable Delivery Deployment and Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 M1–M3 已交付的 portable 包、导入器和 partner 服务之上，完成 Cloudflare Tunnel/Access、真实 Access JWT/JWKS 验证、会话撤销、Windows 部署、密钥轮换、备份回滚、监控清理、演练和正式发布验收。

**Architecture:** 2030 管理服务继续只监听 `127.0.0.1:2030`，不出现在 Tunnel ingress；partner 服务只监听 `127.0.0.1:2032`，Tunnel 仅把 partner hostname 转发到该地址。Cloudflare Access 负责邮箱 OTP，`internal/partnerauth` 只验证 Access JWT 的签名、`aud`、`email`、`sub`、`exp`、`jti`，再签发绑定 `session_version` 的 15 分钟应用会话。部署、备份、清理和发布脚本均可 dry-run，状态输出不包含 token、密钥、邮箱明文或绝对路径。

**Tech Stack:** Go 标准库 `crypto/rsa`/`crypto/sha256`/`net/http`、SQLite migrations、Cloudflared YAML、PowerShell 7、Windows Service Control Manager、Ed25519 keyring、Prometheus-style metrics、JSON/Markdown release evidence。

---

## 文件结构与责任

- Create: `internal/partnerauth/cloudflare.go`, `internal/partnerauth/cloudflare_test.go` — JWKS cache、JWT 验证、claims policy。
- Modify: `internal/partnerauth/service.go`, `internal/partnerauth/service_test.go` —真实 verifier、短期 session、`session_version` 撤销。
- Create: `internal/config/partner_security.go`, `internal/config/partner_security_test.go` — issuer/audience/email allowlist、TTL、listen safety、keyring 配置。
- Create: `deploy/cloudflared/partner-tunnel.yml`, `deploy/cloudflared/validate-tunnel.ps1`, `deploy/cloudflared/generate-tunnel.ps1` — Tunnel ingress 生成与静态校验。
- Create: `deploy/windows/install-partner-service.ps1`, `deploy/windows/uninstall-partner-service.ps1`, `deploy/windows/start-partner-service.ps1`, `deploy/windows/protect-signing-key.ps1`, `deploy/windows/partner-service.xml`, `deploy/windows/install-partner-service.Tests.ps1` — Windows 服务部署、测试和启动参数。
- Modify: `cmd/console/main.go`, `internal/app/partner.go` —生产配置强制注入 verifier、2032 loopback 与优雅停止。
- Create: `internal/ops/metrics.go`, `internal/ops/metrics_test.go`, `internal/ops/maintenance.go`, `internal/ops/maintenance_test.go`, `internal/ops/backup.go`, `internal/ops/backup_test.go`, `internal/ops/rollback.go`, `internal/ops/rollback_test.go` —脱敏指标、调用 M2 清理器、数据库备份、迁移回滚。
- Create: `internal/portablepackage/keystore.go`, `internal/portablepackage/keystore_test.go`；Modify: `internal/portablepackage/sign.go` —签名私钥保护、轮换、公钥发布和 Importer 兼容矩阵，保留 M2 `Keyring` 作为纯公钥验证器。
- Modify: `scripts/release.ps1`; Create: `scripts/m4-release.ps1`, `scripts/m4-drill.ps1`, `scripts/m4-access-check.ps1`, `scripts/m4-key-rotate.ps1`, `docs/operations/partner-m4.md`, `docs/operations/cloudflare-access-checklist.md`, `docs/releases/partner-compatibility.json`, `docs/releases/partner-m4-checklist.md` —发布、演练、人工 checklist 与证据。

### Task 1: 定义生产安全配置与真实 Access verifier 接口

**Files:**
- Create: `internal/config/partner_security.go`, `internal/config/partner_security_test.go`
- Create: `internal/partnerauth/cloudflare.go`, `internal/partnerauth/cloudflare_test.go`
- Modify: `internal/partnerauth/service.go`, `internal/partnerauth/service_test.go`

- [ ] **Step 1: 写失败测试**：覆盖缺 issuer/audience/JWKS URL、`aud` 不匹配、email 不在 allowlist、`exp` 过期、`nbf` 尚未生效、`jti` 缺失、RS256 签名错误和 `sub` 变化；断言统一 `ErrAccessAssertionRejected`，且不建立 session。

```go
func TestCloudflareVerifierRejectsInvalidClaims(t *testing.T) {
    v, err := NewCloudflareVerifier(CloudflareVerifierConfig{Issuer: "https://acct.cloudflareaccess.com", Audience: "app-aud", JWKSURL: server.URL, AllowedEmails: []string{"a@example.com"}}); if err != nil { t.Fatal(err) }
    for _, tc := range []struct{name string; mutate func(*Claims)}{{"aud", func(c *Claims){c.Audience=Audience{"wrong"}}}, {"email", func(c *Claims){c.Email="b@example.com"}}, {"exp", func(c *Claims){c.ExpiresAt=time.Now().Add(-time.Minute)}}, {"jti", func(c *Claims){c.JTI=""}}} {
        t.Run(tc.name, func(t *testing.T) { c:=validClaims(); tc.mutate(&c); if _, err:=v.Verify(context.Background(), signedJWT(t,c)); !errors.Is(err, ErrAccessAssertionRejected) { t.Fatalf("err=%v",err) } })
    }
}
```

- [ ] **Step 2: 运行失败测试**：`go test ./internal/partnerauth ./internal/config -run 'TestCloudflareVerifier|TestPartnerSecurity' -count=1`；预期 FAIL，原因是 `CloudflareVerifier`、claims policy 和配置解析尚未定义。
- [ ] **Step 3: 实现最小接口**：定义 `CloudflareVerifierConfig`, `CloudflareVerifier`, `JWKSCache`, `Claims`, `Verify(context.Context,string) (Assertion,error)`；只接受 issuer、audience、JWKS URL、允许邮箱和 clock skew 通过配置传入，JWT header 仅允许 `RS256`，JWKS kid 缺失时刷新一次后拒绝。

```go
type CloudflareVerifierConfig struct { Issuer, Audience, JWKSURL string; AllowedEmails []string; ClockSkew time.Duration; HTTPClient *http.Client; Now func() time.Time }
type CloudflareVerifier struct { cfg CloudflareVerifierConfig; cache *JWKSCache }
func NewCloudflareVerifier(cfg CloudflareVerifierConfig) (*CloudflareVerifier,error) {
    if cfg.Issuer==""||cfg.Audience==""||cfg.JWKSURL=="" { return nil,ErrAccessAssertionRejected }
    if cfg.Now==nil { cfg.Now=time.Now }; if cfg.HTTPClient==nil { cfg.HTTPClient=&http.Client{Timeout:10*time.Second} }
    return &CloudflareVerifier{cfg:cfg,cache:NewJWKSCache(cfg.JWKSURL,cfg.HTTPClient,cfg.Now)},nil
}
func (v *CloudflareVerifier) Verify(ctx context.Context, raw string) (Assertion, error) {
    header, claims, signingInput, signature, err := parseRS256JWT(raw)
    if err != nil || header.Alg != "RS256" || strings.TrimSpace(header.KID) == "" { return Assertion{}, ErrAccessAssertionRejected }
    key, err := v.cache.Key(ctx, header.KID); if err != nil { return Assertion{}, ErrAccessAssertionRejected }
    digest := sha256.Sum256(signingInput)
    if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil { return Assertion{}, ErrAccessAssertionRejected }
    now := v.cfg.Now(); if claims.Issuer != v.cfg.Issuer || !claims.Audience.Contains(v.cfg.Audience) || claims.ExpiresAt.Before(now.Add(-v.cfg.ClockSkew)) || claims.NotBefore.After(now.Add(v.cfg.ClockSkew)) { return Assertion{}, ErrAccessAssertionRejected }
    email := strings.ToLower(strings.TrimSpace(claims.Email)); if email == "" || claims.Subject == "" || claims.JTI == "" || !emailAllowed(email, v.cfg.AllowedEmails) { return Assertion{}, ErrAccessAssertionRejected }
    return Assertion{Email:email,Subject:claims.Subject,Audience:v.cfg.Audience,JTI:claims.JTI,ExpiresAt:claims.ExpiresAt}, nil
}
func emailAllowed(email string, allowed []string) bool { for _,candidate:=range allowed { if subtle.ConstantTimeCompare([]byte(email),[]byte(strings.ToLower(strings.TrimSpace(candidate))))==1{return true} }; return false }
```

- [ ] **Step 3a: Implement exact three-segment decoding and string-or-array audience parsing.**

```go
type jwtHeader struct { Alg string `json:"alg"`; KID string `json:"kid"` }
type Audience []string
func (a *Audience) UnmarshalJSON(data []byte) error { var one string; if json.Unmarshal(data,&one)==nil{*a=[]string{one};return nil}; var many []string; if err:=json.Unmarshal(data,&many);err!=nil{return err};*a=many;return nil }
func (a Audience) Contains(want string) bool { for _,value:=range a{if value==want{return true}};return false }
type jwtClaims struct { Issuer string `json:"iss"`; Audience Audience `json:"aud"`; Email string `json:"email"`; Subject string `json:"sub"`; JTI string `json:"jti"`; Exp int64 `json:"exp"`; NBF int64 `json:"nbf"` }
type Claims struct { Issuer string; Audience Audience; Email,Subject,JTI string; ExpiresAt,NotBefore time.Time }
func parseRS256JWT(raw string) (jwtHeader,Claims,[]byte,[]byte,error) {
    parts:=strings.Split(raw,"."); if len(parts)!=3{return jwtHeader{},Claims{},nil,nil,ErrAccessAssertionRejected}
    headerBytes,err:=base64.RawURLEncoding.DecodeString(parts[0]);if err!=nil{return jwtHeader{},Claims{},nil,nil,ErrAccessAssertionRejected}
    claimBytes,err:=base64.RawURLEncoding.DecodeString(parts[1]);if err!=nil{return jwtHeader{},Claims{},nil,nil,ErrAccessAssertionRejected}
    signature,err:=base64.RawURLEncoding.DecodeString(parts[2]);if err!=nil{return jwtHeader{},Claims{},nil,nil,ErrAccessAssertionRejected}
    var header jwtHeader;var rawClaims jwtClaims;if json.Unmarshal(headerBytes,&header)!=nil||json.Unmarshal(claimBytes,&rawClaims)!=nil{return jwtHeader{},Claims{},nil,nil,ErrAccessAssertionRejected}
    claims:=Claims{Issuer:rawClaims.Issuer,Audience:rawClaims.Audience,Email:rawClaims.Email,Subject:rawClaims.Subject,JTI:rawClaims.JTI,ExpiresAt:time.Unix(rawClaims.Exp,0),NotBefore:time.Unix(rawClaims.NBF,0)}
    return header,claims,[]byte(parts[0]+"."+parts[1]),signature,nil
}
```

- [ ] **Step 4: 运行通过测试**：`go test ./internal/partnerauth ./internal/config -run 'TestCloudflareVerifier|TestPartnerSecurity' -count=1`；预期 `PASS`。
- [ ] **Step 5: 提交**：`git add internal/config/partner_security.go internal/config/partner_security_test.go internal/partnerauth/cloudflare.go internal/partnerauth/cloudflare_test.go internal/partnerauth/service.go internal/partnerauth/service_test.go; git commit -m "feat: verify Cloudflare Access assertions"`。

### Task 2: 实现 JWKS 缓存、jti 重放拒绝与 session_version 撤销

**Files:** Modify `internal/partnerauth/cloudflare.go`, `internal/partnerauth/service.go`, `internal/partnerauth/service_test.go`; create `internal/partnerauth/replay_test.go`.

- [ ] **Step 1: 写失败测试**：验证缓存命中不重复 HTTP 请求，过期后刷新，未知 kid 只刷新一次；同一 `jti` 第二次拒绝；`session_version` 递增后旧 cookie 返回 401；session TTL 只能是 15 分钟以内。

```go
func TestReplayAndSessionVersion(t *testing.T) {
    svc, users := fixtureProductionService(t); raw := signedJWTWithJTI(t,"j-1"); first:=login(t,svc,raw); if first.Code!=http.StatusNoContent {t.Fatal(first.Code)}; if login(t,svc,raw).Code!=http.StatusUnauthorized {t.Fatal("replay accepted")}; users.Revoke("u1"); if require(t,svc,first.Cookie).Code!=http.StatusUnauthorized {t.Fatal("revoked session accepted")}
}
```

- [ ] **Step 2: 运行失败测试**：`go test ./internal/partnerauth -run 'TestReplay|TestSessionVersion|TestJWKSCache' -count=1`；预期 FAIL，原因是 replay store 和版本检查未接入。
- [ ] **Step 3: 实现最小代码**：M3 `Store.ConsumeAssertion` 执行 `INSERT INTO partner_access_replays(jti_hash,expires_at,created_at) VALUES(?,?,?) ON CONFLICT(jti_hash) DO NOTHING` 并要求一行受影响；`GetSessionByHash` 同表关联 `partner_users.session_version`；JWKS cache 使用 mutex、`max-age`、单次 refresh，数据库只存 jti/session token 的 SHA-256；cookie 沿用 M3 的 `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`，以便同时覆盖 `/partner/*` 页面和 `/api/partner/*` API。

```go
func (s *Service) RequireRequestSession(ctx context.Context, r *http.Request) (OwnerScope,error) {
    c,err:=r.Cookie("partner_session"); if err!=nil{return OwnerScope{},ErrUnauthenticated}
    return s.RequireSession(ctx,c.Value)
}
```

- [ ] **Step 4: 运行通过测试**：`go test ./internal/partnerauth -run 'TestReplay|TestSessionVersion|TestJWKSCache' -count=1`；预期 `PASS`。
- [ ] **Step 5: 提交**：`git add internal/partnerauth; git commit -m "feat: reject Access replay and revoke sessions"`。

### Task 3: 强制 2030/2032 loopback 与生产 verifier 启动门

**Files:** Modify `cmd/console/main.go`, `cmd/console/main_test.go`, `internal/app/partner.go`, `internal/app/partner_test.go`, `internal/config/config.go`, `internal/config/config_test.go`.

- [ ] **Step 1: 写失败测试**：`2030=0.0.0.0:2030`、`2032=0.0.0.0:2032`、2032 不是 loopback 或 verifier 缺失时启动失败；Tunnel header 伪造不能绕过真实 verifier；两个 mux 不共享管理员路由。

```go
func TestProductionBindsOnlyLoopback(t *testing.T) { for _,a:=range []string{"0.0.0.0:2030","0.0.0.0:2032","192.168.1.2:2032"} { if err:=ValidatePartnerListen(a); err==nil {t.Fatalf("accepted %s",a)} }; if err:=ValidatePartnerListen("127.0.0.1:2032"); err!=nil {t.Fatal(err)} }
```

- [ ] **Step 2: 运行失败测试**：`go test ./internal/config ./internal/app ./cmd/console -run 'TestProductionBinds|TestPartnerVerifierRequired' -count=1`；预期 FAIL，原因是当前仅有 2030 配置且 partner 生产启动未强制 verifier。
- [ ] **Step 3: 实现最小代码**：沿用 M3 `PartnerListenAddr` 默认 `127.0.0.1:2032` 和 `ValidatePartnerListen`；新增 `ValidateLoopbackAddr` 同时校验 2030；生产 verifier 工厂必须返回非 nil `partnerauth.Verifier` 才能调用 `NewPartnerApp`，测试只通过依赖注入 fake verifier；启动分别创建两个 `http.Server`，signal context 关闭二者和 worker。

```go
func ValidatePartnerListen(addr string) error { host,_,err:=net.SplitHostPort(addr); if err!=nil{return err}; ip:=net.ParseIP(host); if ip==nil||!ip.IsLoopback(){return fmt.Errorf("partner listen must be loopback")}; return nil }
```

- [ ] **Step 4: 运行通过测试**：`go test ./internal/config ./internal/app ./cmd/console -run 'TestProductionBinds|TestPartnerVerifierRequired' -count=1`; `go test ./...`; 预期均 `PASS`。
- [ ] **Step 5: 提交**：`git add cmd/console/main.go internal/app/partner.go internal/config; git commit -m "feat: isolate production partner listener"`。

### Task 4: 生成并验证 Cloudflare Tunnel/Access 配置

**Files:** Create `deploy/cloudflared/partner-tunnel.yml`, `deploy/cloudflared/generate-tunnel.ps1`, `deploy/cloudflared/validate-tunnel.ps1`, `deploy/cloudflared/validate-tunnel.Tests.ps1`; modify `docs/operations/partner-m4.md`.

- [ ] **Step 1: 写失败校验**：YAML 含 2030、非 loopback service、未配置 Access hostname 或 catch-all 时失败；正确配置只含 partner hostname→`http://127.0.0.1:2032`。

```powershell
Describe "Tunnel policy" { It "rejects admin ingress" { { .\deploy\cloudflared\validate-tunnel.ps1 -Path .\bad.yml } | Should -Throw "2030" } }
```

- [ ] **Step 2: 运行失败校验**：`pwsh -File deploy/cloudflared/validate-tunnel.ps1 -Path deploy/cloudflared/partner-tunnel.yml`; 预期 FAIL，原因是配置文件和禁止 ingress 规则尚未存在。
- [ ] **Step 3: 生成最小配置**：脚本参数为 `-Hostname`, `-TunnelUUID`, `-OutputPath`, `-DryRun`; 输出 ingress、404 fallback、`service: http://127.0.0.1:2032`，绝不写 token；验证脚本检查 hostname、2032、无 2030/0.0.0.0/localhost 外网地址，并输出 `PASS tunnel-policy`。

```yaml
tunnel: "{{TUNNEL_UUID}}"
credentials-file: "C:\\ProgramData\\VideoProductionConsole\\cloudflared\\{{TUNNEL_UUID}}.json"
ingress:
  - hostname: "{{PARTNER_HOSTNAME}}"
    service: "http://127.0.0.1:2032"
  - service: "http_status:404"
```

- [ ] **Step 4: 通过校验**：`pwsh -File deploy/cloudflared/generate-tunnel.ps1 -Hostname partner.example.com -TunnelUUID 00000000-0000-0000-0000-000000000001 -OutputPath $env:TEMP\partner-tunnel.yml -DryRun`; `pwsh -File deploy/cloudflared/validate-tunnel.ps1 -Path $env:TEMP\partner-tunnel.yml`; 预期均 `PASS`。
- [ ] **Step 5: 提交**：`git add deploy/cloudflared/partner-tunnel.yml deploy/cloudflared/generate-tunnel.ps1 deploy/cloudflared/validate-tunnel.ps1 deploy/cloudflared/validate-tunnel.Tests.ps1 docs/operations/partner-m4.md; git commit -m "ops: constrain Cloudflare partner tunnel"`。

### Task 5: 完成 Access dashboard 人工配置与可验证 checklist

**Files:** Modify `docs/operations/partner-m4.md`; create `docs/operations/cloudflare-access-checklist.md`, `scripts/m4-access-check.ps1`。

- [ ] **Step 1: 写失败检查**：无 Cloudflare API token 时，检查脚本必须明确报告需人工确认，而不是伪造成功；本地测试验证 2030 hostname 不在 Access application/ingress 目标。

```powershell
$r = pwsh -File .\scripts\m4-access-check.ps1 -Hostname partner.example.com -ExpectedAudience app-aud -AllowedEmails @('partner@example.com') -DryRun
if ($r -notmatch 'MANUAL-CHECK-REQUIRED') { throw 'missing manual gate' }
```

- [ ] **Step 2: 运行失败检查**：`pwsh -File scripts/m4-access-check.ps1 -Hostname partner.example.com -ExpectedAudience app-aud -DryRun`; 预期 FAIL 或 `MANUAL-CHECK-REQUIRED`，原因是 dashboard 状态不能由本地代码推断。
- [ ] **Step 3: 写人工 checklist 与脚本**：要求人工在 Zero Trust→Access→Applications 中创建 hostname application、OTP policy 仅允许 allowlist 邮箱、JWT audience 记录为配置值、service token 不用于 partner 登录；Cloudflare dashboard 操作后运行 `cloudflared tunnel ingress validate`、`curl.exe --resolve partner.example.com:443:127.0.0.1 https://partner.example.com/healthz`，确认 2030 不可达。

```markdown
- [ ] Access application hostname exactly equals `partner.example.com`.
- [ ] One-time PIN is enabled by Cloudflare Access; application sends no OTP.
- [ ] Policy includes only approved email domains/addresses.
- [ ] JWT audience copied exactly to `PARTNER_ACCESS_AUDIENCE`; no secret pasted into evidence.
- [ ] `2030` has no Access application and tunnel ingress validation reports no `127.0.0.1:2030` target.
```

- [ ] **Step 4: 通过验收**：人工签名 checklist 后执行 `pwsh -File scripts/m4-access-check.ps1 -Hostname partner.example.com -ExpectedAudience app-aud -AllowedEmails partner@example.com`; 预期输出 `PASS access-check`，没有凭证时只输出人工待验项。
- [ ] **Step 5: 提交**：`git add docs/operations/partner-m4.md docs/operations/cloudflare-access-checklist.md scripts/m4-access-check.ps1; git commit -m "docs: define Cloudflare Access acceptance"`。

### Task 6: Windows 服务部署、启动参数与密钥保护

**Files:** Create `deploy/windows/install-partner-service.ps1`, `deploy/windows/start-partner-service.ps1`, `deploy/windows/uninstall-partner-service.ps1`, `deploy/windows/protect-signing-key.ps1`, `deploy/windows/partner-service.xml`, `deploy/windows/install-partner-service.Tests.ps1`, `internal/portablepackage/keystore.go`, `internal/portablepackage/keystore_test.go`.

- [ ] **Step 1: 写失败测试**：安装脚本拒绝不存在的 exe/data root、非管理员、明文私钥参数；服务命令必须含 `PARTNER_CONFIG` 和受 ACL 保护的 key directory，启动检查 2032 loopback。

```powershell
It "rejects plaintext signing key" { { .\deploy\windows\install-partner-service.ps1 -Binary .\console.exe -SigningPrivateKey secret } | Should -Throw "private key" }
```

- [ ] **Step 2: 运行失败测试**：`Invoke-Pester deploy/windows/*.Tests.ps1`; 预期 FAIL，原因是服务脚本和 keyring 尚未实现。
- [ ] **Step 3: 实现最小部署/key store**：安装参数固定为 `-Binary -Config -DataRoot -ServiceName -KeyDirectory -PartnerListenAddr`; 使用 Windows `sc.exe create`，固定服务账户、`FailureActions=restart/60000`、日志目录；私钥文件用 DPAPI 加密并用 ACL 限制到服务账户，进程只读私钥，输出仅 key id。

```powershell
param([Parameter(Mandatory)] [string]$Binary,[Parameter(Mandatory)] [string]$Config,[string]$DataRoot='C:\ProgramData\VideoProductionConsole',[string]$ServiceName='VideoProductionPartner',[string]$PartnerListenAddr='127.0.0.1:2032')
if (-not (Test-Path -LiteralPath $Binary)) { throw "binary missing" }
if ($PartnerListenAddr -notmatch '^127\.0\.0\.1:2032$') { throw "partner listen must be 127.0.0.1:2032" }
$serviceAccount = 'NT AUTHORITY\LocalService'
sc.exe create $ServiceName binPath= "`"$Binary`" serve --config `"$Config`"" start= auto obj= $serviceAccount | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'service creation failed' }
sc.exe failure $ServiceName reset= 86400 actions= restart/60000/restart/60000/none/0 | Out-Null
icacls.exe $DataRoot /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "LOCAL SERVICE:(OI)(CI)RX" | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'data-root ACL failed' }
```

- [ ] **Step 3a: Implement `protect-signing-key.ps1`** with mandatory `-InputKeyPath -OutputPath`; read bytes from the file, protect with `ProtectedData.Protect(..., LocalMachine)`, atomically write the encrypted blob, apply `SYSTEM:F` and `LOCAL SERVICE:R` ACLs, then zero the in-memory byte array. No private key is accepted as a command-line string.

```powershell
$plain = [IO.File]::ReadAllBytes((Resolve-Path -LiteralPath $InputKeyPath))
try { $cipher = [Security.Cryptography.ProtectedData]::Protect($plain,$null,[Security.Cryptography.DataProtectionScope]::LocalMachine); $partial = "$OutputPath.partial"; [IO.File]::WriteAllBytes($partial,$cipher); Move-Item -LiteralPath $partial -Destination $OutputPath -Force; icacls.exe $OutputPath /inheritance:r /grant:r 'SYSTEM:F' 'LOCAL SERVICE:R' | Out-Null } finally { [Array]::Clear($plain,0,$plain.Length) }
```

- [ ] **Step 4: 通过测试**：`Invoke-Pester deploy/windows/*.Tests.ps1`; `pwsh -File deploy/windows/start-partner-service.ps1 -ServiceName VideoProductionPartner -Config C:\ProgramData\VideoProductionConsole\partner.json`; `Get-NetTCPConnection -LocalPort 2032`; 预期服务运行且 `LocalAddress=127.0.0.1`。
- [ ] **Step 5: 提交**：`git add deploy/windows internal/portablepackage/keystore.go internal/portablepackage/keystore_test.go; git commit -m "ops: install partner service with protected keys"`。

### Task 7: 公钥、签名密钥轮换与 Importer 兼容矩阵

**Files:** Modify `internal/portablepackage/keystore.go`, `internal/portablepackage/keystore_test.go`, `internal/portablepackage/sign.go`, `scripts/release.ps1`; create `docs/releases/partner-compatibility.json`, `scripts/m4-key-rotate.ps1`。

- [ ] **Step 1: 写失败测试**：active key 可签名，新 key 生效后 old key 仍可验证；被撤销 key 拒绝；Importer schema/min version 不兼容时拒绝且未知可选字段仍通过。

```go
func TestKeyRotationKeepsPreviousVerification(t *testing.T) { store:=newMemorySigningKeyStore(t,"k-2026-08"); old:=store.ActiveSigner(); sig,_:=old.Sign([]byte("manifest")); store.Rotate("k-2026-09"); keys:=store.VerificationKeyring(); if err:=keys.Verify("k-2026-08",[]byte("manifest"),sig); err!=nil {t.Fatal(err)}; store.Revoke("k-2026-08"); if store.VerificationKeyring().Verify("k-2026-08",[]byte("manifest"),sig)==nil {t.Fatal("revoked key accepted")} }
```

- [ ] **Step 2: 运行失败测试**：`go test ./internal/portablepackage -run 'TestKeyRotation|TestCompatibility' -count=1`; 预期 FAIL，原因是 keyring rotation 和兼容门未实现。
- [ ] **Step 3: 实现最小代码**：定义 `SigningKeyStore.ActiveSigner`, `Rotate`, `Revoke`, `VerificationKeyring`, `PublicRecords`；M2 `Keyring.Verify` 继续只负责公钥验证；发布 JSON 仅含 key id、算法、公钥、状态、有效期；Importer 矩阵明确 `schema_version=1`, `importer_min_version<=1.x`，未知必需字段拒绝。

```go
type PublicKeyRecord struct { KeyID,Algorithm,Status,PublicKey string; NotBefore,NotAfter time.Time }
type SigningKeyStore interface { ActiveSigner() Signer; Rotate(string)(PublicKeyRecord,error); Revoke(string) error; VerificationKeyring() Keyring; PublicRecords() []PublicKeyRecord }
func (s *DPAPIKeyStore) VerificationKeyring() Keyring { out:=Keyring{};for _,record:=range s.records{if record.Status=="active"||record.Status=="verify-only"{out[record.KeyID]=append(ed25519.PublicKey(nil),record.Public...)}};return out }
```

```json
{"schema_version":1,"importer_min_version":"1.0.0","accepted":{"draft_version":["jianying-v1"],"signature_algorithms":["Ed25519"]},"keys":[{"key_id":"k-2026-09","status":"active","public_key":"BASE64"},{"key_id":"k-2026-08","status":"verify-only","public_key":"BASE64"}]}
```

- [ ] **Step 4: 通过测试**：`go test ./internal/portablepackage -run 'TestKeyRotation|TestCompatibility' -count=1`; `pwsh -File scripts/m4-key-rotate.ps1 -KeyDirectory C:\ProgramData\VideoProductionConsole\keys -NewKeyId k-2026-09 -DryRun`; 预期 `PASS`。
- [ ] **Step 5: 提交**：`git add internal/portablepackage/keystore.go internal/portablepackage/keystore_test.go internal/portablepackage/sign.go scripts/m4-key-rotate.ps1 scripts/release.ps1 docs/releases/partner-compatibility.json; git commit -m "release: publish key rotation compatibility matrix"`。

### Task 8: 数据库迁移、备份、升级与回滚

**Files:** Create `internal/ops/backup.go`, `internal/ops/backup_test.go`, `internal/ops/rollback.go`, `internal/ops/rollback_test.go`, `scripts/m4-release.ps1`; modify `docs/operations/partner-m4.md`。

- [ ] **Step 1: 写失败测试**：备份必须含 SQLite integrity、schema version 和 SHA-256；迁移失败自动停止服务并可恢复；回滚只接受已验证备份，错误路径/跨目录目标拒绝。

```go
func TestBackupAndRollback(t *testing.T) { ctx:=context.Background();db,dataRoot:=backupFixture(t);b,err:=BackupDatabase(ctx,db,dataRoot,t.TempDir(),time.Now());if err!=nil||b.SHA256==""{t.Fatalf("record=%#v err=%v",b,err)};destination:=filepath.Join(t.TempDir(),"console.db");if err=RollbackDatabase(ctx,b.DatabasePath,destination);err!=nil{t.Fatal(err)};if err=store.CheckIntegrity(ctx,destination+".restored");err!=nil{t.Fatal(err)} }
```

- [ ] **Step 2: 运行失败测试**：`go test ./internal/ops -run 'TestBackup|TestRollback|TestMigration' -count=1`; 预期 FAIL，原因是 backup/rollback API 不存在。
- [ ] **Step 3: 实现最小编排接口**：复用现有 `store.CreateBackup`, `store.CheckIntegrity`, `store.RestoreBackup`，不再实现第二套 SQLite copy 逻辑；`BackupDatabase` 计算现有备份函数的时间戳文件名并追加 SHA-256 证据，`RollbackDatabase` 只向新的 staging 目标恢复，完整性通过后由停服脚本执行同卷原子替换。升级顺序为 stop→backup→migration→verify→start，失败执行 restore。

```go
type BackupRecord struct { DatabasePath,ManifestPath,SHA256 string; CreatedAt time.Time }
func BackupDatabase(ctx context.Context,db *sql.DB,dataRoot,backupDir string,now time.Time)(BackupRecord,error){ stamp:=now.Format("20060102-150405.000000000");if err:=store.CreateBackup(db,dataRoot,backupDir,now);err!=nil{return BackupRecord{},err};database:=filepath.Join(backupDir,"console-"+stamp+".db");if err:=store.CheckIntegrity(ctx,database);err!=nil{return BackupRecord{},err};hash,err:=sha256File(database);return BackupRecord{DatabasePath:database,ManifestPath:filepath.Join(backupDir,"data-manifest-"+stamp+".json"),SHA256:hash,CreatedAt:now},err}
func RollbackDatabase(ctx context.Context,backup,destination string) error { staging:=destination+".restored";if err:=store.RestoreBackup(ctx,backup,staging);err!=nil{return err};return store.CheckIntegrity(ctx,staging) }
```

```powershell
param([string]$Config,[switch]$Rollback,[string]$LatestVerifiedBackup)
Stop-Service VideoProductionPartner -ErrorAction Stop
$dataRoot = 'C:\ProgramData\VideoProductionConsole\data'; $backupRoot = 'D:\VideoProductionBackups'; $database = Join-Path $dataRoot 'console.db'
if ($Rollback) { if (-not (Test-Path -LiteralPath $LatestVerifiedBackup)) { throw 'verified backup path is required' }; .\console-maintenance.exe restore -backup $LatestVerifiedBackup -destination "$database.restored"; if ($LASTEXITCODE -ne 0) { throw 'rollback restore failed' }; .\console-maintenance.exe check -database "$database.restored"; if ($LASTEXITCODE -ne 0) { throw 'rollback integrity failed' }; Move-Item -LiteralPath $database -Destination "$database.pre-rollback"; Move-Item -LiteralPath "$database.restored" -Destination $database }
else { .\console-maintenance.exe backup -data-root $dataRoot -output $backupRoot; if ($LASTEXITCODE -ne 0) { throw 'backup failed' }; .\video-production-console.exe migrate --config $Config; if ($LASTEXITCODE -ne 0) { throw 'migration failed; run this script with -Rollback -LatestVerifiedBackup <path>' } }
Start-Service VideoProductionPartner
```

- [ ] **Step 4: 通过测试**：`go test ./internal/ops -run 'TestBackup|TestRollback|TestMigration' -count=1`; `pwsh -File scripts/m4-release.ps1 -Config C:\ProgramData\VideoProductionConsole\partner.json -DryRun`; 预期 `PASS` 并打印 backup hash。
- [ ] **Step 5: 提交**：`git add internal/ops scripts/m4-release.ps1 docs/operations/partner-m4.md; git commit -m "ops: add verified migration backup rollback"`。

### Task 9: 脱敏监控、过期清理、磁盘门与恢复演练

**Files:** Create `internal/ops/metrics.go`, `internal/ops/metrics_test.go`, `internal/ops/maintenance.go`, `internal/ops/maintenance_test.go`, `scripts/m4-drill.ps1`; modify `docs/operations/partner-m4.md`.

- [ ] **Step 1: 写失败测试**：指标包含 Access reject/replay、session revoke、stage duration/retry/gate failure、disk free、package failure、Range error、expired cleanup、receipt failure；日志不含 token/email/path；清理只 purge expired package/staging，保留项目摘要。

```go
func TestMetricsAndCleanupAreRedacted(t *testing.T) { log:=NewRedactingLogger(); log.Info("x","token","abc","email","a@example.com","path","C:\\secret"); if strings.Contains(log.String(),"abc")||strings.Contains(log.String(),"a@example.com")||strings.Contains(log.String(),"C:\\secret"){t.Fatal("secret leaked")}; m:=maintenanceFixture(t); r,err:=m.Run(context.Background(),time.Now(),100); if err!=nil{t.Fatal(err)}; if r.ProjectsDeleted!=0||r.PackagesPurged!=1 {t.Fatalf("%+v",r)} }
```

- [ ] **Step 2: 运行失败测试**：`go test ./internal/ops -run 'TestMetrics|TestCleanup|TestDisk' -count=1`; 预期 FAIL，原因是 metrics/cleanup 尚未实现。
- [ ] **Step 3: 实现最小接口**：定义 `Metrics`, `Maintenance.Run(ctx, now, limit)`, `DiskGate`, `RedactingLogger`; `Maintenance` 调用 M2 `portablepackage.Cleaner.Run`，不复制清理状态机；标签只允许稳定枚举和 hash 前缀，邮箱做域/哈希摘要，路径统一 `<redacted>`；失败保留可重试状态。

```go
type PackageCleaner interface { Run(context.Context,time.Time,int)(portablepackage.CleanupResult,error) }
type Maintenance struct { Cleaner PackageCleaner; Metrics *Metrics }
func (m *Maintenance) Run(ctx context.Context,now time.Time,limit int)(portablepackage.CleanupResult,error){ result,err:=m.Cleaner.Run(ctx,now,limit);m.Metrics.ObserveCleanup(result,err);return result,err }
```

```powershell
param([string]$Config,[switch]$InjectFailure)
& .\console.exe ops drill --config $Config --scenario "tunnel-down,db-backup-failure,worker-crash,disk-low,expired-purge" $(if($InjectFailure){'--inject-failure'}else{''})
if ($LASTEXITCODE -ne 0) { throw 'drill failed' }
Write-Output 'PASS recovery-drill: restore, lease-reclaim, cleanup, redaction'
```

- [ ] **Step 4: 通过测试与演练**：`go test ./internal/ops -run 'TestMetrics|TestCleanup|TestDisk' -count=1`; `pwsh -File scripts/m4-drill.ps1 -Config C:\ProgramData\VideoProductionConsole\partner.json`; 预期故障注入后恢复、租约接管、清理和脱敏断言均 `PASS`。
- [ ] **Step 5: 提交**：`git add internal/ops scripts/m4-drill.ps1 docs/operations/partner-m4.md; git commit -m "ops: add redacted monitoring cleanup and drills"`。

### Task 10: 正式发布包、人工验收和最终回归

**Files:** Modify `scripts/release.ps1`, `scripts/m4-release.ps1`, `docs/operations/partner-m4.md`; create `docs/releases/partner-m4-checklist.md`, `internal/integration/partner_m4_test.go`。

- [ ] **Step 1: 写失败集成测试**：从 Access JWKS fixture 到 2032 health、JWT 登录、重放拒绝、session revoke、ready 下载、Range、过期清理、Importer 公钥验证、旧 local_jianying 回归全部串联；2030 端口不可由 Tunnel 配置访问。

```go
func TestM4ReleaseAcceptance(t *testing.T) { env:=StartM4Fixture(t); defer env.Close(); if env.Partner.StatusCode("/healthz")!=200 {t.Fatal("2032 unavailable")}; if env.AdminThroughTunnel()!=404 {t.Fatal("2030 exposed")}; if env.Replay()==nil||env.RevokedSession()==nil {t.Fatal("auth gate failed")}; if err:=env.ImportAndVerify(); err!=nil {t.Fatal(err)} }
```

- [ ] **Step 2: 运行失败集成测试**：`go test ./internal/integration -run TestM4ReleaseAcceptance -count=1`; 预期 FAIL，原因是 M4 配置、服务、演练和发布证据尚未全部装配。
- [ ] **Step 3: 实现发布清单与构建**：`m4-release.ps1` 固定输出 server version、`signature_key_id`、公钥 JSON、`jianying-draft-importer.exe` SHA-256、compatibility matrix、DB backup hash；发布前检查无秘密、无绝对路径、2030 未 ingress、2032 loopback、Access 人工 checklist 签名和 M1–M4 evidence。

```markdown
- [ ] `go test ./...` PASS; frontend typecheck/build/e2e PASS.
- [ ] M1 cross-machine receipt and Jianying screenshot archived without source paths.
- [ ] M2 package SHA-256 and Ed25519 verification archived; key id is verifyable.
- [ ] M3 owner-scope/Range/expiry/session tests PASS; partner service is `127.0.0.1:2032`.
- [ ] M4 Access dashboard checklist signed; Cloudflare OTP is the only OTP; no application OTP code exists.
- [ ] Migration backup integrity and rollback drill PASS; alerts are redacted.
- [ ] `git diff --check` PASS; release directory contains only approved artifacts.
```

- [ ] **Step 4: 通过最终验证**：`go test ./...`; `pwsh -File scripts/m4-release.ps1 -Config C:\ProgramData\VideoProductionConsole\partner.json -Output release/m4 -Verify`; `git diff --check`; 预期测试、发布和格式均 `PASS`。
- [ ] **Step 5: 提交**：`git add scripts/release.ps1 scripts/m4-release.ps1 docs/operations/partner-m4.md docs/releases/partner-m4-checklist.md internal/integration/partner_m4_test.go; git commit -m "release: validate partner portable M4"`。

## Self-review checklist

- [ ] Cloudflare Access OTP 是唯一 OTP；真实 JWT 的 signature/JWKS、issuer、audience、email、subject、exp、nbf、jti 均有失败测试和实现接口。
- [ ] replay、session TTL、`session_version` 撤销、2030/2032 loopback、Tunnel ingress 禁止 2030 均覆盖。
- [ ] Windows 服务参数、密钥 ACL/轮换、旧公钥验证、Importer schema/version 矩阵均覆盖。
- [ ] 迁移备份、integrity、升级、回滚、清理、磁盘门、监控告警、日志脱敏和故障恢复演练均覆盖。
- [ ] Dashboard 无法代码自动完成的项目均是明确人工 checklist，并有 `cloudflared`/`curl` 验证命令；没有虚构凭证。
- [ ] 10 个任务各含至少一个失败测试代码块和一个实际最小实现代码块，围栏总数为 20 以上。
- [ ] 计划内命名一致：`CloudflareVerifier`, `JWKSCache`, `Store.ConsumeAssertion`, `PartnerListenAddr`, `BackupDatabase`, `RollbackDatabase`, `Maintenance.Run`。
- [ ] 计划文件不得出现未确定事项或含糊指令；实现者必须按任务独立 `git add`/`git commit`。
