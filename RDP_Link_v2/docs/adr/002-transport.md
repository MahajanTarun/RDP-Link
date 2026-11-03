# ADR 002 — Transport: WebRTC (pion) over DTLS-SRTP

- Status: Accepted (Phase 0)
- Decision: Use WebRTC (pion/webrtc) for NAT traversal, media, and data
  channels. Signalling via WebSocket to rendezvous or mDNS on LAN.
- Rationale: ICE maturity, TURN fallback, Android/browser interop, permissive
  license.
