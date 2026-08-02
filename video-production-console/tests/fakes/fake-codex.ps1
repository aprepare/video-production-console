param([string]$Mode = "needs_input")
$lines = @(
  '{"type":"thread.started","thread_id":"11111111-1111-1111-1111-111111111111"}',
  '{"type":"item.completed","item":{"type":"agent_message","text":"searching"}}'
)
if ($Mode -eq "delay") { Start-Sleep -Milliseconds 100 }
$lines | ForEach-Object { [Console]::Out.WriteLine($_); [Console]::Out.Flush() }
if ($Mode -eq "failed") { [Console]::Error.WriteLine("technical failure"); exit 1 }
$status = if ($Mode -eq "completed") { "completed" } else { "needs_input" }
$result = if ($status -eq "completed") { '{"type":"turn.completed","result":{"status":"completed","summary":"done","artifacts":[]}}' } else { '{"type":"turn.completed","result":{"status":"needs_input","summary":"choose","question":{"text":"pick","options":["a","b"]},"artifacts":[]}}' }
[Console]::Out.WriteLine($result); [Console]::Out.Flush()
