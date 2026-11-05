# needs_admin: false
# List printers, ports and queued jobs. Safe read-only diagnostic.
$ErrorActionPreference = 'Stop'
$printers = Get-Printer | Select-Object Name, DriverName, PortName, Shared, Published
$ports = Get-PrinterPort | Select-Object Name, PrinterHostAddress, PortNumber
$jobs = Get-PrintJob -ErrorAction SilentlyContinue | Select-Object PrinterName, Id, DocumentName, JobStatus
[pscustomobject]@{ printers = $printers; ports = $ports; jobs = $jobs } | ConvertTo-Json -Depth 5
