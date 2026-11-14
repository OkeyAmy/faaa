# faaa installer for Windows — irm https://raw.githubusercontent.com/OkeyAmy/faaa/main/install.ps1 | iex
# FAAA_VERSION=0.0.6 pins a release instead of "latest".
$ErrorActionPreference = 'Stop'
# Invoke-WebRequest is glacial on PS 5.1 unless the progress bar is off.
$ProgressPreference = 'SilentlyContinue'

# ARM64 Windows under x64 PowerShell reports PROCESSOR_ARCHITECTURE=AMD64 and
# puts the truth in PROCESSOR_ARCHITEW6432, so check that one first.
$arch = if ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64' -or $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

if ($env:FAAA_VERSION -and $env:FAAA_VERSION -ne 'latest') {
  $tag = 'v' + $env:FAAA_VERSION.TrimStart('v')
  $base = "https://github.com/OkeyAmy/faaa/releases/download/$tag"
} else {
  $tag = 'latest'
  $base = 'https://github.com/OkeyAmy/faaa/releases/latest/download'
}

$dir = Join-Path $env:LOCALAPPDATA 'faaa'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$asset = "faaa_windows_$arch.zip"
# One private directory per run: two installers racing each other (or a stale
# half-download in TEMP) can no longer collide on a fixed filename.
$work = Join-Path $env:TEMP ("faaa-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$zip = Join-Path $work $asset
$sums = Join-Path $work 'checksums.txt'
try {
  # -UseBasicParsing: Windows Server Core has no IE engine, so the default
  # parser fails there with "The response content cannot be parsed because the
  # Internet Explorer engine is not available". windows-latest never catches it.
  Invoke-WebRequest "$base/$asset" -OutFile $zip -UseBasicParsing
  Invoke-WebRequest "$base/checksums.txt" -OutFile $sums -UseBasicParsing

  # Look up this exact asset's line and compare the hashes by hand; there is no
  # portable `sha256sum -c` on Windows PowerShell.
  $want = (Get-Content $sums |
    Where-Object { $_ -match "[ \t]$([regex]::Escape($asset))$" } |
    ForEach-Object { ($_ -split '\s+')[0] } |
    Select-Object -First 1)
  if (-not $want) {
    throw "faaa: $asset is not listed in checksums.txt for $tag"
  }
  $got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash
  if ($got.ToLower() -ne $want.ToLower()) {
    throw "faaa: checksum mismatch for $asset (expected $want, got $got); refusing to install"
  }

  # Unpack beside the download, never over the working install: a broken build
  # must not be able to clobber a binary that currently works.
  $unpacked = Join-Path $work 'unpack'
  Expand-Archive $zip -DestinationPath $unpacked -Force
  $staged = Join-Path $unpacked 'faaa.exe'

  # Prove the staged copy runs before it is allowed anywhere near $dir.
  $LASTEXITCODE = 0
  $smoke = & $staged version 2>&1
  if ($LASTEXITCODE -ne 0) {
    throw "faaa: the downloaded binary does not run ($staged version exited $LASTEXITCODE): $smoke"
  }

  # Atomic-ish swap; if faaa.exe is running, this move fails and the existing
  # binary is left untouched. -Force replaces the old file.
  Move-Item -Force -Path $staged -Destination (Join-Path $dir 'faaa.exe')
} finally {
  Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

$exe = Join-Path $dir 'faaa.exe'

$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($path -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$path;$dir", 'User')
  $env:Path += ";$dir"
}
& $exe install
Write-Host 'run: faaa   (open a new terminal if the command is not found)'
