$arguments = @($args)
$Mode = if ($arguments.Count -gt 0) { [string]$arguments[0] } else { "awaiting_input" }
$OutputLastMessage = ""
$TaskID = "11111111-1111-1111-1111-111111111111"
$Action = "remix.standard"
$OutputDir = ""

for ($i = 1; $i -lt $arguments.Count; $i++) {
    switch ([string]$arguments[$i]) {
        "--output-last-message" { $i++; $OutputLastMessage = [string]$arguments[$i] }
        "--task-id" { $i++; $TaskID = [string]$arguments[$i] }
        "--action" { $i++; $Action = [string]$arguments[$i] }
        "--output-dir" { $i++; $OutputDir = [string]$arguments[$i] }
    }
}

$utf8 = New-Object System.Text.UTF8Encoding($false)

function Write-JSONL([object]$Value) {
    [Console]::Out.WriteLine(($Value | ConvertTo-Json -Depth 10 -Compress))
    [Console]::Out.Flush()
}

function New-Result([string]$Status, [string]$Summary, [bool]$WithOutputs) {
    $questions = @()
    if ($Status -eq "awaiting_input") {
        $questions = @([ordered]@{ text = "pick"; options = @("a", "b") })
    }
    $artifacts = @()
    $assets = @()
    if ($WithOutputs) {
        if ([string]::IsNullOrWhiteSpace($OutputDir)) { throw "--output-dir is required for output mode" }
        $selfCheckPath = Join-Path $OutputDir "self_check.json"
        $scriptPath = Join-Path $OutputDir "script.md"
        [System.IO.File]::WriteAllText($selfCheckPath, "{`"ok`":true}", $utf8)
        [System.IO.File]::WriteAllText($scriptPath, "script", $utf8)
        $scriptInfo = Get-Item -LiteralPath $scriptPath
        # Go's child environment may not expose PowerShell's optional utility module.
        $hasher = [System.Security.Cryptography.SHA256]::Create()
        try {
            $scriptSHA = [BitConverter]::ToString($hasher.ComputeHash([System.IO.File]::ReadAllBytes($scriptPath))).Replace("-", "").ToLowerInvariant()
        } finally { $hasher.Dispose() }
        $artifacts = @([ordered]@{ type = "self_check"; path = $selfCheckPath; description = "self check" })
        $assets = @([ordered]@{
            type = "continuous_script"
            path = $scriptPath
            storage_kind = "file"
            filename = $scriptInfo.Name
            mime = "text/markdown"
            size = $scriptInfo.Length
            sha256 = $scriptSHA
        })
    }
    return [ordered]@{
        schema_version = "2.0"
        task_id = $TaskID
        action = $Action
        status = $Status
        summary = $Summary
        questions = $questions
        artifacts = $artifacts
        asset_outputs = $assets
        warnings = @()
    }
}

function Result-JSON([object]$Result) {
    return ($Result | ConvertTo-Json -Depth 10 -Compress)
}

function Write-LastMessage([string]$Text) {
    if ([string]::IsNullOrWhiteSpace($OutputLastMessage)) { return }
    $parent = Split-Path -Parent $OutputLastMessage
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        [System.IO.Directory]::CreateDirectory($parent) | Out-Null
    }
    [System.IO.File]::WriteAllText($OutputLastMessage, $Text, $utf8)
}

function Write-AgentMessage([string]$Text) {
    Write-JSONL ([ordered]@{ type = "item.completed"; item = [ordered]@{ type = "agent_message"; text = $Text } })
}

Write-JSONL ([ordered]@{ type = "thread.started"; thread_id = "11111111-1111-1111-1111-111111111111" })

$agentJSON = Result-JSON (New-Result "completed" "agent result" $false)
$lastJSON = Result-JSON (New-Result "completed" "last result" $false)
$exitCode = 0

switch ($Mode) {
    "completed" {
        Write-AgentMessage $agentJSON
        Write-LastMessage $agentJSON
    }
    "agent_preferred" {
        Write-AgentMessage $agentJSON
        Write-LastMessage $lastJSON
    }
    "awaiting_input" {
        $agentJSON = Result-JSON (New-Result "awaiting_input" "choose" $false)
        Write-AgentMessage $agentJSON
        Write-LastMessage $agentJSON
    }
    "invalid_schema" {
        $invalid = '{"status":"completed"}'
        Write-AgentMessage $invalid
        Write-LastMessage $invalid
    }
    "agent_only" {
        Write-AgentMessage $agentJSON
    }
    "last_message_only" {
        Write-LastMessage $lastJSON
    }
    "invalid_agent" {
        Write-AgentMessage "not a result"
        Write-LastMessage $lastJSON
    }
    "latest_invalid" {
        Write-AgentMessage $agentJSON
        Write-AgentMessage "still working"
        Write-LastMessage $lastJSON
    }
    "completed_with_outputs" {
        $agentJSON = Result-JSON (New-Result "completed" "agent result" $true)
        Write-AgentMessage $agentJSON
        Write-LastMessage $agentJSON
    }
    "completed_with_bad_asset_hash" {
        $result = New-Result "completed" "agent result" $true
        $result.asset_outputs[0].sha256 = ("0" * 64)
        $agentJSON = Result-JSON $result
        Write-AgentMessage $agentJSON
        Write-LastMessage $agentJSON
    }
    "failed" {
        $agentJSON = Result-JSON (New-Result "completed" "must not commit" $true)
        Write-AgentMessage $agentJSON
        Write-LastMessage $agentJSON
        [Console]::Error.WriteLine("technical failure")
        [Console]::Error.Flush()
        $exitCode = 1
    }
    "large" {
        Write-AgentMessage ("x" * 100000)
        Write-AgentMessage $agentJSON
        Write-LastMessage $agentJSON
    }
    default {
        throw "unsupported fake Codex mode: $Mode"
    }
}

Write-JSONL ([ordered]@{ type = "turn.completed" })
if ($exitCode -ne 0) { exit $exitCode }
