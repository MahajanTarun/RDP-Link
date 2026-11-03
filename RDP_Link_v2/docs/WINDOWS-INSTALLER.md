# Windows installer (RDP Link) — NSIS

## What ships

| File | Role |
| --- | --- |
| `rdp-link.exe` | Desktop launcher: share this screen, connect to another device (PyInstaller onefile, no Python needed) |
| `office-rdp.exe` | Go host agent: GDI screen capture, input injection, browser control panel, `--doctor` |
| `office-rdp-rendezvous.exe` | Self-hostable rendezvous server for WAN connections |
| `README.txt` | Quick start for the installed binaries |

The Start-menu and desktop shortcuts launch `rdp-link.exe`. The browser control
panel (`office-rdp.exe --ui`) is installed alongside as a fallback and as the
cross-platform guest, so Windows is usable even if the launcher is removed.

## Requirements

- Windows 10/11 x64 (to run)
- Go 1.26 (to build)
- mingw-w64 (to build `office-rdp.exe` with real GDI capture)
- NSIS — `choco install nsis` (to build the installer)
- Python 3.12 + PyInstaller (only to build `rdp-link.exe`)

## Build

### Fully cross-platform (no Windows machine)

```sh
# macOS/Linux: freezes rdp-link.exe through Wine
bash packaging/build-windows-app-wine.sh

# macOS/Linux: builds office-rdp.exe with mingw-w64, then the NSIS installer
bash packaging/windows/build-installer.sh
```

`build-installer.sh` includes `rdp-link.exe` automatically when it exists, via
the generated `packaging/windows/optional-launcher.nsh`.

### On Windows

```powershell
pwsh -File packaging\build-windows.ps1 -NoOpen
```

Outputs:

- `dist\windows\rdp-link.exe`
- `dist\windows\office-rdp.exe`
- `dist\windows\office-rdp-rendezvous.exe`
- `dist\windows\RDPLink-setup.exe`

## Runtime

- Installs to `C:\Program Files\RDP Link\` and registers in Add/Remove
  Programs.
- Adds a firewall rule for `office-rdp.exe` on private networks.
- Share from the launcher (**Start sharing**) or a terminal:
  `office-rdp.exe --host --port 8088 --password <secret>`.
- Connect from the launcher (**Connect to a device**) or any browser at
  `http://<this-pc>:8088/`.
- `office-rdp.exe --test-pattern --host ...` streams a synthetic frame for CI
  and for hosts without a capturable desktop.

## Notes

- `office-rdp.exe` is a CGO build (GDI32/USER32), statically linked with the
  GCC runtime, so no MinGW DLLs are required on the target machine.
- The host agent needs an interactive desktop session; it cannot capture the
  secure desktop (UAC/login) or a disconnected RDP session.
- Real GDI capture cannot be exercised under Wine (`BitBlt` fails), so Wine
  validation uses `--test-pattern`; CI runs the real capture path on Windows.
