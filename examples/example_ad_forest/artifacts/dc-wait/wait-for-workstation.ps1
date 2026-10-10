$ErrorActionPreference = "Stop"
$DomainName = "adforest.test"
$DomainControllerAddress = "192.168.77.10"
$ComputerName = "OG-WIN11-01"
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "wait-for-workstation.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    Import-Module ActiveDirectory
    $deadline = (Get-Date).AddMinutes(8)
    $joined = $false
    while ((Get-Date) -lt $deadline) {
        $computer = Get-CimInstance -ClassName Win32_ComputerSystem
        $adComputer = Get-ADComputer -Server localhost -Identity $ComputerName -ErrorAction SilentlyContinue
        $dnsRecord = Resolve-DnsName -Name "$ComputerName.$DomainName" -Server $DomainControllerAddress -ErrorAction SilentlyContinue
        if ($computer.PartOfDomain -and $computer.Domain -eq $DomainName -and $null -ne $adComputer -and $null -ne $dnsRecord) {
            $joined = $true
            break
        }
        Start-Sleep -Seconds 10
    }
    if (-not $joined) {
        throw "The workstation computer account and DNS record were not ready within 8 minutes."
    }
    Write-Host "$ComputerName is joined and registered in AD DNS."
}
finally {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
}
