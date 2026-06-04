#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$repo = "jonathanhecl/vibe-coder"
$bin = "vibe-coder.exe"

# Detect architecture
$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
if ($arch -eq "386") {
    Write-Error "[ERROR] 32-bit Windows is not supported"
    exit 1
}

$os = "windows"
Write-Output "[INFO] Detected ${os}/${arch}"

# Fetch latest release from GitHub API
Write-Output "[INFO] Looking up latest release ..."
try {
    $response = Invoke-RestMethod -Uri "https://api.github.com/repos/${repo}/releases/latest" -UseBasicParsing
    $latest = $response.tag_name
} catch {
    Write-Error "[ERROR] Could not determine latest release. Are you offline or rate-limited?"
    exit 1
}
Write-Output "[INFO] Latest release: ${latest}"

# Build download URL
$asset = "vibe-coder_${latest}_${os}_${arch}.zip"
$url = "https://github.com/${repo}/releases/download/${latest}/${asset}"

# Determine install directory
$installDir = "$env:LOCALAPPDATA\Programs\vibe-coder"
if (-not (Test-Path $installDir)) {
    $null = New-Item -ItemType Directory -Force -Path $installDir
}

# Download
Write-Output "[INFO] Downloading ${asset} ..."
$tmpDir = [System.IO.Path]::GetTempPath() + [System.Guid]::NewGuid().ToString()
$null = New-Item -ItemType Directory -Force -Path $tmpDir
try {
    $zipPath = Join-Path $tmpDir $asset
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing

    # Extract
    Write-Output "[INFO] Extracting ..."
    Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force

    # Install
    Write-Output "[INFO] Installing to ${installDir} ..."
    $source = Join-Path $tmpDir $bin
    $dest = Join-Path $installDir $bin
    Move-Item -Path $source -Destination $dest -Force

    # Add to PATH if not present
    $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($userPath -notlike "*${installDir}*") {
        Write-Output "[INFO] Adding ${installDir} to user PATH ..."
        [Environment]::SetEnvironmentVariable("PATH", "${userPath};${installDir}", "User")
        Write-Output "[INFO] Restart your terminal for PATH changes to take effect"
    }

    Write-Output "[OK] Installed vibe-coder ${latest} to ${installDir}\${bin}"
} finally {
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}
