# Builds the Linux partner-gateway deployment bundle.
# Output: release/gateway/<version>/{partner-gateway,Dockerfile,compose.yml,gateway.env,SHA256SUMS.txt}
param(
    [Parameter(Mandatory = $true)][string]$Version
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$versionValue = $Version.TrimStart('v')
if ($versionValue -notmatch '^\d+\.\d+\.\d+$') {
    throw "Version must be semver"
}

$allowedNames = @(
    "partner-gateway",
    "Dockerfile",
    "compose.yml",
    "gateway.env",
    "SHA256SUMS.txt"
)

function Get-Utf8NoBom {
    return New-Object System.Text.UTF8Encoding $false
}

function Get-Sha256File([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Write-TextFile([string]$Path, [string]$Content) {
    $encoding = Get-Utf8NoBom
    [System.IO.File]::WriteAllText($Path, $Content, $encoding)
}

function Test-WindowsExecutable([string]$Path) {
    $name = [System.IO.Path]::GetFileName($Path)
    if ($name -match '\.exe$') {
        return $true
    }
    $stream = [System.IO.File]::Open($Path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read)
    try {
        if ($stream.Length -lt 2) {
            return $false
        }
        $first = $stream.ReadByte()
        $second = $stream.ReadByte()
        return ($first -eq 0x4D -and $second -eq 0x5A)
    } finally {
        $stream.Dispose()
    }
}

function Test-SecretBearingEnv([string]$Path) {
    $lines = Get-Content -LiteralPath $Path
    foreach ($line in $lines) {
        $trimmed = $line.Trim()
        if ($trimmed -eq "" -or $trimmed.StartsWith("#")) {
            continue
        }
        $eq = $trimmed.IndexOf("=")
        if ($eq -lt 1) {
            continue
        }
        $key = $trimmed.Substring(0, $eq)
        $value = $trimmed.Substring($eq + 1)
        $keyUpper = $key.ToUpperInvariant()
        $isSecretName = ($keyUpper -match 'PASSWORD|SECRET|TOKEN|AUTHORIZATION') -or (
            $keyUpper -match 'API_KEY$' -and $keyUpper -notmatch '_FILE$'
        )
        if (-not $isSecretName) {
            continue
        }
        if ($value.Trim() -ne "") {
            return $true
        }
    }
    return $false
}

function Assert-CleanBundle([string]$BundleDir) {
    $items = @(Get-ChildItem -LiteralPath $BundleDir -Force)
    $names = @($items | ForEach-Object { $_.Name } | Sort-Object)
    $expected = @($allowedNames | Sort-Object)
    if ($names.Count -ne $expected.Count) {
        throw "bundle must contain exactly: $($allowedNames -join ', ')"
    }
    for ($i = 0; $i -lt $expected.Count; $i++) {
        if ($names[$i] -ne $expected[$i]) {
            throw "bundle must contain exactly: $($allowedNames -join ', ')"
        }
    }

    foreach ($item in $items) {
        if ($item.PSIsContainer) {
            throw "bundle must not contain directories"
        }
        $name = $item.Name
        $ext = [System.IO.Path]::GetExtension($name).ToLowerInvariant()
        if ($ext -in @(".key", ".crt", ".db") -or $name -eq "config.yaml" -or $ext -eq ".log") {
            throw "bundle contains forbidden file: $name"
        }
        if (Test-WindowsExecutable $item.FullName) {
            throw "bundle must not contain a Windows executable"
        }
        if ($ext -eq ".env" -or $name -eq "gateway.env") {
            if (Test-SecretBearingEnv $item.FullName) {
                throw "bundle .env contains secret values"
            }
        }
    }
}

$deployDir = Join-Path $repoRoot "deploy\partner-gateway"
$dockerfile = Join-Path $deployDir "Dockerfile"
$composeFile = Join-Path $deployDir "compose.yml"
foreach ($required in @($dockerfile, $composeFile)) {
    if (!(Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "missing template: $required"
    }
}

$bundleDir = Join-Path $repoRoot "release\gateway\$versionValue"
if (Test-Path -LiteralPath $bundleDir) {
    Remove-Item -LiteralPath $bundleDir -Recurse -Force
}
New-Item -ItemType Directory -Path $bundleDir -Force | Out-Null

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    throw "go is not available on PATH; cannot cross-compile partner-gateway"
}

$binaryPath = Join-Path $bundleDir "partner-gateway"
$previousCgo = $env:CGO_ENABLED
$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $buildArgs = @(
        "build",
        "-o", $binaryPath,
        ".\cmd\partner-gateway"
    )
    Push-Location $repoRoot
    try {
        & go @buildArgs
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
} finally {
    if ($null -eq $previousCgo) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $previousCgo }
    if ($null -eq $previousGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousGoos }
    if ($null -eq $previousGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousGoarch }
}

Copy-Item -LiteralPath $dockerfile -Destination (Join-Path $bundleDir "Dockerfile") -Force
Copy-Item -LiteralPath $composeFile -Destination (Join-Path $bundleDir "compose.yml") -Force

$gatewayEnv = @"
PARTNER_GATEWAY_VERSION=$versionValue
PARTNER_GATEWAY_LISTEN=:2443
PARTNER_GATEWAY_DB=/var/lib/partner-gateway/gateway.db
PARTNER_GATEWAY_TLS_CERT=/run/tls/gateway.crt
PARTNER_GATEWAY_TLS_KEY=/run/tls/gateway.key
PARTNER_GATEWAY_UPSTREAM=http://cli-proxy-api:2001/v1
PARTNER_GATEWAY_UPSTREAM_KEY_FILE=/run/secrets/upstream_api_key
PARTNER_GATEWAY_AURA_KEY_FILE=/run/secrets/aura_api_key
PARTNER_GATEWAY_ADMIN_KEY_FILE=/run/secrets/admin_password
PARTNER_GATEWAY_AURA_BASE_URL=https://tts.aurastd.com
PARTNER_GATEWAY_AURA_MODEL=speech-2.8-hd
PARTNER_GATEWAY_AURA_VOICE_ID=moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67
PARTNER_GATEWAY_SESSION_TTL=12h
"@
Write-TextFile (Join-Path $bundleDir "gateway.env") ($gatewayEnv.TrimEnd() + "`n")

$sumLines = foreach ($name in @("partner-gateway", "Dockerfile", "compose.yml", "gateway.env")) {
    $hash = Get-Sha256File (Join-Path $bundleDir $name)
    "{0}  {1}" -f $hash, $name
}
Write-TextFile (Join-Path $bundleDir "SHA256SUMS.txt") (($sumLines -join "`n") + "`n")

Assert-CleanBundle $bundleDir
Write-Host "bundle ready: $bundleDir"
