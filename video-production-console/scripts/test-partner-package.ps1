# Validates a partner self-extracting EXE and its sidecar .sha256.
#
# Launcher inspect contract:
#   video-production-console-partner-<version>.exe --inspect-payload
#   prints the canonical UTF-8 JSON payload manifest to stdout and exits 0
#   without extracting files. Manifest shape:
#     {"schema_version":1,"app_version":"...","payload_sha256":"...","entries":[{"path":"...","size":N,"sha256":"..."}]}
# Overlay fallback (used when the flag is not on cmd/partner-launcher yet):
#   [launcher PE][payload.zip][manifest.json][VPCPARTNERPAY01!][zipLen u64 LE][manifestLen u64 LE]
#
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$PackageDir,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$versionValue = $Version.TrimStart('v')
$trailerMagic = "VPCPARTNERPAY01!"
$requiredPaths = @(
    "bin/video-production-console.exe",
    "runtime/python/python.exe",
    "runtime/python/Lib/site-packages/pyJianYingDraft/",
    "runtime/ffmpeg/bin/ffmpeg.exe",
    "runtime/ffmpeg/bin/ffprobe.exe",
    "resources/tls/partner-ca.crt",
    "resources/montage/",
    "schemas/settings.schema.json",
    "schemas/task-manifest.schema.json"
)

function Get-Utf8NoBom {
    return New-Object System.Text.UTF8Encoding $false
}

function Get-Sha256Bytes([byte[]]$Bytes) {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ($sha.ComputeHash($Bytes) | ForEach-Object { $_.ToString("x2") }) -join ""
    } finally {
        $sha.Dispose()
    }
}

function Get-Sha256File([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function ConvertTo-ForwardSlash([string]$Path) {
    return $Path.Replace('\', '/')
}

function Test-HasRequiredPath {
    param([string[]]$EntryPaths, [string]$Required)
    $normalized = $EntryPaths | ForEach-Object { ConvertTo-ForwardSlash $_ }
    if ($Required.EndsWith('/')) {
        foreach ($path in $normalized) {
            if ($path.StartsWith($Required, [System.StringComparison]::OrdinalIgnoreCase)) { return $true }
            if ((ConvertTo-ForwardSlash $path).TrimEnd('/') + '/' -eq $Required) { return $true }
        }
        return $false
    }
    foreach ($path in $normalized) {
        if ([string]::Equals($path, $Required, [System.StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    return $false
}

function Assert-NoLeakage {
    param([string[]]$EntryPaths, [object[]]$TextSnippets)
    $forbiddenName = '(?i)(\.key$|\.pfx$|\.p12$|(^|/)\.env($|\.)|\.(db|sqlite|sqlite3)$|\.log$|(^|/)logs/|cookie|api[_-]?key|session_token|(^|/)secrets?(/|$)|credentials)'
    foreach ($path in $EntryPaths) {
        $unix = ConvertTo-ForwardSlash $path
        if ($unix -match $forbiddenName) {
            throw "payload contains forbidden path: $unix"
        }
        if ($unix -match '(?i)(C:/Users/|C:\\Users\\|C:/PartnerBuildDeps|C:\\PartnerBuildDeps|\\.codex/|worktrees/)') {
            throw "payload contains a development workspace absolute path: $unix"
        }
        if ($unix -match '(?i)\.exe$' -and -not ($unix.StartsWith('bin/', [System.StringComparison]::OrdinalIgnoreCase) -or $unix.StartsWith('runtime/', [System.StringComparison]::OrdinalIgnoreCase))) {
            throw "unlisted .exe outside bin/runtime: $unix"
        }
    }
    foreach ($snippet in $TextSnippets) {
        if ($null -eq $snippet) { continue }
        $text = $null
        $path = ""
        if ($snippet -is [string]) {
            $text = $snippet
        } else {
            $text = [string]$snippet.Text
            $path = [string]$snippet.Path
        }
        if ([string]::IsNullOrEmpty($text)) { continue }
        if ($text -match 'PRIVATE KEY') { throw "payload contains a private key in $path" }
        if ($text -match '23\.138\.12\.112:2001') { throw "payload contains an upstream secret in $path" }
        $schemaPayload = $path -match '(?i)^schemas/'
        if (-not $schemaPayload -and $text -match '(?i)(api[_-]?key|session_token)') {
            throw "payload contains an upstream secret in $path"
        }
        if ($text -match '(?i)(C:\\Users\\|C:/Users/|C:\\PartnerBuildDeps|\\.codex\\|worktrees\\)') {
            throw "payload contains a development workspace absolute path"
        }
        if ($repoRoot -and $text.IndexOf($repoRoot, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
            throw "payload contains a development workspace absolute path"
        }
    }
}

function Read-PartnerOverlay {
    param([string]$ExePath)
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $file = Get-Item -LiteralPath $ExePath
    $fs = [System.IO.File]::Open($ExePath, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read)
    $zipTemp = Join-Path ([System.IO.Path]::GetTempPath()) ("vpc-inspect-" + [guid]::NewGuid().ToString("N") + ".zip")
    try {
        if ($file.Length -lt 32) { throw "overlay trailer missing" }
        [void]$fs.Seek(-32, [System.IO.SeekOrigin]::End)
        $trailer = New-Object byte[] 32
        if ($fs.Read($trailer, 0, 32) -ne 32) { throw "overlay trailer truncated" }
        $magic = [System.Text.Encoding]::ASCII.GetString($trailer, 0, 16)
        if ($magic -ne $trailerMagic) { throw "invalid overlay magic: $magic" }
        $zipLen = [BitConverter]::ToUInt64($trailer, 16)
        $manLen = [BitConverter]::ToUInt64($trailer, 24)
        if ($zipLen -eq [uint64]0 -or $manLen -eq [uint64]0) { throw "overlay lengths must be positive" }
        $overlayStart = [int64]$file.Length - 32 - [int64]$manLen - [int64]$zipLen
        if ($overlayStart -lt 0) { throw "overlay bounds invalid" }

        [void]$fs.Seek($overlayStart, [System.IO.SeekOrigin]::Begin)
        $zipStream = [System.IO.File]::Create($zipTemp)
        try {
            $remaining = [int64]$zipLen
            $buffer = New-Object byte[] 65536
            $hasher = [System.Security.Cryptography.SHA256]::Create()
            try {
                while ($remaining -gt 0) {
                    $readSize = [int][Math]::Min([int64]$buffer.Length, $remaining)
                    $read = $fs.Read($buffer, 0, $readSize)
                    if ($read -le 0) { throw "payload zip truncated" }
                    $zipStream.Write($buffer, 0, $read)
                    [void]$hasher.TransformBlock($buffer, 0, $read, $null, 0)
                    $remaining -= $read
                }
                [void]$hasher.TransformFinalBlock($buffer, 0, 0)
                $payloadHash = ($hasher.Hash | ForEach-Object { $_.ToString("x2") }) -join ""
            } finally {
                $hasher.Dispose()
            }
        } finally {
            $zipStream.Dispose()
        }

        $manBytes = New-Object byte[] ([int]$manLen)
        if ($fs.Read($manBytes, 0, [int]$manLen) -ne [int]$manLen) { throw "manifest truncated" }
        $manifestJson = [System.Text.Encoding]::UTF8.GetString($manBytes)
        $manifest = $manifestJson | ConvertFrom-Json
        if ([int]$manifest.schema_version -ne 1) { throw "unsupported overlay schema_version" }
        if ($manifest.payload_sha256 -ne $payloadHash) { throw "hash mismatch: payload" }

        $zip = [System.IO.Compression.ZipFile]::OpenRead($zipTemp)
        $entryPaths = New-Object System.Collections.Generic.List[string]
        $textSnippets = New-Object System.Collections.Generic.List[object]
        try {
            $byPath = @{}
            foreach ($entry in $manifest.entries) {
                $key = ConvertTo-ForwardSlash $entry.path
                if ($byPath.ContainsKey($key)) { throw "duplicate manifest path: $key" }
                $byPath[$key] = $entry
            }
            foreach ($zipEntry in $zip.Entries) {
                $name = ConvertTo-ForwardSlash $zipEntry.FullName
                if ($name.EndsWith('/')) { continue }
                $entryPaths.Add($name)
                $want = $byPath[$name]
                if (-not $want) { throw "unexpected zip path: $name" }
                $entryStream = $zipEntry.Open()
                try {
                    $ms = New-Object System.IO.MemoryStream
                    try {
                        $entryStream.CopyTo($ms)
                        $bytes = $ms.ToArray()
                    } finally {
                        $ms.Dispose()
                    }
                } finally {
                    $entryStream.Dispose()
                }
                if ([int64]$bytes.Length -ne [int64]$want.size) { throw "hash mismatch: size $name" }
                $actual = Get-Sha256Bytes $bytes
                if ($actual -ne ([string]$want.sha256).ToLowerInvariant()) { throw "hash mismatch: $name" }
                $ext = [System.IO.Path]::GetExtension($name)
                if ($ext -match '(?i)\.(json|txt|xml|ini|cfg|conf|crt|pem|md|yml|yaml|env)$' -or $name -match '(?i)(^|/)(\.env)') {
                    $textSnippets.Add([pscustomobject]@{
                            Path = $name
                            Text = [System.Text.Encoding]::UTF8.GetString($bytes)
                        })
                }
                $byPath.Remove($name)
            }
            foreach ($leftover in $byPath.Keys) {
                throw "missing zip path: $leftover"
            }
        } finally {
            $zip.Dispose()
        }

        return @{
            Manifest     = $manifest
            ManifestJson = $manifestJson
            EntryPaths   = $entryPaths.ToArray()
            TextSnippets = $textSnippets.ToArray()
            PayloadHash  = $payloadHash
        }
    } finally {
        $fs.Dispose()
        if (Test-Path -LiteralPath $zipTemp) { Remove-Item -LiteralPath $zipTemp -Force -ErrorAction SilentlyContinue }
    }
}

function Invoke-LauncherInspect {
    param([string]$ExePath)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $ExePath
    $psi.Arguments = "--inspect-payload"
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $proc = New-Object System.Diagnostics.Process
    $proc.StartInfo = $psi
    try {
        [void]$proc.Start()
        if (-not $proc.WaitForExit(15000)) {
            try { $proc.Kill() } catch { }
            return $null
        }
        if ($proc.ExitCode -ne 0) { return $null }
        $stdout = $proc.StandardOutput.ReadToEnd()
        if ($stdout -match 'schema_version') { return $stdout.Trim() }
        return $null
    } catch {
        return $null
    } finally {
        $proc.Dispose()
    }
}

function Test-PartnerPackageDir {
    param([string]$Dir, [string]$Ver)
    $exeName = "video-production-console-partner-$Ver.exe"
    $exe = Join-Path $Dir $exeName
    $shaFile = "$exe.sha256"
    if (!(Test-Path -LiteralPath $exe -PathType Leaf)) {
        throw "partner package not found: $exe"
    }
    if (!(Test-Path -LiteralPath $shaFile -PathType Leaf)) {
        throw "partner package not found: $shaFile"
    }
    $shaText = (Get-Content -LiteralPath $shaFile -Raw).Trim()
    $actual = Get-Sha256File $exe
    if ($shaText -notmatch [regex]::Escape($actual)) {
        throw "hash mismatch: $exeName"
    }

    $overlay = Read-PartnerOverlay -ExePath $exe
    $inspectJson = Invoke-LauncherInspect -ExePath $exe
    if (-not $inspectJson) {
        throw "launcher --inspect-payload failed; the EXE will flash and exit on double-click"
    }
    $inspected = $inspectJson | ConvertFrom-Json
    if ($inspected.payload_sha256 -ne $overlay.PayloadHash) {
        throw "hash mismatch: launcher inspect payload"
    }

    $paths = @($overlay.EntryPaths)
    foreach ($required in $requiredPaths) {
        if (-not (Test-HasRequiredPath -EntryPaths $paths -Required $required)) {
            throw "missing path: $required"
        }
    }
    Assert-NoLeakage -EntryPaths $paths -TextSnippets $overlay.TextSnippets
    Write-Host "Partner package $Ver accepted ($($paths.Count) payload files)"
}

function New-SelfTestPackage {
    param([string]$Dir, [string]$Ver, [hashtable]$ExtraFiles)
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $payload = Join-Path $Dir "payload"
    $files = @{
        "bin/video-production-console.exe" = [byte[]](0x4D, 0x5A, 0x90)
        "runtime/python/python.exe" = [byte[]](0x4D, 0x5A, 0x91)
        "runtime/python/Lib/site-packages/pyJianYingDraft/__init__.py" = [System.Text.Encoding]::UTF8.GetBytes("# pyJianYingDraft")
        "runtime/ffmpeg/bin/ffmpeg.exe" = [byte[]](0x4D, 0x5A, 0x92)
        "runtime/ffmpeg/bin/ffprobe.exe" = [byte[]](0x4D, 0x5A, 0x93)
        "resources/tls/partner-ca.crt" = [System.Text.Encoding]::UTF8.GetBytes("-----BEGIN CERTIFICATE-----`nMIIB`n-----END CERTIFICATE-----`n")
        "resources/montage/bgm/theme.mp3" = [byte[]](0x00, 0x01, 0x02)
        "schemas/settings.schema.json" = [System.Text.Encoding]::UTF8.GetBytes("{}")
        "schemas/task-manifest.schema.json" = [System.Text.Encoding]::UTF8.GetBytes("{}")
    }
    if ($ExtraFiles) {
        foreach ($key in $ExtraFiles.Keys) { $files[$key] = $ExtraFiles[$key] }
    }
    foreach ($rel in $files.Keys) {
        $dest = Join-Path $payload ($rel.Replace('/', '\'))
        New-Item -ItemType Directory -Path (Split-Path $dest) -Force | Out-Null
        [System.IO.File]::WriteAllBytes($dest, $files[$rel])
    }

    $entries = New-Object System.Collections.Generic.List[object]
    $zipPath = Join-Path $Dir "payload.zip"
    if (Test-Path -LiteralPath $zipPath) { Remove-Item -LiteralPath $zipPath -Force }
    $zip = [System.IO.Compression.ZipFile]::Open($zipPath, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($rel in ($files.Keys | Sort-Object)) {
            $bytes = $files[$rel]
            $entry = $zip.CreateEntry($rel, [System.IO.Compression.CompressionLevel]::Fastest)
            $stream = $entry.Open()
            try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
            $entries.Add([pscustomobject]@{
                    path   = $rel
                    size   = [int64]$bytes.Length
                    sha256 = Get-Sha256Bytes $bytes
                })
        }
    } finally {
        $zip.Dispose()
    }

    $zipBytes = [System.IO.File]::ReadAllBytes($zipPath)
    $payloadHash = Get-Sha256Bytes $zipBytes
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append('{"schema_version":1,"app_version":"')
    [void]$sb.Append($Ver)
    [void]$sb.Append('","payload_sha256":"')
    [void]$sb.Append($payloadHash)
    [void]$sb.Append('","entries":[')
    for ($i = 0; $i -lt $entries.Count; $i++) {
        if ($i -gt 0) { [void]$sb.Append(',') }
        $e = $entries[$i]
        [void]$sb.Append('{"path":"')
        [void]$sb.Append($e.path)
        [void]$sb.Append('","size":')
        [void]$sb.Append($e.size)
        [void]$sb.Append(',"sha256":"')
        [void]$sb.Append($e.sha256)
        [void]$sb.Append('"}')
    }
    [void]$sb.Append(']}')
    $manifestJson = $sb.ToString()

    $exe = Join-Path $Dir "video-production-console-partner-$Ver.exe"
    $stub = [System.Text.Encoding]::ASCII.GetBytes("MZ-fake-stub")
    $manBytes = (Get-Utf8NoBom).GetBytes($manifestJson)
    $fs = [System.IO.File]::Create($exe)
    try {
        $fs.Write($stub, 0, $stub.Length)
        $fs.Write($zipBytes, 0, $zipBytes.Length)
        $fs.Write($manBytes, 0, $manBytes.Length)
        $magic = [System.Text.Encoding]::ASCII.GetBytes($trailerMagic)
        $fs.Write($magic, 0, $magic.Length)
        $zipLen = [BitConverter]::GetBytes([uint64]$zipBytes.Length)
        $manLen = [BitConverter]::GetBytes([uint64]$manBytes.Length)
        $fs.Write($zipLen, 0, 8)
        $fs.Write($manLen, 0, 8)
    } finally {
        $fs.Dispose()
    }
    $hash = Get-Sha256File $exe
    Set-Content -LiteralPath "$exe.sha256" -Value "$hash  video-production-console-partner-$Ver.exe" -Encoding ASCII
    return $Dir
}

if ($SelfTest) {
    $temp = Join-Path ([System.IO.Path]::GetTempPath()) ("vpc-partner-pkg-selftest-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $temp | Out-Null
    $failures = 0
    try {
        $good = New-SelfTestPackage -Dir (Join-Path $temp "good") -Ver $versionValue
        Test-PartnerPackageDir -Dir $good -Ver $versionValue

        $leaky = New-SelfTestPackage -Dir (Join-Path $temp "leaky") -Ver $versionValue -ExtraFiles @{
            "notes/.env" = [System.Text.Encoding]::UTF8.GetBytes("SECRET=1")
        }
        try {
            Test-PartnerPackageDir -Dir $leaky -Ver $versionValue
            Write-Host "FAIL: leakage .env was accepted"
            $failures++
        } catch {
            if ($_.Exception.Message -match 'forbidden path|\.env') {
                Write-Host "PASS: leakage .env rejected"
            } else {
                Write-Host "FAIL: unexpected leakage error: $($_.Exception.Message)"
                $failures++
            }
        }

        $extraExe = New-SelfTestPackage -Dir (Join-Path $temp "extraexe") -Ver $versionValue -ExtraFiles @{
            "tools/helper.exe" = [byte[]](0x4D, 0x5A)
        }
        try {
            Test-PartnerPackageDir -Dir $extraExe -Ver $versionValue
            Write-Host "FAIL: unlisted exe was accepted"
            $failures++
        } catch {
            if ($_.Exception.Message -match 'unlisted \.exe') {
                Write-Host "PASS: unlisted exe outside bin/runtime rejected"
            } else {
                Write-Host "FAIL: unexpected exe error: $($_.Exception.Message)"
                $failures++
            }
        }
    } finally {
        Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ($failures -gt 0) { throw "SelfTest failed: $failures check(s)" }
    Write-Host "SelfTest passed"
    exit 0
}

if (-not $PackageDir) {
    $PackageDir = Join-Path $repoRoot "release\partner\$versionValue"
}
Test-PartnerPackageDir -Dir $PackageDir -Ver $versionValue
