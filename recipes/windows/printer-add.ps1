# needs_admin: true
# Add a local/network printer. Example:
#   -Name "HP-2F" -DriverName "HP Universal Printing PS" -PrinterIP 192.168.1.50
param(
  [Parameter(Mandatory=$true)][string]$Name,
  [Parameter(Mandatory=$true)][string]$DriverName,
  [string]$PortName,
  [string]$PrinterIP
)
$ErrorActionPreference = 'Stop'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Write-Error 'requires administrator'; exit 2
}
if (-not $PortName) { $PortName = if ($PrinterIP) { "IP_$PrinterIP" } else { "U-Link_$Name" } }
if ($PrinterIP -and -not (Get-PrinterPort -Name $PortName -ErrorAction SilentlyContinue)) {
  Add-PrinterPort -Name $PortName -PrinterHostAddress $PrinterIP
}
if (-not (Get-PrinterDriver -Name $DriverName -ErrorAction SilentlyContinue)) {
  Write-Error "printer driver not installed: $DriverName (install the vendor INF first)"; exit 3
}
if (Get-Printer -Name $Name -ErrorAction SilentlyContinue) {
  Write-Output "already exists: $Name"; exit 0
}
Add-Printer -Name $Name -DriverName $DriverName -PortName $PortName
Get-Printer -Name $Name | Select-Object Name, DriverName, PortName | ConvertTo-Json
