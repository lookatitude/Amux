---
phase: init
mode: brownfield
repository_kind: regular
run_id: init-20260715-023954
status: complete
generated_from_commit: unknown
source_refs:
  - .guild/indexes/codebase-map.json
  - .guild/raw/init-sources.md
  - .guild/wiki/overview.md
  - .guild/wiki/concepts/architecture-map.md
---

# Guild Init: Amux

## Detection verdict

Guild classified `/Users/miguelp/Projects/Amux` as a regular repository. It detected zero sub-guilds using the fixed depth-1 rule: an immediate child must contain `.git/` or `.guild/` to be registered as a sub-guild.

The pre-existing `.guild/` contained only a prior research run. It had no settings, wiki, init record, or indexes, so Init preserved the research and introduced no conflicting overwrite.

## Cheap-scan result

- Source files: 0
- Languages: 0
- Frameworks: 0
- Modules: 0
- Import edges: 0
- Generated commit: `unknown` because the repository has no commit yet

This is an empty, research-stage project rather than an implemented brownfield codebase. The architecture map therefore records absence and defers implementation claims.

## Artifacts

- Project settings: `.guild/settings.json`
- Codebase map: `.guild/indexes/codebase-map.json`
- Raw source inventory: `.guild/raw/init-sources.md`
- Wiki index: `.guild/wiki/index.md`
- Architecture stub: `.guild/wiki/concepts/architecture-map.md`

## Deferred by contract

Plain `/guild:init` completes at the cheap-scan tier. It does not generate `.guild/indexes/knowledge-graph.json` or `.guild/indexes/onboarding-tour.md`. Run `/guild:learn` later to produce the deep knowledge graph, recall projection, domain model, and onboarding tour.

## G-init review

Status: skipped. Resolved project review mode is `local`; no `review: cross` or explicit high-risk signal required the cross-family broker.

## Follow-up

The next meaningful lifecycle step is to convert the cmux research into an explicit product specification and architecture decision before implementation.
