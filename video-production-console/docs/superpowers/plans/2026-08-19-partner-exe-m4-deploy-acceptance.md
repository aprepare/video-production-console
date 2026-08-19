# Partner Gateway Deployment and Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy the completed gateway safely to `cpa`, provision isolated credentials for the four named partners, and release the single EXE only after a clean-Windows 天中观局 pilot passes.

**Architecture:** Package the Linux gateway binary into a small non-root Docker image attached to the existing external `cliproxyapi_default` network, while exposing only pinned-TLS port 2443. Provision a gateway-only CLIProxyAPI client key through its hot-reload management API, keep all secrets in root-owned VPS files, and record sanitized acceptance evidence plus deterministic rollback commands.

**Tech Stack:** PowerShell, SSH/SCP alias `cpa`, Docker 29/Compose 5, Ubuntu 22.04, CLIProxyAPI management API, private-CA TLS, Go smoke helpers, Windows/Jianying manual acceptance.

---

## Confirmed server facts

- Host alias `cpa` resolves to `23.138.12.112`, SSH user `root`.
- Docker network `cliproxyapi_default` contains service/DNS name `cli-proxy-api`.
- Existing API remains mapped on public port `2001`; this plan does not change its container or Compose file.
- Port `2443` is currently unused.
- Gateway persistent root is `/opt/video-partner-gateway`.
- Partner display names are exactly `天中观局`, `居中观`, `认知漫步`, `观局思考`.

## File map

Create:

- `deploy/partner-gateway/Dockerfile` — non-root runtime image.
- `deploy/partner-gateway/compose.yml` — independent service on external CLIProxy network.
- `deploy/partner-gateway/gateway.env.example` — non-secret configuration names/defaults.
- `deploy/partner-gateway/README.md` — server layout and local-only admin commands.
- `scripts/build-partner-gateway-bundle.ps1` — Linux binary/deployment bundle.
- `scripts/provision-cpa-upstream-key.ps1` — atomic gateway-only CLIProxy client key provisioning and rotation.
- `scripts/deploy-partner-gateway.ps1` — backup, upload, Compose validation, health and rollback.
- `scripts/smoke-partner-gateway.ps1` — pinned-CA health/auth/model smoke runner with redacted output.
- `docs/operations/partner-gateway-runbook.md` — deploy, backup, disable, unbind, rotate and rollback.
- `docs/operations/partner-pilot-acceptance.md` — clean-Windows checklist/evidence template without secrets.

Modify:

- `.gitignore` — gateway bundles, TLS private material, activation-key output and acceptance media.

### Task 1: Build the Docker and deployment bundle contract

**Files:**
- Create: `deploy/partner-gateway/Dockerfile`
- Create: `deploy/partner-gateway/compose.yml`
- Create: `deploy/partner-gateway/gateway.env.example`
- Create: `scripts/build-partner-gateway-bundle.ps1`
- Modify: `.gitignore`

- [ ] **Step 1: Write a failing bundle validation in the build script**

The script accepts `-Version` and creates `release/gateway/<version>/`. Before implementation, add assertions for these exact files:

```text
partner-gateway
Dockerfile
compose.yml
gateway.env
SHA256SUMS.txt
```

It must reject any bundle containing `.key`, `.crt`, `.db`, `.env` with secret values, `config.yaml`, logs or a Windows executable.

- [ ] **Step 2: Run and verify the bundle is absent**

```powershell
pwsh -File scripts/build-partner-gateway-bundle.ps1 -Version 0.1.0
```

Expected: FAIL because the script/files do not exist.

- [ ] **Step 3: Implement exact container configuration**

Use this Dockerfile shape:

```dockerfile
FROM alpine:3.22
RUN addgroup -g 10001 -S app && adduser -u 10001 -S -G app app
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --chown=10001:10001 partner-gateway /app/partner-gateway
USER 10001:10001
ENTRYPOINT ["/app/partner-gateway"]
CMD ["serve"]
```

Use this Compose contract, with no secret value in YAML:

```yaml
services:
  partner-gateway:
    build: .
    image: video-partner-gateway:${PARTNER_GATEWAY_VERSION}
    restart: unless-stopped
    env_file: gateway.env
    ports:
      - "2443:2443"
    volumes:
      - ./data:/var/lib/partner-gateway
      - ./tls/gateway.crt:/run/tls/gateway.crt:ro
      - ./tls/gateway.key:/run/tls/gateway.key:ro
      - ./secrets/upstream_api_key:/run/secrets/upstream_api_key:ro
      - ./secrets/aura_api_key:/run/secrets/aura_api_key:ro
    networks:
      - cliproxy
    healthcheck:
      test: ["CMD", "wget", "--no-check-certificate", "-qO-", "https://127.0.0.1:2443/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
networks:
  cliproxy:
    external: true
    name: cliproxyapi_default
```

`gateway.env.example` fixes listen `:2443`, DB `/var/lib/partner-gateway/gateway.db`, TLS paths, upstream `http://cli-proxy-api:2001/v1`, secret file paths, Aura base/model/voice non-secret values and 12-hour session TTL. The build script sets `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`, copies only the Linux binary/config templates, writes non-secret `gateway.env`, and generates SHA-256.

- [ ] **Step 4: Build and inspect the bundle**

```powershell
pwsh -File scripts/build-partner-gateway-bundle.ps1 -Version 0.1.0
Get-Content release/gateway/0.1.0/SHA256SUMS.txt
```

Expected: PASS; exactly five declared bundle files plus no secret material.

- [ ] **Step 5: Commit deployment packaging**

```powershell
git add .gitignore deploy/partner-gateway scripts/build-partner-gateway-bundle.ps1
git commit -m "build: package partner gateway deployment"
```

### Task 2: Implement safe upstream-key provisioning and rotation

**Files:**
- Create: `scripts/provision-cpa-upstream-key.ps1`
- Create: `scripts/provision-cpa-upstream-key.tests.ps1`

- [ ] **Step 1: Write dry-run tests against a mock management server**

The test starts a local HTTP listener and asserts the script sends:

```json
{"old":null,"new":"generated-value"}
```

to `PATCH /v0/management/api-keys`, uses `Authorization: Bearer <management password>`, writes the same generated value to the requested secret file, and never prints either value. A failure after PATCH must issue `DELETE /v0/management/api-keys?value=<url-encoded-generated-value>` before deleting the local secret file.

- [ ] **Step 2: Run and verify failure**

```powershell
pwsh -File scripts/provision-cpa-upstream-key.tests.ps1
```

Expected: FAIL because the provisioning script is absent.

- [ ] **Step 3: Implement the non-interactive remote helper safely**

The PowerShell script uploads and executes a fixed Bash/Python helper over `ssh cpa`. The remote helper:

1. extracts `MANAGEMENT_PASSWORD` from `docker inspect cli-proxy-api` into a shell variable without printing it;
2. generates 32 random bytes with `openssl rand -hex 32`;
3. calls `PATCH http://cli-proxy-api:2001/v0/management/api-keys` from a temporary container attached to `cliproxyapi_default`, or falls back to the host-published port after a health check;
4. requires HTTP 200 and `{"status":"ok"}`;
5. writes the generated key atomically to `/opt/video-partner-gateway/secrets/upstream_api_key`, owner `10001:10001`, mode `0400`;
6. prints only `upstream key provisioned`.

Add `-RemoveOldKey` mode that reads the old key from PowerShell `Read-Host -AsSecureString`, transmits it through SSH stdin rather than command arguments, calls `DELETE ...?value=<encoded>`, clears the plaintext buffer, and prints only the HTTP status. Never use `GET /api-keys`, because it would return every key.

- [ ] **Step 4: Run mock tests and static leakage checks**

```powershell
pwsh -File scripts/provision-cpa-upstream-key.tests.ps1
rg -n "api-keys|MANAGEMENT_PASSWORD|upstream_api_key" scripts/provision-cpa-upstream-key.ps1
```

Expected: tests PASS; source contains only variable names and endpoint paths, not a value.

- [ ] **Step 5: Commit provisioning tooling**

```powershell
git add scripts/provision-cpa-upstream-key.ps1 scripts/provision-cpa-upstream-key.tests.ps1
git commit -m "ops: provision isolated gateway upstream key"
```

### Task 3: Implement idempotent VPS deployment and rollback

**Files:**
- Create: `scripts/deploy-partner-gateway.ps1`
- Create: `scripts/deploy-partner-gateway.tests.ps1`
- Create: `docs/operations/partner-gateway-runbook.md`

- [ ] **Step 1: Write deployment dry-run tests**

Inject a fake SSH/SCP command runner and assert the generated operation sequence is exactly:

```text
preflight disk/memory/2443/network/container checks
create /opt/video-partner-gateway/releases/0.1.0
upload bundle to that release directory
verify SHA256SUMS.txt
docker compose config --quiet
backup current compose/env/tls metadata and SQLite
switch current symlink atomically
docker compose build --pull
docker compose up -d
wait for healthy
verify existing cli-proxy-api container and port 2001 unchanged
```

On health failure, restore the previous `current` symlink and run its `docker compose up -d` without deleting the failed release or data.

- [ ] **Step 2: Run and verify failure**

```powershell
pwsh -File scripts/deploy-partner-gateway.tests.ps1
```

Expected: FAIL because deployment script is absent.

- [ ] **Step 3: Implement the exact server layout and checks**

Use:

```text
/opt/video-partner-gateway/
├─ current -> releases/<version>
├─ releases/<version>/
├─ data/
├─ secrets/
├─ tls/
└─ backups/
```

Require SSH alias `cpa` to resolve to `23.138.12.112`. Preflight requires Ubuntu 22.04+, amd64, Docker/Compose, at least 2 GiB free disk, external network `cliproxyapi_default`, running container `cli-proxy-api`, free port 2443, and existing port 2001. Resolve and verify every remote path begins `/opt/video-partner-gateway/` before creating/moving anything.

Upload the production leaf certificate/key generated by M3 from `C:\PartnerBuildDeps\tls\gateway.crt` and `gateway.key`; never upload `partner-ca.key`. Set data ownership `10001:10001`, secret/key modes `0400`, certificate `0444`. Validate Compose before switching. After `up -d`, poll `docker inspect` health for at most 90 seconds and make a pinned-CA request from the local workstation. Record container image ID and release hash in a non-secret deployment receipt.

- [ ] **Step 4: Run dry-run and shell checks**

```powershell
pwsh -File scripts/deploy-partner-gateway.tests.ps1
pwsh -File scripts/deploy-partner-gateway.ps1 -Version 0.1.0 -DryRun
```

Expected: PASS; dry-run shows no delete of data, existing `/opt/CLIProxyAPI`, or port-2001 container.

- [ ] **Step 5: Commit deployment/runbook**

```powershell
git add scripts/deploy-partner-gateway.ps1 scripts/deploy-partner-gateway.tests.ps1 docs/operations/partner-gateway-runbook.md
git commit -m "ops: deploy partner gateway safely"
```

### Task 4: Deploy gateway and create four partner identities

**Files:**
- No repository code changes; write secrets only to approved local password storage and VPS secret paths.

- [ ] **Step 1: Build and provision the new upstream key**

```powershell
pwsh -File scripts/build-partner-gateway-bundle.ps1 -Version 0.1.0
pwsh -File scripts/provision-cpa-upstream-key.ps1
```

Expected: gateway-only key is added through CLIProxyAPI PATCH hot reload and stored at `/opt/video-partner-gateway/secrets/upstream_api_key`; no key appears in terminal output or PowerShell history.

- [ ] **Step 2: Place the existing Aura secret without printing it**

Use the deployment script's `-ProvisionAuraSecret` mode, which prompts with `Read-Host -AsSecureString`, streams bytes through SSH stdin, writes `/opt/video-partner-gateway/secrets/aura_api_key` atomically, then clears the local plaintext buffer.

```powershell
pwsh -File scripts/deploy-partner-gateway.ps1 -Version 0.1.0 -ProvisionAuraSecret
```

Expected: remote file exists with owner `10001:10001`, mode `0400`; no value is returned.

- [ ] **Step 3: Deploy and verify both services**

```powershell
pwsh -File scripts/deploy-partner-gateway.ps1 -Version 0.1.0
ssh cpa "docker ps --filter name=partner-gateway --filter name=cli-proxy-api --format '{{.Names}} {{.Status}} {{.Ports}}'"
```

Expected: partner gateway is healthy on 2443; `cli-proxy-api` remains running with its original port mappings including 2001.

- [ ] **Step 4: Create the four keys one at a time**

Run on VPS and copy each one-time output directly into the user's password manager; do not redirect to disk or paste into Git/chat:

```powershell
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner create --db /var/lib/partner-gateway/gateway.db --name '天中观局'"
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner create --db /var/lib/partner-gateway/gateway.db --name '居中观'"
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner create --db /var/lib/partner-gateway/gateway.db --name '认知漫步'"
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner create --db /var/lib/partner-gateway/gateway.db --name '观局思考'"
```

Expected: four unique partner IDs/prefixes, status active, no device binding yet. Only 天中观局 key is delivered before pilot approval.

- [ ] **Step 5: Back up the new database and deployment receipt**

```powershell
ssh cpa "cd /opt/video-partner-gateway/current && docker compose exec partner-gateway /app/partner-gateway partner list --db /var/lib/partner-gateway/gateway.db"
ssh cpa "test -f /opt/video-partner-gateway/backups/deployment-0.1.0.json && echo receipt-ok"
```

Expected: sanitized list contains four names/status/prefixes but no key hash/device secret/session; receipt check prints `receipt-ok`.

### Task 5: Add pinned-CA smoke tooling and perform real model checks

**Files:**
- Create: `scripts/smoke-partner-gateway.ps1`
- Create: `scripts/smoke-partner-gateway.tests.ps1`

- [ ] **Step 1: Write mock smoke tests**

The script accepts `-CAFile`, prompts for an activation key as SecureString, activates a caller-supplied device hash, and calls:

```text
GET /v1/models
POST /v1/chat/completions model=gpt-5.6-sol reasoning_effort=medium
POST /v1/chat/completions model=grok-4.6 reasoning_effort=medium
POST /v1/images/generations model=gpt-image-2 n=1 size=1024x1024
```

Mock tests assert pinned CA is mandatory, no `-SkipCertificateCheck` switch exists, raw response/image/key is not printed, and output contains only route/model/status/request id/duration.

- [ ] **Step 2: Run and verify failure**

```powershell
pwsh -File scripts/smoke-partner-gateway.tests.ps1
```

Expected: FAIL because the script is absent.

- [ ] **Step 3: Implement the redacted smoke client**

Use `HttpClientHandler.ServerCertificateCustomValidationCallback` to build a chain against only the supplied private CA and require certificate IP SAN `23.138.12.112`; do not use `Invoke-WebRequest -SkipCertificateCheck`. Keep session and activation key in local variables, clear them in `finally`, and write generated image bytes only to a caller-supplied temporary directory outside the repository.

- [ ] **Step 4: Run mock checks; reserve real activation for the pilot PC**

```powershell
pwsh -File scripts/smoke-partner-gateway.tests.ps1
```

Expected: PASS. Do not activate 天中观局 on the build computer, because that would bind the wrong device. Run the real script only on the selected clean Windows pilot machine during Task 6.

- [ ] **Step 5: Commit smoke tooling**

```powershell
git add scripts/smoke-partner-gateway.ps1 scripts/smoke-partner-gateway.tests.ps1
git commit -m "test: add pinned gateway smoke checks"
```

### Task 6: Run the 天中观局 clean-Windows pilot

**Files:**
- Create: `docs/operations/partner-pilot-acceptance.md`
- Create during execution: `docs/operations/evidence/partner-pilot-0.1.0.md` containing sanitized text only.

- [ ] **Step 1: Prepare the clean pilot machine**

Confirm Windows and Jianying are installed; confirm `python --version`, `ffmpeg -version` and `codex --version` are absent or irrelevant to the app. Copy only the partner EXE and scenery library. Record Windows/Jianying versions, EXE SHA-256 and material-root hash summary; do not record user name, activation key or absolute home path.

- [ ] **Step 2: Activate and run real gateway smoke on the pilot machine**

Enter the 天中观局 key once in the app, then run:

```powershell
pwsh -File .\scripts\smoke-partner-gateway.ps1 -CAFile C:\PartnerBuildDeps\tls\partner-ca.crt -UseStoredPartnerCredential
```

Expected: both text models, medium reasoning and `gpt-image-2` return success; script displays no prompt, image content, URL or key.

- [ ] **Step 3: Verify single-device and startup rules**

Close/reopen the app and confirm online verify occurs before the UI. Attempt the same activation key on a second Windows machine and record only `device_mismatch`. Temporarily disable 天中观局 via the documented admin command and confirm the running/new client is blocked; re-enable it and verify recovery. Disconnect network and confirm a fresh start cannot enter.

- [ ] **Step 4: Complete one real scenery montage**

Use an approved source script, current Aura voice, bundled BGM/SFX/transitions and the copied scenery library. Verify narration audio, SRT and word-timing files exist; verify no subtitle track is inserted; generate/register the draft and open/play it in Jianying. Record project ID, durations, asset counts, draft ID and pass/fail only—not source text or absolute media paths.

- [ ] **Step 5: Complete one real image-text project**

Generate through `gpt-image-2`, verify image files remain local, create/open the resulting project or draft, and record model, ratio, item count and pass/fail without committing generated images.

- [ ] **Step 6: Verify update and rollback**

Run a newly built test version EXE, confirm activation/paths/history remain, then run the previous EXE and confirm data remains readable. Verify `%LOCALAPPDATA%\VideoProductionConsole\app` contains only current/previous versions and `data` remains untouched.

- [ ] **Step 7: Commit sanitized pilot evidence**

```powershell
git add docs/operations/partner-pilot-acceptance.md docs/operations/evidence/partner-pilot-0.1.0.md
git commit -m "test: record partner pilot acceptance"
```

Expected: evidence contains no activation/API/Aura key, private CA, session, original script, generated media, absolute home path or screenshot binary.

### Task 7: Rotate the exposed old upstream key and release remaining partners

**Files:**
- Modify: `docs/operations/partner-gateway-runbook.md` only if actual verified commands differ.

- [ ] **Step 1: Remove the old shared CLIProxy client key after pilot success**

```powershell
pwsh -File scripts/provision-cpa-upstream-key.ps1 -RemoveOldKey
```

Expected: prompt accepts the old value securely; CLIProxyAPI DELETE hot reload returns success; the gateway-only key continues to serve both real text models and image model.

- [ ] **Step 2: Re-run 天中观局 startup and model smoke**

Expected: app verifies and all three models still pass. A direct request using the removed old key receives unauthorized; do not print or log the value.

- [ ] **Step 3: Deliver the remaining three activation keys and EXE hash**

Deliver only after Tasks 1-2 pass, in this order: 居中观、认知漫步、观局思考. Each user activates on exactly one Windows PC and completes startup/path/draft smoke. Do not reuse the 天中观局 key.

- [ ] **Step 4: Verify final server state and backup**

```powershell
ssh cpa "cd /opt/video-partner-gateway/current && docker compose ps && docker compose exec partner-gateway /app/partner-gateway partner list --db /var/lib/partner-gateway/gateway.db"
```

Expected: gateway healthy; four active partners each have at most one device; no secret fields are printed; current port-2001 service remains unchanged.

- [ ] **Step 5: Commit any runbook corrections**

```powershell
git add docs/operations/partner-gateway-runbook.md
git commit -m "docs: finalize partner gateway rollout"
```

Expected: skip when no correction was required.
