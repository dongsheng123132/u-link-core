# needs_admin: true
# Clear a stuck queue and restart the spooler (common printer fix).
$ErrorActionPreference = 'Stop'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Write-Error 'requires administrator'; exit 2
}
Get-PrintJob -ErrorAction SilentlyContinue | Remove-PrintJob -ErrorAction SilentlyContinue
Restart-Service Spooler -Force
Start-Sleep -Seconds 2
Get-Service Spooler | Select-Object Name, Status | ConvertTo-Json
