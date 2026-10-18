param([ValidateSet("amd64", "arm64")][string]$TargetArch = "amd64", [string]$Destination = (Join-Path $env:LOCALAPPDATA "CodexVision"))
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$source = Join-Path $root "bin/codex-vision-windows-$TargetArch.exe"
if (!(Test-Path $source)) { & (Join-Path $PSScriptRoot "build.ps1") -TargetArch $TargetArch }
$exe = Join-Path $Destination "codex-vision.exe"
$config = Join-Path $Destination "config.json"
if (Test-Path $exe) { throw "Executable already exists: $exe. Choose a new destination; no existing file was replaced." }
New-Item -ItemType Directory -Force $Destination | Out-Null
Copy-Item $source $exe
if (!(Test-Path $config)) { Copy-Item (Join-Path $root "configs/config.example.json") $config }
Write-Host "Installed $exe"
Write-Host "Set MCP cwd to $Destination; config: $config"
Write-Host "No service, startup task, PATH modification or Codex config modification was made."
