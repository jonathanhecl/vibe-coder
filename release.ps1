#!/usr/bin/env pwsh
#requires -Version 5.1
<#
.SYNOPSIS
    Builds cross-platform binaries for vibe-coder, tags the release, pushes to origin, and creates a GitHub Release with assets.
.EXAMPLE
    ./release.ps1 v0.1.0
#>
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Version,

    [switch]$SkipTests
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# 1. Validate version format (e.g., v1.0.0)
if ($Version -notmatch '^v\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$') {
    Write-Host "[ERROR] Version must be in the format vX.Y.Z (e.g., v1.0.0)" -ForegroundColor Red
    exit 1
}

# 2. Set working directory to repository root
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ScriptDir

# 3. Check for Git repository
if (-not (Test-Path .git)) {
    Write-Host "[ERROR] This script must be run from the root of a Git repository." -ForegroundColor Red
    exit 1
}

# 4. Check for uncommitted changes
$status = git status --porcelain
if ($status) {
    Write-Host "[ERROR] There are uncommitted changes in the repository:" -ForegroundColor Red
    Write-Host $status -ForegroundColor Yellow
    Write-Host "[ERROR] Please commit or stash your changes before releasing." -ForegroundColor Red
    exit 1
}

# 5. Get current branch
$branch = (git branch --show-current).Trim()
if ([string]::IsNullOrEmpty($branch)) {
    Write-Host "[ERROR] Could not determine the current branch (are you in detached HEAD?)." -ForegroundColor Red
    exit 1
}
if ($branch -ne 'main') {
    Write-Host "[WARN] Current branch is '$branch', not 'main'. Make sure you intend to release from this branch." -ForegroundColor Yellow
}

# 6. Check if tag already exists locally
$tagExists = git tag -l $Version
if ($tagExists) {
    Write-Host "[ERROR] Tag '$Version' already exists locally." -ForegroundColor Red
    exit 1
}

# 7. Parse Owner and Repo from remote origin URL
$remoteUrl = (git remote get-url origin).Trim()
if ($remoteUrl -match 'github\.com[:/]([^/]+)/([^/.]+?)(\.git)?$') {
    $owner = $Matches[1]
    $repo = $Matches[2]
}
else {
    Write-Host "[ERROR] Could not determine GitHub owner/repo from origin URL: $remoteUrl" -ForegroundColor Red
    exit 1
}

# 8. Get GitHub Personal Access Token (PAT)
$token = $env:GITHUB_TOKEN
if ([string]::IsNullOrEmpty($token)) {
    Write-Host "[WARN] GITHUB_TOKEN is not set in the environment." -ForegroundColor Yellow
    Write-Host "[INFO] Please enter a GitHub Personal Access Token with repo and write:packages permissions:" -ForegroundColor Yellow
    $token = Read-Host -AsSecureString
    if (-not $token) {
        Write-Host "[ERROR] A GitHub token is required to create a release." -ForegroundColor Red
        exit 1
    }
    $BSTR = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($token)
    $token = [System.Runtime.InteropServices.Marshal]::PtrToStringAuto($BSTR)
}

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "   PREPARING RELEASE                         " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "Version:   $Version" -ForegroundColor Gray
Write-Host "Repo:      $owner/$repo" -ForegroundColor Gray
Write-Host "Branch:    $branch" -ForegroundColor Gray
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host ""

$confirmation = Read-Host "Continue with release build and GitHub upload? (y/n)"
if ($confirmation -ne "y" -and $confirmation -ne "yes") {
    Write-Host "[INFO] Release cancelled." -ForegroundColor Yellow
    exit 0
}

# 9. Run tests unless skipped
if (-not $SkipTests) {
    Write-Host "`n[1/5] Running tests..." -ForegroundColor Cyan
    go test -timeout 10m ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[ERROR] Tests failed. Aborting release." -ForegroundColor Red
        exit 1
    }
    Write-Host "[OK] Tests passed." -ForegroundColor Green
}
else {
    Write-Host "`n[1/5] Skipping tests as requested." -ForegroundColor Cyan
}

# 10. Prepare output directory
$distDir = Join-Path $ScriptDir "dist"
if (Test-Path $distDir) {
    Remove-Item -Recurse -Force $distDir
}
New-Item -ItemType Directory -Force -Path $distDir | Out-Null

# 11. Build cross-platform binaries
Write-Host "`n[$($SkipTests ? 2 : 2)/5] Compiling cross-platform binaries..." -ForegroundColor Cyan

$targets = @(
    @{ GOOS = 'windows'; GOARCH = 'amd64'; Ext = '.exe' },
    @{ GOOS = 'linux'; GOARCH = 'amd64'; Ext = '' },
    @{ GOOS = 'linux'; GOARCH = 'arm64'; Ext = '' },
    @{ GOOS = 'darwin'; GOARCH = 'amd64'; Ext = '' },
    @{ GOOS = 'darwin'; GOARCH = 'arm64'; Ext = '' }
)

$ldflags = "-s -w -X github.com/jonathanhecl/vibe-coder/internal/version.Value=$Version"

foreach ($target in $targets) {
    $goos = $target.GOOS
    $goarch = $target.GOARCH
    $ext = $target.Ext
    $binName = "vibe-coder$ext"
    $assetName = "vibe-coder_${Version}_${goos}_${goarch}.zip"
    $stageDir = Join-Path $distDir "build_${goos}_${goarch}"

    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null
    $binPath = Join-Path $stageDir $binName

    Write-Host "[INFO] Building $goos/$goarch..." -ForegroundColor Gray
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $env:CGO_ENABLED = '0'

    go build -ldflags $ldflags -o $binPath ./cmd/vibe-coder
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[ERROR] Build failed for $goos/$goarch." -ForegroundColor Red
        exit 1
    }

    $assetPath = Join-Path $distDir $assetName
    Push-Location $stageDir
    try {
        Compress-Archive -Path $binName -DestinationPath $assetPath -Force
    }
    finally {
        Pop-Location
    }
    Remove-Item -Recurse -Force $stageDir
    Write-Host "[OK] Built $assetName" -ForegroundColor Green
}

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue

# 12. Create Git tag and push
Write-Host "`n[$($SkipTests ? 3 : 3)/5] Creating tag and pushing to origin..." -ForegroundColor Cyan
git tag -a $Version -m "Release $Version"
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] Failed to create local tag." -ForegroundColor Red
    exit 1
}

git push origin $branch
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] Failed to push branch '$branch' to origin." -ForegroundColor Red
    git tag -d $Version 2>&1 | Out-Null
    exit 1
}

git push origin $Version
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] Failed to push tag '$Version' to origin." -ForegroundColor Red
    exit 1
}

Write-Host "[OK] Tag pushed to origin." -ForegroundColor Green

# 13. Create GitHub release
Write-Host "`n[$($SkipTests ? 4 : 4)/5] Creating GitHub release..." -ForegroundColor Cyan
$releaseUrl = "https://api.github.com/repos/$owner/$repo/releases"
$headers = @{
    "Authorization"        = "Bearer $token"
    "Accept"               = "application/vnd.github+json"
    "X-GitHub-Api-Version" = "2022-11-28"
}

$releaseBody = @{
    tag_name               = $Version
    target_commitish       = $branch
    name                   = "Release $Version"
    body                   = "Release $Version of vibe-coder."
    draft                  = $false
    prerelease             = $false
    generate_release_notes = $true
} | ConvertTo-Json -Depth 10

try {
    $response = Invoke-RestMethod -Uri $releaseUrl -Method Post -Headers $headers -Body $releaseBody -ContentType "application/json; charset=utf-8"
    $uploadUrlTemplate = $response.upload_url
    $htmlUrl = $response.html_url
    $uploadUrlBase = $uploadUrlTemplate -replace '\{.*?\}', ''
    Write-Host "[OK] Created GitHub release: $htmlUrl" -ForegroundColor Green
}
catch {
    Write-Host "[ERROR] Failed to create GitHub release: $_" -ForegroundColor Red
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $responseBody = $reader.ReadToEnd()
        Write-Host "[ERROR] GitHub API response: $responseBody" -ForegroundColor Red
    }
    Write-Host "[INFO] To retry, delete the remote tag with: git push origin --delete $Version" -ForegroundColor Yellow
    exit 1
}

# 14. Upload assets
Write-Host "`n[$($SkipTests ? 5 : 5)/5] Uploading release assets..." -ForegroundColor Cyan
$assets = Get-ChildItem -Path $distDir -Filter "*.zip" -File
if ($assets.Count -eq 0) {
    Write-Host "[ERROR] No zip assets found in dist/ to upload." -ForegroundColor Red
    exit 1
}

foreach ($asset in $assets) {
    $fileName = $asset.Name
    $filePath = $asset.FullName
    $encodedName = [System.Uri]::EscapeDataString($fileName)
    $uploadUrl = "${uploadUrlBase}?name=$encodedName"

    Write-Host "[INFO] Uploading $fileName..." -ForegroundColor Gray

    $uploadHeaders = @{
        "Authorization"        = "Bearer $token"
        "Accept"               = "application/vnd.github+json"
        "X-GitHub-Api-Version" = "2022-11-28"
        "Content-Type"         = "application/octet-stream"
    }

    try {
        $null = Invoke-RestMethod -Uri $uploadUrl -Method Post -Headers $uploadHeaders -InFile $filePath
        Write-Host "[OK] Uploaded $fileName" -ForegroundColor Green
    }
    catch {
        Write-Host "[ERROR] Failed to upload $fileName : $_" -ForegroundColor Red
        if ($_.Exception.Response) {
            $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
            $responseBody = $reader.ReadToEnd()
            Write-Host "[ERROR] Upload response: $responseBody" -ForegroundColor Red
        }
    }
}

Write-Host "`n[OK] Release process completed." -ForegroundColor Green
Write-Host "[INFO] Release available at: $htmlUrl" -ForegroundColor Green
exit 0
