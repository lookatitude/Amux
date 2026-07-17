You are performing a receipt-only repair for completed task T4-backend.

Do not modify any repository file. Read the existing specialist handoff at:
.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md

The implementation and prose handoff already exist. Your only job is to return
the single embedded guild.handoff.v2 JSON object from that file, preserving its
truthful content. Verify that summary is at most 600 characters and notes is at
most 200 characters; shorten those two strings if required without changing the
claims. Return exactly one fenced block and no prose outside it:

```guild.handoff.v2
{ ... }
```

Do not run tests, do not edit files, do not add keys, and do not emit markdown
other than that single fence.
