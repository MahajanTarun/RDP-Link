# Windows build script (run on Windows with Python 3.12 + PyInstaller)
param([switch]$NoOpen)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$AppDir = Join-Path $Root "app"
$Dist = Join-Path $Root "dist\windows"
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

Push-Location $AppDir
python -m pip install --upgrade pyinstaller
python -m PyInstaller --noconfirm --clean --onefile --name rdp-link --add-data "receiver;receiver" --add-data "office_viewer.py;." ubuntu_rdp_manager.py
Pop-Location

Copy-Item (Join-Path $AppDir "dist\rdp-link.exe") (Join-Path $Dist "rdp-link.exe") -Force

# Cross-build Go binaries for Windows
$OfficeBin = Join-Path $Dist "office-rdp.exe"
$RendezvousBin = Join-Path $Dist "office-rdp-rendezvous.exe"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
Push-Location $Root
go build -o $OfficeBin ./cmd/office-rdp
go build -o $RendezvousBin ./cmd/rendezvous
Pop-Location

Write-Host "Built Go binaries:" $OfficeBin, $RendezvousBin

# Build NSIS installer if makensis is available
$NSIS = Get-Command makensis -ErrorAction SilentlyContinue
if ($NSIS) {
    Push-Location (Join-Path $Root "packaging\windows")
    makensis installer.nsi
    Pop-Location
    Write-Host "Built installer: $Root\dist\windows\OfficeRDP-setup.exe"
}

Write-Host "Built: $Dist\rdp-link.exe"
