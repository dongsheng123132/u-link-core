# needs_admin: false
# List third-party drivers and problem devices (read-only).
$ErrorActionPreference = 'Stop'
[pscustomobject]@{
  drivers = pnputil /enum-drivers
  problemDevices = Get-PnpDevice -Status Error -ErrorAction SilentlyContinue |
    Select-Object FriendlyName, InstanceId, Status, Class
} | ConvertTo-Json -Depth 4
