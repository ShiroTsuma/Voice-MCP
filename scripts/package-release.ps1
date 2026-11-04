[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$')]
    [string]$Version,
    [string]$Go = 'go'
)

# Builds dist/voice-windows-x64.zip: voice.exe with everything it installs
# from its own folder (sidecar code, Claude Code mod, skill, example config),
# plus license texts. The archive is the whole release; `voice install` sets
# it up where it is unpacked.
$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$distRoot = Join-Path $repoRoot 'dist'
$stageRoot = Join-Path $distRoot ('stage-' + [guid]::NewGuid().ToString('N'))
$archiveName = 'voice-windows-x64.zip'
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }

function Invoke-Go {
    param([string[]]$GoArgs)
    & $Go @GoArgs
    if ($LASTEXITCODE -ne 0) { throw "Go failed: $($GoArgs[0])" }
}

Push-Location $repoRoot
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    New-Item -ItemType Directory -Path $stageRoot -Force | Out-Null
    $executable = Join-Path $stageRoot 'voice.exe'
    Invoke-Go -GoArgs @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Version", '-o', $executable, './cmd/voice')
    if ($env:OS -eq 'Windows_NT') {
        $actual = & $executable version
        if ($LASTEXITCODE -ne 0 -or $actual -notmatch [regex]::Escape($Version)) { throw "Packaged voice.exe reports '$actual', not $Version." }
    }

    # An explicit list: never the checkout's local state (config, models, logs).
    $files = @('README.md', 'LICENSE', 'config.example.toml', 'skills/voice/SKILL.md', 'sidecar/setup.ps1') +
        @(git ls-files 'claude-plugin/**' 'sidecar/voice_sidecar/**' 'sidecar/profiles/**' 'sidecar/requirements/**' 'integrations/**')
    foreach ($relative in $files) {
        $target = Join-Path $stageRoot $relative
        New-Item -ItemType Directory -Path (Split-Path $target) -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $repoRoot $relative) -Destination $target
    }

    # Dependency copyright and license texts go with the binary.
    $modules = Invoke-Go -GoArgs @('list', '-deps', '-f', '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}', './cmd/voice') |
        Where-Object { $_ } | Sort-Object -Unique
    $notices = [Text.StringBuilder]::new()
    [void]$notices.AppendLine('voice third-party notices')
    [void]$notices.AppendLine('The following components keep their own licenses. Speech models downloaded by `voice models pull` have theirs; see README.md.')
    foreach ($line in $modules) {
        $parts = $line -split '\|', 3
        if ($parts[0] -eq 'voice') { continue }
        $license = $null
        foreach ($name in @('LICENSE', 'LICENSE.txt', 'LICENSE.md', 'COPYING')) {
            $candidate = Join-Path $parts[2] $name
            if (Test-Path -LiteralPath $candidate -PathType Leaf) { $license = $candidate; break }
        }
        if (-not $license) { throw "Missing license text for $($parts[0])." }
        [void]$notices.AppendLine("`n--- $($parts[0]) $($parts[1]) ---`n")
        [void]$notices.AppendLine([IO.File]::ReadAllText($license))
    }
    $goRoot = Invoke-Go -GoArgs @('env', 'GOROOT')
    [void]$notices.AppendLine("`n--- Go runtime ---`n")
    [void]$notices.AppendLine([IO.File]::ReadAllText((Join-Path $goRoot 'LICENSE')))
    [IO.File]::WriteAllText((Join-Path $stageRoot 'THIRD_PARTY_NOTICES.txt'), $notices.ToString(), [Text.UTF8Encoding]::new($false))

    $archivePath = Join-Path $distRoot $archiveName
    Compress-Archive -Path (Join-Path $stageRoot '*') -DestinationPath $archivePath -Force
    $hash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $distRoot 'SHA256SUMS'), "$hash  $archiveName`n", [Text.UTF8Encoding]::new($false))
    Write-Output "Created $archivePath ($Version)"
} finally {
    foreach ($k in $saved.Keys) { Set-Item -Path "env:$k" -Value $saved[$k] -ErrorAction SilentlyContinue; if ($null -eq $saved[$k]) { Remove-Item "env:$k" -ErrorAction SilentlyContinue } }
    Pop-Location
    $resolved = [IO.Path]::GetFullPath($stageRoot)
    $parent = [IO.Path]::GetFullPath($distRoot) + [IO.Path]::DirectorySeparatorChar
    if (-not $resolved.StartsWith($parent, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe staging path.' }
    if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
}
