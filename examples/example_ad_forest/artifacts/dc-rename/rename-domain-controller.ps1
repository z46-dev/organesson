$ErrorActionPreference = "Stop"
$ComputerName = "OG-AD-DC01"
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "rename-domain-controller.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    if ($env:COMPUTERNAME -ine $ComputerName) {
        Rename-Computer -NewName $ComputerName -Force
        Write-Host "Renamed the server to $ComputerName."
        Stop-Transcript | Out-Null
    }
    else {
        Write-Host "The server is already named $ComputerName."
        Stop-Transcript | Out-Null
    }
}
catch {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
    throw
}
