# Rollback & recovery runbook (D6)

T3 devops · work package D6. Symptom-first operational runbook for a bad
release, a failed upgrade, or state corruption. It documents the **ops
procedure**; the persistence *mechanism* it drives is frozen by ADR-0005 and
implemented by backend (T4) — this runbook references that contract, it does not
redefine it. Where a step depends on a command that is not currently exposed,
the limitation is named explicitly rather than presented as implemented.

State locations (XDG, per-user):

- data / snapshots — `~/.local/share/amux/`
- runtime state — `~/.local/state/amux/`
- control socket — `$XDG_RUNTIME_DIR` (owner-only)

Security state (trust epochs, grants, revocation, audit) is **monotonic**: it is
never restored, decreased, or reactivated from a snapshot (ADR-0005). No rollback
step below may attempt to.

---

## Runbook A — a released version is bad

**Symptom:** a shipped `vX.Y.Z` crashes, regresses, or fails verification.

1. **Detect / confirm.** Reproduce, and capture evidence (§ Evidence collection).
   Verify the artifact is authentic first (`artifact-verification.md`) — rule out
   a tampered download before blaming the code.
2. **Stop the bleeding.** Do not publish further. If the AUR package was pushed,
   do **not** rewrite history — supersede with a fixed `pkgrel`/`pkgver`
   (`aur-maintenance.md`).
3. **Artifact rollback** (see below) to the last known-good version.
4. **Post-incident:** open a fix, add a regression test (QA), and record the
   failure mode in the changelog `Security`/`Bug fixes` group.

## Runbook B — artifact rollback (pin the previous version)

Amux binaries are self-contained (static, `CGO_ENABLED=0`); rolling **binaries**
back is just reinstalling the prior tarball. **State** is the delicate part —
see Runbook D before starting an *older* daemon on *newer* state.

```bash
# 1. Stop the daemon (if run as a user unit)
systemctl --user stop amuxd            # or terminate your foreground amuxd

# 2. Back up current state BEFORE changing anything (Runbook C)

# 3. Reinstall the previous good version
#    AUR:      downgrade to the prior amux-bin pkg (e.g. via a cached .pkg.tar.zst)
#    tarball:  extract amux_<prev>_linux_<arch>.tar.gz over /usr/bin (verify checksums first)

# 4. Confirm identity
amux --version && amuxd --version      # must report <prev>, protocol as expected

# 5. Start only after the compatibility check in Runbook D passes
```

## Runbook C — snapshot backup & restore

**Back up before every upgrade, downgrade, or risky operation.** A snapshot is a
point-in-time copy of the daemon's persisted layout.

The two XDG trees (`share/amux`, `state/amux`) must keep **distinct** paths in
the archive, so restore recreates each under its own tree. Root the archive at
`~/.local` and name the two subtrees explicitly — do **not** archive both as a
bare `amux` (that collides two members under one name and restores the state
tree *inside* the share tree). This pattern is proven by
`scripts/release/backup-restore-selftest.sh`.

```bash
# Cold backup (daemon stopped) — always safe, no coordination needed.
# Assumes XDG defaults: XDG_DATA_HOME=~/.local/share, XDG_STATE_HOME=~/.local/state
# (both under ~/.local). If you have overridden either to a non-sibling path,
# make one archive per tree instead.
systemctl --user stop amuxd
tar -czf amux-state-$(date -u +%Y%m%dT%H%M%SZ).tar.gz \
    -C ~/.local share/amux state/amux
```

Restore:

```bash
systemctl --user stop amuxd
# Move BOTH current trees aside (never delete until the restore is verified):
ts=$(date -u +%s)
mv ~/.local/share/amux ~/.local/share/amux.bak.$ts 2>/dev/null || true
mv ~/.local/state/amux ~/.local/state/amux.bak.$ts 2>/dev/null || true
# Extract at ~/.local — recreates ~/.local/share/amux AND ~/.local/state/amux:
tar -xzf amux-state-<stamp>.tar.gz -C ~/.local
systemctl --user start amuxd
```

On next open, the daemon classifies every restored surface as exactly one of
`live | restarted | stopped` (ADR-0005 `internal/persist.Classify`) — it will
**not** claim a process is `live` from a stale snapshot. An explicit snapshot
restore may import only its notification/read export; it can never restore
security state.

- A logical, coordinated `amux snapshot export` / `import` command is not
  currently exposed. Until it is, the **cold tar backup above is the supported
  procedure** and is sufficient because it copies the whole layout while the
  daemon is stopped.

## Runbook D — upgrade / downgrade compatibility check

Two independent version axes (see `versioning-and-release.md`):

- **Release version** — cosmetic for compatibility; state format is governed by
  the schema, not the SemVer.
- **Schema / migration version** — the one that matters for state.
- **Protocol version** (`internal/version.Protocol`) — daemon⇄CLI wire
  compatibility (ADR-0003).

Rules:

1. **Upgrade (newer daemon on older state).** Safe by design: ADR-0005 migrations
   are **ordered, transactional, and forward-only at runtime**. Back up first
   (Runbook C), then start the new daemon; it migrates on open.
2. **Downgrade (older daemon on newer/migrated state).** **Not guaranteed** —
   forward-only migrations have no automatic down-path. Do **not** point an older
   daemon at migrated state. Instead: restore the pre-upgrade snapshot (Runbook
   C) taken before the migration, then run the older daemon against it.
3. **CLI/daemon skew.** If `amux` and `amuxd` report different `Protocol`
   values, negotiation may refuse. Match their versions (they share
   `internal/version`, so a matched release guarantees a matched protocol).

Check the axes before starting:

```bash
amux --version      # amux <ver> (protocol <p>, commit <c>, built <d>)
amuxd --version     # protocol must match amux's
# There is no standalone schema-version inspection command yet. For a downgrade,
# restore the pre-upgrade backup rather than trying an older daemon on newer state.
```

## Runbook E — failed migration containment

**Symptom:** the daemon reports a migration failure on open.

ADR-0005 guarantees the fail-safe posture, and this runbook relies on it rather
than improvising: a migration failure **preserves and reports the previous
known-good snapshot and commits no partial load** (fail-safe); a partial temp
generation is ignored on next open.

1. **Do not delete anything.** The previous known-good layout is intact by
   contract.
2. Capture evidence (§ below): the daemon's migration error + the on-disk
   generation state.
3. **Recover** by restoring the pre-upgrade snapshot (Runbook C) and running the
   **prior** daemon version (Runbook B) — the combination that produced the
   known-good state.
4. File the migration bug with the captured evidence for backend (T4) /
   architect. Do not hand-edit the persisted store to "fix" it — that bypasses
   the transactional guarantee.

## Evidence collection (attach to every incident)

Collect before mutating anything, so the failure is reproducible:

```bash
mkdir -p incident-$(date -u +%Y%m%dT%H%M%SZ) && cd "$_"
amux --version   > versions.txt 2>&1
amuxd --version >> versions.txt 2>&1
journalctl --user -u amuxd --no-pager > amuxd.journal.txt 2>&1   # if a user unit
cp -a ~/.local/state/amux/*.log . 2>/dev/null || true
# state fingerprint WITHOUT copying secrets/audit content:
( cd ~/.local/share/amux && find . -maxdepth 2 -printf '%y %10s %p\n' ) > state-layout.txt
uname -srm > host.txt; (. /etc/os-release; echo "$PRETTY_NAME") >> host.txt
```

This mirrors the soak evidence layout (`operations/reference-profile.md`):
versions, host, logs, and a state *fingerprint* (not a raw dump of security
state). Audit/trust content is **not** collected here — that is the security
lane's surface, and it is monotonic and non-restorable.

## Deferrals (honest)

- `amux snapshot export/import` and standalone schema-version reporting are not
  currently exposed. The **cold tar backup + prior-binary restore** procedures
  above are fully usable today and are the supported path until those land.
- `systemctl --user` and `journalctl --user` require a Linux systemd session;
  on non-systemd or non-Linux hosts, substitute your own supervision/log source.
- QA exercises these runbooks end-to-end against the integrated candidate on
  Arch and Ubuntu before release promotion.
