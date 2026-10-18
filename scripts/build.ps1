param([ValidateSet("windows", "darwin", "linux")][string]$TargetOS = "windows", [ValidateSet("amd64", "arm64")][string]$TargetArch = "amd64", [string]$Output = "")
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
if (!$Output) { $extension = if ($TargetOS -eq "windows") { ".exe" } else { "" }; $Output = Join-Path $root "bin/codex-vision-$TargetOS-$TargetArch$extension" }
$oldOS = $env:GOOS; $oldArch = $env:GOARCH; $oldCGO = $env:CGO_ENABLED
Push-Location $root
try {
    $env:GOOS = $TargetOS; $env:GOARCH = $TargetArch; $env:CGO_ENABLED = "0"
    if ($TargetOS -eq "darwin") { $env:CGO_ENABLED = "1" }
    New-Item -ItemType Directory -Force (Split-Path ([IO.Path]::GetFullPath($Output))) | Out-Null
    & go build -trimpath -o $Output ./cmd/codex-vision
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    Write-Host "Built $Output"
} finally { $env:GOOS = $oldOS; $env:GOARCH = $oldArch; $env:CGO_ENABLED = $oldCGO; Pop-Location }
