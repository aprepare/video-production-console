# Partner pilot acceptance checklist

Template for a clean-Windows pilot of the partner EXE against the pinned
gateway. Fill this on the pilot machine after Task 6. Do not paste secrets,
activation keys, private CA material, sessions, original scripts, generated
media, absolute home paths, or screenshot binaries into this file or into Git.

Use a separate sanitized evidence note under `docs/operations/evidence/` when
the live pilot is actually run. This file is the checklist only.

## Machine (no user name, no home path)

- [ ] Windows version: ________________
- [ ] Jianying version: ________________
- [ ] `python`, `ffmpeg`, and `codex` are absent or unused by the app
- [ ] Only the partner EXE and the scenery library were copied
- [ ] EXE SHA-256: ________________
- [ ] Scenery library hash summary: ________________

## Activation and gateway smoke

Run on the pilot PC only, with the pinned CA file. Do not activate a partner
on a build or operator computer.

```powershell
pwsh -File .\scripts\smoke-partner-gateway.ps1 -CAFile <pinned-ca> -UseStoredPartnerCredential -DeviceHash <pilot-device-hash> -ImageOutputDir <temp-dir-outside-repo>
```

- [ ] `GET /v1/models` status 200
- [ ] `POST /v1/chat/completions` model `gpt-5.6-sol` reasoning `medium` status 200
- [ ] `POST /v1/chat/completions` model `grok-4.6` reasoning `medium` status 200
- [ ] `POST /v1/images/generations` model `gpt-image-2` n=1 size=1024x1024 status 200
- [ ] Smoke output contains only route, model, status, request id, and duration
- [ ] First app launch accepts the activation key once and binds this device
- [ ] Restart verifies online before the UI
- [ ] Second Windows machine with the same key records only `device_mismatch`
- [ ] Disable blocks the client; enable recovers
- [ ] Offline start cannot enter

## Scenery montage

- [ ] Approved source script (do not paste the script)
- [ ] Current Aura voice
- [ ] Bundled BGM / SFX / transitions
- [ ] Copied scenery library
- [ ] Narration audio present
- [ ] SRT present
- [ ] Word-timing file present
- [ ] No subtitle track inserted into the draft
- [ ] Draft generated, registered, and playable in Jianying
- [ ] Project id: ________
- [ ] Durations: ________
- [ ] Asset counts: ________
- [ ] Draft id: ________
- [ ] Result: pass / fail

## Image-text project

- [ ] Generated through `gpt-image-2`
- [ ] Image files remain local (not committed)
- [ ] Project or draft opens
- [ ] Model: `gpt-image-2`
- [ ] Ratio: ________
- [ ] Item count: ________
- [ ] Result: pass / fail

## Update and rollback

- [ ] Newer test EXE keeps activation, paths, and history
- [ ] Previous EXE still reads the same data
- [ ] `%LOCALAPPDATA%\VideoProductionConsole\app` contains only current and previous versions
- [ ] `data` remains untouched

## Evidence rules

Record pass/fail, versions, hashes, ids, counts, and durations only. Never
commit activation keys, API keys, Aura keys, the private CA, session tokens,
source text, generated images, or `.mp4` / `.png` binaries from the pilot.
