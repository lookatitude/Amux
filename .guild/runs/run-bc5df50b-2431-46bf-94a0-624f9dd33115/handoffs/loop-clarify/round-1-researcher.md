# L1 Researcher Handoff — Round 1

- run_id: `run-bc5df50b-2431-46bf-94a0-624f9dd33115`
- loop: `L1`
- lane: `phase:brainstorm`
- role: `researcher`
- dispatch: independent Guild Codex host-adapter session, read-only, model `gpt-5.4`
- status: `questions`

## Blocking findings

1. Freeze the authoritative MVP object model; the hierarchy controls IDs, snapshots, CLI verbs, and replay.
2. Decide whether the MVP renders terminal surfaces inside the TUI or only supervises external terminals.
3. Define the exact restore contract for cwd, environment, argv, replay/scrollback, notifications, and stopped processes.
4. Define clean-room compatibility: concepts only, UX parity, CLI parity, state-model parity, or protocol compatibility.
5. Make performance criteria testable with fixed benchmark profiles, soak duration, an event-gap definition, usable-restore definition, and an enumerated operator-flow list.
6. Define hook trust: grants, redaction, cwd/environment, network assumption, timeouts, output caps, and approval UX.
7. Resolve whether repository identity is the workspace boundary or optional pane metadata.
8. State an exact Linux/Arch support and packaging matrix; Wayland/X11 concerns should be separated from the headless/TUI MVP.
9. Remove browser and Windows from the MVP definition gate if they are truly post-MVP non-goals.

## Evidence

- `.guild/runs/019f6360-6c58-7d41-ba38-c4498e3c719d/research/cmux-linux-replication-deep-dive.md`
- `.guild/runs/run-bc5df50b-2431-46bf-94a0-624f9dd33115/handoffs/loop-clarify/round-1-architect.md`

## Handoff receipt

- loop_id: `loop-clarify`
- lane_id: `phase:brainstorm`
- round: 1
- role: `researcher`
- status: `questions`
- unresolved_questions: 9
- next: `architect revision`
