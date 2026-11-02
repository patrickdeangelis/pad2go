# Build the WinUHid driver and its MSI from the pinned upstream source, for the
# Windows release job (see .github/workflows/release.yml). The MSI is what the
# Pad2Go installer runs when "pad2go check winuhid" fails: it installs the
# WinUHid driver package and creates its root device.
#
# Needs MSBuild (microsoft/setup-msbuild) and the Windows Driver Kit (the
# windowsdriverkit11 package). Builds only the WiX package project, which pulls
# in the driver and the RootDevCA custom action, not the test or device
# projects.
#
#   pwsh packaging/windows/build-winuhid.ps1 -Source winuhid-src -Out winuhid.msi
param(
  [Parameter(Mandatory = $true)] [string] $Source,
  [Parameter(Mandatory = $true)] [string] $Out
)

$ErrorActionPreference = 'Stop'

$project = Join-Path $Source 'Installer\WinUHid Package\WinUHid Package.wixproj'
if (-not (Test-Path $project)) {
  throw "WinUHid package project not found: $project (is -Source the WinUHid checkout?)"
}

$msbuild = Get-Command msbuild -ErrorAction SilentlyContinue
if (-not $msbuild) {
  throw 'msbuild not on PATH; run microsoft/setup-msbuild first'
}

# /restore pulls the WiX SDK and the packages.config dependencies of RootDevCA.
& $msbuild.Source $project /restore /t:Build /p:Configuration=Release /p:Platform=x64 /nologo /v:minimal
if ($LASTEXITCODE -ne 0) {
  throw "msbuild failed with exit code $LASTEXITCODE"
}

$msis = @(Get-ChildItem -Path (Join-Path $Source 'Installer') -Recurse -Filter '*.msi' -File)
if ($msis.Count -ne 1) {
  throw "expected exactly one WinUHid MSI under Installer\, found $($msis.Count)"
}

$dir = Split-Path -Parent $Out
if ($dir) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
Copy-Item -Path $msis[0].FullName -Destination $Out -Force
Write-Host "built $Out from $($msis[0].Name)"
