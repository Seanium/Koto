param(
    [Parameter(Mandatory=$true)][string]$CLI,
    [string]$Installer = (Join-Path $PSScriptRoot '../../site/install.ps1')
)
$ErrorActionPreference = 'Stop'
$sourceCLI = (Resolve-Path -LiteralPath $CLI).Path
$reported = & $sourceCLI version
if ($LASTEXITCODE -ne 0 -or $reported -notmatch '^koto (.+)$') { throw 'Cannot read fixture CLI version.' }
$version = $Matches[1]
$fixtureTag = "v$version"
$fixtureBase = "https://github.com/Seanium/koto/releases/download/$fixtureTag/"
$installer = (Resolve-Path -LiteralPath $Installer).Path
$root = Join-Path (Split-Path $PSScriptRoot -Parent) ('.cache/installer-test-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
$expectedHash = (Get-FileHash $sourceCLI -Algorithm SHA256).Hash.ToLowerInvariant()
$downloadCapture = @{asset=''}
$scenario = ''

# Exercise the actual script and native executable without depending on a
# published version or consuming GitHub's anonymous API limit.
function Invoke-RestMethod { param($Uri,$Headers,$TimeoutSec); return @{tag_name=$fixtureTag} }
function Invoke-WebRequest {
    param([switch]$UseBasicParsing,$Uri,$OutFile,$TimeoutSec,$Headers)
    if (-not $Uri.StartsWith($fixtureBase)) { throw "Unexpected installer URL: $Uri" }
    $name = $Uri.Substring($fixtureBase.Length)
    if ($name -eq 'SHA256SUMS') {
        $lines = @("$expectedHash  koto-cli-windows-amd64.exe", "$expectedHash  koto-cli-windows-arm64.exe")
        if ($scenario -eq 'missing') { $lines = @("$expectedHash  unrelated.exe") }
        if ($scenario -eq 'duplicate') { $lines += $lines }
        [IO.File]::WriteAllLines($OutFile, $lines)
    } else {
        if ($name -notin @('koto-cli-windows-amd64.exe','koto-cli-windows-arm64.exe')) { throw 'Unexpected binary.' }
        $downloadCapture.asset = $name
        if ($scenario -eq 'download-failed') { throw 'Synthetic network failure.' }
        Copy-Item -LiteralPath $sourceCLI -Destination $OutFile
        if ($scenario -eq 'corrupt') { [IO.File]::WriteAllText($OutFile, 'corrupted') }
    }
}

try {
    foreach ($scenario in @('valid','reinstall','unknown-target','corrupt','missing','duplicate','download-failed','directory-target')) {
        $targetDir = Join-Path $root $scenario
        [IO.Directory]::CreateDirectory($targetDir) | Out-Null
        $target = Join-Path $targetDir 'koto.exe'
        if ($scenario -eq 'directory-target') { [IO.Directory]::CreateDirectory($target) | Out-Null }
        elseif ($scenario -eq 'reinstall') { Copy-Item -LiteralPath $sourceCLI -Destination $target }
        elseif ($scenario -ne 'valid') { [IO.File]::WriteAllText($target, 'previous installation') }
        $failed = $false
        $failure = ''
        try { & $installer -Version $version -InstallDir $targetDir | Out-Null }
        catch { $failed = $true; $failure = $_.Exception.Message }
        if ($scenario -in @('valid','reinstall')) {
            if ($failed -or (Get-FileHash $target -Algorithm SHA256).Hash -ine $expectedHash) { throw "Valid install failed: $failure" }
            # Native machine must be used even when this PowerShell is emulated.
            [UInt16]$processMachine = 0
            [UInt16]$nativeMachine = 0
            if (-not [KotoInstallerPlatform]::IsWow64Process2([IntPtr](-1),[ref]$processMachine,[ref]$nativeMachine)) { throw 'Native architecture check failed.' }
            $expectedAsset = if ($nativeMachine -eq 0xAA64) { 'koto-cli-windows-arm64.exe' } else { 'koto-cli-windows-amd64.exe' }
            if ($downloadCapture.asset -cne $expectedAsset) { throw "Downloaded $($downloadCapture.asset), expected $expectedAsset" }
        } else {
            if (-not $failed) { throw "$scenario did not fail." }
            if ($scenario -ne 'directory-target' -and [IO.File]::ReadAllText($target) -cne 'previous installation') { throw "$scenario changed the old installation." }
        }
        if (@(Get-ChildItem -LiteralPath $targetDir -Filter '.koto-install-*' -Force).Count -ne 0) { throw "$scenario left temporary files." }
        Write-Output "PASS: $scenario"
    }
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force
}
