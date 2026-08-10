param([switch]$StrictEmbeddedDist)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    go test ./...
    go vet ./...
    npm --prefix web run lint
    npm --prefix web run test
    npm --prefix web run test:e2e
    npm --prefix web run build:verify
    & "$PSScriptRoot/check-embedded-dist.ps1" -Strict:$StrictEmbeddedDist
} finally {
    Pop-Location
}
