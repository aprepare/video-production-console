param([Parameter(Mandatory=$true)][string]$Version)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$versionValue = $Version.TrimStart('v')
if ($versionValue -notmatch '^\d+\.\d+\.\d+$') { throw "Version must be semver" }
$packageDir = Join-Path $root "release/$versionValue"
if (Test-Path $packageDir) { throw "Release already exists: $packageDir" }
$archive = "$packageDir.zip"
if (Test-Path $archive) { throw "Release archive already exists: $archive" }
$commit = (git -C $root rev-parse --short=12 HEAD).Trim()
$built = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")

Push-Location $root
try {
    npm --prefix web run build:embed
    & "$PSScriptRoot/check-embedded-dist.ps1" -Strict
    New-Item -ItemType Directory -Path $packageDir | Out-Null
    $ldflags = "-s -w -X video-production-console/internal/buildinfo.Version=$versionValue -X video-production-console/internal/buildinfo.Commit=$commit -X video-production-console/internal/buildinfo.BuildTime=$built"
    go build -trimpath -ldflags $ldflags -o (Join-Path $packageDir "video-production-console.exe") ./cmd/console
    go build -trimpath -ldflags $ldflags -o (Join-Path $packageDir "console-maintenance.exe") ./cmd/maintenance
    $actual = & (Join-Path $packageDir "video-production-console.exe") --version
    if ($actual -notmatch [regex]::Escape($versionValue) -or $actual -notmatch [regex]::Escape($commit)) {
        throw "Built version metadata mismatch: $actual"
    }
    Copy-Item schemas (Join-Path $packageDir "schemas") -Recurse
    Copy-Item README.md (Join-Path $packageDir "README.md")
    $hashes = Get-ChildItem $packageDir -File -Recurse | Sort-Object FullName | ForEach-Object {
        $relative = [IO.Path]::GetRelativePath($packageDir, $_.FullName).Replace('\', '/')
        "$((Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant())  $relative"
    }
    Set-Content (Join-Path $packageDir "SHA256SUMS.txt") $hashes -Encoding utf8NoBOM
    Compress-Archive -Path (Join-Path $packageDir '*') -DestinationPath $archive
    Set-Content "$archive.sha256" "$((Get-FileHash $archive -Algorithm SHA256).Hash.ToLowerInvariant())  $([IO.Path]::GetFileName($archive))" -Encoding ascii
} finally {
    Pop-Location
}
