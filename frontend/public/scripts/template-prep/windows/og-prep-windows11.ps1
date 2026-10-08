param([switch]$PrepareOnly)
. (Join-Path $PSScriptRoot "og-prep-windows-common.ps1")
Invoke-OrganessonWindowsPrep -ExpectedRelease "Windows11" -EntryScriptPath $PSCommandPath -PrepareOnly:$PrepareOnly
