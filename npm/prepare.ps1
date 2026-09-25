# Stages the npm package for a released terrarium version: downloads that
# release's Windows and Linux archives, verifies them against the release's
# checksums.txt, and copies the binaries into dist/. The binaries are never
# committed - they come from the GitHub release so npm, scoop and the release
# page all ship the same bytes.
#
#   .\prepare.ps1 -Version 0.1.1
#   npm publish
param([Parameter(Mandatory = $true)][string]$Version)
$ErrorActionPreference = 'Stop'

$here = $PSScriptRoot
$tmp = Join-Path $env:TEMP "terrarium-npm-$Version"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
$dist = Join-Path $here 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null

$targets = @(
    @{ Archive = "terrarium_${Version}_windows_amd64.zip"; Binary = 'terrarium.exe'; Staged = 'terrarium.exe' },
    @{ Archive = "terrarium_${Version}_linux_amd64.tar.gz"; Binary = 'terrarium'; Staged = 'terrarium-linux-x64' }
)

$patterns = @('--pattern', 'checksums.txt')
foreach ($t in $targets) { $patterns += @('--pattern', $t.Archive) }
gh release download "v$Version" --repo chryaner/terrarium @patterns --dir $tmp --clobber
if ($LASTEXITCODE -ne 0) { throw "gh release download failed for v$Version" }

$sums = @{}
foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
    $hash, $file = $line -split '\s+', 2
    $sums[$file.Trim()] = $hash.ToLower()
}

foreach ($t in $targets) {
    $archive = Join-Path $tmp $t.Archive
    $want = $sums[$t.Archive]
    if (-not $want) { throw "checksums.txt has no entry for $($t.Archive)" }
    $got = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want) { throw "checksum mismatch for $($t.Archive): want $want got $got" }

    $out = Join-Path $tmp ([IO.Path]::GetFileNameWithoutExtension($t.Staged) + '-extract')
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    # tar.exe ships with Windows 10 and later and reads both formats.
    tar -xf $archive -C $out
    if ($LASTEXITCODE -ne 0) { throw "extracting $($t.Archive) failed" }
    Copy-Item -Path (Join-Path $out $t.Binary) -Destination (Join-Path $dist $t.Staged) -Force
}

Push-Location $here
try { npm version $Version --no-git-tag-version --allow-same-version | Out-Null } finally { Pop-Location }
Write-Output "terrarium-mcp $Version staged: $(($targets | ForEach-Object { $_.Staged }) -join ', ') from release v$Version, checksums ok"
