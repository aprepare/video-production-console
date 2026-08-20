# Partner gateway deployment

Independent Docker service for the partner gateway. It attaches to the existing
external Docker network `cliproxyapi_default` and publishes only port `2443`.
The existing `cli-proxy-api` service and its published port `2001` stay unchanged.

## Bundle

Build a Linux amd64 bundle from the repository root:

```powershell
powershell -File scripts/build-partner-gateway-bundle.ps1 -Version 0.1.0
```

The output directory `release/gateway/<version>/` contains exactly:

- `partner-gateway`
- `Dockerfile`
- `compose.yml`
- `gateway.env`
- `SHA256SUMS.txt`

The bundle never includes TLS private keys, certificates, SQLite files, secret
values, `config.yaml`, logs, or a Windows executable.

## Server layout

```text
/opt/video-partner-gateway/
├─ current -> releases/<version>
├─ releases/<version>/
├─ data/
├─ secrets/
├─ tls/
└─ backups/
```

Release directories keep the Compose file and Linux binary. Persistent data,
secrets, and TLS material live in the sibling directories above and are linked
into the active release so volume paths `./data`, `./tls`, and `./secrets` stay
stable across versions.

## Configuration

Copy `gateway.env.example` to `gateway.env` on the server. Values are non-secret
paths and public settings:

- listen `:2443`
- SQLite `/var/lib/partner-gateway/gateway.db`
- TLS files under `/run/tls`
- upstream `http://cli-proxy-api:2001/v1`
- secret *file paths* under `/run/secrets`
- Aura base URL, model, and voice id
- 12-hour session TTL

Secret material is written only to `/opt/video-partner-gateway/secrets/` with
owner `10001:10001` and mode `0400`. Do not put secret values in Compose YAML,
`gateway.env`, Git, or the image.

The operator ledger is `https://<gateway-host>:2443/admin`. It requires the
admin password file and never lists stored activation keys.

## Local-only admin commands

Run these on the VPS from `/opt/video-partner-gateway/current`. They are not
published on the public port.

```bash
docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner list --db /var/lib/partner-gateway/gateway.db
docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner create --db /var/lib/partner-gateway/gateway.db --name '<display-name>'
docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner enable --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'
docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner disable --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'
docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner rotate-key --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'
docker compose -f compose.yml exec partner-gateway /app/partner-gateway partner unbind --db /var/lib/partner-gateway/gateway.db --id '<partner-id>'
```

`create` and `rotate-key` print a one-time activation key. Copy it into a
password manager; do not redirect it to disk, chat, or Git.

## Related scripts

- `scripts/provision-cpa-upstream-key.ps1` — isolated upstream client key
- `scripts/deploy-partner-gateway.ps1` — backup, upload, health, rollback
- `scripts/smoke-partner-gateway.ps1` — pinned-CA smoke checks
- `docs/operations/partner-gateway-runbook.md` — operations
