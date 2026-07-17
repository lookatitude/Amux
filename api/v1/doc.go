// Package v1 is the frozen Amux local control-protocol contract (ADR-0003). It
// is the single wire authority shared by the daemon, the CLI, and the TUI; it
// depends only on the Go standard library so it can be vendored or code-generated
// without pulling in runtime internals.
//
// # Frame format
//
// Every message is a bounded, length-prefixed frame:
//
//	frame  := headerLen(uint32 BE) || header || bodyLen(uint32 BE) || body
//	header := UTF-8 JSON object, 1..MaxHeaderBytes
//	body   := opaque bytes, 0..MaxBodyBytes (0 for control frames)
//
// The header is always JSON. The body carries raw, already-sequenced terminal
// output bytes with NO base64 expansion (PRD F4); control frames set bodyLen 0.
// Both length prefixes are validated against the limits below before any
// allocation, so a hostile peer cannot force an unbounded read.
//
// # Message kinds
//
// The header's "type" field selects the envelope: hello, welcome, request,
// response, event. A connection begins with hello/welcome negotiation and
// rejects an unsupported major version before accepting any request.
//
// # Compatibility policy
//
//   - Major version: breaking. A peer MUST reject a major it does not implement
//     with error code unsupported_version.
//   - Minor version: additive and backward compatible. A newer peer negotiates
//     down to the older peer's minor. Unknown ADDITIVE fields in envelope headers
//     are ignored (forward compatibility), so a v1.N server tolerates a v1.(N+1)
//     client's extra header fields.
//   - Durable payloads: command Params and persisted structures decode STRICTLY
//     (unknown fields are rejected) because they are contract boundaries, not
//     evolvable envelopes (PRD F5/F10). Use DecodeStrict for those.
package v1
