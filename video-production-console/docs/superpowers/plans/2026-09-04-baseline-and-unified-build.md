# 2026-09-04 Baseline and Unified Build

## Existing changes

- [x] Production baseline recorded in `75e4f5e` (`feat: checkpoint current production workflows`), covering production code, tests, frontend source, and `webui` dist; these cross-stack and generated artifacts remain one revertible production baseline.
- [x] Local artifact baseline recorded in `0a72eda` (`chore: checkpoint local research and migration artifacts`), covering local research, migration, experiments, and media assets as an independent baseline.

## Next unified build

The root unified build must run these commands in order:

1. `npm --prefix web run build:embed`
2. `go test ./...`
3. `go build -o dist/video-production-console.exe ./cmd/console`

Any failed step must exit immediately with a non-zero status.
