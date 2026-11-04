<#
.SYNOPSIS
Creates the sidecar's Python environments and installs GPU voice backends.

.DESCRIPTION
Each backend gets its own environment in <Target>\envs\<backend>, so their
dependency pins (chatterbox pins an old torch) cannot break each other.
`voice sidecar setup` passes the configured sidecar_dir as -Target. Downloads
are cached in <Target>\.pip-cache, so a rerun after an interruption resumes
instead of downloading the multi-GB wheels again; finished stages are skipped
(-Force redoes them).

PyTorch build by GPU (-Gpu auto detects it):
  nvidia  CUDA wheels from download.pytorch.org (-Cuda, default cu128; the
          RTX 50 series needs cu128 or newer)
  amd     AMD's ROCm wheels for Windows from a profile in profiles\ (default
          amd-rocm-windows: Radeon RX 9000 and some RX 7000, Python 3.12), or
          explicit -TorchWheels URLs. A GPU computation test checks the result.
  cpu     CPU wheels: works on any machine, several times slower.

.EXAMPLE
.\setup.ps1 -Backend chatterbox
.\setup.ps1 -Backend chatterbox,xtts -Gpu nvidia -Target D:\voice-sidecar
.\setup.ps1 -Backend chatterbox -Gpu amd -TorchProfile amd-rocm-windows
.\setup.ps1 -Help
#>
param(
    [string[]]$Backend = @(),
    [ValidateSet('auto', 'nvidia', 'amd', 'cpu')][string]$Gpu = 'auto',
    [string]$Cuda = 'cu128',
    [string[]]$TorchWheels = @(),
    [string]$TorchProfile = '',            # name in profiles\ or a .json path
    [string]$Python = '',                  # python.exe to use; default: found automatically
    [string]$Target = $PSScriptRoot,
    [switch]$Force,
    [switch]$Help
)
$ErrorActionPreference = 'Stop'
$usage = 'usage: setup.ps1 -Backend chatterbox|f5|xtts|test[,...] [-Gpu auto|nvidia|amd|cpu] [-Cuda cu128] [-TorchProfile name|file.json] [-TorchWheels url,...] [-Python path] [-Target dir] [-Force]'
# -File passes a comma list as one string.
$Backend = @($Backend | ForEach-Object { $_ -split ',' } | Where-Object { $_ })
$TorchWheels = @($TorchWheels | ForEach-Object { $_ -split ',' } | Where-Object { $_ })

# Everything is checked before anything is installed.
if ($Help -or $Backend -contains '--help' -or $Backend -contains '-h' -or $Backend -contains 'help') { Write-Host $usage; Get-Help $PSCommandPath -Detailed | Out-String | Write-Host; exit 0 }
if (-not $Backend) { Write-Host $usage; exit 2 }
# Python package, and the module that proves it imports.
$packages = @{
    chatterbox = @{ pip = @('chatterbox-tts'); module = 'chatterbox' }
    f5         = @{ pip = @('f5-tts'); module = 'f5_tts' }
    xtts       = @{ pip = @('coqui-tts'); module = 'TTS' }
    test       = @{ pip = @(); module = '' }
}
foreach ($b in $Backend) { if (-not $packages.ContainsKey($b)) { Write-Host $usage; throw "unknown backend $b (chatterbox, f5, xtts, test)" } }

if ($Gpu -eq 'auto') {
    $names = @(Get-CimInstance Win32_VideoController -ErrorAction SilentlyContinue | ForEach-Object Name)
    $Gpu = if ($names -match 'NVIDIA') { 'nvidia' } elseif ($names -match 'AMD|Radeon') { 'amd' } else { 'cpu' }
    Write-Host "GPU: $($names -join ', ') -> $Gpu"
}

$torchSet = $null
if ($Gpu -eq 'amd' -and -not $TorchWheels) {
    if (-not $TorchProfile) { $TorchProfile = 'amd-rocm-windows' }
    $file = if (Test-Path -LiteralPath $TorchProfile) { $TorchProfile } else { Join-Path $PSScriptRoot "profiles\$TorchProfile.json" }
    if (-not (Test-Path -LiteralPath $file)) { throw "torch profile $TorchProfile not found ($file)" }
    $torchSet = Get-Content -LiteralPath $file -Raw | ConvertFrom-Json
    $TorchWheels = @($torchSet.wheels)
    Write-Host "Torch profile: $($torchSet.description)"
}

# Python: AMD's wheels are built for 3.12 only; the others take 3.10 to 3.12.
$wanted = if ($torchSet -and $torchSet.python) { @($torchSet.python) } elseif ($Gpu -eq 'amd') { @('3.12') } else { @('3.12', '3.11', '3.10') }
# Runs a native command whose stderr is redirected: Windows PowerShell 5.1 turns
# each stderr line into an error, which 'Stop' would make fatal.
function Invoke-Native([scriptblock]$cmd) {
    $ErrorActionPreference = 'Continue'
    & $cmd
}
function Get-PythonInfo([string]$exe, [string[]]$pre) {
    try {
        $out = @(Invoke-Native { & $exe @pre -c "import sys; print('%d.%d' % sys.version_info[:2]); print(sys.executable)" 2>$null })
        if ($LASTEXITCODE -eq 0 -and $out.Count -ge 2) { return [pscustomobject]@{ Version = $out[0].Trim(); Exe = $out[1].Trim() } }
    } catch {}
}
function Find-Python {
    if ($Python) {
        $info = Get-PythonInfo $Python @()
        if (-not $info) { throw "-Python $Python does not run" }
        if ($info.Version -notin $wanted) { throw "-Python $Python is $($info.Version); this setup needs Python $($wanted -join ' or ')" }
        return $info.Exe
    }
    $candidates = @()
    if (Get-Command py -ErrorAction SilentlyContinue) { $candidates += $wanted | ForEach-Object { , @('py', "-$_") } }
    foreach ($v in $wanted) {
        $short = $v -replace '\.', ''
        $candidates += , @("$env:LOCALAPPDATA\Programs\Python\Python$short\python.exe")
        $candidates += , @("$env:ProgramFiles\Python$short\python.exe")
        $candidates += , @("C:\Python$short\python.exe")
    }
    $candidates += , @('python'); $candidates += , @('python3')
    foreach ($c in $candidates) {
        $exe, $pre = $c[0], @($c | Select-Object -Skip 1)
        if (-not (Get-Command $exe -ErrorAction SilentlyContinue)) { continue }
        $info = Get-PythonInfo $exe $pre
        if ($info -and $info.Version -in $wanted) { return $info.Exe }
    }
    throw "Python $($wanted -join ' or ') not found (tried py, the standard install folders and PATH). Install it from python.org (64-bit) or pass -Python C:\path\to\python.exe"
}
$basePython = Find-Python
Write-Host "Python: $basePython"

New-Item -ItemType Directory -Force $Target | Out-Null
# A persistent download cache next to the environments (data_root's drive, not
# the profile): an interrupted setup resumes without downloading again.
Remove-Item Env:PIP_NO_CACHE_DIR -ErrorAction SilentlyContinue
$env:PIP_CACHE_DIR = Join-Path $Target '.pip-cache'
$tmp = Join-Path $Target '.tmp'
New-Item -ItemType Directory -Force $tmp | Out-Null
$env:TMP = $tmp
$env:TEMP = $tmp
$marker = @{ nvidia = '+cu'; amd = 'rocm'; cpu = '' }[$Gpu]
if ($torchSet -and $torchSet.marker) { $marker = $torchSet.marker }

function Invoke-Pip([string]$py, [string[]]$arguments, [string]$what) {
    & $py -m pip install --disable-pip-version-check @arguments
    if ($LASTEXITCODE) { throw "installing $what failed (rerun to resume; downloads are cached in $env:PIP_CACHE_DIR)" }
}
function Install-Torch([string]$py, [switch]$Again) {
    $extra = if ($Again) { @('--force-reinstall', '--no-deps') } else { @() }
    switch ($Gpu) {
        'nvidia' { Invoke-Pip $py (@($extra) + 'torch', 'torchaudio', '--index-url', "https://download.pytorch.org/whl/$Cuda") "PyTorch ($Gpu)" }
        'amd'    { Invoke-Pip $py (@($extra) + $TorchWheels) "PyTorch ($Gpu)" }
        'cpu'    { Invoke-Pip $py (@($extra) + 'torch', 'torchaudio', '--index-url', 'https://download.pytorch.org/whl/cpu') "PyTorch ($Gpu)" }
    }
}
# Runs a real computation on the GPU: is_available() alone passes on broken drivers.
$gpuTest = "import torch; ok = torch.cuda.is_available() and '$marker' in torch.__version__; print('torch', torch.__version__, 'gpu', torch.cuda.get_device_name(0) if torch.cuda.is_available() else 'none'); x = torch.randn(1024, 1024, device='cuda') if ok else None; print('compute', 'ok' if ok and torch.isfinite((x @ x).sum()).item() else 'FAILED')"
function Test-Torch([string]$py) {
    $out = @(Invoke-Native { & $py -c $gpuTest 2>&1 })
    $out | ForEach-Object { Write-Host "  $_" }
    return ($LASTEXITCODE -eq 0) -and ($out -match '^compute ok')
}
$gpuHint = @{
    amd    = 'Check: AMD graphics driver 26.2.2 or newer, Visual C++ 2015-2022 Redistributable, Smart App Control and Application Guard off, a supported Radeon (see profiles\amd-rocm-windows.json).'
    nvidia = 'Check: a current NVIDIA driver; RTX 50 cards need -Cuda cu128 or newer.'
    cpu    = ''
}[$Gpu]

# Stages done per environment, so a rerun skips them (-Force redoes them).
function Read-State([string]$envDir) {
    $f = Join-Path $envDir '.voice-setup.json'
    $s = @{}
    if (-not $Force -and (Test-Path -LiteralPath $f)) { (Get-Content -LiteralPath $f -Raw | ConvertFrom-Json).PSObject.Properties | ForEach-Object { $s[$_.Name] = $_.Value } }
    return $s
}
function Save-State([string]$envDir, $s) { $s | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $envDir '.voice-setup.json') -Encoding UTF8 }

$done = @(); $skipped = @()
foreach ($b in $Backend) {
    Write-Host "`n== $b =="
    $envDir = Join-Path $Target "envs\$b"
    $py = Join-Path $envDir 'Scripts\python.exe'
    $state = Read-State $envDir
    # Another PyTorch build: start over, and replace the installed torch (pip
    # keeps an installed build of the same version otherwise).
    $switched = [bool]($state.gpu -and $state.gpu -ne $Gpu)
    if ($switched) { $state = @{} }
    if (-not (Test-Path -LiteralPath $py)) {
        & $basePython -m venv $envDir
        if ($LASTEXITCODE) { throw "creating the environment $envDir failed" }
        $state = @{}; $switched = $false
    }
    $state.gpu = $Gpu
    if (-not $state.pip) { Invoke-Pip $py @('--upgrade', 'pip') 'pip'; $state.pip = $true; Save-State $envDir $state }
    $needsTorch = $b -ne 'test'
    if ($needsTorch -and -not $state.torch) {
        Install-Torch $py -Again:$switched
        if ($marker -and -not (Test-Torch $py)) { throw "PyTorch ($Gpu) does not compute on the GPU. $gpuHint" }
        $state.torch = $true; Save-State $envDir $state
    }
    # A backend this PyTorch build cannot run is skipped before its downloads.
    $reason = if ($torchSet -and $torchSet.skip -and $torchSet.skip.PSObject.Properties[$b]) { $torchSet.skip.$b } else { '' }
    if ($b -eq 'f5' -and $needsTorch) {
        Invoke-Native { & $py -c "import torch.distributed as d; d.ReduceOp" 2>$null }
        if ($LASTEXITCODE) { $reason = 'this PyTorch build has no torch.distributed.ReduceOp, which F5-TTS imports' } else { $reason = '' }
    }
    if ($reason) { Write-Warning "skipping ${b}: $reason"; $skipped += $b; continue }
    $spec = $packages[$b]
    if ($spec.pip -and -not $state.backend) {
        # Tested pins, when the repository has them for this backend and GPU.
        $constraints = Join-Path $PSScriptRoot "requirements\$b-$Gpu.txt"
        $extra = if (Test-Path -LiteralPath $constraints) { @('-c', $constraints) } else { @() }
        Invoke-Pip $py (@($spec.pip) + $extra) $b
        if ($marker) {
            # Backends such as chatterbox-tts pin an old torch, and pip replaces the
            # GPU build with that CPU wheel. Put the GPU build back without
            # letting pip touch other packages.
            Invoke-Native { & $py -c "import torch, sys; sys.exit(0 if torch.cuda.is_available() and '$marker' in torch.__version__ else 1)" 2>$null }
            if ($LASTEXITCODE) {
                Install-Torch $py -Again
                if (-not (Test-Torch $py)) { throw "PyTorch ($Gpu) does not compute on the GPU after installing $b. $gpuHint" }
            }
        }
        & $py -c "import $($spec.module)"
        if ($LASTEXITCODE) { throw "$b installed but does not import; see the error above" }
        $state.backend = $true; Save-State $envDir $state
    }
    $done += $b
}
Get-ChildItem -LiteralPath $tmp -Force | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
Write-Host "`nsidecar ready in $Target ($Gpu): $(if ($done) { $done -join ', ' } else { 'nothing' })$(if ($skipped) { "; skipped: $($skipped -join ', ')" })"
if ($skipped -and -not $done) { exit 1 }
