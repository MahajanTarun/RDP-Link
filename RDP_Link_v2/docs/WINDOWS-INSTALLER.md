# Windows installer (Office RDP) — NSIS

## Requirements
- Windows 10/11 x64
- Python 3.12 + PyInstaller
- Go 1.26 (for `office-rdp` and `rendezvous`)
- NSIS ([choco install nsis](https://nsis.sourceforge.io/Download))

## Build
Run from repo root on Windows:
```
pwsh -File packaging\build-windows.ps1 -NoOpen
```

Outputs:
- `dist\windows\rdp-link.exe` (PyInstaller)
- `dist\windows\office-rdp.exe` (Go host agent)
- `dist\windows\office-rdp-rendezvous.exe` (Go rendezvous)
- `dist\windows\OfficeRDP-setup.exe` (NSIS installer)

## Runtime
- The NSIS installer installs the Python/GUI app and the Go `office-rdp` + `rendezvous` binaries to `C:\Program Files\Office RDP\`.
- Use `office-rdp --host --port 8088 --password <secret>` to start the Windows host agent.
- Use `office-rdp --connect <host-id>` to start the guest (currently image-only).

## Notes
- The Windows host agent uses CGO; the current submission builds it with CGO disabled (no screen capture). Cross-build from macOS uses CGO disabled. Full Windows capture/video requires building on Windows with an MSYS2/MinGW toolchain.
