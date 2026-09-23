param(
    [ValidateSet('win-x64', 'win-arm64')]
    [string]$Runtime = 'win-x64',
    [string]$Configuration = 'Release',
    [string]$AriadneExecutable = '',
    [switch]$FrameworkDependent
)

$ErrorActionPreference = 'Stop'
$project = Join-Path $PSScriptRoot 'Ariadne.Windows.csproj'
$output = Join-Path $PSScriptRoot "bin\publish\$Runtime"
$selfContained = (-not $FrameworkDependent).ToString().ToLowerInvariant()
dotnet publish $project -c $Configuration -r $Runtime --self-contained $selfContained -p:ContinuousIntegrationBuild=true -o $output
if ($LASTEXITCODE -ne 0) {
    throw "dotnet publish failed with exit code $LASTEXITCODE"
}
if ($AriadneExecutable) {
    Copy-Item -LiteralPath $AriadneExecutable -Destination (Join-Path $output 'ariadne.exe') -Force
}
Write-Output $output
