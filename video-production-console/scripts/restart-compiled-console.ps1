param([switch] $FunctionsOnly)

$ErrorActionPreference = 'Stop'

function Wait-ConsoleExit {
    param($Process, [int] $TimeoutMilliseconds = 15000)
    if (-not $Process.WaitForExit($TimeoutMilliseconds)) {
        throw 'The previous process has not exited. Program files have not been replaced.'
    }
}

function Test-ConsoleFileBusy {
    param([System.Exception] $Exception)
    while ($null -ne $Exception) {
        if (($Exception.HResult -band 0xFFFF) -in @(32, 33)) { return $true }
        $Exception = $Exception.InnerException
    }
    return $false
}

function Copy-ConsoleBinary {
    param(
        [string] $Source, [string] $Destination,
        [double] $TimeoutSeconds = 20,
        [int] $RetryDelayMilliseconds = 250,
        [scriptblock] $CopyOperation = {
            param($From, $To)
            Copy-Item -LiteralPath $From -Destination $To -Force -ErrorAction Stop
        }
    )
    $expectedHash = (Get-FileHash -LiteralPath $Source -Algorithm SHA256).Hash
    $timer = [System.Diagnostics.Stopwatch]::StartNew()
    $attempt = 0
    while ($true) {
        $attempt++
        try {
            & $CopyOperation $Source $Destination
            break
        } catch {
            if (-not (Test-ConsoleFileBusy -Exception $_.Exception)) { throw }
            if ($timer.Elapsed.TotalSeconds -ge $TimeoutSeconds) {
                throw [System.IO.IOException]::new("Program file is still in use after $attempt attempts. Close other console instances and try again.", $_.Exception)
            }
            if ($attempt -eq 1) { Write-Host 'Waiting for Windows to release the program file...' }
            Start-Sleep -Milliseconds $RetryDelayMilliseconds
        }
    }
    if ((Get-FileHash -LiteralPath $Destination -Algorithm SHA256).Hash -ne $expectedHash) {
        throw 'Copied program does not match the compiled file. It will not be started.'
    }
    return $attempt
}

# This switch is used only by offline helper tests; no service operations run.
if ($FunctionsOnly) { return }

# Manual entry point. Run after building dist/video-production-console.next.exe.
$consoleRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$consoleTarget = Join-Path $consoleRoot 'dist/video-production-console.exe'
$consoleNext = Join-Path $consoleRoot 'dist/video-production-console.next.exe'
if (-not (Test-Path -LiteralPath $consoleNext -PathType Leaf)) {
    throw 'The newly compiled program is missing.'
}
$consoleListeners = @(Get-NetTCPConnection -LocalPort 2030 -State Listen -ErrorAction SilentlyContinue)
$consoleIDs = @($consoleListeners | Select-Object -ExpandProperty OwningProcess -Unique)
if ($consoleIDs.Count -gt 1) { throw 'Multiple processes are listening on port 2030. No process was stopped.' }
if ($consoleIDs.Count -eq 1) {
    $consolePrevious = Get-CimInstance Win32_Process -Filter "ProcessId = $($consoleIDs[0])"
    if (-not [string]::Equals($consolePrevious.ExecutablePath, $consoleTarget, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Port 2030 belongs to a different program. No process was stopped.'
    }
    $consoleProcess = Get-Process -Id $consoleIDs[0] -ErrorAction Stop
    if (-not [string]::Equals($consoleProcess.Path, $consoleTarget, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'The listening process changed before restart. No process was stopped.'
    }
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$consoleBackup = Join-Path $consoleRoot "artifacts/console-before-manual-restart-$stamp.exe"
Copy-Item -LiteralPath $consoleTarget -Destination $consoleBackup
if ($consoleIDs.Count -eq 1) {
    Stop-Process -InputObject $consoleProcess -ErrorAction Stop
    Wait-ConsoleExit -Process $consoleProcess
    $consoleProcess.Dispose()
}

function Start-ConsoleProgram {
    param([string] $LogSuffix)
    return Start-Process -FilePath $consoleTarget -WorkingDirectory $consoleRoot -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $consoleRoot "dist/console-$LogSuffix.stdout.log") `
        -RedirectStandardError (Join-Path $consoleRoot "dist/console-$LogSuffix.stderr.log") -PassThru
}

function Test-ConsoleHealth {
    param([int] $ExpectedProcessId)
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        try {
            $owners = @(Get-NetTCPConnection -LocalPort 2030 -State Listen -ErrorAction Stop | Select-Object -ExpandProperty OwningProcess -Unique)
            if ($owners.Count -eq 1 -and $owners[0] -eq $ExpectedProcessId) {
                $response = Invoke-WebRequest -Uri 'http://127.0.0.1:2030/api/health' -NoProxy -TimeoutSec 1
                if ($response.StatusCode -eq 200 -and ($response.Content | ConvertFrom-Json).status -eq 'ok') { return $true }
            }
        } catch {}
        Start-Sleep -Milliseconds 300
    }
    return $false
}

$consoleStarted = $null
try {
    $null = Copy-ConsoleBinary -Source $consoleNext -Destination $consoleTarget
    $consoleStarted = Start-ConsoleProgram -LogSuffix "visual-$stamp"
    if (-not (Test-ConsoleHealth -ExpectedProcessId $consoleStarted.Id)) { throw 'New program failed its health check.' }
    Write-Host "Restarted successfully. PID: $($consoleStarted.Id)"
    Write-Host 'Open http://127.0.0.1:2030/ai-shorts'
    Write-Host "Backup: $consoleBackup"
} catch {
    $restartError = $_
    Write-Host 'New version was NOT enabled. Restoring the previous version...'
    try {
        if ($null -ne $consoleStarted -and -not $consoleStarted.HasExited) {
            Stop-Process -InputObject $consoleStarted -ErrorAction Stop
            Wait-ConsoleExit -Process $consoleStarted
            $consoleStarted.Dispose()
        }
        # If replacement never started, do not overwrite an unchanged old file again.
        if (-not (Test-Path -LiteralPath $consoleTarget -PathType Leaf) -or
            (Get-FileHash -LiteralPath $consoleBackup).Hash -ne (Get-FileHash -LiteralPath $consoleTarget).Hash) {
            $null = Copy-ConsoleBinary -Source $consoleBackup -Destination $consoleTarget
        }
        $restored = Start-ConsoleProgram -LogSuffix "rollback-$stamp"
        if (-not (Test-ConsoleHealth -ExpectedProcessId $restored.Id)) { throw 'Rollback service did not pass its health check.' }
        Write-Host 'Previous version restored and healthy. New version is still NOT enabled.'
    } catch {
        throw "Restart failed: $($restartError.Exception.Message) Rollback also failed: $($_.Exception.Message)"
    }
    throw $restartError
}
