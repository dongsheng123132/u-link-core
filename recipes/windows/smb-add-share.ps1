# needs_admin: true
# Create an SMB share and grant a group/user read access.
#   -Path C:\Share\Tools -Name Tools -Account "DOMAIN\Domain Users"
param(
  [Parameter(Mandatory=$true)][string]$Path,
  [Parameter(Mandatory=$true)][string]$Name,
  [Parameter(Mandatory=$true)][string]$Account
)
$ErrorActionPreference = 'Stop'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Write-Error 'requires administrator'; exit 2
}
if (-not (Test-Path -LiteralPath $Path)) { New-Item -ItemType Directory -Path $Path | Out-Null }
if (Get-SmbShare -Name $Name -ErrorAction SilentlyContinue) { Write-Output "already exists: $Name"; exit 0 }
New-SmbShare -Name $Name -Path $Path -ReadAccess $Account
Get-SmbShare -Name $Name | Select-Object Name, Path, Description | ConvertTo-Json
