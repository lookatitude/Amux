Adopt `.guild/agents/backend.md` and execute the mandatory T4 G-lane round-2 rework. The exact independent finding is `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T4-backend/result-2.json`; the review trail and archived round-2 receipt are beside it. Work autonomously and close only the real F2 blocker plus the tests and receipt facts required by that fix. Preserve the accepted closure of F1 and the rest of B1-B12.

The blocker is literal: ADR-0005 and the approved B8 contract define `restarted` as a new process launched under automatic policy. The current production `RestoreSnapshot` can classify an automatic-policy surface `restarted` but never spawns its replacement PTY. A class/reason response must describe completed behavior, not intent.

Implement TDD-first. Wire production automatic-policy restore so a surface is reported `restarted` only after a replacement PTY is successfully launched and installed with the restored replay/VT/runtime state. Preserve exact graph identity, argv/cwd/env/geometry policy, checkpoint replay sequencing, input/attach semantics, output callbacks, exit tracking, and trust/security exclusion. Decide and encode a fail-closed result for launch failure—never report `restarted` when launch failed, never retain a false live owner, and do not leave an unvouched prior process running. Preserve the round-1 guarantees:

- a genuinely owned same-checkpoint/same-spawn surface remains `live` and is not restarted;
- ownership mismatch, stopped/manual-policy, and clean/fresh-daemon cases never claim `live`;
- fresh-daemon automatic restore may report `restarted` only if it actually launched the new process;
- stale trust generations/grants/audit authority remain excluded and monotonic;
- no persisted/protocol schema weakening and no claim of resurrecting the original process.

Add production-path tests and real CLI E2E coverage proving at minimum:

1. automatic-policy restore launches exactly one replacement PTY and returns `restarted` only after success;
2. the replacement uses the expected executable/argv/cwd/env/geometry and emits output into the restored stream;
3. launch failure returns a typed/fail-closed outcome that cannot be mistaken for `restarted` or `live`;
4. in-daemon live reconcile still leaves the owned PTY untouched;
5. manual policy remains stopped and never launches;
6. fresh-daemon automatic restore performs a real replacement launch while fresh-daemon manual restore remains never-live;
7. the 20 required CLI flows remain meaningful—extend assertions without disguising a separate operator restart as restore behavior.

Do not weaken ADR-0005, change persisted/protocol compatibility, modify frozen securitytest sources, or expand into TUI/CI/packaging/research. If the contract is genuinely impossible without an ADR change, stop at the ask-gate; otherwise implement it fully.

Run focused restore tests, the CLI E2E, gofmt, vet, all tests, full race tests, attach stress, security conformance, Linux amd64/arm64 no-cgo compile checks, module verify/tidy diff, and scope audit. Replace `.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md` with an accurate final receipt. Remove the former disclaimer that automatic restore does not relaunch; describe exact success/failure semantics and update counts. Emit exactly one valid `guild.handoff.v2` (summary <=600, notes <=200).

When you finish, emit your result as a SINGLE fenced code block and nothing after it:
```guild.handoff.v2
{ ... a valid guild.handoff.v2 object ... }
```
The block MUST validate against the guild.handoff.v2 contract. Do not add prose after it.
