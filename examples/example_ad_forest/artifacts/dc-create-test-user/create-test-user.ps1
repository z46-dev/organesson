$ErrorActionPreference = "Stop"
$DomainName = "adforest.test"
$NetBIOSName = "ADFOREST"
$UserName = "labuser"
$UserPrincipalName = "labuser@$DomainName"
$ExamplePassword = ConvertTo-SecureString "password123!" -AsPlainText -Force
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "create-test-user.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
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
        throw "Active Directory Web Services did not become ready within 8 minutes after promotion."
    }

    $user = $null
    try {
        $user = Get-ADUser -Server localhost -Identity $UserName -ErrorAction Stop
    }
    catch {
        $user = $null
    }
    if ($null -eq $user) {
        New-ADUser `
            -Name "Organesson Lab User" `
            -DisplayName "Organesson Lab User" `
            -SamAccountName $UserName `
            -UserPrincipalName $UserPrincipalName `
            -Path $domain.UsersContainer `
            -AccountPassword $ExamplePassword `
            -Enabled $true `
            -PasswordNeverExpires $true `
            -Server localhost
    }
    else {
        Set-ADAccountPassword -Server localhost -Identity $user -Reset -NewPassword $ExamplePassword
        Enable-ADAccount -Server localhost -Identity $user
        Set-ADUser -Server localhost -Identity $user -PasswordNeverExpires $true
    }

    $user = Get-ADUser -Server localhost -Identity $UserName -Properties Enabled, PasswordNeverExpires
    if (-not $user.Enabled -or -not $user.PasswordNeverExpires) {
        throw "The lab test account did not reach the expected enabled, fixed-password state."
    }
    Write-Host "Created and verified $NetBIOSName\$UserName for the lab."
}
finally {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
}
