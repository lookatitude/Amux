# Contributing to Amux

Amux is Linux-first and evidence-driven. Contributions should preserve the
daemon as the single mutation authority, keep platform claims tied to real
runtime evidence, and update documentation when public behavior changes.

## Setup

Use the pinned Go toolchain from `go.mod` and `scripts/tools.env`.

```bash
git clone git@github.com:lookatitude/Amux.git
cd Amux
make tools
make test
```

`make help` lists all supported targets. Generated evidence under
`.amux-artifacts/`, build output under `build/` and `dist/`, and locally
installed tools under `.tools/` are intentionally ignored.

## Branch and PR flow

1. Branch from `next`: `git switch -c feature/<slug> next`.
2. Make a focused change with tests and documentation.
3. Run the relevant checks and push the short-lived branch.
4. Open a PR to `next` and let the full CI matrix run.
5. Promote tested work with a separate `next` to `main` PR.
6. Update `CHANGELOG.md` in the promotion PR with human-readable release notes.

Do not open a feature PR directly to `main`. The exact policy, hotfix flow, and
merge rules are documented in
[`docs/development-workflow.md`](docs/development-workflow.md).

## Required validation

For most changes:

```bash
make test
make verify
make build-linux
```

Also run the narrowest applicable integration, race, fuzz, security, soak, or
release gate described in [`docs/testing/strategy.md`](docs/testing/strategy.md).
Cross-compilation proves compilation only; do not describe it as Linux runtime
evidence.

## Change requirements

- Add or update tests for behavior changes and regressions.
- Keep `go.mod`, `go.sum`, and `docs/dependencies.md` coherent when dependencies
  change.
- Add or amend an ADR when changing authority, ordering, persistence, protocol,
  platform, or compatibility decisions.
- Update operator docs and `--help` text together when CLI/TUI behavior changes.
- Preserve stable JSON schemas and documented exit codes unless the change is
  intentionally versioned.
- Do not commit generated local evidence, binaries, credentials, or secrets.

## Pull request description

Explain:

- what changed and why;
- user or developer impact;
- architecture, security, compatibility, or migration implications;
- exact checks run and their results;
- follow-up work that is genuinely deferred.

Be explicit about unexecuted gates. “Not run” is useful evidence; inferred or
fabricated success is not.
