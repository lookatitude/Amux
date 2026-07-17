---
name: terminal-ui-performance
description: "Profiles and improves terminal redraw latency, allocation behavior, resize storms, terminal capability degradation, keyboard-only accessibility, and render-loop stability. Use for measured TUI performance and compatibility work. Do not use for daemon profiling, release CI ownership, web performance, or suite-wide quality policy."
when_to_use: "When the terminal interface must meet frame latency, responsiveness, compatibility, or accessibility acceptance criteria."
type: specialist
derived_from_template: guild.skill_template.v1
---

# When to use it

Use for TUI frame-time and allocation profiling, damage reduction, resize storms, slow terminals, color/capability fallback, keyboard-only operation, and visible status semantics.

# When not to use it

Do not profile daemon persistence or PTY supervision, own CI runners, optimize browser metrics, or define the overall test and release gate policy.

# Required inputs

- Reference and CI hardware profiles.
- Frame-latency, interaction-latency, memory, and soak criteria.
- Supported terminal capability baseline and degradation policy.
- Reproducible 8-pane and 20-PTY fixtures from QA/backend.

# Output format

Produce benchmark code, profiles, before/after measurements, capability fallback behavior, accessibility checks, and a bounded optimization report tied to acceptance thresholds.

# Workflow steps

1. Reproduce the target fixture and capture baseline frame/interaction metrics.
2. Separate renderer, layout, event ingestion, and terminal-write costs.
3. Optimize the dominant measured cost without weakening correctness fixtures.
4. Exercise resize storms, burst output, narrow terminals, reduced color, and slow consumers.
5. Verify focus, status, and errors remain distinguishable without color or pointer input.
6. Re-run golden, interaction, benchmark, and soak fixtures and report deltas.

# Evidence requirements

Every optimization includes reproducible commands, environment profile, before/after percentiles, allocations or memory, and correctness-test results.

# Escalation rules

Escalate daemon-side bottlenecks to `backend`, CI/profile ownership to `devops`, suite-wide acceptance changes to `qa`, and architecture tradeoffs to `architect`.

# Safety constraints

Do not drop protocol events, skip rendering correctness, hide disconnects, or weaken sequence checks to improve benchmarks.

# Eval cases

- TUI redraw performance request yields a benchmark-first optimization plan.
- No-color keyboard-only request includes observable focus and status semantics.
- Daemon SQLite profiling request is routed to `backend`.

