<#
.SYNOPSIS
  Generates the MiniMedia test library under testdata\media (PLAN.md section 10).

.DESCRIPTION
  Works in Windows PowerShell 5.1 and PowerShell 7+. Existing files are kept
  unless -Force is given. Non-ASCII text is written with \uXXXX escapes so the
  script itself stays plain ASCII (5.1 misreads UTF-8 scripts without a BOM).

.EXAMPLE
  .\testdata\generate.ps1
  .\testdata\generate.ps1 -Force -Ffmpeg C:\tools\ffmpeg\bin\ffmpeg.exe
#>
[CmdletBinding()]
param(
    [switch]$Force,
    [string]$Ffmpeg = 'ffmpeg'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3

function U([string]$s) { [regex]::Unescape($s) }   # 'Bun\u0103' -> 'Bună'

# .NET Core needs the code-pages provider for Windows-1250 / ISO-8859-2.
if ($PSVersionTable.PSEdition -eq 'Core') {
    try { [System.Text.Encoding]::RegisterProvider([System.Text.CodePagesEncodingProvider]::Instance) } catch { }
}
$enc1250   = [System.Text.Encoding]::GetEncoding(1250)
$enc88592  = [System.Text.Encoding]::GetEncoding('iso-8859-2')
$utf8Bom   = New-Object System.Text.UTF8Encoding $true
$utf8NoBom = New-Object System.Text.UTF8Encoding $false

$root = Join-Path $PSScriptRoot 'media'
$tmp  = Join-Path ([System.IO.Path]::GetTempPath()) ('minimedia-gen-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force $tmp | Out-Null

function Out-Path([string]$rel) {
    $p = Join-Path $root $rel
    New-Item -ItemType Directory -Force (Split-Path $p -Parent) | Out-Null
    return $p
}

function Should-Write([string]$path) {
    if ($Force -or -not (Test-Path -LiteralPath $path)) { return $true }
    Write-Host "  exists, skipping: $path"
    return $false
}

function Invoke-Ffmpeg([string[]]$ffArgs) {
    & $Ffmpeg -nostdin -hide_banner -loglevel error -y @ffArgs
    if ($LASTEXITCODE -ne 0) { throw "ffmpeg failed ($LASTEXITCODE): $($ffArgs -join ' ')" }
}

function Video([int]$seconds) { @('-f', 'lavfi', '-i', "testsrc2=s=1280x720:r=24:d=$seconds") }
function Sine([int]$seconds, [int]$freq = 440) { @('-f', 'lavfi', '-i', "sine=f=${freq}:d=$seconds") }
$h264 = @('-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p')

# SRT text with \n line endings; callers choose the byte encoding.
function Srt([string[]]$cues, [int]$every = 10) {
    $sb = New-Object System.Text.StringBuilder
    for ($i = 0; $i -lt $cues.Count; $i++) {
        $s = [TimeSpan]::FromSeconds($i * $every + 1)
        $e = [TimeSpan]::FromSeconds($i * $every + 4)
        [void]$sb.AppendFormat("{0}`r`n{1:hh\:mm\:ss\,fff} --> {2:hh\:mm\:ss\,fff}`r`n{3}`r`n`r`n", ($i + 1), $s, $e, $cues[$i])
    }
    return $sb.ToString()
}

# Windows-1250 / ISO-8859-2 have no comma-below s/t, so real-world Romanian
# files use the cedilla forms (s-cedilla U+015F, t-cedilla U+0163). Do the same.
function To-Cedilla([string]$s) {
    $s.Replace([char]0x0219, [char]0x015F).Replace([char]0x0218, [char]0x015E).Replace([char]0x021B, [char]0x0163).Replace([char]0x021A, [char]0x0162)
}

$roCues = @(
    (U 'Bun\u0103 ziua! \u0218tiin\u021Ba e u\u0219oar\u0103.'),
    (U '\u00CEnc\u0103 o dat\u0103: \u0103 \u00E2 \u00EE \u0219 \u021B'),
    (U '\u021Aara mea, \u00EEntr-o diminea\u021B\u0103 de toamn\u0103.'),
    (U 'Mul\u021Bumesc frumos \u0219i la revedere!')
)

function Chapters([int]$durationSec) {
    $d = $durationSec * 1000
    $c = $d - 30000
    return ";FFMETADATA1`n" +
        "[CHAPTER]`nTIMEBASE=1/1000`nSTART=0`nEND=20000`ntitle=Intro`n" +
        "[CHAPTER]`nTIMEBASE=1/1000`nSTART=20000`nEND=$c`ntitle=Episode`n" +
        "[CHAPTER]`nTIMEBASE=1/1000`nSTART=$c`nEND=$d`ntitle=Credits`n"
}

# Show episode: H.264 + AC3 + embedded English SRT + Intro/Episode/Credits chapters.
function New-Episode([string]$rel, [int]$seconds, [string]$label) {
    $out = Out-Path $rel
    if (-not (Should-Write $out)) { return }
    Write-Host "  $rel"
    $srt = Join-Path $tmp 'embedded.srt'
    $meta = Join-Path $tmp 'chapters.txt'
    [System.IO.File]::WriteAllText($srt, (Srt @("$label - first line", "$label - second line", "$label - third line")), $utf8NoBom)
    [System.IO.File]::WriteAllText($meta, (Chapters $seconds), $utf8NoBom)
    Invoke-Ffmpeg (@() + (Video $seconds) + (Sine $seconds 660) + @('-i', $srt, '-i', $meta,
        '-map', '0:v', '-map', '1:a', '-map', '2:s', '-map_metadata', '3', '-map_chapters', '3') + $h264 + @(
        '-c:a', 'ac3', '-b:a', '192k', '-ac', '2',
        '-c:s', 'srt',
        '-metadata:s:a:0', 'language=eng',
        '-metadata:s:s:0', 'language=eng', '-metadata:s:s:0', 'title=English',
        $out))
}

Write-Host "Generating test media in $root"

# --- Movies -------------------------------------------------------------------
$rel = 'Movies\Big Test Movie (2021)\Big Test Movie (2021).mp4'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    Invoke-Ffmpeg (@() + (Video 120) + (Sine 120) + $h264 + @('-c:a', 'aac', '-b:a', '128k',
        '-metadata:s:a:0', 'language=eng', '-movflags', '+faststart', $out))
}
$dir = Split-Path $out -Parent
$roSrt = Join-Path $dir 'Big Test Movie (2021).ro.srt'
if (Should-Write $roSrt) {
    Write-Host "  ...ro.srt (Windows-1250)"
    [System.IO.File]::WriteAllBytes($roSrt, $enc1250.GetBytes((To-Cedilla (Srt $roCues))))
}
$enSrt = Join-Path $dir 'Big Test Movie (2021).en.forced.srt'
if (Should-Write $enSrt) {
    Write-Host "  ...en.forced.srt (UTF-8 with BOM)"
    [System.IO.File]::WriteAllText($enSrt, (Srt @('[Forced] Foreign dialogue line one', '[Forced] Foreign dialogue line two') 30), $utf8Bom)
}
# Extra (not in PLAN.md): an Extras folder that the scanner must ignore.
$rel = 'Movies\Big Test Movie (2021)\Extras\Making Of.mp4'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    Invoke-Ffmpeg (@() + (Video 5) + (Sine 5) + $h264 + @('-c:a', 'aac', $out))
}

$rel = 'Movies\Irregular.Keys.2019.1080p.BluRay.x264-GRP.mkv'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    # x264 alone puts keyframes every 240 frames on testsrc2 (no scene cuts), so
    # force a few at irregular times as well to get genuinely uneven GOPs.
    Invoke-Ffmpeg (@() + (Video 60) + (Sine 60 330) + $h264 + @(
        '-x264-params', 'keyint=240:min-keyint=10',
        '-force_key_frames', '0,1.5,2,7.25,9,19,21.5,33,34,47.5',
        '-c:a', 'dts', '-strict', '-2', '-ar', '48000', '-ac', '2',
        '-metadata:s:a:0', 'language=eng', $out))
}

$rel = 'Movies\HDR Sample (2020).mkv'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    $x265 = 'log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1:' +
            'master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400'
    # Convert the BT.709 test pattern to real PQ/BT.2020 pixel values (SDR white
    # = 100 nits), so the file looks washed out unless players and thumbnail
    # code tone-map it, like real HDR content. Tagging alone would not do that.
    $toPQ = 'zscale=tin=bt709:min=bt709:pin=bt709:rin=tv:t=smpte2084:m=bt2020nc:p=bt2020:r=tv:npl=100,format=yuv420p10le'
    if (-not ((& $Ffmpeg -hide_banner -filters 2>$null) -match '\szscale\s')) {
        # e.g. Homebrew's ffmpeg: tag the file as HDR without converting the
        # pixels. The tone-mapping tests skip themselves with such an ffmpeg.
        Write-Warning 'ffmpeg has no zscale filter: HDR Sample is tagged as HDR but not converted to PQ'
        $toPQ = 'format=yuv420p10le'
    }
    Invoke-Ffmpeg (@() + (Video 30) + (Sine 30) + @(
        '-vf', $toPQ,
        '-c:v', 'libx265', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p10le', '-x265-params', $x265,
        '-color_primaries', 'bt2020', '-color_trc', 'smpte2084', '-colorspace', 'bt2020nc',
        '-c:a', 'aac', '-b:a', '128k', $out))
}

$rel = U 'Movies\\\u0218tiin\u021Ba U\u0219oar\u0103 (2018).mp4'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    Invoke-Ffmpeg (@() + (Video 30) + (Sine 30 550) + $h264 + @('-c:a', 'aac', '-b:a', '128k', $out))
}

# --- Shows --------------------------------------------------------------------
New-Episode 'Shows\Test Show (2020)\Season 01\Test Show - S01E01 - Pilot.mkv' 180 'Pilot'
New-Episode 'Shows\Test Show (2020)\Season 01\Test Show - S01E02 - Second.mkv' 180 'Second'
New-Episode 'Shows\Test Show (2020)\Season 01\Test Show - S01E03E04 - Double.mkv' 180 'Double'

$rel = 'Shows\Test Show (2020)\Specials\Test Show - S00E01 - Special.mp4'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    Invoke-Ffmpeg (@() + (Video 60) + (Sine 60 880) + $h264 + @('-c:a', 'aac', '-b:a', '128k', $out))
}

# Extra (not in PLAN.md): a Romanian ISO-8859-2 sidecar in a Subs\ subfolder.
$subs = Out-Path 'Shows\Test Show (2020)\Season 01\Subs\Test Show - S01E01 - Pilot.ro.srt'
if (Should-Write $subs) {
    Write-Host "  Season 01\Subs\...Pilot.ro.srt (ISO-8859-2)"
    [System.IO.File]::WriteAllBytes($subs, $enc88592.GetBytes((To-Cedilla (Srt $roCues))))
}
# Extra (not in PLAN.md): a file with no episode number, which must be listed as unmatched.
$rel = 'Shows\Test Show (2020)\Season 01\random clip.mkv'
$out = Out-Path $rel
if (Should-Write $out) {
    Write-Host "  $rel"
    Invoke-Ffmpeg (@() + (Video 5) + $h264 + @($out))
}

Remove-Item -Recurse -Force $tmp
Write-Host "Done."
