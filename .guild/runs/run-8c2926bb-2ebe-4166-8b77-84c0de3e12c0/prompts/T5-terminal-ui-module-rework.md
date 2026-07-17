Perform a narrow T5-terminal-ui rework. The implementation is otherwise
complete, but independent orchestrator verification rejected the receipt because
the final staticcheck invocation polluted the durable module graph.

Exact evidence from the current workspace:

- `go mod tidy -diff` exits 1.
- It removes `honnef.co/go/tools v0.7.0` from go.mod/go.sum.
- It adds `golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa` as the correct
  indirect module and removes stale staticcheck-only sums.
- The receipt currently claims a green final gate, so it is not acceptable
  until the graph is tidy-clean and all frozen manifests/docs reflect the
  post-tidy truth.

Fix only this defect and any direct fallout:

1. Run `go mod tidy` to restore the canonical module graph. Do not add
   staticcheck as an application dependency; future staticcheck runs must use a
   pinned external tool invocation that cannot mutate go.mod/go.sum.
2. Regenerate `scripts/expected-modules-build.txt` and
   `scripts/expected-modules-test.txt` only if the actual compiled graph changes.
   Update `docs/dependencies.md` so its full module graph and verification claims
   match the canonical post-tidy files.
3. Prove `go mod tidy -diff` is empty, `go mod verify`, dependency manifest,
   license gate, gofmt, vet, all tests, all race tests, Linux amd64/arm64 no-cgo
   builds, and focused TUI tests are green. Re-run staticcheck only in a way that
   leaves go.mod/go.sum byte-identical, or report the prior clean output without
   re-running it; the durable graph is the higher-priority contract.
4. Update the existing T5 handoff receipt with truthful final evidence and emit
   exactly one valid `guild.handoff.v2` fence, no prose outside it. Summary <=600
   characters, notes <=200. Do not change production TUI semantics or any
   out-of-scope file.
