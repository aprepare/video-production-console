# 2026-09-04 Baseline and Unified Build

## Existing changes

- [ ] Commit Remix Lab full-stack changes together, including runtime, HTTP API, store, domain, web UI, tests, and generated `internal/webui/dist` assets.
- [ ] Commit AI Shorts and Jianying draft workflow changes together, including `internal/aishorts`, `internal/jianyingdraft`, API handlers, scripts, web UI, and tests.
- [ ] Commit local migration artifacts under `.tmp-rename` as a separately revertible checkpoint.
- [ ] Commit experimental prompt/library HTML, JSON, Python, Chinese-script, and PNG assets as a separately revertible artifact checkpoint after confirming they are user files.

## Next unified build

The root unified build must run these commands in order:

1. `npm --prefix web run build:embed`
2. `go test ./...`
3. `go build -o dist/video-production-console.exe ./cmd/console`

Any failed step must exit immediately with a non-zero status.
