# Dry-run tests for scripts/deploy-partner-gateway.ps1 using a fake SSH/SCP runner.
# Does not SSH to cpa and does not require C:\PartnerBuildDeps\tls.
param()

$ErrorActionPreference = "Stop"
$scriptPath = Join-Path $PSScriptRoot "deploy-partner-gateway.ps1"
$failures = 0

function Write-Pass([string]$Message) { Write-Host "PASS: $Message" }
function Write-Fail([string]$Message) {
    $script:failures++
    Write-Host "FAIL: $Message"
}

function Get-PowerShellHost {
    $pwsh = Get-Command pwsh -ErrorAction SilentlyContinue
    if ($pwsh) { return $pwsh.Source }
    $current = (Get-Process -Id $PID).Path
    if ($current) { return $current }
    return (Get-Command powershell).Source
}

$expected = @(
    "preflight disk/memory/2443/network/container checks",
    "create /opt/video-partner-gateway/releases/0.1.0",
    "upload bundle to that release directory",
    "verify SHA256SUMS.txt",
    "docker compose config --quiet",
    "backup current compose/env/tls metadata and SQLite",
    "switch current symlink atomically",
    "docker compose build --pull",
    "docker compose up -d",
    "wait for healthy",
    "verify existing cli-proxy-api container and port 2001 unchanged"
)

if (!(Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    Write-Fail "deploy script is absent: $scriptPath"
    Write-Host "RESULT: $failures failed"
    exit 1
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("pgw-deploy-" + [guid]::NewGuid().ToString("n"))
New-Item -ItemType Directory -Path $work -Force | Out-Null

function Invoke-DeployTest {
    param(
        [string]$LogPath,
        [string]$HookPath,
        [string]$RemoteLogPath,
        [string]$BundleDir,
        [string]$FailAt,
        [switch]$DryRunOnly
    )
    $hook = @"
`$script:PartnerGatewayRemoteLog = '$($RemoteLogPath.Replace('\','\\'))'
function Invoke-PartnerGatewayRemote {
    param([string]`$Command, [string]`$StdinText)
    `$line = (`$Command + ' | ' + `$StdinText)
    Add-Content -LiteralPath `$script:PartnerGatewayRemoteLog -Value `$line
    if (`$StdinText -match 'readlink') { return 'releases/0.0.9' }
    if (`$StdinText -match 'preflight') { return 'preflight-ok' }
    return 'ok'
}
function Invoke-PartnerGatewayUpload {
    param([string]`$LocalPath, [string]`$RemotePath)
    Add-Content -LiteralPath `$script:PartnerGatewayRemoteLog -Value ("upload " + `$LocalPath + " -> " + `$RemotePath)
}
"@
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($HookPath, $hook, $utf8)
    $hostExe = Get-PowerShellHost
    $stdout = "$LogPath.stdout"
    $stderr = "$LogPath.stderr"
    $args = @(
        "-NoProfile", "-File", $scriptPath,
        "-Version", "0.1.0",
        "-OperationLogPath", $LogPath,
        "-TestHookPath", $HookPath,
        "-BundleRoot", $BundleDir
    )
    if ($FailAt) { $args += @("-FailAt", $FailAt) }
    if ($DryRunOnly) { $args += "-DryRun" }
    $proc = Start-Process -FilePath $hostExe -ArgumentList $args -Wait -PassThru -NoNewWindow `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    return @{
        ExitCode = $proc.ExitCode
        StdOut = (Get-Content -LiteralPath $stdout -Raw -ErrorAction SilentlyContinue)
        StdErr = (Get-Content -LiteralPath $stderr -Raw -ErrorAction SilentlyContinue)
        Operations = @(Get-Content -LiteralPath $LogPath -ErrorAction SilentlyContinue)
        Remote = (Get-Content -LiteralPath $RemoteLogPath -Raw -ErrorAction SilentlyContinue)
    }
}

try {
    $dummyBundle = Join-Path $work "bundle"
    New-Item -ItemType Directory -Path $dummyBundle -Force | Out-Null
    foreach ($name in @("partner-gateway", "Dockerfile", "compose.yml", "gateway.env", "SHA256SUMS.txt")) {
        Set-Content -LiteralPath (Join-Path $dummyBundle $name) -Value "placeholder-$name"
    }

    $okLog = Join-Path $work "ops.txt"
    $okHook = Join-Path $work "hook.ps1"
    $okRemote = Join-Path $work "remote.txt"
    $ok = Invoke-DeployTest -LogPath $okLog -HookPath $okHook -RemoteLogPath $okRemote -BundleDir $dummyBundle
    if ($ok.ExitCode -ne 0) {
        Write-Fail "deploy test exit $($ok.ExitCode): $($ok.StdErr)"
    } else {
        $same = ($ok.Operations.Count -eq $expected.Count)
        if ($same) {
            for ($i = 0; $i -lt $expected.Count; $i++) {
                if ($ok.Operations[$i] -ne $expected[$i]) { $same = $false; break }
            }
        }
        if (-not $same) {
            Write-Fail "operation sequence mismatch: $($ok.Operations -join ' | ')"
        } else {
            Write-Pass "deploy sequence matches the documented operations"
        }
    }
    if ($ok.Remote -match 'rm -rf /opt/video-partner-gateway/data' -or $ok.Remote -match '/opt/CLIProxyAPI') {
        Write-Fail "remote commands deleted data or mentioned /opt/CLIProxyAPI"
    } else {
        Write-Pass "remote commands do not delete data or the existing API tree"
    }

    $failLog = Join-Path $work "fail-ops.txt"
    $failHook = Join-Path $work "fail-hook.ps1"
    $failRemote = Join-Path $work "fail-remote.txt"
    $fail = Invoke-DeployTest -LogPath $failLog -HookPath $failHook -RemoteLogPath $failRemote -BundleDir $dummyBundle -FailAt "healthy"
    $failText = "$($fail.StdOut)`n$($fail.StdErr)`n$($fail.Remote)"
    if ($fail.ExitCode -eq 0) {
        Write-Fail "health failure still exited 0"
    } elseif ($failText -notmatch 'rolled back to previous current') {
        Write-Fail "health failure did not restore previous current"
    } elseif ($failText -match 'rm -rf .*/releases/0\.1\.0' -or $failText -match 'delete failed release') {
        Write-Fail "health failure deleted the failed release"
    } elseif ($failText -notmatch 'docker compose -f compose.yml --env-file gateway.env up -d') {
        Write-Fail "health failure did not compose up the previous current"
    } else {
        Write-Pass "health failure restores previous current without deleting release or data"
    }

    $dryOut = Join-Path $work "dry.out"
    $dryErr = Join-Path $work "dry.err"
    $hostExe = Get-PowerShellHost
    $dry = Start-Process -FilePath $hostExe -ArgumentList @(
        "-NoProfile", "-File", $scriptPath, "-Version", "0.1.0", "-DryRun"
    ) -Wait -PassThru -NoNewWindow -RedirectStandardOutput $dryOut -RedirectStandardError $dryErr
    $dryText = (Get-Content -LiteralPath $dryOut -Raw -ErrorAction SilentlyContinue) + "`n" + (Get-Content -LiteralPath $dryErr -Raw -ErrorAction SilentlyContinue)
    $dryOps = @($expected | Where-Object { $dryText -match [regex]::Escape($_) })
    if ($dry.ExitCode -ne 0) {
        Write-Fail "DryRun exit $($dry.ExitCode): $dryText"
    } elseif ($dryOps.Count -ne $expected.Count) {
        Write-Fail "DryRun missing operations"
    } elseif ($dryText -match 'delete.*/opt/CLIProxyAPI' -or $dryText -match 'deleting /opt/CLIProxyAPI') {
        Write-Fail "DryRun mentioned deleting /opt/CLIProxyAPI"
    } elseif ($dryText -match 'delete data' -or $dryText -match 'rm -rf /opt/video-partner-gateway/data') {
        Write-Fail "DryRun mentioned deleting data"
    } else {
        Write-Pass "DryRun prints the sequence and does not delete data"
    }

    $source = Get-Content -LiteralPath $scriptPath -Raw
    if ($source -match 'partner-ca\.key' -and $source -match 'never upload partner-ca.key') {
        Write-Pass "deploy refuses to upload partner-ca.key"
    } else {
        Write-Fail "deploy must refuse partner-ca.key"
    }
    if ($source -match 'Read-Host -AsSecureString') {
        Write-Pass "Aura provision uses Read-Host -AsSecureString"
    } else {
        Write-Fail "Aura provision must prompt with Read-Host -AsSecureString"
    }
} finally {
    if (Test-Path -LiteralPath $work) {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Write-Host ""
if ($failures -gt 0) {
    Write-Host "RESULT: $failures failed"
    exit 1
}
Write-Host "RESULT: all passed"
exit 0
