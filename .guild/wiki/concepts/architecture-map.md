---
title: Architecture map
status: candidate
confidence: high
source_refs:
  - .guild/indexes/codebase-map.json
---

# Architecture map

## Current state

**Confidence: high.** The deterministic Init scan found zero source files, zero languages, zero modules, zero frameworks, and zero import edges. The Git repository has no `HEAD`, so the generated commit marker is `unknown`.

## Established boundaries

- `.guild/runs/**/research/` contains research artifacts.
- `.guild/wiki/` is the canonical Guild knowledge surface.
- `.guild/indexes/codebase-map.json` is the derived cheap-scan map.

## Deferred architecture

**Confidence: high.** No runtime, UI framework, terminal engine, browser engine, IPC protocol, persistence layer, or packaging strategy has been implemented in this repository. The existing research compares candidates, but it is not an approved architecture decision.

The deep KnowledgeGraph and onboarding tour are intentionally deferred until `/guild:learn` or a later planning gate requires them.
