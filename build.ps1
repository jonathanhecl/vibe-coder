$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ScriptDir

go build -o vibe-coder.exe ./cmd/vibe-coder
exit $LASTEXITCODE
