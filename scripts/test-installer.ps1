param([switch]$DesktopUI)

$ErrorActionPreference = 'Stop'
$out = Join-Path (Split-Path -Parent $PSScriptRoot) 'build/out'
$setup = Join-Path $out 'LocalProjectHub-Setup-x64.exe'
$testRoot = Join-Path $out ('install-smoke-' + [guid]::NewGuid().ToString('N'))
$installed = Join-Path $testRoot 'program'
$workspace = Join-Path $installed 'workspace-sentinel.txt'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\LocalProjectHub'
$shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'Local Project Hub.lnk'

# The installer uses per-user registrations. Leave existing installations alone.
if ((Test-Path $uninstallKey) -or (Test-Path $shortcut)) {
    throw 'Installer smoke test requires a user account without an existing installation.'
}

function Invoke-Setup([string]$Executable) {
    $process = Start-Process -FilePath $Executable -ArgumentList "/S /D=$installed" -WindowStyle Hidden -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw "Setup returned $($process.ExitCode)" }
}

Invoke-Setup $setup
$installedApp = Join-Path $installed 'LocalProjectHub.exe'
if ((Get-FileHash $installedApp).Hash -ne (Get-FileHash (Join-Path $out 'LocalProjectHub.exe')).Hash) {
    throw 'Installed application differs from the build'
}
$desktopFailure = $null
if ($DesktopUI) {
    try {
        & (Join-Path $PSScriptRoot 'test-installed-desktop.ps1') -Executable $installedApp -TestRoot $testRoot -ScreenshotDir (Join-Path $out 'installed-ui')
    } catch {
        # Keep the test install removable even if the desktop session fails.
        $desktopFailure = $_
    }
}
'workspace data' | Set-Content -LiteralPath $workspace
$before = (Get-FileHash $workspace).Hash
Invoke-Setup $setup
if ((Get-FileHash $workspace).Hash -ne $before) { throw 'Upgrade changed workspace data' }
Invoke-Setup (Join-Path $installed 'Uninstall.exe')
if ((Test-Path $installedApp) -or (Test-Path $uninstallKey) -or (Test-Path $shortcut)) {
    throw 'Uninstall left application entries behind'
}
if ((Get-FileHash $workspace).Hash -ne $before) { throw 'Uninstall changed workspace data' }
Write-Output 'Install, upgrade and uninstall smoke test passed.'
if ($desktopFailure) { throw $desktopFailure }
