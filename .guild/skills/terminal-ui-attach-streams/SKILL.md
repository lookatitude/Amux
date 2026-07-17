---
name: terminal-ui-attach-streams
description: "Presents an architect/backend-approved pane-attachment contract in the terminal client: replay-to-live UI state, input-lease controls, detach/reconnect feedback, and snapshot-on-gap presentation. Do not use for defining attach transport, lifecycle semantics, replay/event sequencing, IPC, retention, restore states, process control, or hook trust policy."
when_to_use: "When implementing client-side visible behavior over an already-approved pane attachment and input-lease contract."
type: specialist
derived_from_template: guild.skill_template.v1
---

# When to use it

Use for presenting contract-defined attachment states, initial replay, ordered live output, gap recovery, input-lease acquisition/takeover/release, detach, reconnect, and slow-consumer conditions.

# When not to use it

Do not define attach transport, lifecycle semantics, wire schemas, replay/event ordering, event durability, restore classification, PTY ownership, kill/restart semantics, or hook authorization; those remain backend, architecture, and security concerns.

# Required inputs

- Versioned attach/event contract and sequence semantics.
- Replay bounds, snapshot-on-gap behavior, and disconnect reasons.
- Input-lease command/event contract and authorization errors.
- Pane lifecycle states and user-visible recovery requirements.

# Output format

Produce an explicit client attachment state machine, Bubble Tea integration, sequence-aware fixtures, disconnect/recovery messages, and interaction tests for concurrent readers and input lease transitions.

# Workflow steps

1. Import the approved attachment states and legal transitions without extending them.
2. Pin the atomic metadata/replay/live handoff with sequence fixtures.
3. Implement shared output and explicit input-lease acquisition/takeover.
4. Handle detach, owner disconnect, server restart, gap, and slow consumer distinctly.
5. Keep stopped/restarted/live process state separate from client attachment state.
6. Test two-client concurrency and prove non-owner input never reaches the send command.

# Evidence requirements

Fixtures force replay/live boundary races, duplicate/out-of-order events, disconnects, lease takeover, and recovery. Each visible state names its last accepted sequence.

# Escalation rules

Escalate ambiguous sequencing or authorization to `backend`, unsafe takeover semantics to `security`, and cross-component state redesign to `architect`.

# Safety constraints

Fail closed on missing lease authority, unknown boot identity, invalid sequence, or ambiguous recovery. Never imply a stopped process is live.

# Eval cases

- Approved-attach-contract presentation request produces a sequence-aware client state machine.
- Concurrent client input request enforces one explicit input lease.
- Event-retention redesign is routed to `backend` rather than implemented client-side.
