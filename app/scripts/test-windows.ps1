# Native Windows/WebView2 smoke. All conversations are newly seeded under app/.cache.
param(
  [Parameter(Mandatory=$true)][string]$Executable,
  [string]$Go = "go"
)
$ErrorActionPreference = "Stop"
$appDir = Split-Path $PSScriptRoot -Parent
$binary = (Resolve-Path $Executable).Path
$sandbox = Join-Path $appDir (".cache/windows-smoke-" + [guid]::NewGuid().ToString("N"))
$report = "$sandbox-result.json"
$savedSandbox = $env:KOTO_APP_SANDBOX
$savedSmoke = $env:KOTO_APP_PLATFORM_SMOKE
$savedLive = $env:KOTO_APP_LIVE_MS
Push-Location $appDir
try {
  & $Go run ./cmd/seed $sandbox
  if ($LASTEXITCODE -ne 0) { throw "Sandbox seed failed" }
  [System.IO.File]::WriteAllText((Join-Path $sandbox 'app-prefs.json'), '{"language":"zh","appearance":"light","liveMinutes":0}', [System.Text.UTF8Encoding]::new($false))
  $env:KOTO_APP_SANDBOX = $sandbox
  $env:KOTO_APP_PLATFORM_SMOKE = $report
  $env:KOTO_APP_LIVE_MS = '0'
  $process = Start-Process -FilePath $binary -PassThru -RedirectStandardOutput "$sandbox-stdout.log" -RedirectStandardError "$sandbox-stderr.log"
  if (-not $process.WaitForExit(180000)) {
    $process.Kill()
    throw "WebView2 smoke timed out; logs: $sandbox-*.log"
  }
  if (-not (Test-Path $report)) { throw "Application exited without a smoke result: $report" }
  $result = Get-Content -Raw $report | ConvertFrom-Json
  if (-not $result.passed) { throw "Native smoke failed: $($result.error)" }
  Copy-Item $report (Join-Path $appDir ".cache/platform-smoke-windows-$($result.arch).json")
  Write-Output "Native Windows/$($result.arch) WebView2 copy, view, trash and restore passed."
} finally {
  $env:KOTO_APP_SANDBOX = $savedSandbox
  $env:KOTO_APP_PLATFORM_SMOKE = $savedSmoke
  $env:KOTO_APP_LIVE_MS = $savedLive
  Pop-Location
}
