Act as the mandatory independent Guild G-lane reviewer for the blocked T6 QA
receipt. Read
`.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/review/G-lane:T6-qa/packet-1.md`,
the exact receipt and SHA, approved spec/PRD/plan/ADRs, current source/tests,
the fresh security review, and raw evidence artifacts. Reopen production paths
and run read-only focused checks where useful. Validate which findings are
current, which were closed by evidence created after the security-review
snapshot, and which gates remain honestly unproven. A truthful blocked receipt
is not a satisfied release gate.

Return only one valid `review_result.v1` object for the packet id and SHA. No
markdown prose outside the structured result.
