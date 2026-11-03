# Android host hardening notes

## Foreground service behaviour
- The host runs as a foreground service with `mediaProjection` type.
- Notification is ongoing/auto-cancel disabled.
- `onDestroy` releases the virtual display, closes the image reader, stops projection and HTTP server.

## Battery & Doze notes
- Keep the screen on while hosting (recommended) because screen capture stops when the display is off.
- Do not rely on background work while device is locked.
- If OEM battery saver kills the host service, re-add the device to app battery whitelist.

## Input injection
- tapping works via AccessibilityService (`InputAccessibilityService.tap`).
- keyboard injection is not implemented yet and is listed in remaining work.
- If accessibility is disabled, `/input` returns success but does nothing.

## Security
- HTTP endpoints are currently unauthenticated; production should use pairing token/password.
- Audit logging and policy enforcement should be added.
