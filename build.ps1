<#
.SYNOPSIS
  Builds MiniMedia for installation: the server for Windows
  (dist\minimedia.exe), macOS (dist\minimedia-macos-arm64 / -amd64) and Linux
  (dist\minimedia-linux-amd64 / -arm64), and dist\MiniMedia-tv.apk (the
  Google TV app).

.DESCRIPTION
  The server is a console program with the version embedded. The app is the
  optimized release build (R8, baseline profile; several times faster on a TV
  than a debug build), signed with the Android debug key and installed with
  adb; see README.md.

.EXAMPLE
  .\build.ps1
  .\build.ps1 -SkipApp
#>
[CmdletBinding()]
param(
    [switch]$SkipApp,
    [string]$Go = 'go'
)

$ErrorActionPreference = 'Stop'
$repo = $PSScriptRoot
$dist = Join-Path $repo 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null

$ver = 'dev'
try { $ver = (git -C $repo describe --tags --always --dirty 2>$null) } catch { }
if (-not $ver) { $ver = 'dev' }

# The server is pure Go (no cgo), so every target builds from here.
$targets = @(
    @{ os = 'windows'; arch = 'amd64'; name = 'minimedia.exe' },
    @{ os = 'darwin';  arch = 'arm64'; name = 'minimedia-macos-arm64' },
    @{ os = 'darwin';  arch = 'amd64'; name = 'minimedia-macos-amd64' },
    @{ os = 'linux';   arch = 'amd64'; name = 'minimedia-linux-amd64' },
    @{ os = 'linux';   arch = 'arm64'; name = 'minimedia-linux-arm64' }
)
Push-Location (Join-Path $repo 'server')
try {
    $env:CGO_ENABLED = '0'
    foreach ($t in $targets) {
        $env:GOOS = $t.os; $env:GOARCH = $t.arch
        $out = Join-Path $dist $t.name
        & $Go build -trimpath -ldflags "-s -w -X github.com/acip/minimedia/server/internal/version.Version=$ver" -o $out ./cmd/minimedia
        if ($LASTEXITCODE -ne 0) { throw "go build failed for $($t.os)/$($t.arch)" }
        Write-Host "Built $out ($ver)"
    }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

if (-not $SkipApp) {
    Push-Location (Join-Path $repo 'tv-app')
    try {
        & .\gradlew.bat -q assembleRelease
        if ($LASTEXITCODE -ne 0) { throw "app build failed" }
    } finally { Pop-Location }
    $apk = Join-Path $dist 'MiniMedia-tv.apk'
    Copy-Item (Join-Path $repo 'tv-app\app\build\outputs\apk\release\app-release.apk') $apk -Force
    Write-Host "Built $apk"
}
