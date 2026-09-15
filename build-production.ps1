# build-production.ps1 - Production Release Builder
# Generates stripped, hardened, production-grade binaries with baked metadata.
param(
    [string]$TargetOS = $env:GOOS,
    [string]$TargetArch = $env:GOARCH,
    [string]$OutputPath = ""
)

$ErrorActionPreference = "Stop"

if (-not $TargetOS) {
    $TargetOS = $(go env GOOS)
}
if (-not $TargetArch) {
    $TargetArch = $(go env GOARCH)
}

$commit = $(git rev-parse --short HEAD 2>$null)
if (-not $commit) { $commit = "none" }

$version = $(git describe --tags --always --dirty 2>$null)
if (-not $version) { $version = "v7.0.0-phase5" }

$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

$binaryName = "cli-proxy-api"
if ($TargetOS -eq "windows") {
    $binaryName += ".exe"
}

if (-not $OutputPath) {
    $OutputPath = Join-Path "bin" $binaryName
}

$outputDir = Split-Path -Parent $OutputPath
if ($outputDir -and -not (Test-Path $outputDir)) {
    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
}

$ldflags = "-s -w -X 'main.Version=$version' -X 'main.Commit=$commit' -X 'main.BuildDate=$buildDate' " +
           "-X 'github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.Version=$version' " +
           "-X 'github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.Commit=$commit' " +
           "-X 'github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.BuildDate=$buildDate'"

Write-Host "==================================================" -ForegroundColor Cyan
Write-Host "Building Production CLIProxyAPI Binary" -ForegroundColor Cyan
Write-Host "  OS/Arch:    $TargetOS/$TargetArch"
Write-Host "  Version:    $version"
Write-Host "  Commit:     $commit"
Write-Host "  Build Date: $buildDate"
Write-Host "  Output:     $OutputPath"
Write-Host "==================================================" -ForegroundColor Cyan

$env:GOOS = $TargetOS
$env:GOARCH = $TargetArch
$env:CGO_ENABLED = "0"

go build -trimpath -ldflags "$ldflags" -o $OutputPath ./cmd/server

if (Test-Path $OutputPath) {
    $item = Get-Item $OutputPath
    $sizeMB = [math]::Round($item.Length / 1MB, 2)
    $hash = (Get-FileHash $OutputPath -Algorithm SHA256).Hash
    Write-Host "Build Succeeded!" -ForegroundColor Green
    Write-Host "  Binary Size: $sizeMB MB ($($item.Length) bytes)"
    Write-Host "  SHA-256:     $hash"
} else {
    Write-Error "Build failed: Output binary not found at $OutputPath"
}
