<#
.SYNOPSIS
Retakes the README's screenshots (docs/images) of the current settings pages,
with sample data.

.DESCRIPTION
Needs voice running (for the shape of its settings), Python 3, Node 22 or
newer and Microsoft Edge. It reads voice's live settings, builds the pages
from this checkout with sample sessions, characters and lines in their place
(build.py), serves them on 127.0.0.1, takes each screenshot at 2x scale
(capture.mjs) and writes them to docs/images. The build stops if anything the
live data says about this PC (sessions, projects, characters, cloned voices,
devices, folders) would be shown.

.EXAMPLE
.\docs\screenshots\make.ps1
.\docs\screenshots\make.ps1 -Out $env:TEMP\shots   # look first, copy later
#>
param(
    [string]$Out = (Join-Path $PSScriptRoot '..\images'),
    [string]$Python = 'python'
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$work = Join-Path ([IO.Path]::GetTempPath()) ('voice-shots-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force $work | Out-Null
$server = $null
try {
    # The settings' shape, from the running voice.
    $discovery = Join-Path $env:APPDATA 'voice\daemon.json'
    if (-not (Test-Path -LiteralPath $discovery)) { throw 'voice is not running (no %APPDATA%\voice\daemon.json); start it with voice serve' }
    $d = Get-Content -LiteralPath $discovery -Raw | ConvertFrom-Json
    foreach ($api in 'state', 'status', 'integrations', 'themes') {
        $r = Invoke-WebRequest "http://$($d.addr)/settings/api/$api" -Headers @{ 'X-Voice-Token' = $d.token } -UseBasicParsing
        [IO.File]::WriteAllText((Join-Path $work "$api.json"), $r.Content)
    }
    # The story theme, when it is installed.
    $themes = Get-Content (Join-Path $work 'themes.json') -Raw | ConvertFrom-Json
    $story = Join-Path ([string]$themes.dir) 'storybook\theme.css'
    if ($themes.dir -and (Test-Path -LiteralPath $story)) { Copy-Item -LiteralPath $story (Join-Path $work 'storybook.css') }

    $env:SHOT_VERSION = (git -C $repo describe --tags --abbrev=0 2>$null)
    & $Python (Join-Path $PSScriptRoot 'build.py') $work $repo
    if ($LASTEXITCODE) { throw 'building the sample pages failed (see above)' }

    $port = Get-Random -Minimum 18000 -Maximum 19000
    $server = Start-Process -FilePath $Python -ArgumentList '-m', 'http.server', $port, '--bind', '127.0.0.1', '--directory', (Join-Path $work 'site') -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 1
    node (Join-Path $PSScriptRoot 'capture.mjs') "http://127.0.0.1:$port" $Out (Join-Path $work 'edge')
    if ($LASTEXITCODE) { throw 'taking the screenshots failed' }
    Write-Host "screenshots in $((Resolve-Path $Out).Path)"
} finally {
    if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id -Force }
    Start-Sleep -Milliseconds 500
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
