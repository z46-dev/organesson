$ErrorActionPreference = "Stop"
$DomainName = "adforest.test"
$NetBIOSName = "ADFOREST"
$DomainControllerHost = "og-ad-dc01.adforest.test"
$DomainControllerAddress = "192.168.77.10"
$ComputerName = "OG-WIN11-01"
$LocalUserName = "Administrator"
$ExamplePassword = ConvertTo-SecureString "password123!" -AsPlainText -Force
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "join-workstation.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    $localUser = Get-LocalUser | Where-Object { $_.SID.Value -match "-500$" } | Select-Object -First 1
    if ($null -eq $localUser) {
        throw "The built-in local Administrator account was not found."
    }
    $administratorNameConflict = Get-LocalUser -Name $LocalUserName -ErrorAction SilentlyContinue
    if ($null -ne $administratorNameConflict -and $administratorNameConflict.SID -ne $localUser.SID) {
        throw "A different local account already uses the Administrator name."
    }
    if ($localUser.Name -ne $LocalUserName) {
        Rename-LocalUser -Name $localUser.Name -NewName $LocalUserName
    }
    Set-LocalUser -Name $LocalUserName -Password $ExamplePassword -PasswordNeverExpires $true
    Enable-LocalUser -Name $LocalUserName
    $administrators = Get-LocalGroup -SID "S-1-5-32-544"
    Add-LocalGroupMember -Group $administrators.Name -Member $LocalUserName -ErrorAction SilentlyContinue
    $bootstrapUser = Get-LocalUser -Name "ogadmin" -ErrorAction SilentlyContinue
    if ($null -ne $bootstrapUser) {
        Remove-LocalUser -Name $bootstrapUser.Name
    }

    $activeAdapter = Get-NetAdapter | Where-Object { $_.Status -eq "Up" } | Sort-Object ifIndex | Select-Object -First 1
    if ($null -eq $activeAdapter) {
        throw "No active network adapter was found. Confirm the Organesson NIC is attached."
    }

    Set-DnsClientServerAddress -InterfaceIndex $activeAdapter.ifIndex -ServerAddresses $DomainControllerAddress

    $credential = [System.Management.Automation.PSCredential]::new("$NetBIOSName\Administrator", $ExamplePassword)
    $computer = Get-CimInstance Win32_ComputerSystem
    if ($computer.PartOfDomain -and $computer.Domain -ieq $DomainName -and $env:COMPUTERNAME -ieq $ComputerName) {
        Write-Host "$ComputerName is already joined to $DomainName."
    }
    elseif ($computer.PartOfDomain) {
        throw "This workstation is already joined to unexpected domain '$($computer.Domain)' or has an unexpected name '$env:COMPUTERNAME'."
    }
    else {
        $deadline = (Get-Date).AddMinutes(15)
        $joined = $false
        $lastJoinError = "The domain controller was not ready."
        while ((Get-Date) -lt $deadline) {
            try {
                $computer = Get-CimInstance Win32_ComputerSystem
                if ($computer.PartOfDomain) {
                    if ($computer.Domain -ieq $DomainName -and $env:COMPUTERNAME -ieq $ComputerName) {
                        $joined = $true
                        break
                    }
                    throw "This workstation joined unexpected domain '$($computer.Domain)' or has unexpected name '$env:COMPUTERNAME'."
                }

                $dcRecord = Resolve-DnsName -Server $DomainControllerAddress `
                    -Name "_ldap._tcp.pdc._msdcs.$DomainName" -Type SRV -ErrorAction Stop |
                    Where-Object { $_.NameTarget } | Select-Object -First 1
                if ($null -eq $dcRecord -or $dcRecord.NameTarget.TrimEnd(".") -ine $DomainControllerHost) {
                    throw "The PDC locator record did not identify $DomainControllerHost."
                }
                if (-not (Test-NetConnection -ComputerName $DomainControllerHost -Port 389 -InformationLevel Quiet)) {
                    throw "Active Directory LDAP on $DomainControllerHost is not reachable yet."
                }

                $joinResult = Add-Computer -DomainName $DomainName -Server $DomainControllerHost `
                    -Credential $credential -Force -PassThru -ErrorAction Stop
                if (-not $joinResult.HasSucceeded) {
                    throw "Add-Computer did not confirm a successful domain join."
                }
                $joined = $true
                break
            }
            catch {
                $lastJoinError = $_.Exception.Message
                if ($computer -and $computer.PartOfDomain) {
                    throw
                }
                Start-Sleep -Seconds 10
            }
        }
        if (-not $joined) {
            throw "The workstation could not join $DomainName within 15 minutes. Last error: $lastJoinError"
        }

        if ($env:COMPUTERNAME -ine $ComputerName) {
            throw "The workstation must be renamed to $ComputerName before joining the domain."
        }
        Write-Host "Joined $ComputerName to $DomainName through $DomainControllerHost."
    }

    Stop-Transcript | Out-Null
}
catch {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
    throw
}
