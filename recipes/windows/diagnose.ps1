# needs_admin: false
# Read-only health snapshot for an employee Windows PC.
$ErrorActionPreference = 'Stop'
[pscustomobject]@{
  computer = $env:COMPUTERNAME
  os = Get-CimInstance Win32_OperatingSystem | Select-Object Caption, Version, LastBootUpTime
  disk = Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3" | Select-Object DeviceID, Size, FreeSpace
  services = Get-Service | Where-Object Status -ne 'Running' | Select-Object Name, DisplayName, Status
  recentErrors = Get-WinEvent -FilterHashtable @{LogName='System'; Level=2; StartTime=(Get-Date).AddDays(-2)} -MaxEvents 20 -ErrorAction SilentlyContinue |
    Select-Object TimeCreated, Id, ProviderName, Message
  printers = Get-Printer | Select-Object Name, PortName, PrinterStatus
  shares = Get-SmbShare | Select-Object Name, Path
  ip = Get-NetIPConfiguration | Select-Object InterfaceAlias, IPv4Address, IPv4DefaultGateway
} | ConvertTo-Json -Depth 6
