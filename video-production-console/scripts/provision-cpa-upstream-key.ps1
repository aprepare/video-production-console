# Provisions an isolated CLIProxyAPI client key for the partner gateway.
# Local tests pass -ManagementURL and -SecretFile. Production uploads a remote helper over ssh cpa.
# Never prints key material or the management password.
param(
    [string]$HostAlias = "cpa",
    [string]$ExpectedHost = "23.138.12.112",
    [string]$RemoteSecretPath = "/opt/video-partner-gateway/secrets/upstream_api_key",
    [string]$ManagementURL,
    [string]$ManagementPassword,
    [string]$SecretFile,
    [string]$GeneratedKey,
    [string]$RemoteHelperPath,
    [switch]$RemoveOldKey,
    [switch]$DryRun,
    [System.Security.SecureString]$OldKeySecure
)

$ErrorActionPreference = "Stop"
$script:PlainBuffers = New-Object System.Collections.Generic.List[object]

function ConvertFrom-SecureText {
    param([System.Security.SecureString]$Secure)
    if (-not $Secure) {
        return $null
    }
    $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
    }
}

function Clear-PlainBuffers {
    foreach ($buffer in $script:PlainBuffers) {
        if ($buffer -is [System.Text.StringBuilder]) {
            [void]$buffer.Clear()
        }
    }
    $script:PlainBuffers.Clear()
    $script:GeneratedKeyPlain = $null
    $script:OldKeyPlain = $null
    $script:ManagementPasswordPlain = $null
}

function New-RandomKeyHex {
    $bytes = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($bytes)
    } finally {
        $rng.Dispose()
    }
    return ([BitConverter]::ToString($bytes).Replace("-", "")).ToLowerInvariant()
}

function Get-ManagementApiRoot {
    param([string]$Base)
    $trimmed = $Base.TrimEnd("/")
    if ($trimmed -match '/v0/management/api-keys$') {
        return $trimmed
    }
    return "$trimmed/v0/management/api-keys"
}

function Invoke-JsonMethod {
    param(
        [string]$Url,
        [string]$Method,
        [string]$Bearer,
        [string]$Body
    )
    $request = [System.Net.HttpWebRequest]::Create($Url)
    $request.Method = $Method
    $request.Timeout = 30000
    $request.ContentType = "application/json"
    if ($Bearer) {
        $request.Headers.Add("Authorization", "Bearer $Bearer")
    }
    if ($null -ne $Body) {
        $payload = [System.Text.Encoding]::UTF8.GetBytes($Body)
        $request.ContentLength = $payload.Length
        $stream = $request.GetRequestStream()
        try {
            $stream.Write($payload, 0, $payload.Length)
        } finally {
            $stream.Dispose()
        }
    } else {
        $request.ContentLength = 0
    }
    try {
        $response = $request.GetResponse()
    } catch [System.Net.WebException] {
        $errorResponse = $_.Exception.Response
        if ($errorResponse) {
            $status = [int]$errorResponse.StatusCode
            $errorResponse.Close()
            return @{ StatusCode = $status; Body = "" }
        }
        throw
    }
    try {
        $status = [int]$response.StatusCode
        $reader = New-Object System.IO.StreamReader($response.GetResponseStream())
        try {
            try {
                $text = $reader.ReadToEnd()
            } catch {
                $text = ""
            }
        } finally {
            $reader.Dispose()
        }
        return @{ StatusCode = $status; Body = $text }
    } finally {
        $response.Close()
    }
}

function Write-SecretAtomic {
    param([string]$Path, [string]$Value)
    $dir = Split-Path -Parent $Path
    if (!(Test-Path -LiteralPath $dir -PathType Container)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    $partial = "$Path.partial"
    $encoding = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($partial, $Value, $encoding)
    if (Test-Path -LiteralPath $Path) {
        Remove-Item -LiteralPath $Path -Force
    }
    Move-Item -LiteralPath $partial -Destination $Path -Force
}

function Remove-SecretFileIfPresent {
    param([string]$Path)
    if ($Path -and (Test-Path -LiteralPath $Path)) {
        Remove-Item -LiteralPath $Path -Force
    }
    $partial = "$Path.partial"
    if ($Path -and (Test-Path -LiteralPath $partial)) {
        Remove-Item -LiteralPath $partial -Force
    }
}

function Get-RemoteHelperText {
    @"
#!/bin/bash
set -euo pipefail
umask 077
REMOTE_SECRET='__REMOTE_SECRET_PATH__'
SECRET_DIR=`$(dirname "`$REMOTE_SECRET")
mkdir -p "`$SECRET_DIR"
MANAGEMENT_PASSWORD=`$(docker inspect cli-proxy-api --format '{{range .Config.Env}}{{println .}}{{end}}' | awk -F= '/^MANAGEMENT_PASSWORD=/{print substr(`$0, index(`$0, "=") + 1); exit}')
if [ -z "`${MANAGEMENT_PASSWORD:-}" ]; then
  echo 'management password missing' >&2
  exit 1
fi
NEW_KEY=`$(openssl rand -hex 32)
export MANAGEMENT_PASSWORD
export NEW_KEY
python3 - <<'PY'
import json, os, sys, urllib.error, urllib.parse, urllib.request

pw = os.environ["MANAGEMENT_PASSWORD"]
new_key = os.environ["NEW_KEY"]
url = "http://127.0.0.1:2001/v0/management/" + "api-keys"

def call(method, body=None, query=""):
    target = url + query
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(target, data=data, method=method)
    req.add_header("Authorization", "Bearer " + pw)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")

status, text = call("GET")
if status != 200:
    sys.stderr.write("upstream key list failed\n")
    sys.exit(1)
payload = json.loads(text)
keys = payload.get("api-keys")
if not isinstance(keys, list):
    sys.stderr.write("upstream key list rejected\n")
    sys.exit(1)
keys = [item for item in keys if isinstance(item, str) and item]
if new_key not in keys:
    keys.append(new_key)
status, text = call("PUT", keys)
compact = text.replace(" ", "")
if status != 200 or '"status":"ok"' not in compact:
    encoded = urllib.parse.quote(new_key, safe="")
    call("DELETE", query="?value=" + encoded)
    sys.stderr.write("upstream key put failed\n")
    sys.exit(1)
print("ok")
PY
TMP_SECRET=`$(mktemp "`$SECRET_DIR/.upstream_api_key.XXXXXX")
printf '%s' "`$NEW_KEY" > "`$TMP_SECRET"
chown 10001:10001 "`$TMP_SECRET"
chmod 0400 "`$TMP_SECRET"
mv -f "`$TMP_SECRET" "`$REMOTE_SECRET"
echo 'upstream key provisioned'
"@
}

function Get-RemoveOldKeyHelperText {
    @"
#!/bin/bash
set -euo pipefail
umask 077
OLD_KEY=`$(cat)
MANAGEMENT_PASSWORD=`$(docker inspect cli-proxy-api --format '{{range .Config.Env}}{{println .}}{{end}}' | awk -F= '/^MANAGEMENT_PASSWORD=/{print substr(`$0, index(`$0, "=") + 1); exit}')
if [ -z "`${MANAGEMENT_PASSWORD:-}" ] || [ -z "`${OLD_KEY:-}" ]; then
  echo '000'
  exit 1
fi
ENC=`$(python3 -c 'import os,urllib.parse,sys; print(urllib.parse.quote(os.environ["OLD_KEY"], safe=""))' 2>/dev/null || printf '%s' "`$OLD_KEY")
STATUS=""
if docker network inspect cliproxyapi_default >/dev/null 2>&1; then
  set +e
  STATUS=`$(docker run --rm --network cliproxyapi_default -e OLD_KEY="`$OLD_KEY" -e MANAGEMENT_PASSWORD="`$MANAGEMENT_PASSWORD" curlimages/curl:8.13.0 \
    -sS -o /dev/null -w '%{http_code}' \
    -X DELETE \
    -H "Authorization: Bearer `$MANAGEMENT_PASSWORD" \
    "http://cli-proxy-api:2001/v0/management/api-keys?value=`$(python3 - <<'PY'
import os, urllib.parse
print(urllib.parse.quote(os.environ.get("OLD_KEY",""), safe=""))
PY
)")
  set -e
fi
if [ -z "`${STATUS:-}" ] || [ "`$STATUS" = "000" ]; then
  ENC=`$(python3 -c 'import os,urllib.parse; print(urllib.parse.quote(os.environ["OLD_KEY"], safe=""))')
  set +e
  STATUS=`$(curl -sS -o /dev/null -w '%{http_code}' \
    -X DELETE \
    -H "Authorization: Bearer `$MANAGEMENT_PASSWORD" \
    "http://127.0.0.1:2001/v0/management/api-keys?value=`$ENC")
  set -e
fi
printf '%s\n' "`$STATUS"
"@
}

function Resolve-RemoteGatewayPath {
    param([string]$Path)
    if (-not $Path.StartsWith("/opt/video-partner-gateway/")) {
        throw "remote path must begin with /opt/video-partner-gateway/"
    }
    return $Path
}

function Invoke-SshCpa {
    param([string]$Command, [string]$StdinText)
    if ($DryRun) {
        Write-Host "dry-run ssh $HostAlias"
        return "upstream key provisioned"
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
    if ($stderr.Trim() -ne "") {
        Write-Host $stderr.Trim()
    }
    return $stdout
}

try {
    $script:GeneratedKeyPlain = $GeneratedKey
    $script:ManagementPasswordPlain = $ManagementPassword
    $script:OldKeyPlain = $null

    if ($RemoveOldKey) {
        if (-not $OldKeySecure) {
            $OldKeySecure = Read-Host -AsSecureString "old upstream key"
        }
        $script:OldKeyPlain = ConvertFrom-SecureText $OldKeySecure
        if ([string]::IsNullOrWhiteSpace($script:OldKeyPlain)) {
            throw "old key is required"
        }
        if ($ManagementURL) {
            if (-not $script:ManagementPasswordPlain) {
                throw "ManagementPassword is required for local remove"
            }
            $encoded = [uri]::EscapeDataString($script:OldKeyPlain)
            $url = (Get-ManagementApiRoot $ManagementURL) + "?value=$encoded"
            $result = Invoke-JsonMethod -Url $url -Method "DELETE" -Bearer $script:ManagementPasswordPlain -Body $null
            Write-Host ([string]$result.StatusCode)
            return
        }
        Resolve-RemoteGatewayPath $RemoteSecretPath | Out-Null
        $helper = Get-RemoveOldKeyHelperText
        $remoteCmd = "bash -s"
        $output = Invoke-SshCpa -Command $remoteCmd -StdinText ($helper + "`n" + $script:OldKeyPlain)
        $statusLine = ($output -split "`r?`n" | Where-Object { $_ -match '^\d+$' } | Select-Object -Last 1)
        if (-not $statusLine) {
            $statusLine = $output.Trim()
        }
        Write-Host $statusLine
        return
    }

    if (-not $script:GeneratedKeyPlain) {
        $script:GeneratedKeyPlain = New-RandomKeyHex
    }

    if ($ManagementURL) {
        if (-not $script:ManagementPasswordPlain) {
            throw "ManagementPassword is required for local provision"
        }
        if (-not $SecretFile) {
            throw "SecretFile is required for local provision"
        }
        $url = Get-ManagementApiRoot $ManagementURL
        $body = ('{"old":null,"new":' + ($script:GeneratedKeyPlain | ConvertTo-Json -Compress) + '}')
        $result = $null
        try {
            $result = Invoke-JsonMethod -Url $url -Method "PATCH" -Bearer $script:ManagementPasswordPlain -Body $body
            if ([int]$result.StatusCode -ne 200) {
                throw "PATCH failed"
            }
            $ok = $false
            if ($result.Body -and $result.Body.Contains('"status"')) {
                try {
                    $parsed = $result.Body | ConvertFrom-Json
                    if ($parsed.status -eq "ok") { $ok = $true }
                } catch {
                    $ok = $result.Body.Contains('"status":"ok"')
                }
            }
            if (-not $ok) {
                throw "PATCH rejected"
            }
            Write-SecretAtomic -Path $SecretFile -Value $script:GeneratedKeyPlain
            if ($env:PARTNER_GATEWAY_PROVISION_FAIL_AFTER_PATCH -eq "1") {
                throw "forced post-patch failure"
            }
            Write-Host "upstream key provisioned"
        } catch {
            if ($result -and [int]$result.StatusCode -eq 200) {
                $encoded = [uri]::EscapeDataString($script:GeneratedKeyPlain)
                $deleteUrl = (Get-ManagementApiRoot $ManagementURL) + "?value=$encoded"
                [void](Invoke-JsonMethod -Url $deleteUrl -Method "DELETE" -Bearer $script:ManagementPasswordPlain -Body $null)
                Remove-SecretFileIfPresent $SecretFile
            }
            throw
        }
        return
    }

    Resolve-RemoteGatewayPath $RemoteSecretPath | Out-Null
    $helper = (Get-RemoteHelperText).Replace("__REMOTE_SECRET_PATH__", $RemoteSecretPath)
    if ($RemoteHelperPath) {
        $encoding = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllText($RemoteHelperPath, $helper, $encoding)
    }
    if ($DryRun) {
        Write-Host "upstream key provisioned"
        return
    }
    $output = Invoke-SshCpa -Command "bash -s" -StdinText $helper
    $line = ($output -split "`r?`n" | Where-Object { $_.Trim() -ne "" } | Select-Object -Last 1)
    if ($line -ne "upstream key provisioned") {
        throw "remote helper did not confirm provision"
    }
    Write-Host "upstream key provisioned"
} finally {
    Clear-PlainBuffers
}
