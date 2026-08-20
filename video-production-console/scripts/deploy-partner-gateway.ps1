# Uploads, validates, and starts a partner-gateway release on cpa.
# Dry-run records the operation sequence without deleting data or touching the existing API service.
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$HostAlias = "cpa",
    [string]$ExpectedHost = "23.138.12.112",
    [string]$RemoteRoot = "/opt/video-partner-gateway",
    [string]$BundleRoot,
    [string]$TlsCertPath = "C:\PartnerBuildDeps\tls\gateway.crt",
    [string]$TlsKeyPath = "C:\PartnerBuildDeps\tls\gateway.key",
    [string]$OperationLogPath,
    [string]$TestHookPath,
    [string]$FailAt,
    [switch]$DryRun,
    [switch]$ProvisionAuraSecret,
    [System.Security.SecureString]$AuraKeySecure
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$versionValue = $Version.TrimStart('v')
if ($versionValue -notmatch '^\d+\.\d+\.\d+$') {
    throw "Version must be semver"
}

$script:Operations = New-Object System.Collections.Generic.List[string]
$script:AuraPlain = $null
$allowedBundle = @("partner-gateway", "Dockerfile", "compose.yml", "gateway.env", "SHA256SUMS.txt")

if ($TestHookPath) {
    if (!(Test-Path -LiteralPath $TestHookPath -PathType Leaf)) {
        throw "TestHookPath not found"
    }
    . $TestHookPath
}

function Write-Operation {
    param([string]$Name)
    $script:Operations.Add($Name)
    Write-Host $Name
    if ($OperationLogPath) {
        Add-Content -LiteralPath $OperationLogPath -Value $Name
    }
}

function ConvertFrom-SecureText {
    param([System.Security.SecureString]$Secure)
    if (-not $Secure) { return $null }
    $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
    }
}

function Resolve-RemoteGatewayPath {
    param([string]$Path)
    $normalized = $Path.Replace('\', '/')
    if ($normalized -ne $RemoteRoot -and -not $normalized.StartsWith("$RemoteRoot/")) {
        throw "remote path must begin with $RemoteRoot/"
    }
    return $normalized
}

function Invoke-Remote {
    param([string]$Command, [string]$StdinText)
    Resolve-RemoteGatewayPath $RemoteRoot | Out-Null
    if (Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue) {
        return Invoke-PartnerGatewayRemote -Command $Command -StdinText $StdinText
    }
    if ($DryRun) {
        return "ok"
    }
    $ssh = Get-Command ssh -ErrorAction SilentlyContinue
    if (-not $ssh) {
        throw "ssh is not available"
    }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $ssh.Source
    $psi.Arguments = "$HostAlias $Command"
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $proc = New-Object System.Diagnostics.Process
    $proc.StartInfo = $psi
    [void]$proc.Start()
    if ($null -ne $StdinText) {
        $normalized = [string]$StdinText
        $normalized = $normalized.Replace("`r`n", "`n").Replace("`r", "`n")
        $proc.StandardInput.Write($normalized)
    }
    $proc.StandardInput.Close()
    $stdout = $proc.StandardOutput.ReadToEnd()
    $stderr = $proc.StandardError.ReadToEnd()
    $proc.WaitForExit()
    if ($proc.ExitCode -ne 0) {
        $detail = $stderr.Trim()
        if ($detail -eq "") {
            $detail = $stdout.Trim()
        }
        if ($detail -eq "") {
            throw "remote command failed"
        }
        throw "remote command failed: $detail"
    }
    return $stdout
}

function Invoke-Upload {
    param([string]$LocalPath, [string]$RemotePath)
    $resolved = Resolve-RemoteGatewayPath $RemotePath
    $leaf = [System.IO.Path]::GetFileName($LocalPath)
    if ($leaf -eq "partner-ca.key") {
        throw "never upload partner-ca.key"
    }
    if (Get-Command Invoke-PartnerGatewayUpload -ErrorAction SilentlyContinue) {
        return Invoke-PartnerGatewayUpload -LocalPath $LocalPath -RemotePath $resolved
    }
    if ($DryRun) {
        return
    }
    $scp = Get-Command scp -ErrorAction SilentlyContinue
    if (-not $scp) {
        throw "scp is not available"
    }
    & $scp.Source -- $LocalPath "${HostAlias}:$resolved"
    if ($LASTEXITCODE -ne 0) {
        throw "upload failed"
    }
}

function Test-CpaAlias {
    if (Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue) {
        return
    }
    if ($DryRun) {
        return
    }
    $ssh = Get-Command ssh -ErrorAction SilentlyContinue
    if (-not $ssh) {
        throw "ssh is not available"
    }
    $output = & $ssh.Source -G $HostAlias
    $hostname = ($output | Where-Object { $_ -match '^hostname\s+' } | Select-Object -First 1)
    if (-not $hostname) {
        throw "SSH alias $HostAlias did not resolve"
    }
    $value = ($hostname -split '\s+', 2)[1].Trim()
    if ($value -ne $ExpectedHost) {
        throw "SSH alias $HostAlias must resolve to $ExpectedHost"
    }
}

function Get-BundleDirectory {
    if ($BundleRoot) {
        return $BundleRoot
    }
    return (Join-Path $repoRoot "release\gateway\$versionValue")
}

function Assert-Bundle {
    param([string]$Dir)
    if ($DryRun -and -not (Test-Path -LiteralPath $Dir -PathType Container)) {
        return
    }
    if (!(Test-Path -LiteralPath $Dir -PathType Container)) {
        throw "bundle directory missing: $Dir"
    }
    $names = @(Get-ChildItem -LiteralPath $Dir -Force | ForEach-Object { $_.Name } | Sort-Object)
    $expected = @($allowedBundle | Sort-Object)
    if (($names -join '|') -ne ($expected -join '|')) {
        throw "bundle must contain exactly: $($allowedBundle -join ', ')"
    }
}

function Invoke-AuraSecretProvision {
    if (-not $AuraKeySecure) {
        $script:AuraKeySecure = Read-Host -AsSecureString "Aura API key"
        $AuraKeySecure = $script:AuraKeySecure
    }
    $script:AuraPlain = ConvertFrom-SecureText $AuraKeySecure
    if ([string]::IsNullOrWhiteSpace($script:AuraPlain)) {
        throw "Aura API key is required"
    }
    $remoteSecret = Resolve-RemoteGatewayPath "$RemoteRoot/secrets/aura_api_key"
    $auraBytes = [Text.Encoding]::UTF8.GetBytes($script:AuraPlain.Trim())
    $auraB64 = [Convert]::ToBase64String($auraBytes)
    $script:AuraPlain = $null
    $helper = @"
set -e
umask 077
mkdir -p $RemoteRoot/secrets
tmp=`$(mktemp $RemoteRoot/secrets/.aura_api_key.XXXXXX)
printf '%s' '$auraB64' | base64 -d > "`$tmp"
chown 10001:10001 "`$tmp"
chmod 0400 "`$tmp"
mv -f "`$tmp" $remoteSecret
echo aura-secret-written
"@
    $auraB64 = $null
    $output = Invoke-Remote -Command "bash -s" -StdinText $helper
    if ($DryRun) {
        Write-Host "aura secret provisioned"
        return
    }
    if ((Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue) -or ($output -match 'aura-secret-written')) {
        Write-Host "aura secret provisioned"
        return
    }
    throw "aura secret provision failed"
}

function Invoke-Preflight {
    Write-Operation "preflight disk/memory/2443/network/container checks"
    $remote = @"
set -e
. /etc/os-release
echo os=`$ID:`$VERSION_ID
echo arch=`$(uname -m)
docker --version
docker compose version
df -B1 --output=avail $RemoteRoot 2>/dev/null || df -B1 --output=avail /
docker network inspect cliproxyapi_default >/dev/null
docker inspect -f '{{.State.Running}}' cli-proxy-api
ss -lnt | awk '{print `$4}' | grep -E ':2001$' >/dev/null
if ss -lnt | awk '{print `$4}' | grep -E ':2443$' >/dev/null; then
  if docker ps --format '{{.Names}}' | grep -qx partner-gateway; then
    echo port2443=gateway
  else
    echo port2443=busy
    exit 1
  fi
else
  echo port2443=free
fi
echo preflight-ok
"@
    $output = Invoke-Remote -Command "bash -s" -StdinText $remote
    if ($DryRun -or (Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue)) {
        return
    }
    if ($output -notmatch 'preflight-ok') {
        throw "preflight failed"
    }
}

function Invoke-CreateReleaseDir {
    $releaseDir = Resolve-RemoteGatewayPath "$RemoteRoot/releases/$versionValue"
    Write-Operation "create $releaseDir"
    $cmd = @"
set -e
mkdir -p $RemoteRoot/releases $RemoteRoot/data $RemoteRoot/secrets $RemoteRoot/tls $RemoteRoot/backups
mkdir -p $releaseDir
ln -sfn ../../data $releaseDir/data
ln -sfn ../../secrets $releaseDir/secrets
ln -sfn ../../tls $releaseDir/tls
if [ ! -s $RemoteRoot/secrets/admin_password ]; then
  umask 077
  tmp=`$(mktemp $RemoteRoot/secrets/.admin_password.XXXXXX)
  openssl rand -hex 24 > "`$tmp"
  chown 10001:10001 "`$tmp"
  chmod 0400 "`$tmp"
  mv -f "`$tmp" $RemoteRoot/secrets/admin_password
fi
chown 10001:10001 $RemoteRoot/data $RemoteRoot/secrets/admin_password
chmod 0400 $RemoteRoot/secrets 2>/dev/null || true
echo created
"@
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-UploadBundle {
    $releaseDir = Resolve-RemoteGatewayPath "$RemoteRoot/releases/$versionValue"
    Write-Operation "upload bundle to that release directory"
    $bundleDir = Get-BundleDirectory
    if ($DryRun -and -not (Test-Path -LiteralPath $bundleDir -PathType Container)) {
        return
    }
    Assert-Bundle $bundleDir
    foreach ($name in $allowedBundle) {
        Invoke-Upload -LocalPath (Join-Path $bundleDir $name) -RemotePath "$releaseDir/$name"
    }
    $uploadTls = -not $DryRun
    if ((Get-Command Invoke-PartnerGatewayUpload -ErrorAction SilentlyContinue) -and (Test-Path -LiteralPath $TlsCertPath) -and (Test-Path -LiteralPath $TlsKeyPath)) {
        $uploadTls = $true
    }
    if ($uploadTls) {
        if ((Split-Path -Leaf $TlsCertPath) -eq "partner-ca.key" -or (Split-Path -Leaf $TlsKeyPath) -eq "partner-ca.key") {
            throw "never upload partner-ca.key"
        }
        if (!(Test-Path -LiteralPath $TlsCertPath) -or !(Test-Path -LiteralPath $TlsKeyPath)) {
            if (-not $DryRun -and -not (Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue)) {
                throw "TLS leaf files are required for a live deploy"
            }
        } else {
            Invoke-Upload -LocalPath $TlsCertPath -RemotePath "$RemoteRoot/tls/gateway.crt"
            Invoke-Upload -LocalPath $TlsKeyPath -RemotePath "$RemoteRoot/tls/gateway.key"
            [void](Invoke-Remote -Command "bash -s" -StdinText @"
chown 10001:10001 $RemoteRoot/tls/gateway.crt $RemoteRoot/tls/gateway.key
chmod 0444 $RemoteRoot/tls/gateway.crt
chmod 0400 $RemoteRoot/tls/gateway.key
"@)
        }
    }
}

function Invoke-VerifyChecksums {
    Write-Operation "verify SHA256SUMS.txt"
    $releaseDir = Resolve-RemoteGatewayPath "$RemoteRoot/releases/$versionValue"
    $cmd = "set -e; cd $releaseDir; sha256sum -c SHA256SUMS.txt"
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-ComposeConfig {
    Write-Operation "docker compose config --quiet"
    $releaseDir = Resolve-RemoteGatewayPath "$RemoteRoot/releases/$versionValue"
    $cmd = "set -e; cd $releaseDir; docker compose -f compose.yml --env-file gateway.env config --quiet"
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-BackupCurrent {
    Write-Operation "backup current compose/env/tls metadata and SQLite"
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $backupDir = Resolve-RemoteGatewayPath "$RemoteRoot/backups/pre-$versionValue-$stamp"
    $cmd = @"
set -e
mkdir -p $backupDir
if [ -L $RemoteRoot/current ] || [ -d $RemoteRoot/current ]; then
  cp -a $RemoteRoot/current/compose.yml $backupDir/ 2>/dev/null || true
  cp -a $RemoteRoot/current/gateway.env $backupDir/ 2>/dev/null || true
  readlink $RemoteRoot/current > $backupDir/current.target || true
fi
if [ -d $RemoteRoot/tls ]; then
  ls -l $RemoteRoot/tls > $backupDir/tls.metadata
fi
if [ -f $RemoteRoot/data/gateway.db ]; then
  cp -a $RemoteRoot/data/gateway.db $backupDir/gateway.db
  cp -a $RemoteRoot/data/gateway.db-wal $backupDir/ 2>/dev/null || true
  cp -a $RemoteRoot/data/gateway.db-shm $backupDir/ 2>/dev/null || true
fi
echo $backupDir
"@
    $script:BackupDir = (Invoke-Remote -Command "bash -s" -StdinText $cmd).Trim()
}

function Get-CurrentTarget {
    $output = Invoke-Remote -Command "bash -s" -StdinText "readlink $RemoteRoot/current || true"
    return ([string]$output).Trim()
}

function Invoke-SwitchCurrent {
    param([string]$Target)
    $releaseDir = Resolve-RemoteGatewayPath "$RemoteRoot/releases/$versionValue"
    if ($Target) {
        $releaseDir = Resolve-RemoteGatewayPath $Target
    }
    $cmd = @"
set -e
ln -sfn $releaseDir $RemoteRoot/current.new
mv -Tf $RemoteRoot/current.new $RemoteRoot/current
"@
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-ComposeBuild {
    Write-Operation "docker compose build --pull"
    $cmd = "set -e; cd $RemoteRoot/current; docker compose -f compose.yml --env-file gateway.env build --pull"
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-ComposeUp {
    Write-Operation "docker compose up -d"
    $cmd = "set -e; cd $RemoteRoot/current; docker compose -f compose.yml --env-file gateway.env up -d"
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-WaitHealthy {
    Write-Operation "wait for healthy"
    if ($FailAt -eq "healthy") {
        throw "gateway health check failed"
    }
    $cmd = @"
set -e
for i in `$(seq 1 18); do
  status=`$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' partner-gateway 2>/dev/null || true)
  if [ "`$status" = "healthy" ]; then
    echo healthy
    exit 0
  fi
  sleep 5
done
echo unhealthy
exit 1
"@
    $output = Invoke-Remote -Command "bash -s" -StdinText $cmd
    if ($DryRun -or (Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue)) {
        return
    }
    if ($output -notmatch 'healthy') {
        throw "gateway health check failed"
    }
}

function Invoke-VerifyExistingApi {
    Write-Operation "verify existing cli-proxy-api container and port 2001 unchanged"
    $cmd = @"
set -e
docker inspect -f '{{.State.Running}} {{.Id}}' cli-proxy-api
ss -lnt | awk '{print `$4}' | grep -E ':2001$' >/dev/null
echo api-unchanged
"@
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

function Invoke-Rollback {
    param([string]$Previous)
    if ($Previous) {
        $previousPath = $Previous
        if (-not $previousPath.StartsWith("/")) {
            $previousPath = "$RemoteRoot/$Previous"
        }
        $resolved = Resolve-RemoteGatewayPath $previousPath
        Invoke-SwitchCurrent -Target $resolved
        $cmd = "set -e; cd $RemoteRoot/current; docker compose -f compose.yml --env-file gateway.env up -d"
        [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
        Write-Host "rolled back to previous current"
        return
    }
    Write-Host "no previous current to restore"
}

function Write-Receipt {
    $receipt = Resolve-RemoteGatewayPath "$RemoteRoot/backups/deployment-$versionValue.json"
    $cmd = @"
set -e
image=`$(docker inspect -f '{{.Image}}' partner-gateway 2>/dev/null || echo unknown)
hash=`$(cd $RemoteRoot/releases/$versionValue && sha256sum SHA256SUMS.txt | awk '{print `$1}')
printf '{"version":"%s","image":"%s","release_hash":"%s"}\n' '$versionValue' "`$image" "`$hash" > $receipt
echo receipt-ok
"@
    [void](Invoke-Remote -Command "bash -s" -StdinText $cmd)
}

try {
    Test-CpaAlias
    if ($ProvisionAuraSecret) {
        Invoke-AuraSecretProvision
        if (-not $DryRun -and -not (Get-Command Invoke-PartnerGatewayRemote -ErrorAction SilentlyContinue)) {
            return
        }
        if ($DryRun -and -not $PSBoundParameters.ContainsKey("Version")) {
            return
        }
    }

    $previous = $null
    Invoke-Preflight
    Invoke-CreateReleaseDir
    Invoke-UploadBundle
    Invoke-VerifyChecksums
    Invoke-ComposeConfig
    $previous = Get-CurrentTarget
    Invoke-BackupCurrent
    Write-Operation "switch current symlink atomically"
    Invoke-SwitchCurrent
    Invoke-ComposeBuild
    Invoke-ComposeUp
    try {
        Invoke-WaitHealthy
    } catch {
        Invoke-Rollback -Previous $previous
        throw
    }
    Invoke-VerifyExistingApi
    Write-Receipt
} finally {
    $script:AuraPlain = $null
}
