---
title: Amux project overview
status: candidate
confidence: high
source_refs:
  - .guild/indexes/codebase-map.json
  - .guild/runs/019f6360-6c58-7d41-ba38-c4498e3c719d/research/cmux-linux-replication-deep-dive.md
---

# Amux project overview

Amux is currently a research-stage repository with no application source files or committed revision. Its first durable artifact is a deep analysis of `manaflow-ai/cmux`, focused on feature parity and a Linux-first implementation strategy.

The research recommends evaluating a portable workspace runtime before selecting a final desktop shell. Rust is the leading option because cmux already contains a portable Rust multiplexer, while Go remains a credible clean-room alternative for a TUI-first control plane.

Implementation architecture is not yet established. Claims about future components remain proposals until a specification and plan are approved.
