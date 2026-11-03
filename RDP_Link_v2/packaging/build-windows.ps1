# Windows build script (run on Windows with Python 3.12 + PyInstaller)
#
#   powershell -ExecutionPolicy Bypass -File packaging\build-windows.ps1
#
# Produces dist\windows\rdp-link.exe (desktop launcher + guest viewer) and,
# when a C toolchain is available, a screen-capturing office-rdp.exe.
param([switch]$NoOpen)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$AppDir = Join-Path $Root "app"
$Dist = Join-Path $Root "dist\windows"
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

Push-Location $AppDir
python -m pip install --upgrade pyinstaller
python -m PyInstaller --noconfirm --clean --onefile --name rdp-link `
  --hidden-import office_viewer `
  --add-data "receiver;receiver" `
  --exclude-module pytest --exclude-module unittest `
  --exclude-module pydoc_data --exclude-module test `
  ubuntu_rdp_manager.py
Pop-Location

Copy-Item (Join-Path $AppDir "dist\rdp-link.exe") (Join-Path $Dist "rdp-link.exe") -Force

# Build the Go agent. CGO gives real GDI screen capture and input injection;
# without a compiler the stub build still serves the API but cannot capture.
$OfficeBin = Join-Path $Dist "office-rdp.exe"
$RendezvousBin = Join-Path $Dist "office-rdp-rendezvous.exe"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
if (Get-Command gcc -ErrorAction SilentlyContinue) {
    $env:CGO_ENABLED = "1"
    $env:CGO_LDFLAGS = "-static-libgcc -static-libstdc++ -static"
} else {
    Write-Warning "No gcc found: building office-rdp.exe without screen capture."
    $env:CGO_ENABLED = "0"
}
Push-Location $Root
go build -ldflags "-s -w" -o $OfficeBin ./cmd/office-rdp
go build -ldflags "-s -w" -o $RendezvousBin ./cmd/rendezvous
Pop-Location

Write-Host "Built Go binaries:" $OfficeBin, $RendezvousBin

# Build NSIS installer if makensis is available.
$NSIS = Get-Command makensis -ErrorAction SilentlyContinue
if ($NSIS) {
    Push-Location (Join-Path $Root "packaging\windows")
    makensis installer.nsi
    Pop-Location
    Write-Host "Built installer: $Root\dist\windows\RDPLink-setup.exe"
}

Write-Host "Built: $Dist\rdp-link.exe"
