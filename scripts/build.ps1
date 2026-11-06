param(
    [string]$MakeNsis
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$repo = Split-Path -Parent $PSScriptRoot
$out = Join-Path $repo 'build/out'
New-Item -ItemType Directory -Force -Path $out | Out-Null

if (-not $MakeNsis) {
    $archive = Join-Path $out 'nsis.zip'
    $tools = Join-Path $out 'tools'
    $MakeNsis = Join-Path $tools 'nsis-3.11/makensis.exe'
    if (-not (Test-Path -LiteralPath $MakeNsis)) {
        $expected = 'C7D27F780DDB6CFFB4730138CD1591E841F4B7EDB155856901CDF5F214394FA1'
        $verified = $false
        # The redirect endpoint sometimes serves a download page on CI.
        # Try fixed official mirrors and accept only the locked ZIP bytes.
        foreach ($mirror in 'master.dl.sourceforge.net', 'netix.dl.sourceforge.net', 'downloads.sourceforge.net') {
            try {
                curl.exe --fail --silent --show-error --location --max-time 35 "https://$mirror/project/nsis/NSIS%203/3.11/nsis-3.11.zip" -o $archive
                if ($LASTEXITCODE -eq 0 -and (Get-FileHash -LiteralPath $archive).Hash -eq $expected) {
                    $verified = $true
                    break
                }
            } catch {
                Write-Host "NSIS download failed on $mirror"
            }
        }
        if (-not $verified) {
            throw 'No NSIS mirror returned the pinned archive checksum'
        }
        Expand-Archive -LiteralPath $archive -DestinationPath $tools -Force
    }
}
$MakeNsis = (Resolve-Path -LiteralPath $MakeNsis).Path

Push-Location (Join-Path $repo 'frontend')
try {
    npm.cmd ci
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally {
    Pop-Location
}

Push-Location $repo
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    go build -mod=readonly -trimpath -tags desktop -ldflags '-H windowsgui' -o (Join-Path $out 'LocalProjectHub.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
    & $MakeNsis "/DOUTDIR=$out" (Join-Path $repo 'build/windows/installer.nsi')
    if ($LASTEXITCODE -ne 0) { throw 'Installer build failed' }
    $installer = 'LocalProjectHub-Setup-x64.exe'
    $hash = (Get-FileHash -LiteralPath (Join-Path $out $installer)).Hash.ToLowerInvariant()
    "$hash  $installer" | Set-Content -Encoding ascii (Join-Path $out "$installer.sha256")
} finally {
    Pop-Location
}
