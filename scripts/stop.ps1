param([Parameter(Mandatory=$true)][string]$StopFile)
$ErrorActionPreference = "Stop"
# Creating a directory also trips the fail-closed stop gate. Do not replace existing content.
if (!(Test-Path -LiteralPath $StopFile)) { New-Item -ItemType File -Path $StopFile | Out-Null }
Write-Host "Stop latched for servers configured to use $StopFile. Leave it present until those processes exit."
