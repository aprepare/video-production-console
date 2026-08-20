param(
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [string]$OpenSSLPath
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$serverIP = "23.138.12.112"

function Resolve-OpenSSLBinary {
    param([string]$Explicit)
    if ($Explicit) {
        if (!(Test-Path -LiteralPath $Explicit -PathType Leaf)) {
            throw "OpenSSLPath not found: $Explicit"
        }
        return (Resolve-Path -LiteralPath $Explicit).Path
    }
    $cmd = Get-Command openssl -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    foreach ($candidate in @(
            (Join-Path $env:ProgramFiles "Git\usr\bin\openssl.exe"),
            (Join-Path $env:ProgramFiles "OpenSSL-Win64\bin\openssl.exe"),
            (Join-Path $env:ProgramFiles "OpenSSL\bin\openssl.exe")
        )) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    throw "OpenSSL is not available. Install OpenSSL or pass -OpenSSLPath."
}

function Get-FullPath([string]$Path) {
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return [System.IO.Path]::GetFullPath($Path)
    }
    return [System.IO.Path]::GetFullPath((Join-Path (Get-Location).Path $Path))
}

function Test-IsAtOrBelowRepoRoot {
    param([string]$Candidate, [string]$Root)
    $full = (Get-FullPath $Candidate).TrimEnd('\')
    $repo = (Get-FullPath $Root).TrimEnd('\')
    if ([string]::Equals($full, $repo, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $true
    }
    $prefix = $repo + [System.IO.Path]::DirectorySeparatorChar
    return $full.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)
}

function Redact-KeyMaterial([string]$Text) {
    if ([string]::IsNullOrEmpty($Text)) { return $Text }
    return [regex]::Replace($Text, '-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?-----END [^-]*PRIVATE KEY-----', '[redacted private key]')
}

function Restrict-KeyAcl([string]$Path) {
    $acl = Get-Acl -LiteralPath $Path
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($rule in @($acl.Access)) {
        [void]$acl.RemoveAccessRule($rule)
    }
    $user = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    $access = New-Object System.Security.AccessControl.FileSystemAccessRule(
        $user,
        [System.Security.AccessControl.FileSystemRights]'Read,Write',
        [System.Security.AccessControl.AccessControlType]::Allow
    )
    $acl.AddAccessRule($access)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Invoke-OpenSSL {
    param(
        [string]$OpenSSL,
        [string[]]$OpenSslArgs,
        [string]$WorkDir,
        [string]$ConfigPath
    )
    $stdout = Join-Path $WorkDir ("openssl-out-" + [guid]::NewGuid().ToString("N") + ".log")
    $stderr = Join-Path $WorkDir ("openssl-err-" + [guid]::NewGuid().ToString("N") + ".log")
    $previousConf = $env:OPENSSL_CONF
    if ($ConfigPath) { $env:OPENSSL_CONF = $ConfigPath }
    try {
        $proc = Start-Process -FilePath $OpenSSL -ArgumentList $OpenSslArgs -WorkingDirectory $WorkDir `
            -Wait -PassThru -NoNewWindow -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    } finally {
        if ($null -eq $previousConf) {
            Remove-Item Env:OPENSSL_CONF -ErrorAction SilentlyContinue
        } else {
            $env:OPENSSL_CONF = $previousConf
        }
    }
    $text = ""
    foreach ($log in @($stdout, $stderr)) {
        if (Test-Path -LiteralPath $log) {
            $text += [System.IO.File]::ReadAllText($log)
            Remove-Item -LiteralPath $log -Force -ErrorAction SilentlyContinue
        }
    }
    if ($proc.ExitCode -ne 0) {
        throw "openssl failed ($($OpenSslArgs -join ' ')): $(Redact-KeyMaterial $text)".Trim()
    }
    return $text
}

$openssl = Resolve-OpenSSLBinary -Explicit $OpenSSLPath
$outputFull = Get-FullPath $OutputRoot
if (Test-IsAtOrBelowRepoRoot -Candidate $outputFull -Root $repoRoot) {
    throw "OutputRoot must not be equal to or below the repository root: $outputFull"
}

$caKey = Join-Path $outputFull "partner-ca.key"
$caCrt = Join-Path $outputFull "partner-ca.crt"
$leafKey = Join-Path $outputFull "gateway.key"
$leafCrt = Join-Path $outputFull "gateway.crt"
foreach ($keyPath in @($caKey, $leafKey)) {
    if (Test-Path -LiteralPath $keyPath) {
        throw "Refusing to overwrite existing key: $keyPath"
    }
}

New-Item -ItemType Directory -Path $outputFull -Force | Out-Null
$work = Join-Path $outputFull (".tmp-cert-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work | Out-Null

$baseCnf = Join-Path $work "openssl.cnf"
$caCnf = Join-Path $work "ca.cnf"
$leafReqCnf = Join-Path $work "leaf-req.cnf"
$leafCnf = Join-Path $work "leaf.cnf"
$leafCsr = Join-Path $work "gateway.csr"
$serial = Join-Path $outputFull "partner-ca.srl"
$serialWork = Join-Path $work "partner-ca.srl"

try {
    Set-Content -LiteralPath $baseCnf -Encoding ASCII -Value @"
[req]
distinguished_name = req_distinguished_name
prompt = no

[req_distinguished_name]
CN = unused
"@

    Set-Content -LiteralPath $caCnf -Encoding ASCII -Value @"
[req]
distinguished_name = req_distinguished_name
x509_extensions = v3_ca
prompt = no

[req_distinguished_name]
CN = Video Production Console Partner CA

[v3_ca]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
"@

    Set-Content -LiteralPath $leafReqCnf -Encoding ASCII -Value @"
[req]
distinguished_name = req_distinguished_name
prompt = no

[req_distinguished_name]
CN = $serverIP
"@

    Set-Content -LiteralPath $leafCnf -Encoding ASCII -Value @"
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:$serverIP
"@

    Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $baseCnf -OpenSslArgs @("genrsa", "-out", $caKey, "4096") | Out-Null
    Restrict-KeyAcl $caKey
    Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $caCnf -OpenSslArgs @(
        "req", "-new", "-x509", "-sha256", "-key", $caKey, "-out", $caCrt,
        "-days", "3650", "-config", $caCnf
    ) | Out-Null

    Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $baseCnf -OpenSslArgs @("genrsa", "-out", $leafKey, "3072") | Out-Null
    Restrict-KeyAcl $leafKey
    Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $leafReqCnf -OpenSslArgs @(
        "req", "-new", "-sha256", "-key", $leafKey, "-out", $leafCsr,
        "-subj", "/CN=$serverIP", "-config", $leafReqCnf
    ) | Out-Null
    Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $baseCnf -OpenSslArgs @(
        "x509", "-req", "-in", $leafCsr, "-CA", $caCrt, "-CAkey", $caKey,
        "-CAcreateserial", "-CAserial", $serialWork, "-out", $leafCrt,
        "-days", "365", "-sha256", "-extfile", $leafCnf
    ) | Out-Null

    $verify = Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $baseCnf -OpenSslArgs @("verify", "-CAfile", $caCrt, $leafCrt)
    if ($verify -notmatch 'OK') {
        throw "openssl verify did not report OK"
    }
    Invoke-OpenSSL -OpenSSL $openssl -WorkDir $work -ConfigPath $baseCnf -OpenSslArgs @(
        "x509", "-in", $leafCrt, "-checkip", $serverIP, "-noout"
    ) | Out-Null

    Write-Host "Wrote partner CA and IP-SAN leaf certificate for $serverIP"
} finally {
    foreach ($temp in @($caCnf, $leafCnf, $leafCsr, $serial, $serialWork, $work)) {
        if (Test-Path -LiteralPath $temp) {
            Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    Get-ChildItem -LiteralPath $outputFull -Force -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -notin @("partner-ca.key", "partner-ca.crt", "gateway.key", "gateway.crt") } |
        ForEach-Object { Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue }
}
