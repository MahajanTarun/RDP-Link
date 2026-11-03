# Android release artifacts

## Current outputs
- `dist/android/OfficeRDP-android.apk` — debug build (usable and installable today).
- `dist/android/OfficeRDP-release.aab` — Play-ready bundle produced by `gradle bundleRelease`.
- `dist/android/OfficeRDP-release.apk` — debug-signed release variant for local install.

## Real store readiness
For a real Play/F-Droid release set:
```sh
OFFICERDP_KEYSTORE_FILE=path/to/officerdp.keystore
OFFICERDP_KEYSTORE_PASSWORD=...
OFFICERDP_KEY_ALIAS=...
OFFICERDP_KEY_PASSWORD=...
```
Then `gradle bundleRelease` (`gradle` locally) or `./gradlew bundleRelease`.

The APK found under `dist/android/` is usable as an instant install and should continue to work.
