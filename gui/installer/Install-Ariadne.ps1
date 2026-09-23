[CmdletBinding()]
param(
    [string]$InstallDirectory = (Join-Path $env:LOCALAPPDATA 'Programs\Ariadne'),
    [switch]$NoPath,
    [switch]$NoShortcut
)

$ErrorActionPreference = 'Stop'
$InstallDirectory = [System.IO.Path]::GetFullPath($InstallDirectory)
$expectedRoot = [System.IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Programs'))
if (-not $InstallDirectory.StartsWith($expectedRoot, [StringComparison]::OrdinalIgnoreCase) -or $InstallDirectory -eq $expectedRoot) {
    throw "InstallDirectory must be a child of $expectedRoot"
}
$source = Split-Path -Parent $PSScriptRoot
$guiName = if (Test-Path -LiteralPath (Join-Path $source 'Ariadne.WinUI.exe')) { 'Ariadne.WinUI.exe' } else { 'Ariadne.Windows.exe' }
$gui = Join-Path $source $guiName
$core = Join-Path $source 'ariadne.exe'
if (-not (Test-Path -LiteralPath $gui) -or -not (Test-Path -LiteralPath $core)) {
    throw 'Ariadne.WinUI.exe (or legacy Ariadne.Windows.exe) and ariadne.exe must be beside the installer directory.'
}

New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
Get-ChildItem -LiteralPath $source -Force | Where-Object { $_.Name -ne 'installer' } |
    Copy-Item -Destination $InstallDirectory -Recurse -Force
Copy-Item -LiteralPath $PSScriptRoot -Destination (Join-Path $InstallDirectory 'installer') -Recurse -Force

if (-not $NoPath) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($current -split ';' | Where-Object { $_ -and $_ -ne $InstallDirectory })
    [Environment]::SetEnvironmentVariable('Path', (($entries + $InstallDirectory) -join ';'), 'User')
}

if (-not $NoShortcut) {
    $shortcutPath = Join-Path ([Environment]::GetFolderPath('Programs')) 'Ariadne.lnk'
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = Join-Path $InstallDirectory $guiName
    $shortcut.WorkingDirectory = $InstallDirectory
    $shortcut.Save()
}

$uninstall = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Ariadne'
New-Item -Path $uninstall -Force | Out-Null
New-ItemProperty -Path $uninstall -Name DisplayName -Value 'Ariadne' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $uninstall -Name Publisher -Value 'ahaha-craft.org' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $uninstall -Name InstallLocation -Value $InstallDirectory -PropertyType String -Force | Out-Null
New-ItemProperty -Path $uninstall -Name DisplayIcon -Value (Join-Path $InstallDirectory $guiName) -PropertyType String -Force | Out-Null
$uninstallCommand = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$(Join-Path $InstallDirectory 'installer\Uninstall-Ariadne.ps1')`""
New-ItemProperty -Path $uninstall -Name UninstallString -Value $uninstallCommand -PropertyType String -Force | Out-Null
New-ItemProperty -Path $uninstall -Name NoModify -Value 1 -PropertyType DWord -Force | Out-Null
New-ItemProperty -Path $uninstall -Name NoRepair -Value 1 -PropertyType DWord -Force | Out-Null

Write-Output "Ariadne installed to $InstallDirectory"
