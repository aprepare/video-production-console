# Builds the partner self-extracting EXE.
#
# Overlay contract written here and consumed by scripts/test-partner-package.ps1
# (and later by cmd/partner-launcher --inspect-payload):
#   [launcher PE][payload.zip][manifest.json][VPCPARTNERPAY01!][zipLen u64 LE][manifestLen u64 LE]
# Manifest is canonical UTF-8 JSON, no BOM, unique entries sorted by path,
# lowercase SHA-256, forward-slash relative names.
#
# --inspect-payload (owned by cmd/partner-launcher): print that JSON and exit 0.
# This script does not edit the launcher. test-partner-package.ps1 reads the
# same trailer when the flag is not present yet.
#
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$PythonRuntimeDir,
    [Parameter(Mandatory = $true)][string]$FFmpegDir,
    [Parameter(Mandatory = $true)][string]$MediaResourcesDir,
    [Parameter(Mandatory = $true)][string]$PinnedCAFile,
    [switch]$ValidateOnly,
    [switch]$SkipFrontend
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$versionValue = $Version.TrimStart('v')
if ($versionValue -notmatch '^\d+\.\d+\.\d+$') { throw "Version must be semver" }
$trailerMagic = "VPCPARTNERPAY01!"
$partnerGatewayURL = "https://23.138.12.112:2443"

function Get-Utf8NoBom { return New-Object System.Text.UTF8Encoding $false }

function Get-FullPath([string]$Path) {
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return [System.IO.Path]::GetFullPath($Path)
    }
    return [System.IO.Path]::GetFullPath((Join-Path (Get-Location).Path $Path))
}

function Resolve-RequiredPath {
    param([string]$Path, [string]$Label, [switch]$Directory, [switch]$File)
    if (!(Test-Path -LiteralPath $Path)) { throw "$Label not found: $Path" }
    $item = Get-Item -LiteralPath $Path
    if ($Directory -and -not $item.PSIsContainer) { throw "$Label is not a directory: $Path" }
    if ($File -and $item.PSIsContainer) { throw "$Label is not a file: $Path" }
    return $item.FullName
}

function Get-RelativeUnixPath([string]$Root, [string]$Full) {
    $rootFull = (Get-FullPath $Root).TrimEnd('\') + '\'
    $fullPath = Get-FullPath $Full
    if (-not $fullPath.StartsWith($rootFull, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "path is not under root"
    }
    return $fullPath.Substring($rootFull.Length).Replace('\', '/')
}

function Get-Sha256File([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-Sha256Bytes([byte[]]$Bytes) {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ($sha.ComputeHash($Bytes) | ForEach-Object { $_.ToString("x2") }) -join ""
    } finally {
        $sha.Dispose()
    }
}

function Get-DirectoryFingerprint([string]$Root) {
    $lines = Get-ChildItem -LiteralPath $Root -Recurse -File -Force | ForEach-Object {
        $rel = Get-RelativeUnixPath $Root $_.FullName
        "{0}  {1}" -f (Get-Sha256File $_.FullName), $rel
    } | Sort-Object
    $text = if ($lines) { ($lines -join "`n") + "`n" } else { "" }
    return Get-Sha256Bytes ((Get-Utf8NoBom).GetBytes($text))
}

function ConvertTo-JsonString([string]$Value) {
    $escaped = $Value.Replace('\', '\\').Replace('"', '\"').Replace("`n", '\n').Replace("`r", '\r').Replace("`t", '\t')
    return '"' + $escaped + '"'
}

function Test-HasMontageResource([string]$Root) {
    $files = Get-ChildItem -LiteralPath $Root -Recurse -File -Force -ErrorAction SilentlyContinue
    foreach ($file in $files) {
        $rel = Get-RelativeUnixPath $Root $file.FullName
        if ($rel -match '(?i)(^|/)(bgm|sfx|transitions?)(/|$)') { return $true }
        if ($file.Extension -match '(?i)\.(mp3|wav|aac|m4a|flac|ogg)$') { return $true }
    }
    return $false
}

function Assert-SourceTreeClean {
    param([string]$Root, [string]$Label)
    $forbidden = '(?i)(\.key$|\.pfx$|\.p12$|(^|/)\.env($|\.)|\.(db|sqlite|sqlite3)$|(^|/)cookies?(/|$)|cookie)'
    Get-ChildItem -LiteralPath $Root -Recurse -File -Force | ForEach-Object {
        $rel = Get-RelativeUnixPath $Root $_.FullName
        if ($rel -match $forbidden) {
            throw "$Label contains a forbidden file: $rel"
        }
    }
}

function Find-FFmpegExe {
    param([string]$Root, [string]$Name)
    $nested = Join-Path $Root "bin\$Name"
    if (Test-Path -LiteralPath $nested -PathType Leaf) { return $nested }
    $flat = Join-Path $Root $Name
    if (Test-Path -LiteralPath $flat -PathType Leaf) { return $flat }
    throw "$Name not found under FFmpegDir"
}

function Copy-FilteredTree {
    param([string]$Source, [string]$Dest)
    New-Item -ItemType Directory -Path $Dest -Force | Out-Null
    Get-ChildItem -LiteralPath $Source -Force | ForEach-Object {
        if ($_.Name -in @("__pycache__", ".git", ".svn", ".DS_Store", ".pytest_cache")) { return }
        $target = Join-Path $Dest $_.Name
        Copy-Item -LiteralPath $_.FullName -Destination $target -Recurse -Force
    }
    Get-ChildItem -LiteralPath $Dest -Recurse -Force | Where-Object {
        $_.Name -in @("__pycache__", ".pytest_cache", ".DS_Store", "Thumbs.db") -or $_.Extension -in @(".pyc", ".pyo", ".log")
    } | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Write-CanonicalManifest {
    param([string]$AppVersion, [string]$PayloadSha, [object[]]$Entries)
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append('{"schema_version":1,"app_version":')
    [void]$sb.Append((ConvertTo-JsonString $AppVersion))
    [void]$sb.Append(',"payload_sha256":')
    [void]$sb.Append((ConvertTo-JsonString $PayloadSha))
    [void]$sb.Append(',"entries":[')
    for ($i = 0; $i -lt $Entries.Count; $i++) {
        if ($i -gt 0) { [void]$sb.Append(',') }
        $entry = $Entries[$i]
        [void]$sb.Append('{"path":')
        [void]$sb.Append((ConvertTo-JsonString $entry.path))
        [void]$sb.Append(',"size":')
        [void]$sb.Append([int64]$entry.size)
        [void]$sb.Append(',"sha256":')
        [void]$sb.Append((ConvertTo-JsonString $entry.sha256))
        [void]$sb.Append('}')
    }
    [void]$sb.Append(']}')
    return $sb.ToString()
}

function New-PayloadZip {
    param([string]$PayloadRoot, [string]$ZipPath)
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    if (Test-Path -LiteralPath $ZipPath) { Remove-Item -LiteralPath $ZipPath -Force }
    $files = @(Get-ChildItem -LiteralPath $PayloadRoot -Recurse -File -Force | ForEach-Object {
        [pscustomobject]@{
            FullName = $_.FullName
            Rel      = Get-RelativeUnixPath $PayloadRoot $_.FullName
            Size     = [int64]$_.Length
            Sha256   = Get-Sha256File $_.FullName
        }
    })
    # Go manifest.Validate uses raw string order, not PowerShell culture/case-insensitive sort.
    [Array]::Sort($files, [System.Comparison[object]]{
            param($a, $b)
            [string]::CompareOrdinal([string]$a.Rel, [string]$b.Rel)
        })
    $zip = [System.IO.Compression.ZipFile]::Open($ZipPath, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($file in $files) {
            $entry = $zip.CreateEntry($file.Rel, [System.IO.Compression.CompressionLevel]::Optimal)
            $input = [System.IO.File]::OpenRead($file.FullName)
            try {
                $output = $entry.Open()
                try { $input.CopyTo($output) } finally { $output.Dispose() }
            } finally {
                $input.Dispose()
            }
        }
    } finally {
        $zip.Dispose()
    }
    return @($files | ForEach-Object {
            [pscustomobject]@{ path = $_.Rel; size = $_.Size; sha256 = $_.Sha256 }
        })
}

function Write-PartnerOverlay {
    param([string]$StubPath, [string]$ZipPath, [string]$ManifestJson, [string]$DestPath)
    $destDir = Split-Path -Parent $DestPath
    New-Item -ItemType Directory -Path $destDir -Force | Out-Null
    Copy-Item -LiteralPath $StubPath -Destination $DestPath -Force
    $utf8 = Get-Utf8NoBom
    $manBytes = $utf8.GetBytes($ManifestJson)
    $fs = [System.IO.File]::Open($DestPath, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
    try {
        $zip = [System.IO.File]::OpenRead($ZipPath)
        try { $zip.CopyTo($fs) } finally { $zip.Dispose() }
        $fs.Write($manBytes, 0, $manBytes.Length)
        $magic = [System.Text.Encoding]::ASCII.GetBytes($trailerMagic)
        $fs.Write($magic, 0, $magic.Length)
        $zipLen = [BitConverter]::GetBytes([uint64](Get-Item -LiteralPath $ZipPath).Length)
        $manLen = [BitConverter]::GetBytes([uint64]$manBytes.Length)
        $fs.Write($zipLen, 0, 8)
        $fs.Write($manLen, 0, 8)
        $fs.Flush()
    } finally {
        $fs.Dispose()
    }
}

$pythonRoot = Resolve-RequiredPath $PythonRuntimeDir "PythonRuntimeDir" -Directory
$ffmpegRoot = Resolve-RequiredPath $FFmpegDir "FFmpegDir" -Directory
$mediaRoot = Resolve-RequiredPath $MediaResourcesDir "MediaResourcesDir" -Directory
$caFile = Resolve-RequiredPath $PinnedCAFile "PinnedCAFile" -File

$pythonExe = Join-Path $pythonRoot "python.exe"
if (!(Test-Path -LiteralPath $pythonExe -PathType Leaf)) { throw "python.exe not found under PythonRuntimeDir" }
$draftDir = Join-Path $pythonRoot "Lib\site-packages\pyJianYingDraft"
if (!(Test-Path -LiteralPath $draftDir)) { throw "pyJianYingDraft not found under PythonRuntimeDir" }
$ffmpegExe = Find-FFmpegExe -Root $ffmpegRoot -Name "ffmpeg.exe"
$ffprobeExe = Find-FFmpegExe -Root $ffmpegRoot -Name "ffprobe.exe"
if (-not $ffmpegExe -or -not $ffprobeExe) { throw "ffmpeg.exe/ffprobe.exe not found under FFmpegDir" }
if (-not (Test-HasMontageResource $mediaRoot)) {
    throw "MediaResourcesDir must contain at least one BGM/SFX/transition resource"
}

$pem = [System.IO.File]::ReadAllText($caFile)
if ($pem -notmatch '-----BEGIN CERTIFICATE-----') { throw "PinnedCAFile must contain BEGIN CERTIFICATE" }
if ($pem -match 'PRIVATE KEY') { throw "PinnedCAFile must not contain a PRIVATE KEY" }

Assert-SourceTreeClean $pythonRoot "PythonRuntimeDir"
Assert-SourceTreeClean $ffmpegRoot "FFmpegDir"
Assert-SourceTreeClean $mediaRoot "MediaResourcesDir"

$pythonHash = Get-DirectoryFingerprint $pythonRoot
$ffmpegHash = Get-DirectoryFingerprint $ffmpegRoot
$mediaHash = Get-DirectoryFingerprint $mediaRoot
$caHash = Get-Sha256File $caFile

if ($ValidateOnly) {
    Write-Host "Partner build inputs validated for $versionValue"
    return
}

$packageDir = Join-Path $repoRoot "release\partner\$versionValue"
$exeName = "video-production-console-partner-$versionValue.exe"
$exePath = Join-Path $packageDir $exeName
if (Test-Path -LiteralPath $packageDir) { throw "Partner release already exists: $packageDir" }

$launcherSrc = Join-Path $repoRoot "cmd\partner-launcher"
if (!(Test-Path -LiteralPath $launcherSrc)) {
    throw "partner launcher source not found: cmd/partner-launcher"
}

$commit = (git -C $repoRoot rev-parse --short=12 HEAD).Trim()
$built = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X video-production-console/internal/buildinfo.Version=$versionValue -X video-production-console/internal/buildinfo.Commit=$commit -X video-production-console/internal/buildinfo.BuildTime=$built -X video-production-console/internal/partneredition.builtEdition=partner -X video-production-console/internal/partneredition.builtGatewayURL=$partnerGatewayURL"

$work = Join-Path $env:TEMP ("vpc-partner-build-" + [guid]::NewGuid().ToString("N"))
$payload = Join-Path $work "payload"
New-Item -ItemType Directory -Path $payload | Out-Null

Push-Location $repoRoot
try {
    if ($SkipFrontend) {
        Write-Host "Skipping frontend embed rebuild; verifying existing internal/webui/dist"
        & (Join-Path $PSScriptRoot "check-embedded-dist.ps1")
    } else {
        npm --prefix web run build:embed
        & (Join-Path $PSScriptRoot "check-embedded-dist.ps1") -Strict
    }

    New-Item -ItemType Directory -Path (Join-Path $payload "bin") | Out-Null
    $consoleOut = Join-Path $payload "bin\video-production-console.exe"
    go build -trimpath -ldflags $ldflags -o $consoleOut ./cmd/console

    Copy-FilteredTree $pythonRoot (Join-Path $payload "runtime\python")
    if (Test-Path -LiteralPath (Join-Path $ffmpegRoot "bin\ffmpeg.exe")) {
        Copy-FilteredTree $ffmpegRoot (Join-Path $payload "runtime\ffmpeg")
    } else {
        $ffmpegDest = Join-Path $payload "runtime\ffmpeg\bin"
        New-Item -ItemType Directory -Path $ffmpegDest -Force | Out-Null
        Copy-Item -LiteralPath $ffmpegExe -Destination (Join-Path $ffmpegDest "ffmpeg.exe")
        Copy-Item -LiteralPath $ffprobeExe -Destination (Join-Path $ffmpegDest "ffprobe.exe")
    }
    Copy-FilteredTree $mediaRoot (Join-Path $payload "resources\montage")
    New-Item -ItemType Directory -Path (Join-Path $payload "resources\tls") | Out-Null
    Copy-Item -LiteralPath $caFile -Destination (Join-Path $payload "resources\tls\partner-ca.crt")
    New-Item -ItemType Directory -Path (Join-Path $payload "schemas") | Out-Null
    Copy-Item -LiteralPath (Join-Path $repoRoot "schemas\settings.schema.json") -Destination (Join-Path $payload "schemas\settings.schema.json")
    Copy-Item -LiteralPath (Join-Path $repoRoot "schemas\task-manifest.schema.json") -Destination (Join-Path $payload "schemas\task-manifest.schema.json")

    $skillNames = @("finance-topic-selector", "finance-viral-remix", "jianying-montage-draft")
    foreach ($skillName in $skillNames) {
        $skillsSource = Join-Path $env:USERPROFILE ".codex\skills\$skillName"
        if (!(Test-Path -LiteralPath $skillsSource)) {
            throw "$skillName skill not found: $skillsSource"
        }
        Copy-FilteredTree $skillsSource (Join-Path $payload "skills\$skillName")
    }
    $movieSkill = Join-Path $env:USERPROFILE ".codex\skills\jianying-movie-montage"
    if (Test-Path -LiteralPath $movieSkill) {
        Copy-FilteredTree $movieSkill (Join-Path $payload "skills\jianying-movie-montage")
    }

    $requiredAfterCopy = @(
        (Join-Path $payload "bin\video-production-console.exe"),
        (Join-Path $payload "runtime\python\python.exe"),
        (Join-Path $payload "runtime\python\Lib\site-packages\pyJianYingDraft"),
        (Join-Path $payload "runtime\ffmpeg\bin\ffmpeg.exe"),
        (Join-Path $payload "runtime\ffmpeg\bin\ffprobe.exe"),
        (Join-Path $payload "resources\tls\partner-ca.crt"),
        (Join-Path $payload "resources\montage"),
        (Join-Path $payload "schemas\settings.schema.json"),
        (Join-Path $payload "schemas\task-manifest.schema.json"),
        (Join-Path $payload "skills\jianying-montage-draft\SKILL.md"),
        (Join-Path $payload "skills\jianying-montage-draft\scripts\run_montage_job.py"),
        (Join-Path $payload "skills\finance-viral-remix\SKILL.md"),
        (Join-Path $payload "skills\finance-topic-selector\SKILL.md")
    )
    foreach ($path in $requiredAfterCopy) {
        if (!(Test-Path -LiteralPath $path)) { throw "payload assembly missing: $path" }
    }
    Assert-SourceTreeClean $payload "payload"

    $zipPath = Join-Path $work "payload.zip"
    $entries = New-PayloadZip -PayloadRoot $payload -ZipPath $zipPath
    $payloadHash = Get-Sha256File $zipPath
    $manifestJson = Write-CanonicalManifest -AppVersion $versionValue -PayloadSha $payloadHash -Entries $entries

    $stubPath = Join-Path $work "launcher-stub.exe"
    go build -trimpath -ldflags $ldflags -o $stubPath ./cmd/partner-launcher

    New-Item -ItemType Directory -Path $packageDir | Out-Null
    Write-PartnerOverlay -StubPath $stubPath -ZipPath $zipPath -ManifestJson $manifestJson -DestPath $exePath
    $exeHash = Get-Sha256File $exePath
    Set-Content -LiteralPath "$exePath.sha256" -Value "$exeHash  $exeName" -Encoding ASCII

    $metadata = @"
{"version":$(ConvertTo-JsonString $versionValue),"commit":$(ConvertTo-JsonString $commit),"build_time":$(ConvertTo-JsonString $built),"dependency_hashes":{"python_runtime_sha256":$(ConvertTo-JsonString $pythonHash),"ffmpeg_sha256":$(ConvertTo-JsonString $ffmpegHash),"media_resources_sha256":$(ConvertTo-JsonString $mediaHash),"pinned_ca_sha256":$(ConvertTo-JsonString $caHash),"payload_sha256":$(ConvertTo-JsonString $payloadHash)}}
"@
    if ($metadata -match 'PRIVATE KEY|api_key|session_token|C:\\Users\\|C:\\PartnerBuildDeps') {
        throw "BUILD-METADATA.json would contain forbidden content"
    }
    [System.IO.File]::WriteAllText((Join-Path $packageDir "BUILD-METADATA.json"), $metadata.Trim(), (Get-Utf8NoBom))
    Write-Host "Wrote $exePath"
} finally {
    Pop-Location
    if (Test-Path -LiteralPath $work) {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }
}
