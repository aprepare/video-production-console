param([switch]$StrictEmbeddedDist)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "go test failed ($LASTEXITCODE)" }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "go vet failed ($LASTEXITCODE)" }
    npm --prefix web run lint
    if ($LASTEXITCODE -ne 0) { throw "frontend lint failed ($LASTEXITCODE)" }
    npm --prefix web run test -- --maxWorkers=2
    if ($LASTEXITCODE -ne 0) { throw "frontend tests failed ($LASTEXITCODE)" }
    npm --prefix web run test:e2e -- --workers=2
    if ($LASTEXITCODE -ne 0) { throw "browser tests failed ($LASTEXITCODE)" }
    npm --prefix web run build:verify
    if ($LASTEXITCODE -ne 0) { throw "frontend build failed ($LASTEXITCODE)" }
    & "$PSScriptRoot/check-embedded-dist.ps1" -Strict:$StrictEmbeddedDist
} finally {
    Pop-Location
}
