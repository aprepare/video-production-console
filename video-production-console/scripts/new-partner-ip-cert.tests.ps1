# Requires: pwsh -File scripts/new-partner-ip-cert.tests.ps1
# Certificate generation tests use a temporary directory only.
# If OpenSSL is missing, generation tests skip with a clear message.
param(
    [string]$OpenSSLPath
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$scriptPath = Join-Path $PSScriptRoot "new-partner-ip-cert.ps1"
$failures = 0
$skips = 0

function Write-Pass([string]$Message) {
    Write-Host "PASS: $Message"
}

function Write-Fail([string]$Message) {
    $script:failures++
    Write-Host "FAIL: $Message"
}

function Write-Skip([string]$Message) {
    $script:skips++
    Write-Host "SKIP: $Message"
}

function Resolve-OpenSSL {
    if ($OpenSSLPath) {
        if (Test-Path -LiteralPath $OpenSSLPath -PathType Leaf) {
            return (Resolve-Path -LiteralPath $OpenSSLPath).Path
        }
        throw "OpenSSLPath not found: $OpenSSLPath"
    }
    $cmd = Get-Command openssl -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    foreach ($candidate in @(
            "${env:ProgramFiles}\Git\usr\bin\openssl.exe",
            "${env:ProgramFiles}\OpenSSL-Win64\bin\openssl.exe",
            "${env:ProgramFiles}\OpenSSL\bin\openssl.exe"
        )) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

function Get-PowerShellHost {
    $pwsh = Get-Command pwsh -ErrorAction SilentlyContinue
    if ($pwsh) { return $pwsh.Source }
    $current = (Get-Process -Id $PID).Path
    if ($current) { return $current }
    return (Get-Command powershell).Source
}

function Invoke-CertScript {
    param(
        [string]$OutputRoot,
        [string]$OpenSSL,
        [string]$StdOutPath,
        [string]$StdErrPath
    )
    $hostExe = Get-PowerShellHost
    $argList = @("-NoProfile", "-File", $scriptPath, "-OutputRoot", $OutputRoot)
    if ($OpenSSL) { $argList += @("-OpenSSLPath", $OpenSSL) }
    $proc = Start-Process -FilePath $hostExe -ArgumentList $argList -Wait -PassThru -NoNewWindow `
        -RedirectStandardOutput $StdOutPath -RedirectStandardError $StdErrPath
    return $proc
}

function Get-Text([string]$Path) {
    if (!(Test-Path -LiteralPath $Path)) { return "" }
    return (Get-Content -LiteralPath $Path -Raw -ErrorAction SilentlyContinue)
}

if (!(Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    Write-Fail "certificate script is absent: $scriptPath"
    Write-Host ""
    Write-Host "RESULT: $failures failed"
    exit 1
}
Write-Pass "certificate script exists"

$openssl = $null
try {
    $openssl = Resolve-OpenSSL
} catch {
    Write-Fail $_.Exception.Message
}

$outputRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("vpc-partner-cert-" + [guid]::NewGuid().ToString("N"))
$logs = Join-Path ([System.IO.Path]::GetTempPath()) ("vpc-partner-cert-logs-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $logs | Out-Null

try {
    $insideOut = Join-Path $logs "inside.out"
    $insideErr = Join-Path $logs "inside.err"
    $inside = Invoke-CertScript -OutputRoot $repoRoot -OpenSSL $openssl -StdOutPath $insideOut -StdErrPath $insideErr
    $insideText = (Get-Text $insideOut) + (Get-Text $insideErr)
    if ($inside.ExitCode -ne 0 -and $insideText -match '(?i)repositor|repo root|inside') {
        Write-Pass "script refuses an output directory inside the repository"
    } else {
        Write-Fail "expected repo-inside OutputRoot to be rejected; exit=$($inside.ExitCode) output=$insideText"
    }

    if (-not $openssl) {
        Write-Skip "OpenSSL is not available; certificate generation tests were skipped. Install OpenSSL or pass -OpenSSLPath."
    } else {
        $genOut = Join-Path $logs "gen.out"
        $genErr = Join-Path $logs "gen.err"
        $gen = Invoke-CertScript -OutputRoot $outputRoot -OpenSSL $openssl -StdOutPath $genOut -StdErrPath $genErr
        $genText = (Get-Text $genOut) + (Get-Text $genErr)
        if ($gen.ExitCode -ne 0) {
            Write-Fail "certificate generation failed; exit=$($gen.ExitCode) output=$genText"
        } else {
            Write-Pass "certificate script completed in a temporary output directory"

            if ($genText -match '-----BEGIN ([A-Z0-9 ]+)?PRIVATE KEY-----') {
                Write-Fail "script printed private key content"
            } else {
                Write-Pass "script did not print key content"
            }

            $expected = @("partner-ca.key", "partner-ca.crt", "gateway.key", "gateway.crt")
            $missing = @()
            foreach ($name in $expected) {
                if (!(Test-Path -LiteralPath (Join-Path $outputRoot $name) -PathType Leaf)) {
                    $missing += $name
                }
            }
            $extraTemps = Get-ChildItem -LiteralPath $outputRoot -Force -ErrorAction SilentlyContinue |
                Where-Object { $_.Name -notin $expected }
            if ($missing.Count -eq 0) {
                Write-Pass "generated exactly the four required certificate files"
            } else {
                Write-Fail "missing generated files: $($missing -join ', ')"
            }
            if ($extraTemps) {
                Write-Fail "temporary files were not deleted: $($extraTemps.Name -join ', ')"
            } else {
                Write-Pass "CSR, serial and temporary config files were deleted"
            }

            $caCrt = Join-Path $outputRoot "partner-ca.crt"
            $leafCrt = Join-Path $outputRoot "gateway.crt"
            if ((Test-Path -LiteralPath $caCrt) -and (Test-Path -LiteralPath $leafCrt)) {
                $verify = & $openssl verify -CAfile $caCrt $leafCrt 2>&1 | Out-String
                if ($LASTEXITCODE -eq 0 -and $verify -match 'OK') {
                    Write-Pass "leaf certificate validates against the CA"
                } else {
                    Write-Fail "openssl verify failed: $verify"
                }

                $checkIp = & $openssl x509 -in $leafCrt -checkip 23.138.12.112 -noout 2>&1 | Out-String
                $ipText = & $openssl x509 -in $leafCrt -noout -text 2>&1 | Out-String
                if ($LASTEXITCODE -eq 0 -and ($checkIp -match '(?i)OK|yes' -or $ipText -match 'IP Address:\s*23\.138\.12\.112')) {
                    Write-Pass "leaf certificate contains IP SAN 23.138.12.112"
                } else {
                    Write-Fail "IP SAN 23.138.12.112 missing; checkip=$checkIp text=$ipText"
                }

                if ($ipText -match 'CA:TRUE' -and $ipText -notmatch 'CA:FALSE') {
                    Write-Fail "leaf certificate is a CA"
                } elseif ($ipText -match 'CA:FALSE') {
                    Write-Pass "leaf certificate is not a CA"
                } else {
                    Write-Fail "could not confirm leaf basicConstraints CA:FALSE: $ipText"
                }
            }

            foreach ($keyName in @("partner-ca.key", "gateway.key")) {
                $keyPath = Join-Path $outputRoot $keyName
                $acl = Get-Acl -LiteralPath $keyPath
                $current = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
                $idents = @($acl.Access | ForEach-Object { $_.IdentityReference.Value })
                if ($acl.AreAccessRulesProtected -and ($idents | Where-Object { $_ -eq $current })) {
                    Write-Pass "$keyName ACL is restricted to the current user"
                } else {
                    Write-Fail "$keyName ACL not restricted (protected=$($acl.AreAccessRulesProtected) idents=$($idents -join ','))"
                }
            }

            $overwriteOut = Join-Path $logs "overwrite.out"
            $overwriteErr = Join-Path $logs "overwrite.err"
            $overwrite = Invoke-CertScript -OutputRoot $outputRoot -OpenSSL $openssl -StdOutPath $overwriteOut -StdErrPath $overwriteErr
            $overwriteText = (Get-Text $overwriteOut) + (Get-Text $overwriteErr)
            if ($overwrite.ExitCode -ne 0 -and $overwriteText -match '(?i)exist|overwrite') {
                Write-Pass "script refuses to overwrite existing keys"
            } else {
                Write-Fail "expected overwrite refusal; exit=$($overwrite.ExitCode) output=$overwriteText"
            }
        }
    }
} finally {
    if (Test-Path -LiteralPath $outputRoot) {
        Remove-Item -LiteralPath $outputRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $logs) {
        Remove-Item -LiteralPath $logs -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Write-Host ""
if ($failures -gt 0) {
    Write-Host "RESULT: $failures failed, $skips skipped"
    exit 1
}
Write-Host "RESULT: all checks passed ($skips skipped)"
exit 0
