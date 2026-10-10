$ErrorActionPreference = "Stop"
$LocalUserName = "ogadmin"
$ExamplePassword = ConvertTo-SecureString "password123!" -AsPlainText -Force
$LogDirectory = "C:\ProgramData\Organesson\example-ad-forest"
$LogPath = Join-Path $LogDirectory "prepare-windows.log"

New-Item -ItemType Directory -Force -Path $LogDirectory | Out-Null
Start-Transcript -Path $LogPath -Append | Out-Null

try {
    $setupState = Get-ItemProperty -LiteralPath "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State" -Name ImageState -ErrorAction Stop
    $oobeState = Get-ItemProperty -LiteralPath "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE" -Name SkipMachineOOBE -ErrorAction SilentlyContinue
    $firstBootAdmin = Get-LocalUser -Name $LocalUserName -ErrorAction SilentlyContinue
    $oobeProcess = Get-Process -Name "msoobe" -ErrorAction SilentlyContinue
    if ($setupState.ImageState -ne "IMAGE_STATE_COMPLETE" -or [int]$oobeState.SkipMachineOOBE -ne 1 -or $null -eq $firstBootAdmin -or $null -ne $oobeProcess) {
        throw "Windows unattended first boot is not complete; refusing to change accounts or remove the answer file (image=$($setupState.ImageState); skip=$($oobeState.SkipMachineOOBE); admin=$($null -ne $firstBootAdmin); msoobe=$($null -ne $oobeProcess))."
    }

    $localUser = Get-LocalUser -Name $LocalUserName -ErrorAction SilentlyContinue
    if ($null -eq $localUser) {
        $localUser = New-LocalUser -Name $LocalUserName -FullName "Organesson Local Administrator" -Password $ExamplePassword -PasswordNeverExpires
    }
    Set-LocalUser -Name $LocalUserName -Password $ExamplePassword -PasswordNeverExpires $true
    Enable-LocalUser -Name $LocalUserName
    $administrators = Get-LocalGroup -SID "S-1-5-32-544"
    Add-LocalGroupMember -Group $administrators.Name -Member $LocalUserName -ErrorAction SilentlyContinue

    $oobeRegistryPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE"
    Set-ItemProperty -LiteralPath $oobeRegistryPath -Name BypassNRO -Type DWord -Value 1
    Set-ItemProperty -LiteralPath $oobeRegistryPath -Name SkipMachineOOBE -Type DWord -Value 1

    $winlogonPath = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon"
    foreach ($setting in @("AutoAdminLogon", "AutoLogonCount", "DefaultUserName", "DefaultDomainName", "DefaultPassword")) {
        Remove-ItemProperty -LiteralPath $winlogonPath -Name $setting -ErrorAction SilentlyContinue
    }

    $preparationUser = Get-LocalUser -Name "ogprep" -ErrorAction SilentlyContinue
    if ($null -ne $preparationUser) {
        $preparationProfile = Get-CimInstance Win32_UserProfile | Where-Object { $_.SID -eq $preparationUser.SID.Value }
        if ($preparationProfile | Where-Object { $_.Loaded }) {
            throw "The temporary Organesson preparation account is still signed in; refusing to remove its profile."
        }
        Remove-LocalUser -Name "ogprep"
        $preparationProfile | Remove-CimInstance -ErrorAction Stop
        if (Get-LocalUser -Name "ogprep" -ErrorAction SilentlyContinue) {
            throw "The temporary Organesson preparation account still exists after cleanup."
        }
    }

    foreach ($answerPath in @(
        "C:\Windows\Panther\unattend.xml",
        "C:\Windows\Panther\Unattend\Unattend.xml",
        "C:\Windows\Panther\UnattendGC\Unattend.xml",
        "C:\Windows\System32\Sysprep\unattend.xml"
    )) {
        Remove-Item -LiteralPath $answerPath -Force -ErrorAction SilentlyContinue
    }

    Write-Host "Windows completed unattended OOBE; created and verified local administrator $LocalUserName and removed the temporary setup answer file."
    Stop-Transcript | Out-Null
    Write-Host "Windows restart is managed by the Organesson workflow."
}
catch {
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
    throw
}
