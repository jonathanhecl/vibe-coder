#!/usr/bin/env pwsh
#requires -Version 5.1
<#
.SYNOPSIS
    Compiles and packages vibe for all supported operating systems and architectures.
.EXAMPLE
    ./build-all.ps1
    ./build-all.ps1 -Version v1.0.0
#>
param(
    [Parameter(Position = 0)]
    [string]$Version = "dev"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Error: go is not installed or is not available in PATH." -ForegroundColor Red
    exit 1
}

$distDir = Join-Path $PSScriptRoot "dist"
if (Test-Path -LiteralPath $distDir) {
    Remove-Item -Recurse -Force $distDir
}
New-Item -ItemType Directory -Path $distDir | Out-Null

$module = "github.com/jonathanhecl/vibe-coder"
$ldflags = "-s -w -X ${module}/internal/version.Value=$Version"

$targets = @(
    @{ OS = "windows"; Arch = "amd64" },
    @{ OS = "linux";   Arch = "amd64" },
    @{ OS = "linux";   Arch = "arm64" },
    @{ OS = "darwin";  Arch = "amd64" },
    @{ OS = "darwin";  Arch = "arm64" }
)

Write-Host "Starting multiplatform build ($Version)..." -ForegroundColor Cyan

Push-Location $PSScriptRoot
try {
    foreach ($target in $targets) {
        $targetOs = $target.OS
        $targetArch = $target.Arch
        $binaryName = if ($targetOs -eq "windows") { "vibe.exe" } else { "vibe" }
        $packName = "vibe_${Version}_$($targetOs)_$($targetArch).zip"
        $packPath = Join-Path $distDir $packName
        $tempDir = Join-Path $distDir "build_${targetOs}_$targetArch"

        Write-Host "Building for $targetOs ($targetArch)..."
        New-Item -ItemType Directory -Path $tempDir | Out-Null

        $env:CGO_ENABLED = "0"
        $env:GOOS = $targetOs
        $env:GOARCH = $targetArch
        try {
            & go build -trimpath "-ldflags=$ldflags" -o (Join-Path $tempDir $binaryName) ./cmd/vibe
            if ($LASTEXITCODE -ne 0) {
                throw "Build failed for $targetOs/$targetArch"
            }
            Compress-Archive -Path (Join-Path $tempDir $binaryName) -DestinationPath $packPath -Force
        }
        finally {
            Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue
        }
        Write-Host "Packaged $packName ($binaryName)" -ForegroundColor Green
    }

    $lines = @()
    foreach ($file in Get-ChildItem -LiteralPath $distDir -Filter "*.zip" -File | Sort-Object Name) {
        $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        $lines += "$hash  $($file.Name)"
    }
    Set-Content -LiteralPath (Join-Path $distDir "checksums.txt") -Value $lines -Encoding ascii
    Write-Host "Wrote dist/checksums.txt" -ForegroundColor Green

    Write-Host ""
    Write-Host "Build and packaging completed successfully. Files in dist/:" -ForegroundColor Green
    Get-ChildItem -LiteralPath $distDir | Select-Object Name, Length | Format-Table
}
finally {
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}
