Act as the mandatory independent Guild G-lane reviewer for reopened T4. Read
`review/G-lane:T4-backend/packet-5.md`, the exact receipt/SHA, approved frozen
spec/ADR/API contracts, prior T4 trail, current replay/ring/RPC/client/CLI code
and tests, and resource-exhaustion evidence. Challenge encoded-size safety,
pagination truth, error details, production integration, version behavior, and
the independently observed race-only flake. Run read-only checks where useful.
Return only one valid `review_result.v1` object for the packet id and SHA.
