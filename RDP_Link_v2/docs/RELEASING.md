# Releasing Office RDP

Version is `0.2.0` in:

- `cmd/office-rdp/main.go` (`--version`)
- `packaging/build-linux-deb.sh` (`Version:`)
- `packaging/windows/installer.nsi` (`!define VERSION`)
- `android/app/build.gradle` (`versionCode` / `versionName`)

Bump all four together.

## Android

The release build uses a real keystore when these environment variables are
set, otherwise it falls back to the debug key (installable, not publishable):

```
OFFICERDP_KEYSTORE_FILE
OFFICERDP_KEYSTORE_PASSWORD
OFFICERDP_KEY_ALIAS
OFFICERDP_KEY_PASSWORD
```

This machine keeps a local release key at
`~/.config/officerdp/officerdp-release.keystore` with the matching variables
in `~/.config/officerdp/android-signing.env` (both `0600`, never committed).

```sh
set -a; . ~/.config/officerdp/android-signing.env; set +a
cd android
gradle clean assembleRelease bundleRelease
cp app/build/outputs/apk/release/app-release.apk   ../dist/android/OfficeRDP-android-release.apk
cp app/build/outputs/bundle/release/app-release.aab ../dist/android/OfficeRDP-android-release.aab
```

The gomobile AAR (shared Go identity/peer ID) is optional. Rebuild it with:

```sh
bash packaging/build-android-core.sh   # needs gomobile + Android NDK
```

Without `android/app/libs/mobile.aar` the app falls back to a Kotlin-derived
peer ID.

## macOS

```sh
CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
  bash packaging/build-macos.sh
```

Ad-hoc signing (`CODESIGN_IDENTITY` unset) produces a DMG that runs locally
after Gatekeeper confirmation. For distribution, notarize with:

```sh
APPLE_ID=... APPLE_APP_SPECIFIC_PASSWORD=... APPLE_TEAM_ID=... \
  bash packaging/build-macos.sh
```

## Windows

The installer cross-builds with MinGW when it is installed:

```sh
brew install mingw-w64        # or: apt-get install mingw-w64
bash packaging/windows/build-installer.sh
```

The script statically links the GCC runtime, so the shipped `.exe` needs no
MinGW DLLs. The optional native launcher `rdp-link.exe` is only produced on a
real Windows host:

```powershell
powershell -File packaging/build-windows.ps1
```

## Linux

```sh
bash packaging/build-linux.sh       # arm64 + amd64 binaries, tarball
bash packaging/build-linux-deb.sh   # .deb for both architectures
```

On Apple Silicon the amd64 build runs under a Rosetta-enabled Colima profile:

```sh
colima start -p x86 --arch aarch64 --vm-type vz --vz-rosetta
```

The script defaults `CTX_AMD64=colima-x86` but uses whatever Docker context is
current for arm64. Starting the x86 profile switches the current context, so
pass both explicitly:

```sh
CTX_ARM64=colima CTX_AMD64=colima-x86 \
  bash packaging/build-linux.sh && \
CTX_ARM64=colima CTX_AMD64=colima-x86 \
  bash packaging/build-linux-deb.sh
```

## Verification gate

```sh
bash scripts/test-all.sh
bash scripts/security-scan.sh
```

Both must pass before artifacts are published. See `docs/VALIDATION.md` for
the OS × host/guest matrix and the evidence behind it.
