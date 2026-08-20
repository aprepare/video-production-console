# Pinned-CA smoke runner for the partner gateway.
# Prints only route, model, status, request id, and duration. Never prints keys, sessions, or image bytes.
param(
    [Parameter(Mandatory = $true)][string]$CAFile,
    [Parameter(Mandatory = $true)][string]$DeviceHash,
    [Parameter(Mandatory = $true)][string]$ImageOutputDir,
    [string]$BaseUrl = "https://23.138.12.112:2443",
    [string]$AppVersion = "0.1.0",
    [int]$TimeoutSeconds = 180,
    [switch]$UseStoredPartnerCredential,
    [System.Security.SecureString]$ActivationKeySecure
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$requiredSan = "23.138.12.112"
$script:ActivationPlain = $null
$script:SessionToken = $null
$script:HttpClient = $null

function Get-FullPath([string]$Path) {
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return [System.IO.Path]::GetFullPath($Path)
    }
    return [System.IO.Path]::GetFullPath((Join-Path (Get-Location).Path $Path))
}

function Test-PathOutsideRepo {
    param([string]$Candidate, [string]$Root)
    $full = (Get-FullPath $Candidate).TrimEnd('\', '/')
    $repo = (Get-FullPath $Root).TrimEnd('\', '/')
    if ([string]::Equals($full, $repo, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $false
    }
    $prefix = $repo + [System.IO.Path]::DirectorySeparatorChar
    return -not $full.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)
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

function Import-PinnedCertificate {
    param([string]$Path)
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "CAFile is required and must exist"
    }
    $bytes = [System.IO.File]::ReadAllBytes((Get-FullPath $Path))
    try {
        return New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList @(, $bytes)
    } catch {
        $text = [System.Text.Encoding]::ASCII.GetString($bytes)
        $match = [regex]::Match($text, "-----BEGIN CERTIFICATE-----(.+)-----END CERTIFICATE-----", [System.Text.RegularExpressions.RegexOptions]::Singleline)
        if (-not $match.Success) {
            throw "CAFile is not a certificate"
        }
        $raw = [Convert]::FromBase64String(($match.Groups[1].Value -replace '\s', ''))
        return New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList @(, $raw)
    }
}

function Test-GatewayCertificate {
    param(
        [System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [System.Security.Cryptography.X509Certificates.X509Chain]$Chain,
        [System.Net.Security.SslPolicyErrors]$PolicyErrors,
        [System.Security.Cryptography.X509Certificates.X509Certificate2]$PinnedCA
    )
    if (-not $Certificate -or -not $PinnedCA) {
        return $false
    }
    $hasSan = $false
    foreach ($ext in $Certificate.Extensions) {
        if ($ext.Oid.Value -eq "2.5.29.17") {
            $text = $ext.Format($false)
            if ($text -match [regex]::Escape($requiredSan)) {
                $hasSan = $true
            }
        }
    }
    if (-not $hasSan -and $Certificate.Subject -match [regex]::Escape($requiredSan)) {
        $hasSan = $true
    }
    if (-not $hasSan) {
        return $false
    }

    if ($Certificate.Thumbprint -eq $PinnedCA.Thumbprint) {
        return $true
    }

    $build = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    try {
        $build.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
        $build.ChainPolicy.VerificationFlags = [System.Security.Cryptography.X509Certificates.X509VerificationFlags]::AllowUnknownCertificateAuthority
        $build.ChainPolicy.ExtraStore.Add($PinnedCA) | Out-Null
        [void]$build.Build($Certificate)
        if ($build.ChainElements.Count -lt 1) {
            return $false
        }
        foreach ($element in $build.ChainElements) {
            if ($element.Certificate.Thumbprint -eq $PinnedCA.Thumbprint) {
                return $true
            }
        }
        return $false
    } finally {
        $build.Dispose()
    }
}

function New-PinnedHttpClient {
    param([System.Security.Cryptography.X509Certificates.X509Certificate2]$PinnedCA)
    Add-Type -AssemblyName System.Net.Http | Out-Null
    if (-not ("PartnerGatewayPinnedValidator" -as [type])) {
        Add-Type -ReferencedAssemblies @("System.Net.Http", "System") -TypeDefinition @"
using System;
using System.Net.Http;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
public static class PartnerGatewayPinnedValidator {
    public static X509Certificate2 Ca;
    public static readonly System.Func<HttpRequestMessage, X509Certificate2, X509Chain, SslPolicyErrors, bool> Callback = Validate;
    public static bool Validate(HttpRequestMessage request, X509Certificate2 certificate, X509Chain chain, SslPolicyErrors errors) {
        if (certificate == null || Ca == null) { return false; }
        string san = "";
        foreach (X509Extension ext in certificate.Extensions) {
            if (ext.Oid != null && ext.Oid.Value == "2.5.29.17") {
                san += ext.Format(false);
            }
        }
        if (san.IndexOf("23.138.12.112") < 0 && certificate.Subject.IndexOf("23.138.12.112") < 0) {
            return false;
        }
        if (string.Equals(certificate.Thumbprint, Ca.Thumbprint, StringComparison.OrdinalIgnoreCase)) {
            return true;
        }
        using (X509Chain built = new X509Chain()) {
            built.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
            built.ChainPolicy.VerificationFlags = X509VerificationFlags.AllowUnknownCertificateAuthority;
            built.ChainPolicy.ExtraStore.Add(Ca);
            built.Build(certificate);
            foreach (X509ChainElement element in built.ChainElements) {
                if (string.Equals(element.Certificate.Thumbprint, Ca.Thumbprint, StringComparison.OrdinalIgnoreCase)) {
                    return true;
                }
            }
        }
        return false;
    }
}
"@
    }
    [PartnerGatewayPinnedValidator]::Ca = $PinnedCA
    $handler = New-Object System.Net.Http.HttpClientHandler
    $handler.CheckCertificateRevocationList = $false
    $handler.ServerCertificateCustomValidationCallback = [PartnerGatewayPinnedValidator]::Callback
    $client = New-Object System.Net.Http.HttpClient -ArgumentList $handler
    $client.Timeout = [TimeSpan]::FromSeconds([Math]::Max(5, $TimeoutSeconds))
    return $client
}

function Write-SmokeResult {
    param([string]$Route, [string]$Model, [int]$Status, [string]$RequestId, [double]$DurationMs)
    $modelValue = $Model
    if (-not $modelValue) { $modelValue = "-" }
    $idValue = $RequestId
    if (-not $idValue) { $idValue = "-" }
    Write-Host ("route={0} model={1} status={2} request_id={3} duration_ms={4:N0}" -f $Route, $modelValue, $Status, $idValue, $DurationMs)
}

function Get-RequestId {
    param($Response, [string]$Body)
    foreach ($name in @("X-Request-ID", "Request-ID", "x-request-id")) {
        if ($Response.Headers.Contains($name)) {
            return @($Response.Headers.GetValues($name))[0]
        }
    }
    if ($Body -and $Body -match '"request_id"\s*:\s*"([^"]+)"') {
        return $Matches[1]
    }
    return ""
}

function Invoke-JsonRequest {
    param(
        [string]$Method,
        [string]$Route,
        [string]$Model,
        [string]$Body,
        [string]$Bearer
    )
    $uri = $BaseUrl.TrimEnd("/") + $Route
    $request = New-Object System.Net.Http.HttpRequestMessage -ArgumentList @([System.Net.Http.HttpMethod]::new($Method), $uri)
    if ($Bearer) {
        $request.Headers.Authorization = New-Object System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", $Bearer)
    }
    if ($Method -ne "GET" -and -not [string]::IsNullOrEmpty($Body)) {
        $request.Content = New-Object System.Net.Http.StringContent($Body, [System.Text.Encoding]::UTF8, "application/json")
    }
    $started = [DateTime]::UtcNow
    $response = $script:HttpClient.SendAsync($request).GetAwaiter().GetResult()
    $duration = ([DateTime]::UtcNow - $started).TotalMilliseconds
    $text = ""
    if ($response.Content) {
        $bytes = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
        if ($Route -eq "/v1/images/generations") {
            $text = [System.Text.Encoding]::UTF8.GetString($bytes)
            Save-ImagePayload -Body $text
        } elseif ($bytes) {
            $text = [System.Text.Encoding]::UTF8.GetString($bytes)
        }
    }
    $requestId = Get-RequestId -Response $response -Body $text
    Write-SmokeResult -Route $Route -Model $Model -Status ([int]$response.StatusCode) -RequestId $requestId -DurationMs $duration
    return @{ Status = [int]$response.StatusCode; Body = $text }
}

function Save-ImagePayload {
    param([string]$Body)
    if (-not $Body) { return }
    try {
        $parsed = $Body | ConvertFrom-Json
        if (-not $parsed -or -not $parsed.data) { return }
        $index = 0
        foreach ($item in @($parsed.data)) {
            $b64 = $item.b64_json
            if (-not $b64) { continue }
            $bytes = [Convert]::FromBase64String($b64)
            $path = Join-Path $ImageOutputDir ("smoke-image-{0}.png" -f $index)
            [System.IO.File]::WriteAllBytes($path, $bytes)
            $index++
        }
    } catch {
        return
    }
}

function Get-ActivationKey {
    if ($UseStoredPartnerCredential -and -not $ActivationKeySecure) {
        throw "stored partner credential is not available on this machine"
    }
    if (-not $ActivationKeySecure) {
        $ActivationKeySecure = Read-Host -AsSecureString "activation key"
    }
    $value = ConvertFrom-SecureText $ActivationKeySecure
    if ([string]::IsNullOrWhiteSpace($value)) {
        throw "activation key is required"
    }
    return $value
}

try {
    if ([string]::IsNullOrWhiteSpace($DeviceHash)) {
        throw "DeviceHash is required"
    }
    $ca = Import-PinnedCertificate $CAFile
    if (-not (Test-PathOutsideRepo $ImageOutputDir $repoRoot)) {
        throw "ImageOutputDir must be a temporary directory outside the repository"
    }
    if (!(Test-Path -LiteralPath $ImageOutputDir -PathType Container)) {
        New-Item -ItemType Directory -Path $ImageOutputDir -Force | Out-Null
    }

    $script:HttpClient = New-PinnedHttpClient $ca
    $script:ActivationPlain = Get-ActivationKey
    $activateBody = @{
        activation_key = $script:ActivationPlain
        device_hash    = $DeviceHash
        app_version    = $AppVersion
        edition        = "partner"
    } | ConvertTo-Json -Compress
    $activated = Invoke-JsonRequest -Method "POST" -Route "/auth/activate" -Model "-" -Body $activateBody -Bearer $null
    $session = $null
    try {
        $session = ($activated.Body | ConvertFrom-Json).session_token
    } catch {
        throw "activation failed"
    }
    $script:SessionToken = $session
    $script:ActivationPlain = $null

    [void](Invoke-JsonRequest -Method "GET" -Route "/v1/models" -Model "-" -Body $null -Bearer $script:SessionToken)
    $chatSol = '{"model":"gpt-5.6-sol","reasoning_effort":"medium","messages":[{"role":"user","content":"ping"}]}'
    [void](Invoke-JsonRequest -Method "POST" -Route "/v1/chat/completions" -Model "gpt-5.6-sol" -Body $chatSol -Bearer $script:SessionToken)
    $chatGrok = '{"model":"grok-4.6","reasoning_effort":"medium","messages":[{"role":"user","content":"ping"}]}'
    [void](Invoke-JsonRequest -Method "POST" -Route "/v1/chat/completions" -Model "grok-4.6" -Body $chatGrok -Bearer $script:SessionToken)
    $imageBody = '{"model":"gpt-image-2","n":1,"size":"1024x1024","prompt":"smoke"}'
    [void](Invoke-JsonRequest -Method "POST" -Route "/v1/images/generations" -Model "gpt-image-2" -Body $imageBody -Bearer $script:SessionToken)
} finally {
    $script:ActivationPlain = $null
    $script:SessionToken = $null
    if ($script:HttpClient) {
        try { $script:HttpClient.Dispose() } catch { }
        $script:HttpClient = $null
    }
    try { [System.Net.ServicePointManager]::ServerCertificateValidationCallback = $null } catch { }
}
exit 0

