// Package version carries the build-stamped identity shared by amuxd and amux.
// The values are overridable at link time (-ldflags "-X ...") by the T3 devops
// release pipeline; the defaults below are what an un-stamped `go build`
// reports. Keeping this in one inward package means the daemon and CLI can never
// disagree about the version they advertise during protocol negotiation.
package version

// Protocol is the local control-protocol version this build speaks. It is
// independent of the release version: ADR-0003 evolves the protocol on its own
// major/minor track.
const Protocol = "1.1"

var (
	// Version is the semantic release version, stamped at build time.
	Version = "0.0.0-dev"
	// Commit is the VCS revision, stamped at build time.
	Commit = "unknown"
	// Date is the build date (RFC3339), stamped at build time.
	Date = "unknown"
)

// String returns a stable single-line identity string for --version output.
func String() string {
	return "amux " + Version + " (protocol " + Protocol + ", commit " + Commit + ", built " + Date + ")"
}
