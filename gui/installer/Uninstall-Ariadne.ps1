[CmdletBinding()]
param([string]$InstallDirectory = (Split-Path -Parent $PSScriptRoot))

$ErrorActionPreference = 'Stop'
$resolved = [System.IO.Path]::GetFullPath($InstallDirectory)
$expectedRoot = [System.IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Programs'))
if (-not $resolved.StartsWith($expectedRoot, [StringComparison]::OrdinalIgnoreCase) -or $resolved -eq $expectedRoot) {
    throw "Refusing to uninstall unexpected directory: $resolved"
}

$current = [Environment]::GetEnvironmentVariable('Path', 'User')
$entries = @($current -split ';' | Where-Object { $_ -and $_ -ne $resolved })
[Environment]::SetEnvironmentVariable('Path', ($entries -join ';'), 'User')
$shortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'Ariadne.lnk'
if (Test-Path -LiteralPath $shortcut) { Remove-Item -LiteralPath $shortcut -Force }
Remove-Item -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Ariadne' -Recurse -Force -ErrorAction SilentlyContinue

# Run deletion after this script exits. User configuration and daemon state are
# intentionally outside InstallDirectory and are retained.
$escaped = $resolved.Replace("'", "''")
Start-Process powershell.exe -WindowStyle Hidden -ArgumentList '-NoProfile', '-Command', "Start-Sleep -Milliseconds 500; Remove-Item -LiteralPath '$escaped' -Recurse -Force"
Write-Output 'Ariadne uninstalled; configuration and state were retained.'
