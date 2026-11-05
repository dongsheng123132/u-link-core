# needs_admin: true
# Install a vendor INF driver package.
#   -InfPath C:\Temp\vendor\driver.inf
param([Parameter(Mandatory=$true)][string]$InfPath)
$ErrorActionPreference = 'Stop'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Write-Error 'requires administrator'; exit 2
}
if (-not (Test-Path -LiteralPath $InfPath)) { Write-Error "INF not found: $InfPath"; exit 3 }
pnputil /add-driver $InfPath /install
if ($LASTEXITCODE -ne 0) { Write-Error "pnputil failed: $LASTEXITCODE"; exit $LASTEXITCODE }
Get-PnpDevice -Status Error -ErrorAction SilentlyContinue | Select-Object FriendlyName, Status | ConvertTo-Json
