# Mock tests for scripts/smoke-partner-gateway.ps1.
# Does not call the real gateway or activate any production partner.
param()

$ErrorActionPreference = "Stop"
$scriptPath = Join-Path $PSScriptRoot "smoke-partner-gateway.ps1"
$repoRoot = Split-Path -Parent $PSScriptRoot
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

if (!(Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    Write-Fail "smoke script is absent: $scriptPath"
    Write-Host "RESULT: $failures failed"
    exit 1
}

$source = Get-Content -LiteralPath $scriptPath -Raw
if ($source -match 'SkipCertificateCheck') {
    Write-Fail "smoke script must not expose -SkipCertificateCheck"
} else {
    Write-Pass "no -SkipCertificateCheck switch"
}
if ($source -match 'ServerCertificateCustomValidationCallback') {
    Write-Pass "uses HttpClientHandler custom validation"
} else {
    Write-Fail "must use HttpClientHandler.ServerCertificateCustomValidationCallback"
}
if ($source -match 'Read-Host -AsSecureString') {
    Write-Pass "prompts activation key as SecureString"
} else {
    Write-Fail "must prompt activation key as SecureString"
}

function New-PinnedTestCertificate {
    $rsa = [System.Security.Cryptography.RSA]::Create(2048)
    $req = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new(
        "CN=23.138.12.112",
        $rsa,
        [System.Security.Cryptography.HashAlgorithmName]::SHA256,
        [System.Security.Cryptography.RSASignaturePadding]::Pkcs1
    )
    $req.CertificateExtensions.Add(
        [System.Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]::new($true, $false, 0, $true)
    )
    $san = [System.Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
    $san.AddIpAddress([System.Net.IPAddress]::Parse("23.138.12.112"))
    $req.CertificateExtensions.Add($san.Build($false))
    $cert = $req.CreateSelfSigned([datetime]::UtcNow.AddDays(-1), [datetime]::UtcNow.AddDays(30))
    $pfxPath = Join-Path ([System.IO.Path]::GetTempPath()) ("pgw-ca-" + [guid]::NewGuid().ToString("n") + ".pfx")
    [System.IO.File]::WriteAllBytes($pfxPath, $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pfx, "x"))
    $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::Exportable -bor [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::PersistKeySet
    return New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($pfxPath, "x", $flags)
}

function New-WrongSanCertificate {
    $rsa = [System.Security.Cryptography.RSA]::Create(2048)
    $req = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new(
        "CN=example.test",
        $rsa,
        [System.Security.Cryptography.HashAlgorithmName]::SHA256,
        [System.Security.Cryptography.RSASignaturePadding]::Pkcs1
    )
    $san = [System.Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
    $san.AddDnsName("example.test")
    $req.CertificateExtensions.Add($san.Build($false))
    $cert = $req.CreateSelfSigned([datetime]::UtcNow.AddDays(-1), [datetime]::UtcNow.AddDays(30))
    $pfxPath = Join-Path ([System.IO.Path]::GetTempPath()) ("pgw-wrong-" + [guid]::NewGuid().ToString("n") + ".pfx")
    [System.IO.File]::WriteAllBytes($pfxPath, $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pfx, "x"))
    $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::Exportable -bor [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::PersistKeySet
    return New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($pfxPath, "x", $flags)
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("pgw-smoke-" + [guid]::NewGuid().ToString("n"))
New-Item -ItemType Directory -Path $work -Force | Out-Null

try {
    $caPath = Join-Path $work "partner-ca.crt"
    $imageDir = Join-Path $work "images"
    New-Item -ItemType Directory -Path $imageDir -Force | Out-Null
    $cert = New-PinnedTestCertificate
    [System.IO.File]::WriteAllBytes($caPath, $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Cert))

    $missingOut = Join-Path $work "missing.out"
    $missingErr = Join-Path $work "missing.err"
    $hostExe = Get-PowerShellHost
    $missing = Start-Process -FilePath $hostExe -ArgumentList @(
        "-NoProfile", "-File", $scriptPath,
        "-DeviceHash", "device-test",
        "-ImageOutputDir", $imageDir
    ) -Wait -PassThru -NoNewWindow -RedirectStandardOutput $missingOut -RedirectStandardError $missingErr
    $missingText = (Get-Content -LiteralPath $missingOut -Raw -ErrorAction SilentlyContinue) + (Get-Content -LiteralPath $missingErr -Raw -ErrorAction SilentlyContinue)
    if ($missing.ExitCode -eq 0 -or $missingText -notmatch 'CAFile') {
        Write-Fail "CAFile must be mandatory"
    } else {
        Write-Pass "pinned CA file is mandatory"
    }

    $insideOut = Join-Path $work "inside.out"
    $insideErr = Join-Path $work "inside.err"
    $inside = Start-Process -FilePath $hostExe -ArgumentList @(
        "-NoProfile", "-File", $scriptPath,
        "-CAFile", $caPath,
        "-DeviceHash", "device-test",
        "-ImageOutputDir", (Join-Path $repoRoot "docs")
    ) -Wait -PassThru -NoNewWindow -RedirectStandardOutput $insideOut -RedirectStandardError $insideErr
    $insideText = (Get-Content -LiteralPath $insideOut -Raw -ErrorAction SilentlyContinue) + (Get-Content -LiteralPath $insideErr -Raw -ErrorAction SilentlyContinue)
    if ($inside.ExitCode -eq 0 -or $insideText -notmatch 'outside the repository') {
        Write-Fail "ImageOutputDir inside the repo was accepted"
    } else {
        Write-Pass "image output must stay outside the repository"
    }

    if ($source -match '23\.138\.12\.112') {
        Write-Pass "validation requires IP SAN 23.138.12.112"
    } else {
        Write-Fail "IP SAN requirement not present"
    }

    $png = [Convert]::FromBase64String("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
    $b64 = [Convert]::ToBase64String($png)
    $portFile = Join-Path $work "port.txt"
    $serverLog = Join-Path $work "server.log"
    $pfxPath = Join-Path $work "server.pfx"
    [System.IO.File]::WriteAllBytes($pfxPath, $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pfx, "x"))
    $server = [powershell]::Create()
    [void]$server.AddScript({
        param($PfxPath, $PortFile, $B64, $LogPath)
        $ErrorActionPreference = "Stop"
        try {
            $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::Exportable -bor [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::PersistKeySet
            $serverCert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($PfxPath, "x", $flags)
            if (-not $serverCert.HasPrivateKey) { throw "server certificate has no private key" }
            $listener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
            $listener.Start()
            $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
            [System.IO.File]::WriteAllText($PortFile, [string]$port)
            $replies = @{
                "POST /auth/activate" = '{"session_token":"test-session","partner_id":"p-test"}'
                "GET /v1/models" = '{"data":[{"id":"gpt-5.6-sol"}]}'
                "POST /v1/chat/completions" = '{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"ok"}}]}'
                "POST /v1/images/generations" = ('{"data":[{"b64_json":"' + $B64 + '"}]}')
            }
            for ($n = 0; $n -lt 8; $n++) {
                $tcp = $listener.AcceptTcpClient()
                $ssl = New-Object System.Net.Security.SslStream($tcp.GetStream(), $false)
                $ssl.AuthenticateAsServer($serverCert)
                $buffer = New-Object byte[] 65536
                $read = $ssl.Read($buffer, 0, $buffer.Length)
                $req = [System.Text.Encoding]::ASCII.GetString($buffer, 0, $read)
                $line = ($req -split "`r`n")[0]
                $key = ($line -split " HTTP/")[0]
                $body = '{"ok":true}'
                if ($replies.ContainsKey($key)) { $body = $replies[$key] }
                $payload = [System.Text.Encoding]::UTF8.GetBytes($body)
                $headers = "HTTP/1.1 200 OK`r`nX-Request-ID: req-mock-1`r`nContent-Type: application/json`r`nContent-Length: $($payload.Length)`r`nConnection: close`r`n`r`n"
                $headBytes = [System.Text.Encoding]::ASCII.GetBytes($headers)
                $ssl.Write($headBytes, 0, $headBytes.Length)
                $ssl.Write($payload, 0, $payload.Length)
                $ssl.Flush()
                $ssl.Dispose()
                $tcp.Close()
            }
            $listener.Stop()
        } catch {
            [System.IO.File]::WriteAllText($LogPath, $_.ToString())
        }
    }).AddArgument($pfxPath).AddArgument($portFile).AddArgument($b64).AddArgument($serverLog)
    $serverHandle = $server.BeginInvoke()
    $serverProc = $null
    $deadline = (Get-Date).AddSeconds(8)
    while (-not (Test-Path -LiteralPath $portFile) -and (Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 100
    }
    if (!(Test-Path -LiteralPath $portFile)) {
        $serverErrors = Get-Content -LiteralPath $serverLog -Raw -ErrorAction SilentlyContinue
        Write-Fail "mock HTTPS server did not start: $serverErrors"
        try { $server.Stop() } catch { }
    } else {
        $port = (Get-Content -LiteralPath $portFile -Raw).Trim()
        $wrapper = Join-Path $work "run-smoke.ps1"
        $secureText = @"
`$ErrorActionPreference = 'Stop'
`$secure = ConvertTo-SecureString 'vpc_test_activation_key' -AsPlainText -Force
& '$scriptPath' -CAFile '$caPath' -DeviceHash 'device-hash-test' -ImageOutputDir '$imageDir' -BaseUrl 'https://127.0.0.1:$port' -TimeoutSeconds 15 -ActivationKeySecure `$secure
"@
        [System.IO.File]::WriteAllText($wrapper, $secureText, [System.Text.Encoding]::Unicode)
        $smokeOut = Join-Path $work "smoke.out"
        $smokeErr = Join-Path $work "smoke.err"
        $smoke = Start-Process -FilePath $hostExe -ArgumentList @("-NoProfile", "-File", $wrapper) -PassThru -NoNewWindow `
            -RedirectStandardOutput $smokeOut -RedirectStandardError $smokeErr
        if (-not $smoke.WaitForExit(45000)) {
            Stop-Process -Id $smoke.Id -Force -ErrorAction SilentlyContinue
            $server.Stop()
            throw "smoke mock run timed out"
        }
        $outText = Get-Content -LiteralPath $smokeOut -Raw -ErrorAction SilentlyContinue
        $errText = Get-Content -LiteralPath $smokeErr -Raw -ErrorAction SilentlyContinue
        $serverErrors = Get-Content -LiteralPath $serverLog -Raw -ErrorAction SilentlyContinue
        $combined = "$outText`n$errText`n$serverErrors"
        $routes = @("/auth/activate", "/v1/models", "/v1/chat/completions", "/v1/images/generations")
        $hasRoutes = $true
        foreach ($route in $routes) {
            if ($outText -notmatch [regex]::Escape("route=$route")) { $hasRoutes = $false }
        }
        if ($outText -notmatch 'model=gpt-5.6-sol' -or $outText -notmatch 'model=grok-4.6' -or $outText -notmatch 'model=gpt-image-2') {
            $hasRoutes = $false
        }
        if (-not $hasRoutes) {
            Write-Fail "smoke output missing route/model lines: $combined"
        } elseif ($combined -match 'vpc_test_activation_key' -or $combined -match 'test-session' -or $combined -match 'iVBORw0KGgo') {
            Write-Fail "smoke printed a key, session, or image payload"
        } elseif ($outText -match 'choices' -or $outText -match 'b64_json') {
            Write-Fail "smoke printed a raw response"
        } elseif ($outText -notmatch 'request_id=req-mock-1' -or $outText -notmatch 'status=200' -or $outText -notmatch 'duration_ms=') {
            Write-Fail "smoke output missing status/request id/duration"
        } else {
            $images = @(Get-ChildItem -LiteralPath $imageDir -File -ErrorAction SilentlyContinue)
            if ($images.Count -lt 1) {
                Write-Fail "image bytes were not written to the caller temp directory"
            } else {
                Write-Pass "smoke prints only route/model/status/request id/duration"
            }
        }
        if (Test-Path -LiteralPath $portFile) {
            try {
                $probe = New-Object System.Net.Sockets.TcpClient
                $probe.Connect("127.0.0.1", [int]((Get-Content -LiteralPath $portFile -Raw).Trim()))
                $probe.Close()
            } catch { }
        }
        try { $server.Stop() } catch { }
        try { $server.Dispose() } catch { }
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
