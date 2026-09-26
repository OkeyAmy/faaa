# faaa installer for Windows — irm https://raw.githubusercontent.com/OkeyAmy/faaa/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir = Join-Path $env:LOCALAPPDATA 'faaa'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$zip = Join-Path $env:TEMP 'faaa.zip'
Invoke-WebRequest "https://github.com/OkeyAmy/faaa/releases/latest/download/faaa_windows_$arch.zip" -OutFile $zip
Expand-Archive $zip -DestinationPath $dir -Force
Remove-Item $zip
$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($path -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$path;$dir", 'User')
  $env:Path += ";$dir"
}
& (Join-Path $dir 'faaa.exe') install
Write-Host 'run: faaa   (open a new terminal if the command is not found)'
