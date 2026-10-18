# Installs the standalone CLI; no desktop app or Agent configuration is changed.
[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$InstallDir = $(if ($env:KOTO_INSTALL_DIR) { $env:KOTO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\koto\bin' })
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$repo = 'https://github.com/Seanium/koto'
$headers = @{ 'User-Agent' = 'koto-installer'; Accept = 'application/vnd.github+json' }
if (-not $Version) {
    $release = Invoke-RestMethod -Uri 'https://api.github.com/repos/Seanium/koto/releases/latest' -Headers $headers -TimeoutSec 30
    $Version = $release.tag_name
}
if (-not $Version.StartsWith('v')) { $Version = 'v' + $Version }
if ($Version -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') { throw 'Invalid release version.' }
# Windows PowerShell 5.1 running under x64 emulation reports AMD64 even on
# ARM64. Ask the kernel for the native machine, not the shell's architecture.
if (-not ('KotoInstallerPlatform' -as [type])) {
    Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class KotoInstallerPlatform {
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool IsWow64Process2(IntPtr process, out ushort processMachine, out ushort nativeMachine);
}
'@
}
[UInt16]$processMachine = 0
[UInt16]$nativeMachine = 0
if (-not [KotoInstallerPlatform]::IsWow64Process2([IntPtr](-1), [ref]$processMachine, [ref]$nativeMachine)) {
    throw 'Cannot detect the native Windows architecture.'
}
$arch = switch ($nativeMachine) { 0xAA64 { 'arm64' }; 0x8664 { 'amd64' }; default { throw "Unsupported native CPU architecture: $nativeMachine" } }
$asset = "koto-cli-windows-$arch.exe"
[IO.Directory]::CreateDirectory($InstallDir) | Out-Null
$InstallDir = (Resolve-Path -LiteralPath $InstallDir).Path
$target = Join-Path $InstallDir 'koto.exe'
if (Test-Path -LiteralPath $target) {
    $item = Get-Item -LiteralPath $target
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Installation target must be a regular executable.' }
}
$stage = Join-Path $InstallDir ('.koto-install-' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($stage) | Out-Null
try {
    $sumFile = Join-Path $stage 'SHA256SUMS'
    $payload = Join-Path $stage 'koto.exe'
    Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$Version/SHA256SUMS" -OutFile $sumFile -TimeoutSec 60 -Headers $headers
    $matchesForAsset = @(Get-Content -LiteralPath $sumFile | Where-Object { $_ -cmatch ('^([a-fA-F0-9]{64})\s+\*?' + [regex]::Escape($asset) + '$') })
    if ($matchesForAsset.Count -ne 1) { throw 'Release checksum is missing or duplicated.' }
    $expected = ($matchesForAsset[0] -split '\s+')[0]
    Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$Version/$asset" -OutFile $payload -TimeoutSec 300 -Headers $headers
    if ((Get-FileHash -LiteralPath $payload -Algorithm SHA256).Hash -ine $expected) { throw 'Checksum mismatch; existing installation was not changed.' }
    $reported = & $payload version
    if ($LASTEXITCODE -ne 0 -or $reported -cne "koto $($Version.Substring(1))") { throw 'Downloaded CLI cannot run or reports an unexpected version.' }
    # The verified CLI owns the same OS lock and atomic replacement/recovery
    # transaction as `koto update`, including replacing a running Windows EXE.
    & $payload --koto-install $target $expected
    if ($LASTEXITCODE -ne 0) { throw 'Installation failed; see recovery details above.' }
    Write-Output "Installed koto CLI $($Version.Substring(1)) at $target"
    if (($env:PATH -split ';') -notcontains $InstallDir) { Write-Output "Add this directory to your PATH: $InstallDir" }
} finally {
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
}
