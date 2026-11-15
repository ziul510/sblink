$ErrorActionPreference = 'Stop'
$previousCGO = $env:CGO_ENABLED
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
Push-Location $PSScriptRoot
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    New-Item -ItemType Directory -Path (Join-Path $PSScriptRoot 'dist') -Force | Out-Null
    go build -trimpath -ldflags '-H=windowsgui -s -w' -o (Join-Path $PSScriptRoot 'dist\sblink.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
    Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $PSScriptRoot 'dist\sblink.exe')
} finally {
    $env:CGO_ENABLED = $previousCGO
    $env:GOOS = $previousOS
    $env:GOARCH = $previousArch
    Pop-Location
}
