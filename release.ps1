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
$ldflags = "-s -w -X github.com/jonathanhecl/vibe-coder/internal/version.Value=$version"

$Out = "dist"
$null = New-Item -ItemType Directory -Force -Path $Out

$targets = @(
    @{ GOOS = "linux";   GOARCH = "amd64"; Format = "tar.gz" },
    @{ GOOS = "linux";   GOARCH = "arm64"; Format = "tar.gz" },
    @{ GOOS = "darwin";  GOARCH = "amd64"; Format = "tar.gz" },
    @{ GOOS = "darwin";  GOARCH = "arm64"; Format = "tar.gz" },
    @{ GOOS = "windows"; GOARCH = "amd64"; Format = "zip" }
)

foreach ($t in $targets) {
    $goos = $t.GOOS
    $goarch = $t.GOARCH
    $format = $t.Format
    Write-Output "[INFO] Building ${goos}/${goarch} ..."

    $workdir = Join-Path $Out "${goos}_${goarch}"
    $null = New-Item -ItemType Directory -Force -Path $workdir

    $bin = "vibe-coder"
    if ($goos -eq "windows") {
        $bin = "vibe-coder.exe"
    }

    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags "$ldflags" -o (Join-Path $workdir $bin) ./cmd/vibe-coder
    if ($LASTEXITCODE -ne 0) { throw "build failed for ${goos}/${goarch}" }

    $archiveBase = "vibe-coder_${version}_${goos}_${goarch}"
    if ($format -eq "tar.gz") {
        tar -czf (Join-Path $Out "$archiveBase.tar.gz") -C $workdir $bin
    } else {
        $zipPath = Join-Path $Out "$archiveBase.zip"
        Compress-Archive -Path (Join-Path $workdir $bin) -DestinationPath $zipPath -Force
    }

    Remove-Item -Recurse -Force $workdir
}

Write-Output "[INFO] Generating checksums.txt ..."
$archiveFiles = Get-ChildItem -Path $Out -Filter "vibe-coder_*" -File | Sort-Object Name
$stream = [System.IO.StreamWriter]::new((Join-Path $Out "checksums.txt"))
try {
    foreach ($file in $archiveFiles) {
        $hash = (Get-FileHash -Path $file.FullName -Algorithm SHA256).Hash.ToLower()
        $stream.WriteLine("$hash  $($file.Name)")
    }
} finally {
    $stream.Close()
}

Write-Output "[OK] Release artifacts in ${Out}/"
Get-ChildItem $Out | Format-Table Name, Length
exit 0
