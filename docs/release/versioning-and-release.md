# Versioning & release procedure (D6)

T3 devops · work package D6. Defines how Amux versions are numbered, stamped,
and cut into a release candidate. **Publishing is a separate, explicitly
user-authorized operation** — nothing here pushes a public release (autonomy
policy: publishing externally requires confirmation; the pipeline builds a
candidate and stops).

## Version scheme

- **Release version** — SemVer `MAJOR.MINOR.PATCH`, tagged `vX.Y.Z`. Pre-1.0
  (`0.y.z`) the public surface may change between minors; breaking changes bump
  the minor and are called out in the changelog `Security` / `Features` groups.
- **Protocol version** — the local control protocol
  (`internal/version.Protocol`, currently `1.0`) evolves on its **own** track
  per ADR-0003 and is independent of the release version. Daemon/CLI skew is a
  *protocol* compatibility question, not a release-version one — see
  `rollback-and-recovery.md` § "Upgrade / downgrade compatibility".
- **Toolchain** — pinned in `scripts/tools.env` (`GO_VERSION`, `GOTOOLCHAIN`).
  A toolchain bump is a deliberate change recorded in its commit, same as a
  dependency bump (ADR-0007).

## How the version is stamped

There is one authority: `internal/version` (Version / Commit / Date). An
un-stamped `go build` reports `0.0.0-dev`. The release build injects real values
at link time (`packaging/goreleaser/goreleaser.yaml`):

```
-X github.com/amux-run/amux/internal/version.Version={{ .Version }}
-X github.com/amux-run/amux/internal/version.Commit={{ .FullCommit }}
-X github.com/amux-run/amux/internal/version.Date={{ .CommitDate }}
```

Both `amux` and `amuxd` read the same package, so the CLI and daemon can never
disagree about the version they advertise during protocol negotiation.

## The release pipeline (`.github/workflows/release.yml`)

Triggered by pushing a `vX.Y.Z` tag (or `workflow_dispatch`). Three gated jobs:

1. **gate** — `make verify` (fmt, vet, staticcheck, mod-verify, tidy-check,
   deps-manifest, license, generate-check) + `make test`. Full history is
   fetched for the changelog and `git describe`.
2. **soak-30m** — the 30-minute blocking soak (`scripts/soak/run-soak.sh`).
   It boots the production daemon assembly and real PTY workload; evidence is
   uploaded as `soak-30m-evidence`. A candidate cannot advance without a real
   passing soak summary.
3. **build** — `make release-check` (validate config), `make release-snapshot`
   (build tarballs + checksums + per-archive SBOMs), `record-provenance.sh`
   (reproducible build-input record), `make release-verify` (integrity), and —
   **only when the operator opts in** (`publish_attestation: true`) — a GitHub
   provenance attestation. Outputs upload as `amux-release-candidate`.

`goreleaser.yaml` sets `release.disable: true` and the workflow always uses
`--snapshot`: even a mis-triggered run produces only a local candidate, never a
public GitHub Release or AUR push.

## Artifacts a release produces

For `linux/amd64` and `linux/arm64` (glibc, `CGO_ENABLED=0`, `-trimpath`):

- `amux_<ver>_linux_<arch>.tar.gz` — both `amux` + `amuxd`, shell completions,
  `LICENSE*`/`README*` (if present), `docs/release/*.md`, and the example
  systemd user unit.
- `checksums.txt` — SHA-256 over every archive.
- `amux_<ver>_linux_<arch>.tar.gz.sbom.cdx.json` — CycloneDX SBOM per archive
  (syft, pinned `SYFT_VERSION`).
- `build-metadata.json` — reproducible build-input record.
- *(opt-in)* provenance attestation bundle.

Verification of each is in `artifact-verification.md`. QA (Q8) owns the
**integrated** installable-artifact evidence against the release candidate.
Local snapshot success does not by itself assert an integrated release.

## Cutting a release (human checklist)

1. Land feature work through PRs to `next`; confirm its native `test-*` jobs are
   green, not only the cross-build.
2. Update `CHANGELOG.md` with human-readable release notes and open the
   promotion PR from `next` to `main`. Direct feature PRs to `main` are rejected.
3. Merge the promotion only after the full CI and `release-policy` checks pass.
4. Update the toolchain/dependency pins only if intended; re-run `make verify`.
5. Run `make release-check` locally to validate the GoReleaser config.
6. Optionally run `make release-snapshot` to inspect a local candidate in
   `./dist`.
7. Tag the promoted `main`: `git tag vX.Y.Z && git push origin vX.Y.Z`. The release workflow builds
   the candidate.
8. Download `amux-release-candidate`; verify per `artifact-verification.md`.
9. Hand to QA for integrated smoke + soak evidence (Q8).
10. **Publish** (GitHub Release, AUR) only after explicit user authorization —
   see `aur-maintenance.md`. Not part of this pipeline.

## Local dry-runs (no Linux required)

```bash
make release-tools        # install the pinned goreleaser + syft into ./.tools/bin
export PATH="$PWD/.tools/bin:$PATH"
make release-check        # goreleaser config validation (fails closed unless goreleaser == pin)
make release-snapshot     # build both linux amd64/arm64 tarballs + SBOMs + checksums into ./dist
make release-verify       # recompute checksums, assert SBOM + build-metadata present
```

The linux tarballs cross-compile from any host (`CGO_ENABLED=0`), so
`make release-snapshot` produces the full artifact set on a dev macOS host with
the pinned GoReleaser (`scripts/tools.env` `GORELEASER_VERSION`) and `syft` on
PATH. `release-check`/`release-snapshot` refuse to run unless the goreleaser on
PATH is exactly the pin. What still requires Linux is the **install smoke**
(`packaging/smoke/smoke-install.sh` — runs the produced ELF binaries, checks
static linkage and `--version`); run it in an `archlinux:latest` / `ubuntu:24.04`
container against the built tarball. Hosted CI (release.yml) remains the
authority for the full matrix; nothing here claims hosted-CI evidence.
