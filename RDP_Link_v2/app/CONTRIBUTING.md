# Contributing

Thanks for helping make RDP Link better!

## Ground rules

- **Keep it light**: stdlib-only runtime. No new Python dependencies without
  a strong justification; external tools (`ffmpeg`, FreeRDP, `ssh`) are
  invoked, never bundled.
- **Keep it fast**: nothing blocking may run on the connect path.
- **Fail loudly**: new failure modes must surface in the status line and
  session log, never silently.

## Workflow

1. Fork / branch from `main`.
2. `python3 -m pytest` and `python3 -m pyflakes rdp_manager tests` must pass
   (CI runs both on macOS and Linux).
3. Add tests for behaviour changes — handshake protocol changes need
   matching updates in `receiver/*.sh` *and* `tests/test_bridges.py`.
4. Update docs (README + relevant `docs/*.md`) when behaviour or ports
   change.
5. Open a pull request with a short description of the user-visible change.

## Protocol stability

The UDP handshake (`RDPCTL1`) and the port map (7272–7275) are a public
contract between the app and the receiver scripts — see
[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md). Changes must bump the protocol
version string and stay backward compatible for one minor release.
