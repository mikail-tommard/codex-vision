param([switch]$Race, [switch]$WindowsIntegration)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$oldIntegration = $env:CODEX_VISION_WINDOWS_INTEGRATION
Push-Location $root
try {
    if ($WindowsIntegration) { $env:CODEX_VISION_WINDOWS_INTEGRATION = "1" }
    $testArgs = @("test", "./...")
    if ($Race) { $testArgs = @("test", "-race", "./...") }
    & go @testArgs
    if ($LASTEXITCODE -ne 0) { throw "go test failed" }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
} finally { $env:CODEX_VISION_WINDOWS_INTEGRATION = $oldIntegration; Pop-Location }
