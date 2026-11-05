# needs_admin: false
# needs_screen: false (captures the current interactive desktop once)
# Read-only visual context for cases where the employee cannot describe the error.
# Saves a PNG under %TEMP% and prints its path; pull it with `ulink pull`.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$bounds = [System.Windows.Forms.SystemInformation]::VirtualScreen
$bmp = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
$out = Join-Path $env:TEMP ("ulink-screen-" + (Get-Date -Format 'yyyyMMdd-HHmmss') + ".png")
$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose()
Write-Output $out
