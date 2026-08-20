# Dry-run tests for scripts/provision-cpa-upstream-key.ps1 against a local HTTP listener.
# Does not SSH to cpa.
param()

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$scriptPath = Join-Path $PSScriptRoot "provision-cpa-upstream-key.ps1"
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

function Get-FreePort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
    $listener.Stop()
    return $port
}

function Start-MockManagementServer {
    param(
        [int]$Port,
        [string]$LogPath,
        [string]$Password,
        [int]$PatchStatus = 200,
        [string]$PatchBody = '{"status":"ok"}',
        [int]$DeleteStatus = 200
    )
    $hostExe = Get-PowerShellHost
    $serverScript = @"
`$ErrorActionPreference = 'Stop'
`$listener = New-Object System.Net.HttpListener
`$listener.Prefixes.Add('http://127.0.0.1:$Port/')
`$listener.Start()
`$log = New-Object System.Collections.Generic.List[string]
try {
    while (`$true) {
        `$ctx = `$listener.GetContext()
        `$req = `$ctx.Request
        `$reader = New-Object System.IO.StreamReader(`$req.InputStream)
        `$body = `$reader.ReadToEnd()
        `$reader.Dispose()
        `$auth = `$req.Headers['Authorization']
        `$entry = (@{
            method = `$req.HttpMethod
            path = `$req.Url.AbsolutePath
            query = `$req.Url.Query
            authorization = `$auth
            body = `$body
        } | ConvertTo-Json -Compress)
        `$log.Add(`$entry)
        [System.IO.File]::WriteAllLines('$($LogPath.Replace('\','\\'))', `$log)
        `$status = 404
        `$responseBody = ''
        if (`$req.Url.AbsolutePath -eq '/v0/management/api-keys' -and `$req.HttpMethod -eq 'PATCH') {
            `$status = $PatchStatus
            `$responseBody = '$PatchBody'
        } elseif (`$req.Url.AbsolutePath -eq '/v0/management/api-keys' -and `$req.HttpMethod -eq 'DELETE') {
            `$status = $DeleteStatus
            `$responseBody = '{"status":"ok"}'
        }
        `$ctx.Response.StatusCode = `$status
        `$bytes = [System.Text.Encoding]::UTF8.GetBytes(`$responseBody)
        `$ctx.Response.ContentType = 'application/json'
        `$ctx.Response.OutputStream.Write(`$bytes, 0, `$bytes.Length)
        `$ctx.Response.Close()
        if (`$req.HttpMethod -eq 'DELETE') { break }
        if (`$req.HttpMethod -eq 'PATCH' -and $PatchStatus -ne 200) { break }
    }
} finally {
    `$listener.Stop()
    `$listener.Close()
}
"@
    $tempScript = Join-Path ([System.IO.Path]::GetTempPath()) ("pgw-mgmt-" + [guid]::NewGuid().ToString("n") + ".ps1")
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($tempScript, $serverScript, $utf8)
    $proc = Start-Process -FilePath $hostExe -ArgumentList @("-NoProfile", "-File", $tempScript) -PassThru -WindowStyle Hidden
    Start-Sleep -Milliseconds 400
    return @{ Process = $proc; Script = $tempScript }
}

function Stop-MockManagementServer {
    param($Server)
    if ($Server -and $Server.Process -and -not $Server.Process.HasExited) {
        Stop-Process -Id $Server.Process.Id -Force -ErrorAction SilentlyContinue
    }
    if ($Server -and $Server.Script -and (Test-Path -LiteralPath $Server.Script)) {
        Remove-Item -LiteralPath $Server.Script -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-Provision {
    param(
        [string[]]$Arguments,
        [string]$StdOutPath,
        [string]$StdErrPath,
        [hashtable]$Environment
    )
    $hostExe = Get-PowerShellHost
    $argList = @("-NoProfile", "-File", $scriptPath) + $Arguments
    $quoted = foreach ($arg in $argList) {
        if ($arg -match '[\s"]') { '"' + ($arg.Replace('"', '\"')) + '"' } else { $arg }
    }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $hostExe
    $psi.Arguments = ($quoted -join " ")
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    if ($Environment) {
        foreach ($key in $Environment.Keys) {
            if ($psi.EnvironmentVariables.ContainsKey($key)) {
                $psi.EnvironmentVariables[$key] = [string]$Environment[$key]
            } else {
                $psi.EnvironmentVariables.Add($key, [string]$Environment[$key])
            }
        }
    }
    $proc = New-Object System.Diagnostics.Process
    $proc.StartInfo = $psi
    [void]$proc.Start()
    $stdout = $proc.StandardOutput.ReadToEnd()
    $stderr = $proc.StandardError.ReadToEnd()
    $proc.WaitForExit()
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($StdOutPath, $stdout, $utf8)
    [System.IO.File]::WriteAllText($StdErrPath, $stderr, $utf8)
    return $proc
}

if (!(Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    Write-Fail "provision script is absent: $scriptPath"
    Write-Host "RESULT: $failures failed"
    exit 1
}

$source = Get-Content -LiteralPath $scriptPath -Raw
if ($source -match 'GET\s+/api-keys' -or $source -match 'GET\s+/v0/management/api-keys') {
    Write-Fail "source must never GET /api-keys"
} else {
    Write-Pass "source never GET /api-keys"
}
if ($source -match 'Read-Host -AsSecureString') {
    Write-Pass "RemoveOldKey uses Read-Host -AsSecureString"
} else {
    Write-Fail "RemoveOldKey must use Read-Host -AsSecureString"
}
if ($source -match 'sk-[A-Za-z0-9]{16,}' -or $source -match 'MANAGEMENT_PASSWORD=[A-Za-z0-9_\-]{8,}') {
    Write-Fail "source appears to contain a secret value"
} else {
    Write-Pass "source has no hardcoded secret values"
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("pgw-prov-" + [guid]::NewGuid().ToString("n"))
New-Item -ItemType Directory -Path $work -Force | Out-Null
try {
    $port = Get-FreePort
    $logPath = Join-Path $work "requests.jsonl"
    $secretPath = Join-Path $work "upstream_api_key"
    $stdout = Join-Path $work "out.txt"
    $stderr = Join-Path $work "err.txt"
    $server = Start-MockManagementServer -Port $port -LogPath $logPath -Password "test-mgmt-password"
    try {
        $proc = Invoke-Provision -Arguments @(
            "-ManagementURL", "http://127.0.0.1:$port",
            "-ManagementPassword", "test-mgmt-password",
            "-SecretFile", $secretPath,
            "-GeneratedKey", "generated-value"
        ) -StdOutPath $stdout -StdErrPath $stderr
        Start-Sleep -Milliseconds 200
        $outText = Get-Content -LiteralPath $stdout -Raw -ErrorAction SilentlyContinue
        $errText = Get-Content -LiteralPath $stderr -Raw -ErrorAction SilentlyContinue
        $combined = "$outText`n$errText"
        if ($proc.ExitCode -ne 0) {
            Write-Fail "provision exit $($proc.ExitCode): $combined"
        } elseif ($combined -match 'generated-value' -or $combined -match 'test-mgmt-password') {
            Write-Fail "provision printed a secret"
        } elseif ($outText.Trim() -ne "upstream key provisioned") {
            Write-Fail "provision output was not the confirmation line"
        } elseif (!(Test-Path -LiteralPath $secretPath)) {
            Write-Fail "secret file was not written"
        } elseif ((Get-Content -LiteralPath $secretPath -Raw).Trim() -ne "generated-value") {
            Write-Fail "secret file value mismatch"
        } else {
            $logged = Get-Content -LiteralPath $logPath -ErrorAction SilentlyContinue
            $patch = $logged | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object { $_.method -eq "PATCH" } | Select-Object -First 1
            if (-not $patch) {
                Write-Fail "PATCH was not received"
            } elseif ($patch.path -ne "/v0/management/api-keys") {
                Write-Fail "PATCH path was $($patch.path)"
            } elseif ($patch.authorization -ne "Bearer test-mgmt-password") {
                Write-Fail "PATCH Authorization header mismatch"
            } else {
                $payload = $patch.body | ConvertFrom-Json
                if ($null -ne $payload.old -or $payload.new -ne "generated-value") {
                    Write-Fail "PATCH body was not {old:null,new:generated-value}"
                } else {
                    Write-Pass "PATCH /v0/management/api-keys writes secret and prints no value"
                }
            }
        }
    } finally {
        Stop-MockManagementServer $server
    }

    $failPort = Get-FreePort
    $failLog = Join-Path $work "fail-requests.jsonl"
    $failSecret = Join-Path $work "fail-secret"
    $failOut = Join-Path $work "fail-out.txt"
    $failErr = Join-Path $work "fail-err.txt"
    $failServer = Start-MockManagementServer -Port $failPort -LogPath $failLog -Password "test-mgmt-password"
    try {
        $envMap = @{ PARTNER_GATEWAY_PROVISION_FAIL_AFTER_PATCH = "1" }
        $proc = Invoke-Provision -Arguments @(
            "-ManagementURL", "http://127.0.0.1:$failPort",
            "-ManagementPassword", "test-mgmt-password",
            "-SecretFile", $failSecret,
            "-GeneratedKey", "generated-value"
        ) -StdOutPath $failOut -StdErrPath $failErr -Environment $envMap
        Start-Sleep -Milliseconds 300
        $failCombined = (Get-Content -LiteralPath $failOut -Raw -ErrorAction SilentlyContinue) + "`n" + (Get-Content -LiteralPath $failErr -Raw -ErrorAction SilentlyContinue)
        $logged = @(Get-Content -LiteralPath $failLog -ErrorAction SilentlyContinue)
        $events = @($logged | ForEach-Object { $_ | ConvertFrom-Json })
        $delete = $events | Where-Object { $_.method -eq "DELETE" } | Select-Object -First 1
        if ($proc.ExitCode -eq 0) {
            Write-Fail "forced post-patch failure still exited 0"
        } elseif ($failCombined -match 'generated-value') {
            Write-Fail "failure path printed the generated key"
        } elseif (Test-Path -LiteralPath $failSecret) {
            Write-Fail "local secret file remained after rollback"
        } elseif (-not $delete) {
            Write-Fail "DELETE was not issued after post-PATCH failure"
        } elseif ($delete.query -notmatch [regex]::Escape("value=generated-value") -and $delete.query -notmatch "value=generated-value") {
            Write-Fail "DELETE query was $($delete.query)"
        } else {
            Write-Pass "post-PATCH failure DELETEs key and removes local secret file"
        }
    } finally {
        Stop-MockManagementServer $failServer
    }

    $removePort = Get-FreePort
    $removeLog = Join-Path $work "remove-requests.jsonl"
    $removeOut = Join-Path $work "remove-out.txt"
    $removeErr = Join-Path $work "remove-err.txt"
    $removeServer = Start-MockManagementServer -Port $removePort -LogPath $removeLog -Password "test-mgmt-password"
    $oldSecure = ConvertTo-SecureString "old-key-to-remove" -AsPlainText -Force
    $oldSecurePath = Join-Path $work "old.secure.xml"
    $oldSecure | Export-Clixml -Path $oldSecurePath
    $localScript = Join-Path $work "provision.ps1"
    Copy-Item -LiteralPath $scriptPath -Destination $localScript -Force
    try {
        $wrapper = Join-Path $work "remove.ps1"
        $wrapperText = @"
`$ErrorActionPreference = 'Stop'
`$secure = Import-Clixml -Path '$oldSecurePath'
& '$localScript' -RemoveOldKey -ManagementURL 'http://127.0.0.1:$removePort' -ManagementPassword 'test-mgmt-password' -OldKeySecure `$secure
"@
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllText($wrapper, $wrapperText, $utf8)
        $hostExe = Get-PowerShellHost
        $proc = Start-Process -FilePath $hostExe -ArgumentList @("-NoProfile", "-File", $wrapper) -Wait -PassThru -NoNewWindow `
            -RedirectStandardOutput $removeOut -RedirectStandardError $removeErr
        Start-Sleep -Milliseconds 200
        $removeText = (Get-Content -LiteralPath $removeOut -Raw -ErrorAction SilentlyContinue)
        $removeErrText = (Get-Content -LiteralPath $removeErr -Raw -ErrorAction SilentlyContinue)
        $logged = @(Get-Content -LiteralPath $removeLog -ErrorAction SilentlyContinue)
        $events = @($logged | ForEach-Object { $_ | ConvertFrom-Json })
        $delete = $events | Where-Object { $_.method -eq "DELETE" } | Select-Object -First 1
        if ($proc.ExitCode -ne 0) {
            Write-Fail "RemoveOldKey exit $($proc.ExitCode): $removeText $removeErrText"
        } elseif ($removeText -match 'old-key-to-remove') {
            Write-Fail "RemoveOldKey printed the old key"
        } elseif ($removeText.Trim() -notmatch '^\d+$') {
            Write-Fail "RemoveOldKey must print only HTTP status"
        } elseif (-not $delete) {
            Write-Fail "RemoveOldKey did not DELETE"
        } elseif ($delete.query -notmatch 'value=old-key-to-remove') {
            Write-Fail "RemoveOldKey DELETE query was $($delete.query)"
        } else {
            Write-Pass "RemoveOldKey DELETEs via stdin-equivalent and prints only status"
        }
    } finally {
        Stop-MockManagementServer $removeServer
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
