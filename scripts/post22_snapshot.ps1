$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

Write-Host "Running GoReleaser snapshot..."
if (Get-Command goreleaser -ErrorAction SilentlyContinue) {
    goreleaser release --snapshot --clean --skip=publish
} else {
    go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish
}

if ($LASTEXITCODE -ne 0) {
    throw "GoReleaser snapshot failed."
}

Write-Host "Snapshot artifacts generated under ./dist"

