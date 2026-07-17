// Package ansispike is a throwaway architectural spike (work package A6). Its
// CONCLUSION is frozen into docs/adr/0006-platform-interfaces.md (VT decoding)
// and docs/adr/0007-dependency-and-compatibility-policy.md; the code is retained
// only as executable evidence that github.com/charmbracelet/x/ansi can serve as
// the streaming control-sequence decoder behind Amux's frozen TerminalEngine
// interface, with raw bytes staying authoritative and unsupported sequences
// recorded as bounded diagnostics rather than crashing or desyncing the parser.
package ansispike
