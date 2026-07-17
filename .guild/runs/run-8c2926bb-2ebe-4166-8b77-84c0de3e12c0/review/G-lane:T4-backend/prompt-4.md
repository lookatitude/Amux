Act as the mandatory independent Guild G-lane round-4 reviewer. Read
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T4-backend/packet-4.md`,
the exact current receipt, approved plan/spec/ADRs, prior review trail, and the
live source/tests needed to verify the five T5 contract-completion gaps. Reopen
current files and run read-only focused checks where useful. Be skeptical: test
terminal chunking and projection authority/compatibility rather than trusting
the receipt, but do not re-litigate settled backend areas without a concrete
regression.

Return only one valid `review_result.v1` object for the packet id and SHA. No
markdown prose outside the structured result.
