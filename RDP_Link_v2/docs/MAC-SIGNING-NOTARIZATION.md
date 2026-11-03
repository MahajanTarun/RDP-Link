# macOS code signing & notarization

## Steps
1. Build the app:
```sh
packaging/build-macos.sh
```

2. Optional code signing (ad-hoc by default; set `CSC_IDENTITY_AUTO_DISCOVERY=true` with real identity to use Apple Developer ID).

3. Apple notarization (requires Apple Developer ID + Apple ID app-specific password):
```sh
APPLE_ID="..." \
APPLE_APP_SPECIFIC_PASSWORD="..." \
APPLE_TEAM_ID="..." \
packaging/mac-sign.sh
```

4. Create DMG:
```sh
packaging/build-macos.sh  # already produces the DMG; run again if re-signing
```

## Notes
- `packaging/mac-sign.sh` submits `Office RDP.app` to Apple notary and staples.
- If Apple Developer tools aren't configured, notarization is skipped.
- Windows NSIS installer job in CI builds Windows installers with Go + Python + NSIS.
- Android APK: use Play Store or F-Droid; not automated here.
