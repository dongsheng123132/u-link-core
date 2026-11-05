# needs_admin: false
# List SMB shares, sessions and mapped drives.
$ErrorActionPreference = 'Stop'
[pscustomobject]@{
  shares   = Get-SmbShare | Select-Object Name, Path, Description, Special
  sessions = Get-SmbSession -ErrorAction SilentlyContinue | Select-Object ClientComputerName, ClientUserName
  mappings = Get-SmbMapping -ErrorAction SilentlyContinue | Select-Object LocalPath, RemotePath, Status
} | ConvertTo-Json -Depth 5
