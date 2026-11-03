# macOS code signing & Apple notarization

## Ad-hoc (default)
- Build uses ad-hoc code signing (`codesign -`).
- The app can still run locally.
- `codesign --verify` can be used to validate the signature.

## Real signing + notarization
Set these env vars before building:
```sh
CODESIGN_IDENTITY="Developer ID Application: Your Name"
APPLE_ID="your@apple.com"
APPLE_APP_SPECIFIC_PASSWORD="xxxx"
APPLE_TEAM_ID="1234567890"
```

Then build + package + notarize:
```sh
packaging/build-macos.sh            # uses CODESIGN_IDENTITY and signs with it
packaging/mac-notarize.sh dist/macos/RDPLink-macos.dmg    # submits to Apple
```

## Notes
- Notarization is optional and sends the DMG to Apple.
- Without env vars the build skips notarization.
