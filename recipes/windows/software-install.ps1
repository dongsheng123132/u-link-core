# needs_admin: true
# Install software via winget (silent). Example:
#   -Id 7zip.7zip -Name 7-Zip
param(
  [Parameter(Mandatory=$true)][string]$Id,
  [string]$Name = ''
)
$ErrorActionPreference = 'Stop'
if (-not (Get-Command winget -ErrorAction SilentlyContinue)) { Write-Error 'winget not available'; exit 3 }
winget install --id $Id --exact --silent --accept-package-agreements --accept-source-agreements
if ($LASTEXITCODE -ne 0) { Write-Error "winget failed: $LASTEXITCODE"; exit $LASTEXITCODE }
winget list --id $Id --exact | ConvertTo-Json -Depth 3
