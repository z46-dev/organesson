param([switch]$PrepareOnly)
. (Join-Path $PSScriptRoot "og-prep-windows-common.ps1")
Invoke-OrganessonWindowsPrep -ExpectedRelease "WindowsServer2025" -EntryScriptPath $PSCommandPath -PrepareOnly:$PrepareOnly
