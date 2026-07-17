# Development and promotion workflow

Amux uses two protected long-lived branches and short-lived feature branches.
The intent is to keep active development easy to consume without allowing an
untested change to become release history.

## Branch roles

| Branch | Role | Accepted changes |
|---|---|---|
| `main` | Stable, promoted history and release tags | PRs from `next` only, with an updated human-readable changelog |
| `next` | Tested development channel | PRs from short-lived feature, fix, docs, refactor, test, or chore branches |
| short-lived branch | Isolated implementation work | Direct commits by the contributor; deleted after merge |

Start new work from an up-to-date `next`:

```bash
git switch next
git pull --ff-only origin next
git switch -c feature/<short-description>
```

Use a descriptive prefix such as `feature/`, `fix/`, `docs/`, `refactor/`,
`test/`, or `chore/`. Keep a branch focused enough that its PR can be reviewed
and reverted independently.

## Feature PR: short-lived branch to `next`

Before opening the PR:

```bash
make test
make verify
make build-linux
```

The PR must explain the behavior change, relevant design or security impact,
and actual validation performed. GitHub runs the full blocking CI matrix for
PRs targeting `next`. Merge only after required checks pass and review threads
are resolved.

Feature PRs do not have to edit `CHANGELOG.md`; this avoids merge conflicts
while development is in flight. A noteworthy change should still be described
clearly in the PR so the promotion author can write the release-facing entry.

## Promotion PR: `next` to `main`

A promotion is intentionally separate from feature development:

1. Confirm all intended feature PRs are merged into `next` and CI is green.
2. Update `CHANGELOG.md` under `[Unreleased]` with plain-language Added,
   Changed, Fixed, Security, or Removed entries.
3. Open a PR whose head is exactly `next` and base is `main`.
4. Summarize user-visible changes, migrations, compatibility concerns, and the
   evidence used to validate the candidate.
5. Merge only when the main promotion policy and the full CI matrix are green.

The `release-policy` GitHub check rejects any PR to `main` that does not come
from `next`, does not change `CHANGELOG.md`, or leaves `[Unreleased]` without a
human-readable entry.

After merging, `main` is the source for release tags. Tags must not be created
from `next` or a feature branch.

## Hotfixes

Create a hotfix branch from `main`, implement and test the fix, then merge it
into `next` first. Promote `next` back to `main` with a changelog entry. This
keeps the two channels ordered and prevents a production-only commit from being
lost in the next promotion.

If an emergency requires a different path, document why in the PR and restore
branch parity immediately afterward. Do not bypass protected-branch checks by
force-pushing.

## Merge and history policy

- Never push feature commits directly to `main` or `next` after protection is
  enabled.
- Keep required checks current; do not mark an unavailable gate as passing.
- Prefer squash merges for focused feature PRs. A promotion PR may use a merge
  commit so the boundary between development and stable history remains clear.
- Delete merged short-lived branches.
- Do not rewrite published `main` or `next` history.
