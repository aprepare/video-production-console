# 2026-09-04 Baseline and Unified Build

## Existing changes

- [x] Production baseline recorded in `75e4f5e` (`feat: checkpoint current production workflows`), covering production code, tests, frontend source, and `webui` dist; these cross-stack and generated artifacts remain one revertible production baseline.
- [x] Local artifact baseline recorded in `0a72eda` (`chore: checkpoint local research and migration artifacts`), covering local research, migration, experiments, and media assets as an independent baseline.

## Next unified build

- [ ] Implement the root unified build entry point. It must derive `repoRoot` from
  the entry point's own location (for example, with
  `git -C "$(dirname "$0")" rev-parse --show-toplevel`), set
  `projectDir="${repoRoot}/video-production-console"`, and `cd` to
  `projectDir` before running anything; it must not depend on the caller's
  current directory.

After changing to `${repoRoot}/video-production-console`, the entry point must
run these commands in strict order with immediate non-zero exit on failure
(for example, `set -e`):

1. `npm --prefix web run build:embed`
2. `go test ./...`
3. `go build -o dist/video-production-console.exe ./cmd/console`

Any failed step must exit immediately with a non-zero status.
