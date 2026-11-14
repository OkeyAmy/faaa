# faaa uninstaller for Windows — irm https://raw.githubusercontent.com/OkeyAmy/faaa/main/uninstall.ps1 | iex
# Removes the install directory and the user PATH entry install.ps1 added.
# It deliberately does NOT touch git config: run `faaa uninstall` first if you
# also want faaa disarmed from your repos.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$dir = Join-Path $env:LOCALAPPDATA 'faaa'
$exe = Join-Path $dir 'faaa.exe'

if (Test-Path $exe) {
  Write-Host 'note: git config is left untouched. If you want faaa disarmed from git,'
  Write-Host '      run `faaa uninstall` before removing the binary.'
}

# Drop the user PATH entry, keeping the rest of the value byte-for-byte.
$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($path) {
  $parts = @($path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $dir.TrimEnd('\') })
  $new = $parts -join ';'
  if ($new -ne $path) {
    [Environment]::SetEnvironmentVariable('Path', $new, 'User')
    Write-Host "removed $dir from your user PATH"
  }
} else {
  Write-Host 'no user PATH entry to clean'
}
$env:Path = (@($env:Path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $dir.TrimEnd('\') }) -join ';')

if (Test-Path $dir) {
  try {
    Remove-Item -Recurse -Force $dir
    Write-Host "removed $dir"
  } catch {
    throw "faaa: could not remove $dir — close any running faaa process and try again. ($($_.Exception.Message))"
  }
} else {
  Write-Host "nothing to remove at $dir"
}
