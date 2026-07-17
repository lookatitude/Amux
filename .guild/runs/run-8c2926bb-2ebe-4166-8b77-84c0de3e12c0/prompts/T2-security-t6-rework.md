Adopt `.guild/agents/security.md` and reopen T2-security for the release-blocking
findings validated by the mandatory T6 G-lane review at
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T6-qa/result-1.json`.
This is hard security/architecture work; execute autonomously, TDD-first, and
stay inside the frozen specification unless an actual ask-gate is unavoidable.

Close two findings:

1. Overlayfs can reuse `(st_dev, st_ino)` after `rm -rf root && mkdir root`, so
   the current durable project key remains identical and a trust grant may
   survive root replacement. Preserve the public frozen durable key definition
   exactly: SHA-256 of canonical realpath + dev + ino. Do not silently redefine
   it. Add the smallest Linux-capable replacement-validation mechanism needed
   to ensure a registered/trusted root is invalidated when the object at the
   path is replaced even if overlayfs reuses the inode. Prefer a separately
   persisted validation discriminator derived from a kernel identity surface
   such as statx birth time / inode generation / mount identity, with explicit
   capability and fail-closed semantics. It must not invalidate trust merely
   because ordinary project contents change. Document portability/fallback
   behavior and preserve non-Linux compilation. If no reliable discriminator
   is available, deny trust reuse rather than accepting an ambiguous identity.
   Keep audit epochs/grants monotonic and do not weaken containment.

   Prove on production paths:
   - current ext4/btrfs/xfs-style behavior remains valid;
   - root replacement changes the validation identity even when dev+ino are
     reused under Arch and Ubuntu overlayfs containers;
   - ordinary child create/write/remove operations do not invalidate trust;
   - restart/persistence revalidation detects replacement, not only an
     in-memory open-FD comparison;
   - unsupported identity capability fails closed and is typed/audited;
   - macOS builds/tests retain a documented conservative behavior.

2. Correct the frozen readiness manifest so every `-run` expression binds to
   real substantive tests and cannot pass on zero matches or only skipped
   tests. Retire the old `TestSecondUIDVariantsDeferred` stub or make it
   impossible for the gate to count it. Bind trust-matrix replay to the actual
   integrated/real trust engine coverage; if the existing coverage is only a
   unit-level SUT, add the missing production/integrated test rather than
   relabeling it. Add a deterministic self-gate that enumerates/matches the
   target tests and fails on zero substantive executions or unexpected skips.
   Update security readiness docs and receipt-generation guidance truthfully.

Run focused control/trust/persistence tests, all security tests, full tests,
race tests, vet, tidy/verify, Linux amd64+arm64 no-cgo builds, and the exact
Arch/Ubuntu overlayfs reproduction through `scripts/qa/linux-gates.sh` with an
owner-safe TMPDIR. Re-run corrected manifest checks and write fresh evidence
receipts where appropriate. Do not edit T4 replay logic, CLI behavior, release
tooling, TUI, research, or claim unrun reference/CI evidence.

Replace
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/security-T2-security.md`
with an exact receipt and emit exactly one valid `guild.handoff.v2` object.
