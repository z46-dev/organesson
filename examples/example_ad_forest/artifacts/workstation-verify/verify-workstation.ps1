$ErrorActionPreference = "Stop"
$DomainName = "adforest.test"
$DomainControllerHost = "og-ad-dc01.adforest.test"
$DomainUser = "labuser"
$DomainAdministrator = "Administrator"
$ExamplePassword = "password123!"
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "verify-workstation.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    $computer = Get-CimInstance -ClassName Win32_ComputerSystem
    if (-not $computer.PartOfDomain -or $computer.Domain -ine $DomainName) {
        throw "The workstation is not joined to the expected domain '$DomainName'."
    }
    Write-Host "Workstation domain membership confirmed: $($computer.Domain)."

    if (-not (Test-ComputerSecureChannel -Server $DomainControllerHost -ErrorAction Stop)) {
        throw "The workstation's secure channel to $DomainControllerHost is unhealthy."
    }
    Write-Host "Workstation secure channel confirmed."

    Add-Type -AssemblyName System.DirectoryServices.AccountManagement
    $context = [System.DirectoryServices.AccountManagement.PrincipalContext]::new(
        [System.DirectoryServices.AccountManagement.ContextType]::Domain,
        $DomainControllerHost
    )
    if (-not $context.ValidateCredentials("$DomainUser@$DomainName", $ExamplePassword)) {
        throw "AD authentication failed for $DomainUser@$DomainName."
    }
    if (-not $context.ValidateCredentials("$DomainAdministrator@$DomainName", $ExamplePassword)) {
        throw "Domain Administrator authentication failed on the workstation."
    }
    $domainUserPrincipal = [System.DirectoryServices.AccountManagement.UserPrincipal]::FindByIdentity($context, $DomainUser)
    if ($null -eq $domainUserPrincipal) {
        throw "The standard domain user could not be resolved."
    }
    $domainUserGroups = @($domainUserPrincipal.GetGroups() | ForEach-Object { $_.Name })
    if ($domainUserGroups -contains "Domain Admins" -or $domainUserGroups -contains "Administrators") {
        throw "labuser must remain a standard, non-administrative domain account."
    }
    $domainUserPrincipal.Dispose()
    $context.Dispose()
    Write-Host "Active Directory credentials confirmed for the domain administrator and standard user."

    $localAdministrator = Get-LocalUser -Name "Administrator" -ErrorAction SilentlyContinue
    if ($null -eq $localAdministrator -or -not $localAdministrator.Enabled) {
        throw "The enabled built-in local Administrator account is missing."
    }
    $administrators = Get-LocalGroup -SID "S-1-5-32-544"
    $administratorSID = $localAdministrator.SID.Value
    if (-not (Get-LocalGroupMember -Group $administrators.Name | Where-Object { $_.SID.Value -eq $administratorSID })) {
        throw "The local Administrator account is not a member of the local Administrators group."
    }
    $machineContext = [System.DirectoryServices.AccountManagement.PrincipalContext]::new(
        [System.DirectoryServices.AccountManagement.ContextType]::Machine,
        $env:COMPUTERNAME
    )
    if (-not $machineContext.ValidateCredentials("Administrator", $ExamplePassword)) {
        throw "Local Administrator authentication failed on the workstation."
    }
    $machineContext.Dispose()
    Write-Host "Built-in local Administrator is enabled, privileged, and authenticates on the workstation."
}
catch {
    Write-Error $_
    throw
}
finally {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
}
