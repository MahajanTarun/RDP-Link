# Privacy policy — Office RDP

Last updated: 2026-11-03

Office RDP is a self-hosted remote desktop tool. It has no user accounts and
does not include analytics, advertising, or crash-reporting SDKs.

## What the app processes

- **Peer identity.** A random seed is generated on first launch and stored on
  the device. The 9-digit peer ID is derived from it with SHA-256.
- **Screen frames.** While you are sharing, screen frames are produced locally
  and sent only to guests that authenticate with your session password. Frames
  are not uploaded anywhere else.
- **Input events.** Taps and text typed by an authenticated guest are injected
  through the accessibility helper. The helper does not read screen content.
- **Settings.** Password, port, alias, and optional rendezvous URL are stored in
  the app's private preferences.
- **Rendezvous.** If you configure a rendezvous URL, the app sends its peer ID,
  alias, LAN address, and capabilities to that server every 60 seconds. You can
  leave this blank to stay LAN-only. You may run your own rendezvous server.

## What the app does not do

- No telemetry, tracking, ads, or third-party analytics.
- No microphone, camera, contacts, location, or SMS access.
- No background collection when sharing is stopped.

## Data retention

Settings and the peer identity stay on the device until you clear app data or
uninstall the app. Session recordings, if you start one on desktop, are written
only to the local filesystem path you choose.

## Contact

Open an issue in the project repository for privacy questions.
