# F-Droid and Play Store readiness

## Build facts

- Pure Kotlin + one free dependency (`org.nanohttpd:nanohttpd`, Apache-2.0).
- No Google Play Services, Firebase, crash reporters, or proprietary blobs.
- `minSdk 29`, `targetSdk 36`, `compileSdk 36`.
- Reproducible from source with Gradle and the Android SDK; no prebuilt `.so`
  files are required for the current build.
- The optional Go core can be bound later with `gomobile bind`; it is not
  required for the current APK.

## Play Store

1. Create a keystore (keep it outside the repo):

   ```sh
   keytool -genkeypair -v -keystore officerdp.keystore -alias officerdp \
     -keyalg RSA -keysize 4096 -validity 10000
   ```

2. Export the signing environment and build the bundle:

   ```sh
   export OFFICERDP_KEYSTORE_FILE=$PWD/officerdp.keystore
   export OFFICERDP_KEYSTORE_PASSWORD=...
   export OFFICERDP_KEY_ALIAS=officerdp
   export OFFICERDP_KEY_PASSWORD=...
   packaging/build-android-release.sh
   ```

3. Upload `android/app/build/outputs/bundle/release/app-release.aab`.

4. Store listing assets live in
   `android/fastlane/metadata/android/en-US/` (title, descriptions,
   changelog, icon, feature graphic, screenshots).

5. Complete the Data safety form using `docs/PRIVACY.md`:
   - No data collected or shared.
   - Data is encrypted in transit only when a TLS rendezvous/proxy is used.
   - Users can request deletion by clearing app data (no server account).

6. Declare the accessibility service use in the Play Console. The service is
   used only to inject remote taps and text after the user enables it.

## F-Droid

1. Add the app to `fdroiddata` with:

   ```yaml
   Categories:
     - Connectivity
   License: MIT
   SourceCode: https://github.com/<org>/office-rdp
   IssueTracker: https://github.com/<org>/office-rdp/issues
   AutoName: RDP Link

   RepoType: git
   Repo: https://github.com/<org>/office-rdp.git

   Builds:
     - versionName: 0.2.0
       versionCode: 2
       commit: <tag>
       subdir: android
       gradle:
         - yes
   ```

2. F-Droid builds without signing environment variables, so the release
   variant falls back to the debug key; F-Droid re-signs with its own key.
3. No `NonFreeNet` anti-feature: the rendezvous server is optional and
   self-hostable.
4. Keep `fastlane/` metadata for the F-Droid listing.

## Screenshots

Add phone screenshots to
`android/fastlane/metadata/android/en-US/images/phoneScreenshots/` before
submitting. Capture them from the emulator with:

```sh
adb exec-out screencap -p > phoneScreenshots/01-host.png
```
