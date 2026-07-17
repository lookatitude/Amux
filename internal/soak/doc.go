// Package soak holds the release soak workload (spec "Performance and
// reliability": blocking 30-minute and nightly 8-hour soaks with 20 concurrent
// PTYs). The workload itself lives in soak_test.go behind the `soak` build tag
// and is invoked exclusively through scripts/soak/run-soak.sh, which captures
// the evidence layout (metadata, metrics, pprof, verdict) the spec requires:
//
//	go test -tags soak -run 'TestSoak$' ./internal/soak \
//	  -soak.duration=30m -soak.seed=1 -soak.ptys=20 \
//	  -soak.pprof=<dir> -soak.metrics=<file>
//
// This file carries no build tag so `go build ./...`, `go vet ./...`, and
// staticcheck always see a well-formed package; the tagged test compiles only
// for soak runs and adds no module dependencies.
package soak
