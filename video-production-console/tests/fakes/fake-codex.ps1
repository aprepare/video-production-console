param([string]$Mode = "needs_input")
$lines = @(
  '{"type":"thread.started","thread_id":"11111111-1111-1111-1111-111111111111"}',
  '{"type":"item.completed","item":{"type":"agent_message","text":"searching"}}'
)
if ($Mode -eq "malformed") { [Console]::Out.WriteLine('not json'); [Console]::Out.Flush() }
if ($Mode -eq "large") { $large = ('x' * 100000); [Console]::Out.WriteLine((ConvertTo-Json @{type='item.completed'; item=@{type='agent_message'; text=$large}} -Compress)); [Console]::Out.Flush() }
$lines | ForEach-Object { [Console]::Out.WriteLine($_); [Console]::Out.Flush(); if ($Mode -eq "interleave") { [Console]::Error.WriteLine("stderr $_"); [Console]::Error.Flush() }; if ($Mode -eq "delay") { Start-Sleep -Milliseconds 100 } }
if ($Mode -eq "stderr") { [Console]::Error.WriteLine("technical failure"); [Console]::Error.Flush() }
if ($Mode -eq "failed") { [Console]::Error.WriteLine("technical failure"); exit 1 }
$status = if ($Mode -eq "completed") { "completed" } else { "needs_input" }
$exitCode = 0
if ($Mode -eq "failed_result") { $status = "completed"; $exitCode = 1 }
$result = if ($status -eq "completed") { '{"type":"turn.completed","result":{"status":"completed","summary":"done","artifacts":[]}}' } else { '{"type":"turn.completed","result":{"status":"needs_input","summary":"choose","question":{"text":"pick","options":["a","b"]},"artifacts":[]}}' }
[Console]::Out.WriteLine($result); [Console]::Out.Flush()
if ($exitCode -ne 0) { exit $exitCode }
