$ErrorActionPreference = "Stop"
$DomainName = "adforest.test"
$NetBIOSName = "ADFOREST"
$DomainControllerAddress = "192.168.77.10"
$GatewayAddress = "192.168.77.1"
$PrefixLength = 24
$ExamplePassword = ConvertTo-SecureString "password123!" -AsPlainText -Force
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "promote-domain-controller.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    $computer = Get-CimInstance Win32_ComputerSystem
    if ($computer.DomainRole -in @(4, 5)) {
        Import-Module ActiveDirectory
        $deadline = (Get-Date).AddMinutes(8)
        $domain = $null
        while ((Get-Date) -lt $deadline) {
            try {
                $domain = Get-ADDomain -Server localhost -ErrorAction Stop
                break
            }
            catch {
                Start-Sleep -Seconds 10
            }
        }
        if ($null -eq $domain) {
            throw "This server is already a domain controller, but AD did not become ready; refusing to promote it again."
        }
        if ($domain.DNSRoot -ine $DomainName -or $domain.NetBIOSName -ine $NetBIOSName) {
            throw "This server already hosts unexpected domain '$($domain.DNSRoot)' ($($domain.NetBIOSName)); refusing to replace it."
        }
        Write-Host "Domain controller promotion for $DomainName was already completed; continuing safely."
        Stop-Transcript | Out-Null
        return
    }

    $oobeRegistryPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE"
    Set-ItemProperty -LiteralPath $oobeRegistryPath -Name BypassNRO -Type DWord -Value 1
    Set-ItemProperty -LiteralPath $oobeRegistryPath -Name SkipMachineOOBE -Type DWord -Value 1

    $activeAdapter = Get-NetAdapter | Where-Object { $_.Status -eq "Up" } | Sort-Object ifIndex | Select-Object -First 1
    if ($null -eq $activeAdapter) {
        throw "No active network adapter was found. Confirm the Organesson e1000e NIC is attached."
    }

    Set-NetIPInterface -InterfaceIndex $activeAdapter.ifIndex -AddressFamily IPv4 -Dhcp Disabled
    Get-NetRoute -InterfaceIndex $activeAdapter.ifIndex -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue |
        Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
    Get-NetIPAddress -InterfaceIndex $activeAdapter.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notin @("127.0.0.1", $DomainControllerAddress) } |
        Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue
    if (-not (Get-NetIPAddress -InterfaceIndex $activeAdapter.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
            Where-Object { $_.IPAddress -eq $DomainControllerAddress })) {
        New-NetIPAddress -InterfaceIndex $activeAdapter.ifIndex -IPAddress $DomainControllerAddress -PrefixLength $PrefixLength -DefaultGateway $GatewayAddress | Out-Null
    }
    Set-DnsClientServerAddress -InterfaceIndex $activeAdapter.ifIndex -ServerAddresses $DomainControllerAddress

    $localAdministrator = Get-LocalUser | Where-Object { $_.SID.Value -match "-500$" } | Select-Object -First 1
    if ($null -eq $localAdministrator) {
        throw "The built-in local Administrator account was not found."
    }
    $administratorNameConflict = Get-LocalUser -Name "Administrator" -ErrorAction SilentlyContinue
    if ($null -ne $administratorNameConflict -and $administratorNameConflict.SID -ne $localAdministrator.SID) {
        throw "A different local account already uses the Administrator name."
    }
    if ($localAdministrator.Name -ne "Administrator") {
        Rename-LocalUser -Name $localAdministrator.Name -NewName "Administrator"
        $localAdministrator = Get-LocalUser -Name "Administrator" -ErrorAction Stop
    }
    Enable-LocalUser -Name $localAdministrator.Name
    Set-LocalUser -Name $localAdministrator.Name -Password $ExamplePassword -PasswordNeverExpires $true
    $administrators = Get-LocalGroup -SID "S-1-5-32-544"
    Add-LocalGroupMember -Group $administrators.Name -Member $localAdministrator.Name -ErrorAction SilentlyContinue

    $bootstrapUser = Get-LocalUser -Name "ogadmin" -ErrorAction SilentlyContinue
    if ($null -ne $bootstrapUser) {
        Remove-LocalUser -Name $bootstrapUser.Name
    }

    $domainServices = Get-WindowsFeature -Name AD-Domain-Services
    if (-not $domainServices.Installed) {
        Install-WindowsFeature -Name AD-Domain-Services -IncludeManagementTools
    }

    Import-Module ADDSDeployment
    Install-ADDSForest `
        -DomainName $DomainName `
        -DomainNetbiosName $NetBIOSName `
        -InstallDNS `
        -SafeModeAdministratorPassword $ExamplePassword `
        -NoRebootOnCompletion `
        -Force

    Stop-Transcript | Out-Null
}
catch {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
    throw
}
