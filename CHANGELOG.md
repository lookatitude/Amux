# Changelog

All notable changes to Amux are documented in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases use
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial Linux-first Go implementation of the Amux daemon, CLI, and
  interactive terminal workspace TUI.
- Session, workspace, split-pane, terminal-surface, attach/replay, snapshot,
  notification, diagnostics, and hook-trust workflows.
- Ordered local protocol, durable SQLite state, snapshot restore, PTY process
  supervision, terminal cell projections, input leases, and audit/redaction
  boundaries.
- Native Arch Linux and Ubuntu CI lanes for amd64 and arm64, plus race, fuzz,
  security, soak, packaging, SBOM, checksum, and release verification tooling.
- AUR, systemd user service, rollback/recovery, architecture, security,
  testing, and contributor documentation.
- Protected-channel workflow with feature PRs targeting `next` and changelog
  promotions from `next` to `main`.

### Fixed

- GitHub Actions now loads only valid tool assignments, provisions Arch build
  prerequisites, and uses an owner-safe runtime path inside Arch containers.
- SQLite writes from concurrent daemon components are serialized in-process,
  preventing slower ARM hosts from exhausting the database busy timeout.

[Unreleased]: https://github.com/lookatitude/Amux/compare/main...next
