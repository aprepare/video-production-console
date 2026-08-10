param([switch]$Strict)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root "internal/webui/dist"
$html = Get-Content (Join-Path $dist "index.html") -Raw
$refs = [regex]::Matches($html, '(?:src|href)=["''](/[^"''?#]+)') | % { $_.Groups[1].Value } | Sort-Object -Unique
foreach ($ref in $refs) {
  $path = Join-Path $dist $ref.TrimStart('/').Replace('/', '\')
  if (!(Test-Path $path -PathType Leaf)) { throw "Missing embedded resource: $ref" }
  $repoPath = "internal/webui/dist/" + $ref.TrimStart('/')
  $tracked = git -C $root ls-files -- $repoPath
  if (!$tracked) {
    if ($Strict) { throw "Untracked embedded resource: $ref" }
    Write-Warning "Untracked embedded resource: $ref"
  }
}
Write-Host "Embedded references verified: $($refs.Count)"
