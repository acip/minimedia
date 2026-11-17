<#
.SYNOPSIS
  Builds the MiniMedia server (console build) into testdata\run\ and runs it
  against the generated test media (run generate.ps1 first).

.DESCRIPTION
  The server has no command-line parameters; it reads config.yaml from the
  executable's folder. This script writes a dev config there on first use
  (port 8098: Jellyfin uses 8096, and WSL forwards a service on 8097) and keeps the
  database in testdata\run\data. Edit testdata\run\config.yaml freely.

.EXAMPLE
  .\testdata\run-dev.ps1
  .\testdata\run-dev.ps1 -BuildOnly
#>
[CmdletBinding()]
param(
    [switch]$BuildOnly,
    [string]$Go = 'go'
)

$ErrorActionPreference = 'Stop'
$repo   = Split-Path $PSScriptRoot -Parent
$run    = Join-Path $PSScriptRoot 'run'
$media  = Join-Path $PSScriptRoot 'media'
$exe    = Join-Path $run 'minimedia.exe'
$config = Join-Path $run 'config.yaml'

if (-not (Test-Path -LiteralPath (Join-Path $media 'Movies'))) {
    throw "Test media not found. Run .\testdata\generate.ps1 first."
}
New-Item -ItemType Directory -Force $run | Out-Null

$ver = 'dev'
try { $ver = (git -C $repo describe --tags --always --dirty 2>$null) } catch { }
if (-not $ver) { $ver = 'dev' }

Push-Location (Join-Path $repo 'server')
try {
    & $Go build -ldflags "-X github.com/acip/minimedia/server/internal/version.Version=$ver" -o $exe ./cmd/minimedia
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally { Pop-Location }
Write-Host "Built $exe ($ver)"

if (-not (Test-Path -LiteralPath $config)) {
    $yaml = @"
# Dev config written by testdata\run-dev.ps1
name: "MiniMedia (dev)"
port: 8098
discovery: true
libraries:
  - name: Movies
    type: movies
    path: '$media\Movies'
  - name: Shows
    type: shows
    path: '$media\Shows'
data_dir: data
log_level: debug
"@
    [System.IO.File]::WriteAllText($config, $yaml, (New-Object System.Text.UTF8Encoding $false))
    Write-Host "Wrote $config"
}

if (-not $BuildOnly) {
    & $exe
    exit $LASTEXITCODE
}
