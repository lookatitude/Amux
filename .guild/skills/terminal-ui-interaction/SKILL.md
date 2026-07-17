---
name: terminal-ui-interaction
description: "Implements terminal-native split layout, focus, keyboard routing, resize, command modes, notification inbox/unread navigation, and deterministic interaction state machines over backend semantic state. Do not use for daemon graph/notification authority, PTY lifecycle, web UI, or product microcopy."
when_to_use: "When implementing TUI interaction for panes, focus, navigation, resize, modes, input routing, and terminal-side notification/attention presentation."
type: specialist
derived_from_template: guild.skill_template.v1
---

# When to use it

Use for split-tree projection, pane geometry, directional focus, keymaps, resize/equalize behavior, command modes, overlays, notification inbox/unread navigation, attention routing, and keyboard-only workflows.

# When not to use it

Do not redefine the daemon graph, notification/read-state authority, attention-routing semantics, command protocol, PTY input semantics, web frontend, visual branding, or final user-facing copy.

# Required inputs

- Authoritative split tree and mutation commands from `backend`.
- Interaction and navigation invariants approved by `architect`.
- Terminal dimensions, minimum pane sizes, keymap policy, and focus metadata.
- Accessibility and latency criteria from the spec and QA plan.

# Output format

Produce Bubble Tea models and messages, pure layout/focus state machines, configurable keymaps, interaction fixtures, and an explicit mapping from user actions to backend commands.

# Workflow steps

1. Model layout and focus projection as pure deterministic functions.
2. Define key precedence, modes, escape behavior, and collision handling.
3. Implement split, focus, resize, equalize, surface switching, overlays, notification presentation, and latest-unread navigation over backend events.
4. Preserve stable pane identity through every projection and resize.
5. Add table-driven keyboard, geometry, small-terminal, and rapid-resize tests.
6. Report any required durable mutation not already present in the backend contract.

# Evidence requirements

Every action maps to one documented command or local-only transition; table tests cover all directional edges, minimum sizes, mode exits, and conflicting bindings.

# Escalation rules

Escalate graph/command changes to `architect` and `backend`, wording needs to the main session, and suite-wide interaction strategy to `qa`.

# Safety constraints

Never send input to a pane without explicit focus and input-lease authority; destructive actions require visible confirmation according to the plan's autonomy contract.

# Eval cases

- Split-pane keyboard navigation request yields a deterministic focus model and tests.
- Rapid terminal resize request includes geometry invariants and minimum-size behavior.
- Notification inbox request presents backend-owned read/unread state and focus routing without duplicating authority.
- Daemon workspace mutation redesign is handed to `architect`/`backend`.
