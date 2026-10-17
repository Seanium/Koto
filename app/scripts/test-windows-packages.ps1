# Run both standalone release executables in a project-local synthetic workspace.
param(
  [Parameter(Mandatory=$true)][string]$AppExecutable,
  [Parameter(Mandatory=$true)][string]$CLIExecutable,
  [string]$Go = 'go',
  [switch]$SkipGUI
)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'Run on Windows.' }
$appDir = Split-Path $PSScriptRoot -Parent
$appSource = (Resolve-Path -LiteralPath $AppExecutable).Path
$cliSource = (Resolve-Path -LiteralPath $CLIExecutable).Path
$version = (Get-Content -LiteralPath (Join-Path $appDir 'VERSION') -Raw).Trim()
$work = Join-Path $appDir ('.cache/windows-packages-' + [guid]::NewGuid().ToString('N'))
$binaryDir = Join-Path $work '应用 O''Brien [验证]'
$sandbox = Join-Path $work '会话 用户'
$result = @{ passed=$false; version=$version; work=$work }
$originalPath = [Environment]::GetEnvironmentVariable('Path','User')
$oldLocation = Get-Location
try {
  New-Item -ItemType Directory -Path $binaryDir -Force | Out-Null
  $exe = Join-Path $binaryDir 'koto-app.exe'
  $cli = Join-Path $binaryDir 'koto.exe'
  Copy-Item -LiteralPath $appSource -Destination $exe
  Copy-Item -LiteralPath $cliSource -Destination $cli
  $cliVersion = (& $cli version).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Standalone CLI did not run.' }
  if ($cliVersion -cne "koto $version") { throw "Standalone CLI version mismatch: $cliVersion" }
  $result.cliVersion = $cliVersion
  $help = & $cli --help
  if ($LASTEXITCODE -ne 0 -or -not ($help -match 'copy')) { throw 'Standalone CLI help did not run.' }
  Set-Location -LiteralPath $appDir
  & $Go run ./cmd/seed $sandbox
  if ($LASTEXITCODE -ne 0) { throw 'Fixture seed failed.' }
  $flags = @('--data',(Join-Path $sandbox 'hub'),'--claude-root',(Join-Path $sandbox 'claude'),'--pi-root',(Join-Path $sandbox 'pi'),'--only','claude-code,pi')
  $source = 'claude-code:6b1f2a40-1c2d-4e3f-8a4b-5c6d7e8f9a01'
  Get-ChildItem -LiteralPath (Join-Path $sandbox 'claude') -File -Recurse | ForEach-Object { $_.IsReadOnly = $true }
  $before = @(Get-ChildItem -LiteralPath (Join-Path $sandbox 'claude') -File -Recurse | ForEach-Object { (Get-FileHash -LiteralPath $_.FullName).Hash }) -join ','
  $raw = & $cli copy $source --to pi --json @flags
  if ($LASTEXITCODE -ne 0) { throw "CLI copy failed: $raw" }
  $copy = $raw | ConvertFrom-Json
  if ($copy.state -ne 'copied' -or $copy.counts.turns -ne 5 -or -not $copy.resume.shell.StartsWith('Set-Location -LiteralPath ')) { throw 'Copy result or PowerShell resume contract failed.' }
  $after = @(Get-ChildItem -LiteralPath (Join-Path $sandbox 'claude') -File -Recurse | ForEach-Object { (Get-FileHash -LiteralPath $_.FullName).Hash }) -join ','
  if ($before -cne $after) { throw 'Copy changed source files.' }
  $result.cliCopy = $copy
  if (-not $SkipGUI) { & (Join-Path $PSScriptRoot 'test-windows.ps1') -Executable $exe -Go $Go; $result.nativeWebView = $true }
  if ([Environment]::GetEnvironmentVariable('Path','User') -cne $originalPath) { throw 'Portable executables changed the user PATH.' }
  $result.passed = $true
} catch {
  $result.error = $_.Exception.Message
  throw
} finally {
  Set-Location -LiteralPath $oldLocation.Path
  $result | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $work 'result.json') -Encoding UTF8
}
