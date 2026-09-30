Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

function Write-PrepLog {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Host "[organesson-template-prep] $Message"
}

function Test-PendingRestart {
    $pendingKeys = @(
        "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending",
        "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired"
    )

    foreach ($key in $pendingKeys) {
        if (Test-Path $key) {
            return $true
        }
    }

    $sessionManager = Get-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager" -Name PendingFileRenameOperations -ErrorAction SilentlyContinue
    return $null -ne $sessionManager
}

function Find-GuestAgentInstaller {
    $cdDrives = Get-CimInstance -ClassName Win32_LogicalDisk -Filter "DriveType = 5"
    foreach ($drive in $cdDrives) {
        $installerPath = Join-Path $drive.DeviceID "guest-agent\qemu-ga-x86_64.msi"
        if (Test-Path -LiteralPath $installerPath -PathType Leaf) {
            return $installerPath
        }
    }

    throw "Attach the VirtIO Windows ISO and ensure it contains guest-agent\qemu-ga-x86_64.msi."
}

function Install-GuestAgent {
    $installerPath = Find-GuestAgentInstaller
    $signature = Get-AuthenticodeSignature -FilePath $installerPath
    if ($signature.Status -ne "Valid") {
        throw "The QEMU Guest Agent installer signature is not valid: $($signature.Status)."
    }

    Write-PrepLog "Installing QEMU Guest Agent from the attached VirtIO ISO."
    $install = Start-Process -FilePath "$env:SystemRoot\System32\msiexec.exe" `
        -ArgumentList @("/i", "`"$installerPath`"", "/qn", "/norestart") `
        -Wait -PassThru
    if ($install.ExitCode -notin @(0, 3010)) {
        throw "QEMU Guest Agent installation failed with exit code $($install.ExitCode)."
    }

    if ($install.ExitCode -eq 3010) {
        throw "QEMU Guest Agent installation requires a reboot. Reboot, then run this script again before sealing the source."
    }

    $agentService = Get-CimInstance -ClassName Win32_Service | Where-Object {
        $_.Name -eq "QEMU-GA" -or $_.DisplayName -like "*QEMU*Guest*Agent*"
    } | Select-Object -First 1
    if ($null -eq $agentService) {
        throw "QEMU Guest Agent service was not installed."
    }
    if ($agentService.StartName -notin @("LocalSystem", "NT AUTHORITY\SYSTEM")) {
        throw "QEMU Guest Agent service is not running as LocalSystem ($($agentService.StartName))."
    }

    Set-Service -Name $agentService.Name -StartupType Automatic
    Start-Service -Name $agentService.Name
    $agentService = Get-Service -Name $agentService.Name
    if ($agentService.Status -ne "Running") {
        throw "QEMU Guest Agent service did not reach the Running state."
    }
}

function Install-WindowsUpdates {
    Write-PrepLog "Searching for Windows software updates."
    $session = New-Object -ComObject Microsoft.Update.Session
    $searcher = $session.CreateUpdateSearcher()
    $searchResult = $searcher.Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
    $updates = New-Object -ComObject Microsoft.Update.UpdateColl

    foreach ($update in $searchResult.Updates) {
        if (-not $update.EulaAccepted) {
            $update.AcceptEula()
        }
        [void]$updates.Add($update)
    }

    if ($updates.Count -eq 0) {
        Write-PrepLog "No Windows software updates are available."
        return $false
    }

    $downloader = $session.CreateUpdateDownloader()
    $downloader.Updates = $updates
    $downloadResult = $downloader.Download()
    if ($downloadResult.ResultCode -ne 2) {
        throw "Windows Update download did not succeed (result code $($downloadResult.ResultCode))."
    }

    $installer = $session.CreateUpdateInstaller()
    $installer.Updates = $updates
    $installResult = $installer.Install()
    if ($installResult.ResultCode -ne 2) {
        throw "Windows Update installation did not succeed (result code $($installResult.ResultCode))."
    }

    return [bool]$installResult.RebootRequired
}

function Invoke-OrganessonWindowsPrep {
    param(
        [Parameter(Mandatory = $true)]
        [ValidateSet("Windows11", "WindowsServer2025")]
        [string]$ExpectedRelease,

        [Parameter(Mandatory = $true)]
        [string]$EntryScriptPath
    )

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "Run this script from an elevated PowerShell window."
    }

    $operatingSystem = Get-CimInstance -ClassName Win32_OperatingSystem
    $caption = [string]$operatingSystem.Caption
    if ($ExpectedRelease -eq "Windows11" -and $caption -notmatch "Windows 11") {
        throw "This script supports Windows 11 workstation only; detected '$caption'."
    }
    if ($ExpectedRelease -eq "WindowsServer2025" -and $caption -notmatch "Windows Server 2025") {
        throw "This script supports Windows Server 2025 only; detected '$caption'."
    }

    if (Test-PendingRestart) {
        throw "Windows has a pending restart. Reboot, then run this script again before sealing the source."
    }

    Install-GuestAgent
    if (Install-WindowsUpdates) {
        throw "Windows updates require a reboot. Reboot, then run this script again; do not clone this source yet."
    }
    if (Test-PendingRestart) {
        throw "Windows reports a pending restart. Reboot, then run this script again before sealing the source."
    }

    $sysprepPath = Join-Path $env:SystemRoot "System32\Sysprep\Sysprep.exe"
    if (-not (Test-Path -LiteralPath $sysprepPath -PathType Leaf)) {
        throw "Sysprep was not found at the expected Windows system path."
    }

    Write-PrepLog "QEMU Guest Agent is running as LocalSystem; updates are installed."
    Write-PrepLog "Removing the downloaded prep files and generalizing Windows before shutdown."
    $commonScriptPath = Join-Path $PSScriptRoot "og-prep-windows-common.ps1"
    Remove-Item -LiteralPath $EntryScriptPath -Force
    Remove-Item -LiteralPath $commonScriptPath -Force

    $sysprep = Start-Process -FilePath $sysprepPath `
        -ArgumentList @("/generalize", "/oobe", "/shutdown", "/quiet") `
        -Wait -PassThru
    if ($sysprep.ExitCode -ne 0) {
        throw "Sysprep failed with exit code $($sysprep.ExitCode). Re-download the prep files, resolve the Sysprep issue, and retry."
    }
}
