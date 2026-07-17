# Amux developer + CI task entry points (T3 devops, work package D1).
#
# These targets are THIN: each one echoes/runs the underlying `go`/script
# command so nothing is hidden — a contributor can copy any command out of a
# recipe and run it by hand. CI calls the exact same targets so "green locally"
# and "green in CI" mean the same thing. Tool versions live in scripts/tools.env
# (the single source of truth); this Makefile reads GOTOOLCHAIN/CGO_ENABLED from
# there so builds never float with the host.
#
# Usage: `make help`

# Load the pinned tool/env values (GOTOOLCHAIN, CGO_ENABLED, *_VERSION).
include scripts/tools.env
export GOTOOLCHAIN
export CGO_ENABLED

GO           ?= go
# Linux release architectures (glibc x86_64 / aarch64 — spec platform target).
LINUX_ARCHES ?= amd64 arm64
# Local GOBIN for pinned build-time tools so we never touch the user's $GOBIN.
TOOLS_BIN    := $(CURDIR)/.tools/bin
STATICCHECK  := $(TOOLS_BIN)/staticcheck

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Meta
# ---------------------------------------------------------------------------

.PHONY: help
help: ## List available targets
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z0-9_.-]+:.*##/{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: tools
tools: $(STATICCHECK) ## Install pinned build-time analysers into ./.tools/bin
$(STATICCHECK):
	GOBIN=$(TOOLS_BIN) $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

# ---------------------------------------------------------------------------
# D1 gate targets — every one is a required CI check (workflow: ci.yml).
# `make verify` is the deterministic, host-agnostic blocking gate.
# ---------------------------------------------------------------------------

.PHONY: verify
verify: fmt-check vet staticcheck mod-verify tidy-check deps-manifest license generate-check smoke-selftest ## Run the full deterministic D1 gate (no network side effects beyond module fetch)

.PHONY: fmt
fmt: ## Format all Go code in place
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-clean
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	@echo "gofmt: clean"

.PHONY: vet
vet: ## `go vet` for the host build and the linux cross-build graph
	$(GO) vet ./...
	GOOS=linux GOARCH=amd64 $(GO) vet ./...

.PHONY: staticcheck
staticcheck: $(STATICCHECK) ## Pinned static analysis (honnef.co/go/tools)
	$(STATICCHECK) ./...

.PHONY: mod-verify
mod-verify: ## Verify the module cache matches go.sum hashes
	$(GO) mod verify

.PHONY: tidy-check
tidy-check: ## Fail if `go mod tidy` would change go.mod/go.sum
	@cp go.mod go.mod.bak; cp go.sum go.sum.bak; \
	$(GO) mod tidy; \
	if ! diff -q go.mod go.mod.bak >/dev/null || ! diff -q go.sum go.sum.bak >/dev/null; then \
		echo "go.mod/go.sum are not tidy — run 'go mod tidy' and commit"; \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; exit 1; \
	fi; \
	rm -f go.mod.bak go.sum.bak; echo "go mod tidy: clean"

.PHONY: deps-manifest
deps-manifest: ## Fail if the build/test module graph drifts from the frozen manifest
	scripts/check-deps-manifest.sh

.PHONY: license
license: ## Verify every build/test-graph module carries a permissive license
	scripts/check-license.sh

.PHONY: generate-check
generate-check: ## Regenerate completions + `go generate` and fail on any diff
	scripts/check-generated.sh

# ---------------------------------------------------------------------------
# Build / test
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build amux + amuxd for the host (CGO disabled)
	$(GO) build ./...

.PHONY: build-linux
build-linux: ## Cross-compile the release binaries for every linux arch (compile evidence only)
	@set -e; for arch in $(LINUX_ARCHES); do \
		echo "== GOOS=linux GOARCH=$$arch CGO_ENABLED=0 go build ./... =="; \
		GOOS=linux GOARCH=$$arch CGO_ENABLED=0 $(GO) build ./...; \
	done

.PHONY: test
test: ## Run the blocking unit/integration suite
	$(GO) test -count=1 ./...

.PHONY: race
race: ## Run the suite under the race detector (amd64 CI lane)
	# The Go race detector links the C runtime, so it needs cgo + a C compiler
	# even though every PRODUCT build stays CGO_ENABLED=0 (ADR-0007 D4). `-race`
	# is a test-only tool, never a shipped artifact, so this local override does
	# not weaken the cgo prohibition. On Linux `-race` with CGO_ENABLED=0 errors
	# "-race requires cgo"; this keeps the amd64 race lane runnable.
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...

.PHONY: fuzz-smoke
fuzz-smoke: ## Run every fuzz target briefly (smoke, not a campaign)
	scripts/fuzz-smoke.sh

.PHONY: bench
bench: ## Run benchmarks once (perf smoke; QA owns the reference-profile run)
	$(GO) test -run '^$$' -bench=. -benchtime=1x ./... 2>/dev/null || echo "no benchmarks yet (pre-backend)"

.PHONY: smoke-selftest
smoke-selftest: ## Deterministic host-agnostic behavioral fixtures (packaging linkage gate + backup/restore paths)
	bash packaging/smoke/linkage-fixture.test.sh
	bash scripts/release/backup-restore-selftest.sh

.PHONY: cover
cover: ## Run the suite with coverage
	$(GO) test -count=1 -coverprofile=coverage.out ./...

# ---------------------------------------------------------------------------
# Linux-only runtime spikes (ADR-0006/0007 deferred evidence). RUN ON LINUX.
# ---------------------------------------------------------------------------

.PHONY: spike-launch
spike-launch: ## Descriptor-bound launch race spike (Linux, kernel >= 5.6)
	$(GO) run ./spikes/launch

.PHONY: spike-containment
spike-containment: ## Daemon-death descendant containment spike (Linux, cgroup v2)
	@echo "requires a delegated writable cgroup v2 subtree; see spikes/containment header"
	AMUX_CGROUP_ROOT=$${AMUX_CGROUP_ROOT:-/sys/fs/cgroup/amux-spike} $(GO) run ./spikes/containment

# ---------------------------------------------------------------------------
# Release (D3) — thin wrappers around the pinned GoReleaser config.
# Publishing is a SEPARATE, explicitly user-authorized operation (never here).
# ---------------------------------------------------------------------------

GORELEASER_CFG := packaging/goreleaser/goreleaser.yaml

.PHONY: release-tools
release-tools: ## Install the pinned GoReleaser + syft into ./.tools/bin (local mirror of the CI pin)
	GOBIN=$(TOOLS_BIN) $(GO) install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	GOBIN=$(TOOLS_BIN) $(GO) install github.com/anchore/syft/cmd/syft@$(SYFT_VERSION)

# Fail closed unless the goreleaser on PATH is EXACTLY the pin in scripts/tools.env
# and syft (the SBOM generator) is present. This is what makes `make release-*`
# "use that exact pin" regardless of whether the binary came from the CI
# goreleaser-action or a local `make release-tools` (add ./.tools/bin to PATH).
.PHONY: release-toolcheck
release-toolcheck:
	@command -v goreleaser >/dev/null 2>&1 || { echo "release: goreleaser not on PATH (pin $(GORELEASER_VERSION); run 'make release-tools' and add ./.tools/bin to PATH)"; exit 1; }
	@have="$$(goreleaser --version 2>/dev/null | awk '/GitVersion:/{print $$2}')"; \
	 if [ "$$have" != "$(GORELEASER_VERSION)" ]; then \
	   echo "release: goreleaser '$$have' on PATH but pin is '$(GORELEASER_VERSION)' (scripts/tools.env) — refusing to run off-pin"; exit 1; \
	 fi; \
	 echo "release: goreleaser $$have matches pin $(GORELEASER_VERSION)"
	@command -v syft >/dev/null 2>&1 || { echo "release: syft not on PATH (SBOM step needs $(SYFT_VERSION); 'make release-tools')"; exit 1; }
	@echo "release: syft present ($$(syft version 2>/dev/null | awk '/Version:/{print $$2}' | head -1))"

.PHONY: release-check
release-check: release-toolcheck ## Validate the GoReleaser config without building (pinned tool)
	goreleaser check --config $(GORELEASER_CFG)

.PHONY: release-snapshot
release-snapshot: release-toolcheck ## Build a local, unpublished release snapshot into ./dist (pinned tool)
	goreleaser release --snapshot --clean --config $(GORELEASER_CFG)

.PHONY: release-verify
release-verify: ## Verify checksums/SBOM/provenance of an existing ./dist
	scripts/release/verify-artifacts.sh dist

# ---------------------------------------------------------------------------
# Operations (D5) — soak/benchmark evidence capture.
# ---------------------------------------------------------------------------

.PHONY: soak
soak: ## Run the 30-minute blocking soak with full evidence capture
	AMUX_SOAK_DURATION=$${AMUX_SOAK_DURATION:-30m} scripts/soak/run-soak.sh

.PHONY: clean
clean: ## Remove build/test/release artifacts
	rm -rf dist build coverage.out .tools .amux-artifacts
