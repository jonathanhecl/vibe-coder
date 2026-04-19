$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

Write-Host "== POST-21: race test =="
$env:CGO_ENABLED = "1"
go test ./... -race -count=1
if ($LASTEXITCODE -ne 0) {
    throw "Race tests failed (or toolchain lacks gcc/cgo support)."
}

Write-Host "== POST-21: coverage threshold =="
$profile = "cover_post21.out"
go test ./internal/agent ./internal/session ./internal/ollama ./internal/tools ./internal/permissions "-coverprofile=$profile"
if ($LASTEXITCODE -ne 0) {
    throw "Coverage test command failed."
}

$total = go tool cover "-func=$profile" | Select-String "total:"
if (-not $total) {
    throw "Could not read total coverage."
}

$pctText = ($total.ToString() -split '\s+')[-1].TrimEnd('%')
$pct = [double]$pctText
Write-Host "Coverage total: $pct%"

if ($pct -lt 80.0) {
    throw "Coverage below 80%."
}

Write-Host "POST-21 gate passed."

