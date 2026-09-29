$ErrorActionPreference = "Stop"

# This script is fetched by the already-running guest agent execution adapter as
# SYSTEM. The URL and token are supplied at runtime, never stored in this file.
param(
    [Parameter(Mandatory = $true)][string]$ArtifactUrl,
    [Parameter(Mandatory = $true)][string]$ArtifactToken
)

$headers = @{ Authorization = "Bearer $ArtifactToken" }
Invoke-WebRequest -Uri "$ArtifactUrl/windows-11-postinstall.ps1" -Headers $headers -OutFile "$env:TEMP\organesson-postinstall.ps1"
& powershell.exe -ExecutionPolicy Bypass -File "$env:TEMP\organesson-postinstall.ps1"
Set-Content -Path "C:\ProgramData\Organesson\bootstrap-complete" -Value (Get-Date -Format o)
