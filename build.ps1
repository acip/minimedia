<#
.SYNOPSIS
  Builds MiniMedia for installation: dist\minimedia.exe (the server) and
  dist\MiniMedia-tv.apk (the Google TV app).

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

$exe = Join-Path $dist 'minimedia.exe'
Push-Location (Join-Path $repo 'server')
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    & $Go build -trimpath -ldflags "-s -w -X github.com/acip/minimedia/server/internal/version.Version=$ver" -o $exe ./cmd/minimedia
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}
Write-Host "Built $exe ($ver)"

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
