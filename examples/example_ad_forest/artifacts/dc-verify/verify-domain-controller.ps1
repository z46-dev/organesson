$ErrorActionPreference = "Stop"
$DomainName = "adforest.test"
$NetBIOSName = "ADFOREST"
$DomainControllerHost = "og-ad-dc01.adforest.test"
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "verify-domain-controller.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    $computer = Get-CimInstance Win32_ComputerSystem
    if ($computer.DomainRole -ne 5 -or $computer.Domain -ine $DomainName) {
        throw "This VM is not the expected writable domain controller for $DomainName."
    }

    $oobeRegistryPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE"
    $oobeSettings = Get-ItemProperty -LiteralPath $oobeRegistryPath -ErrorAction Stop
    if ([int]$oobeSettings.SkipMachineOOBE -ne 1) {
        throw "SkipMachineOOBE is not enabled on the domain controller."
    }
    if (Get-Process -Name "msoobe" -ErrorAction SilentlyContinue) {
        throw "Windows OOBE (msoobe.exe) is still active on the domain controller."
    }

    Import-Module ActiveDirectory
    $domain = Get-ADDomain -Server localhost -ErrorAction Stop
    if ($domain.DNSRoot -ine $DomainName -or $domain.NetBIOSName -ine $NetBIOSName -or $domain.PDCEmulator -ine $DomainControllerHost) {
        throw "The expected domain and PDC emulator are not active on this server."
    }
    $user = Get-ADUser -Server localhost -Identity "labuser" -Properties Enabled -ErrorAction Stop
    if (-not $user.Enabled) {
        throw "The AD test account labuser is not enabled."
    }

    $domainAdministrator = Get-ADUser -Server localhost -Identity "Administrator" -Properties Enabled -ErrorAction Stop
    if (-not $domainAdministrator.Enabled) {
        throw "The built-in domain Administrator account is not enabled."
    }
    $bootstrapAccount = Get-ADUser -Server localhost -Filter "SamAccountName -eq 'ogadmin'" -ErrorAction Stop
    if ($null -ne $bootstrapAccount) {
        throw "The temporary ogadmin bootstrap account should not remain in the domain."
    }
    Add-Type -AssemblyName System.DirectoryServices.AccountManagement
    $context = [System.DirectoryServices.AccountManagement.PrincipalContext]::new(
        [System.DirectoryServices.AccountManagement.ContextType]::Domain,
        "localhost"
    )
    if (-not $context.ValidateCredentials("Administrator", "password123!")) {
        throw "Domain Administrator authentication failed on the controller."
    }
    $context.Dispose()

    Write-Host "Domain controller, PDC role, domain Administrator, standard labuser, and completed unattended OOBE are verified."
}
finally {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
}
