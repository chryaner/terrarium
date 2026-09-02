# Stages the npm package for a released terrarium version: downloads that
# release's Windows zip, verifies it against the release's checksums.txt, and
# copies the exe into dist/. The exe is never committed - it comes from the
# GitHub release so npm, scoop and the release page all ship the same bytes.
#
#   .\prepare.ps1 -Version 0.1.1
#   npm publish
param([Parameter(Mandatory = $true)][string]$Version)
$ErrorActionPreference = 'Stop'

$here = $PSScriptRoot
$tmp = Join-Path $env:TEMP "terrarium-npm-$Version"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

$zipName = "terrarium_${Version}_windows_amd64.zip"
gh release download "v$Version" --repo chryaner/terrarium --pattern $zipName --pattern checksums.txt --dir $tmp --clobber
if ($LASTEXITCODE -ne 0) { throw "gh release download failed for v$Version" }

$zip = Join-Path $tmp $zipName
$want = ((Select-String -Path (Join-Path $tmp 'checksums.txt') -Pattern 'windows_amd64\.zip').Line -split '\s+')[0].ToLower()
$got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLower()
if ($got -ne $want) { throw "checksum mismatch for ${zipName}: want $want got $got" }

Expand-Archive -Path $zip -DestinationPath $tmp -Force
New-Item -ItemType Directory -Force -Path (Join-Path $here 'dist') | Out-Null
Copy-Item -Path (Join-Path $tmp 'terrarium.exe') -Destination (Join-Path $here 'dist\terrarium.exe') -Force

Push-Location $here
try { npm version $Version --no-git-tag-version --allow-same-version | Out-Null } finally { Pop-Location }
Write-Output "terrarium-mcp $Version staged: dist\terrarium.exe from release v$Version, checksum ok"
