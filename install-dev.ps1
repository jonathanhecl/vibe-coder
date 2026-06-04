Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ScriptDir

function Get-BuildVersion {
    $baseVersion = "dev"
    $shortSha = "nogit"
    $dirtySuffix = ""
    $timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd.HHmm")

    if (Get-Command git -ErrorAction SilentlyContinue) {
        try {
            $null = git rev-parse --is-inside-work-tree 2>$null
            if ($LASTEXITCODE -eq 0) {
                $tag = git describe --tags --abbrev=0 --match "v[0-9]*" 2>$null
                if (($LASTEXITCODE -eq 0) -and $tag) {
                    $baseVersion = $tag.Trim()
                }

                $sha = git rev-parse --short HEAD 2>$null
                if (($LASTEXITCODE -eq 0) -and $sha) {
                    $shortSha = $sha.Trim()
                }

                $null = git diff --quiet --ignore-submodules HEAD -- 2>$null
                if ($LASTEXITCODE -ne 0) {
                    $dirtySuffix = ".dirty"
                }
            }
        }
        catch {
            # Fallback defaults already set.
        }
    }

    return "$baseVersion+$timestamp.sha$shortSha$dirtySuffix"
}

$version = Get-BuildVersion
$ldflags = "-X github.com/jonathanhecl/vibe-coder/internal/version.Value=$version"

Write-Output "[INFO] Installing vibe-coder $version"
go install -ldflags $ldflags ./cmd/vibe-coder
Write-Output "[OK] Installed vibe-coder"
exit $LASTEXITCODE
