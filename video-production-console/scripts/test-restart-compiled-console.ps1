$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'restart-compiled-console.ps1') -FunctionsOnly

# Temporary files only. These tests never stop or start the real console.
$testDir = Join-Path ([System.IO.Path]::GetTempPath()) ('console-restart-test-' + [guid]::NewGuid().ToString())
$null = New-Item -ItemType Directory -Path $testDir
$source = Join-Path $testDir 'new.fixture'
$target = Join-Path $testDir 'current.fixture'
[IO.File]::WriteAllText($source, 'new compiled fixture')
[IO.File]::WriteAllText($target, 'old compiled fixture')
$passed = 0

# Simulate the observed sharing violation followed by release of the file lock.
$script:copyAttempts = 0
$attempts = Copy-ConsoleBinary -Source $source -Destination $target -RetryDelayMilliseconds 1 -CopyOperation {
    param($From, $To)
    $script:copyAttempts++
    if ($script:copyAttempts -lt 3) { throw [IO.IOException]::new('File is in use.', -2147024864) }
    Copy-Item -LiteralPath $From -Destination $To -Force
}
if ($attempts -ne 3 -or [IO.File]::ReadAllText($target) -ne 'new compiled fixture') { throw 'Transient lock retry failed.' }
$passed++

# Persistent sharing violation has a finite bound and reports failure.
$failed = $false
try {
    $null = Copy-ConsoleBinary -Source $source -Destination $target -TimeoutSeconds 0.02 -RetryDelayMilliseconds 1 -CopyOperation {
        param($From, $To)
        throw [IO.IOException]::new('File is in use.', -2147024864)
    }
} catch {
    if ($_.Exception.Message -notlike '*still in use*') { throw }
    $failed = $true
}
if (-not $failed) { throw 'Persistent lock was accepted.' }
$passed++

# Other errors must not be mistaken for transient locks.
$script:copyAttempts = 0
try {
    $null = Copy-ConsoleBinary -Source $source -Destination $target -CopyOperation {
        param($From, $To)
        $script:copyAttempts++
        throw [UnauthorizedAccessException]::new('Permission fixture')
    }
    throw 'Permission failure was accepted.'
} catch {
    if ($_.Exception.Message -ne 'Permission fixture' -or $script:copyAttempts -ne 1) { throw }
}
$passed++

# The copied bytes must match before starting a program.
try {
    $null = Copy-ConsoleBinary -Source $source -Destination $target -CopyOperation {
        param($From, $To)
        [IO.File]::WriteAllText($To, 'partial copy')
    }
    throw 'Partial copy was accepted.'
} catch { if ($_.Exception.Message -notlike '*does not match*') { throw } }
$passed++

# A failed exit wait must remain an error instead of being silently ignored.
$notExited = [PSCustomObject]@{}
$notExited | Add-Member -MemberType ScriptMethod -Name WaitForExit -Value { param($timeout) return $false }
try {
    Wait-ConsoleExit -Process $notExited -TimeoutMilliseconds 1
    throw 'Exit timeout was ignored.'
} catch { if ($_.Exception.Message -notlike '*has not exited*') { throw } }
$passed++
$exited = [PSCustomObject]@{}
$exited | Add-Member -MemberType ScriptMethod -Name WaitForExit -Value { param($timeout) return $true }
Wait-ConsoleExit -Process $exited -TimeoutMilliseconds 1
$passed++

Write-Output "PASS: $passed offline restart checks. Fixtures: $testDir"
