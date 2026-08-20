# Partner gateway operations runbook

Safe deploy, backup, disable, unbind, rotate, and rollback for the partner
gateway on host alias `cpa` (`23.138.12.112`). This document contains no
secrets, keys, or certificate material.

## Layout

```text
/opt/video-partner-gateway/
├─ current -> releases/<version>
├─ releases/<version>/
├─ data/
├─ secrets/
├─ tls/
└─ backups/
```

The gateway publishes `2443` only. The existing `cli-proxy-api` container and
its published port `2001` stay running. Do not modify that Compose project or
delete its data directory.

Open the partner ledger at `https://23.138.12.112:2443/admin` after accepting
the pinned certificate warning. Sign in with the admin password stored in
`/opt/video-partner-gateway/secrets/admin_password`. The page can create,
enable, disable, rotate, and unbind partners. Activation keys appear once in
the browser and are not written back to the list.

## Deploy

From the repository root on an operator workstation:

```powershell
powershell -File scripts/build-partner-gateway-bundle.ps1 -Version 0.1.0
powershell -File scripts/provision-cpa-upstream-key.ps1
powershell -File scripts/deploy-partner-gateway.ps1 -Version 0.1.0 -ProvisionAuraSecret
powershell -File scripts/deploy-partner-gateway.ps1 -Version 0.1.0
```

The deploy script:

1. Checks disk, memory, port `2443`, Docker network `cliproxyapi_default`, and
   the running `cli-proxy-api` container.
2. Creates `/opt/video-partner-gateway/releases/<version>`.
3. Uploads the five-file bundle and the leaf `gateway.crt` / `gateway.key`.
   It never uploads `partner-ca.key`.
4. Verifies `SHA256SUMS.txt` and `docker compose config --quiet`.
5. Backs up current compose/env/TLS metadata and SQLite.
6. Switches `current` atomically, builds, and starts the service.
7. Waits until the container is healthy and confirms port `2001` is unchanged.

Dry-run the same sequence without writing the server:

```powershell
powershell -File scripts/deploy-partner-gateway.ps1 -Version 0.1.0 -DryRun
```

## Backup

A deploy writes a timestamped copy under `/opt/video-partner-gateway/backups/`
and a non-secret receipt `deployment-<version>.json` (version, image id,
release hash).

Manual extra copy after partner identity changes:

```bash
ssh cpa 'install -d /opt/video-partner-gateway/backups && cp -a /opt/video-partner-gateway/data/gateway.db /opt/video-partner-gateway/backups/gateway-$(date +%Y%m%d-%H%M%S).db'
```

## Disable

Disable a partner immediately. Existing sessions fail on the next request.

```bash
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner disable --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'"
```

Re-enable with `partner enable` and the same `--id`.

## Unbind

Clear the bound device so a replacement Windows PC can activate. The previous
device secret and sessions become invalid.

```bash
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner unbind --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'"
```

## Rotate

Rotate a partner activation key. The new value prints once; copy it into a
password manager and do not save it in Git or chat.

```bash
ssh -t cpa "cd /opt/video-partner-gateway/current && docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner rotate-key --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'"
```

Rotate the isolated upstream client key after a successful pilot:

```powershell
powershell -File scripts/provision-cpa-upstream-key.ps1 -RemoveOldKey
```

The prompt reads the old key as a SecureString and sends it through SSH stdin.
The command prints only the HTTP status.

## Rollback

If the new release is not healthy, the deploy script restores the previous
`current` symlink and runs `docker compose up -d` there. It does not delete the
failed release directory or any files under `data/`.

Manual rollback to a known good version:

```bash
ssh cpa 'ln -sfn /opt/video-partner-gateway/releases/<previous-version> /opt/video-partner-gateway/current.new && mv -Tf /opt/video-partner-gateway/current.new /opt/video-partner-gateway/current && cd /opt/video-partner-gateway/current && docker compose -f compose.yml --env-file gateway.env up -d'
```

Restore SQLite only from a verified backup when the database itself is the
fault. Stop the gateway container first, copy the backup over
`/opt/video-partner-gateway/data/gateway.db`, then start the same current
release again.

## Health

```bash
ssh cpa "docker ps --filter name=partner-gateway --filter name=cli-proxy-api --format '{{.Names}} {{.Status}} {{.Ports}}'"
```

Expect `partner-gateway` healthy on `2443` and `cli-proxy-api` still publishing
`2001`.
