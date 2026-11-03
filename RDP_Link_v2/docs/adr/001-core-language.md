# ADR 001 — Core language: Go (instead of Rust)

- Status: Accepted (Phase 0)
- Context: Plan §9.2 recommends Rust core + per-OS shims. The dev environment
  and CI available to this project has Go 1.26 but no Rust toolchain, and the
  chosen transport library `pion/webrtc` is mature Go with ICE/TURN/mDNS
  support and Android-friendly bindings via gomobile.
- Decision: Implement the core and rendezvous in Go. Revisit Rust if a
  measured need arises (binary size, NDK ergonomics). FreeRDP compat shim
  remains a separate process wrapper.
- Consequences: Single Go module for core + rendezvous + CLI; Python/Tk
  launcher stays for desktop UI in Phase 1.
