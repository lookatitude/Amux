# Reference profile — soak & benchmark evidence (D5)

T3 devops · work package D5. This document standardises **where** soak and
benchmark evidence is produced so numbers are comparable run-to-run and a
release reviewer knows exactly what a given result represents. The harness that
enforces it is `scripts/soak/run-soak.sh`; the wiring lives in
`.github/workflows/nightly-soak.yml` (8-hour nightly) and the 30-minute blocking
gate in `.github/workflows/release.yml`.

## Two profiles, kept distinct

| Profile | Where | Spec | Purpose |
|---|---|---|---|
| **CI standard** | GitHub-hosted `ubuntu-24.04` | 4 vCPU (hosted runners currently ship 4 vCPU / **16 GiB**) | cheap, always-on regression signal; catches leaks/crashes/gaps early |
| **Arch reference** | self-hosted Arch rolling x86_64 workstation | 4 vCPU / **8 GiB**, cgroup v2, native `/dev/ptmx` | release-grade performance numbers on the spec's named reference distro |

The spec names an **Arch x86_64 workstation** as the reference platform and a
**4-vCPU / 8-GiB** target envelope. GitHub-hosted `ubuntu-24.04` gives 4 vCPU
but **16 GiB** — more headroom than the target. Consequences, stated honestly:

- **Correctness signals** (no crash, no unrecovered event gap, no orphaned
  process, no unbounded queue/replay/goroutine/FD growth) are valid on either
  profile — they do not depend on the exact memory ceiling.
- **Performance / memory-ceiling numbers** from a hosted 16-GiB runner are an
  **upper bound**, not the reference result. A memory budget that must hold at
  8 GiB is only *proven* on the Arch reference profile. Do not quote hosted-run
  memory figures as the reference envelope.

### Pointing the nightly soak at the Arch reference

`nightly-soak.yml` runs on `ubuntu-24.04` by default. For release-grade numbers,
register a self-hosted Arch runner and swap the job's `runs-on:` to its label
(e.g. `[self-hosted, arch, x86_64]`). Requirements on that host:

- Arch rolling, kernel ≥ 5.6, **cgroup v2** with a delegated writable subtree
  (the containment spike and the daemon both need it).
- The pinned Go toolchain from `scripts/tools.env` (`GOTOOLCHAIN` is exported so
  `go` refuses to float).
- Native `/dev/ptmx` (present on a real Arch host; the containment/launch
  runtime evidence cannot be produced by cross-compilation — see below).

## Architecture coverage — the full matrix is wired and blocking

The supported matrix is Arch **and** Ubuntu across amd64 **and** arm64. Every
cell is an executable, blocking CI job (no `continue-on-error`):

- **Ubuntu amd64** native runtime: `ubuntu-24.04` (`test-ubuntu-amd64` — full
  blocking suite + race/fuzz/bench + launch/containment spikes).
- **Ubuntu arm64** native runtime: `ubuntu-24.04-arm` (`test-ubuntu-arm64` — the
  feasible subset: blocking suite + build + launch spike).
- **Arch amd64** native runtime: `ubuntu-24.04` + `archlinux:latest` container
  (`test-arch-amd64` — blocking suite + race, reference distro).
- **Arch arm64** native runtime: `ubuntu-24.04-arm` + Arch Linux ARM (aarch64)
  container `menci/archlinuxarm:base-devel` (`test-arch-arm64` — the feasible
  subset: blocking suite + native build + launch spike). race/fuzz/perf are the
  amd64 lane's job per the spec success-criteria split.

**Reference performance** numbers (not correctness signals) are still produced
on a self-hosted Arch host — see the reference profile above. A self-hosted
`[self-hosted, arch, aarch64]` label is the documented drop-in alternative for
the `test-arch-arm64` job if the hosted arm64 runner is unavailable; the CI job
comment carries the exact swap. No cell is a prose deferral or a soft skip.

## Evidence captured for every soak/benchmark run

`run-soak.sh` writes a timestamped dir `.amux-artifacts/soak/<UTC>/` containing:

- `metadata.env` — `run_utc`, `duration`, `seed`, `concurrent_ptys`,
  `git_commit`, `git_describe`, `go_version`, `kernel`, `arch`, `distro`,
  `nproc`. This is the reproducibility record required for every run.
- `soak.log` — the streamed structured log.
- `pprof/` — memory + goroutine profiles (`-soak.pprof`).
- `metrics.jsonl` — sampled metrics (`-soak.metrics`).
- `summary.env` — `result` (`PASS` / `FAIL` / `SKIPPED_NO_WORKLOAD`),
  `exit_code`, and pointers to the artifacts above.

CI uploads this dir as a build artifact (`nightly-soak-evidence`, 14-day
retention; `soak-30m-evidence` on the release gate).

## Soak workload and fail-closed behavior

The soak workload is `internal/soak.TestSoak` (build tag `soak`). It boots the
production daemon assembly, creates 20 concurrent PTYs by default, runs a
seeded command/attach/input/snapshot stimulus mix, and records process and
resource evidence. The harness exits non-zero if the workload is missing,
skipped, fails, or does not produce a real `PASS` summary. A release therefore
cannot advance on a wiring-only or phantom-green soak.

## Running it

```bash
# 30-minute blocking soak (release gate), full evidence capture:
make soak                                   # AMUX_SOAK_DURATION defaults to 30m

# 8-hour nightly equivalent, locally:
AMUX_SOAK_DURATION=8h AMUX_SOAK_PTYS=20 scripts/soak/run-soak.sh

# Harness wiring self-test only; never valid as release evidence:
AMUX_SOAK_ALLOW_SCAFFOLD=1 make soak
```

Runtime execution of the soak requires Linux (native PTY + cgroup v2). On macOS
the harness runs far enough to prove metadata capture and the fail-closed path;
the workload itself is Linux-only.
