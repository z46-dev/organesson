param(
    [ValidateSet("Auto", "Windows11", "WindowsServer2025")]
    [string]$ExpectedRelease = "Auto",
    [switch]$BootstrapOnly,
    [switch]$PrepareOnly
)

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

function Get-DetectedRelease {
    param([Parameter(Mandatory = $true)][string]$Caption)

    if ($Caption -match "Windows Server 2025") {
        return "WindowsServer2025"
    }
    if ($Caption -match "Windows 11") {
        return "Windows11"
    }

    throw "Unsupported Windows release '$Caption'. Supported releases are Windows 11 and Windows Server 2025."
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

function Find-VirtioSerialDriver {
    param([Parameter(Mandatory = $true)][string]$Release)

    $releaseDirectory = if ($Release -eq "Windows11") { "w11" } else { "2k25" }
    $cdDrives = Get-CimInstance -ClassName Win32_LogicalDisk -Filter "DriveType = 5"
    foreach ($drive in $cdDrives) {
        $driverPath = Join-Path $drive.DeviceID "vioserial\$releaseDirectory\amd64\vioser.inf"
        if (Test-Path -LiteralPath $driverPath -PathType Leaf) {
            return $driverPath
        }
    }

    throw "The VirtIO serial driver for $Release was not found. Attach a VirtIO ISO containing vioserial\$releaseDirectory\amd64\vioser.inf."
}

function Install-VirtioSerialDriver {
    param([Parameter(Mandatory = $true)][string]$Release)

    if ($env:PROCESSOR_ARCHITECTURE -ne "AMD64") {
        throw "The Windows prep script currently supports x64 guests only."
    }

    $driverInf = Find-VirtioSerialDriver -Release $Release
    Write-PrepLog "Installing the $Release VirtIO serial driver from the attached ISO."
    $driverOutput = @(& "$env:SystemRoot\System32\pnputil.exe" /add-driver "$driverInf" /install 2>&1)
    $driverExitCode = $LASTEXITCODE
    foreach ($line in $driverOutput) {
        Write-Host $line
    }
    if ($driverExitCode -eq 259 -and ($driverOutput -join [Environment]::NewLine) -match "(?i)up-to-date on device") {
        Write-PrepLog "The VirtIO serial driver is already installed and current on the device."
        return
    }
    if ($driverExitCode -ne 0) {
        throw "VirtIO serial driver installation failed with pnputil exit code $driverExitCode. If it is 259, verify the VirtIO serial device is present and that pnputil reports it is already up to date."
    }
}

function Get-GuestAgentService {
    return Get-CimInstance -ClassName Win32_Service | Where-Object {
        $_.Name -eq "QEMU-GA" -or $_.DisplayName -like "*QEMU*Guest*Agent*"
    } | Select-Object -First 1
}

function Install-GuestAgent {
    param([switch]$AllowRebootRequired)

    $agentService = Get-GuestAgentService
    if ($null -eq $agentService) {
        $installerPath = Find-GuestAgentInstaller
        $signature = Get-AuthenticodeSignature -FilePath $installerPath
        if ($signature.Status -eq "NotSigned") {
            Write-PrepLog "The QEMU Guest Agent MSI is unsigned. It was found on the attached VirtIO CD; only use an ISO from a trusted VirtIO distribution source."
        } elseif ($signature.Status -ne "Valid") {
            throw "The QEMU Guest Agent installer signature is not valid: $($signature.Status)."
        }

        Write-PrepLog "Installing QEMU Guest Agent from the attached VirtIO ISO."
        $install = Start-Process -FilePath "$env:SystemRoot\System32\msiexec.exe" `
            -ArgumentList @("/i", "`"$installerPath`"", "/qn", "/norestart") `
            -Wait -PassThru
        if ($install.ExitCode -notin @(0, 3010)) {
            throw "QEMU Guest Agent installation failed with exit code $($install.ExitCode)."
        }
        if ($install.ExitCode -eq 3010 -and -not $AllowRebootRequired) {
            throw "QEMU Guest Agent installation requires a reboot. Reboot, then rerun this script."
        }
        if ($install.ExitCode -eq 3010) {
            Write-PrepLog "The MSI recommends a reboot; continuing bootstrap to check whether the agent can run now."
        }

        $agentService = Get-GuestAgentService
    }

    if ($null -eq $agentService) {
        throw "QEMU Guest Agent service was not installed."
    }
    if ($agentService.StartName -notin @("LocalSystem", "NT AUTHORITY\SYSTEM")) {
        throw "QEMU Guest Agent service is not running as LocalSystem ($($agentService.StartName))."
    }

    Set-Service -Name $agentService.Name -StartupType Automatic
    Start-Service -Name $agentService.Name
    if ((Get-Service -Name $agentService.Name).Status -ne "Running") {
        throw "QEMU Guest Agent service did not reach the Running state."
    }
}

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run this script from an elevated PowerShell window."
}

$operatingSystem = Get-CimInstance -ClassName Win32_OperatingSystem
$detectedRelease = Get-DetectedRelease -Caption ([string]$operatingSystem.Caption)
if ($ExpectedRelease -ne "Auto" -and $ExpectedRelease -ne $detectedRelease) {
    throw "Expected $ExpectedRelease but detected '$($operatingSystem.Caption)' ($detectedRelease)."
}
Write-PrepLog "Detected $($operatingSystem.Caption) ($detectedRelease)."

$agentService = Get-GuestAgentService
if ($null -ne $agentService -and $agentService.State -eq "Running") {
    Write-PrepLog "QEMU Guest Agent is already running; the VirtIO serial channel is available. Skipping driver installation."
} else {
    Install-VirtioSerialDriver -Release $detectedRelease
}
Install-GuestAgent -AllowRebootRequired:$BootstrapOnly
if ($BootstrapOnly) {
    Write-PrepLog "QEMU Guest Agent is installed and running as LocalSystem. Enable the guest-agent option in Proxmox, then verify it with qm guest exec <VMID> -- whoami."
    exit 0
}

if (Test-PendingRestart) {
    throw "Windows has a pending restart. Reboot, then run this script again."
}

if (Test-PendingRestart) {
    throw "Windows reports a pending restart. Reboot, then rerun this script before sealing the source."
}

$sysprepPath = Join-Path $env:SystemRoot "System32\Sysprep\Sysprep.exe"
if (-not (Test-Path -LiteralPath $sysprepPath -PathType Leaf)) {
    throw "Sysprep was not found at the expected Windows system path."
}

Write-PrepLog "QEMU Guest Agent is running as LocalSystem; no pending restart was detected."
if ($PrepareOnly) {
    Write-PrepLog "Preparation-only mode completed; leaving the guest running for account removal and final sealing."
    exit 0
}

Write-PrepLog "Removing this prep script and generalizing Windows before shutdown."
$scriptPath = $PSCommandPath
Remove-Item -LiteralPath $scriptPath -Force
$sysprep = Start-Process -FilePath $sysprepPath `
    -ArgumentList @("/generalize", "/oobe", "/shutdown", "/quiet") `
    -Wait -PassThru
if ($sysprep.ExitCode -ne 0) {
    throw "Sysprep failed with exit code $($sysprep.ExitCode). Re-download the prep script, resolve the Sysprep issue, and retry."
}
