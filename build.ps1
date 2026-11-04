param(
    [string]$Output = (Join-Path $PSScriptRoot 'bin\voice.exe'),
    [switch]$Test
)
$ErrorActionPreference = 'Stop'

# Go from PATH; a resources folder next to the checkout (Go and MinGW
# unpacked there) is used first when it exists, so nothing needs a global install.
$resources = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\resources'))
if (Test-Path (Join-Path $resources 'go\bin\go.exe')) {
    $env:PATH = "$resources\go\bin;$resources\mingw64\bin;$env:PATH"
    $env:GOPATH = Join-Path $resources 'go-workspace'
    $env:GOCACHE = Join-Path $env:GOPATH 'build-cache'
}
# The product binary stays cgo-free; gcc is only used by layout tests.
$env:CGO_ENABLED = '0'

Push-Location $PSScriptRoot
try {
    if ($Test) {
        go vet ./...
        if ($LASTEXITCODE) { throw 'go vet failed' }
        go test ./...
        if ($LASTEXITCODE) { throw 'go test failed' }
    }
    $version = (git describe --always --dirty 2>$null)
    if (-not $version) { $version = 'dev' }
    go build -trimpath -ldflags "-s -w -X main.version=$version" -o $Output ./cmd/voice
    if ($LASTEXITCODE) { throw 'go build failed' }
    # The Python sidecar runs from next to the binary; keep its venv there.
    $sidecar = Join-Path (Split-Path $Output -Parent) 'sidecar'
    New-Item -ItemType Directory -Force $sidecar | Out-Null
    Copy-Item -Recurse -Force (Join-Path $PSScriptRoot 'sidecar\voice_sidecar'), (Join-Path $PSScriptRoot 'sidecar\setup.ps1') $sidecar
    # The Integrations window's entries live next to voice.exe.
    Copy-Item -Recurse -Force (Join-Path $PSScriptRoot 'integrations') (Split-Path $Output -Parent)
    Write-Host "built $Output ($version)"
}
finally {
    Pop-Location
}
