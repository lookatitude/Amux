package v1

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates the golden vectors under testdata/. Run:
//
//	go test ./api/v1/ -run TestGoldenVectors -update
//
// then review the diff. The committed vectors are the frozen wire contract
// (ADR-0003): a change to any of them is a protocol change requiring an ADR
// amendment, and this test is what makes that change visible in review.
var update = flag.Bool("update", false, "regenerate golden vectors in testdata/")

// goldenCases are the canonical example of each envelope. Field values are
// stable and human-meaningful so the golden files double as documentation.
func goldenCases() []struct {
	name string
	v    any
} {
	return []struct {
		name string
		v    any
	}{
		{"hello", Hello{Type: TypeHello, Major: 1, Minor: 0, Client: "amux/0.0.0-dev", Capabilities: []string{"events", "attach"}}},
		{"welcome", Welcome{Type: TypeWelcome, Major: 1, Minor: 0, BootID: "boot-01HZ", Server: "amuxd/0.0.0-dev"}},
		{"request", Request{Type: TypeRequest, ID: "req-1", Method: "workspace.split", DeadlineMS: 2000,
			Params: json.RawMessage(`{"workspace":"wsp-000001","target":"pan-000001","orientation":"horizontal","ratio":0.5}`)}},
		{"response_ok", Response{Type: TypeResponse, ID: "req-1",
			Result: json.RawMessage(`{"rev":2,"new_pane":"pan-000002","new_surface":"sur-000002"}`)}},
		{"response_error", Response{Type: TypeResponse, ID: "req-2",
			Error: &ErrorBody{Code: ErrNotFound, Message: "target pane not found: pan-999", Retryable: false}}},
		{"event", Event{Type: TypeEvent, BootID: "boot-01HZ", Session: "ses-000001", Seq: 42, Event: "pane_split", TimeMS: 1737000000000,
			Payload: json.RawMessage(`{"target":"pan-000001","new_pane":"pan-000002","orientation":"horizontal","ratio":0.5}`)}},
		{"error_replay_gap", ErrorBody{Code: ErrReplayGap, Message: "requested sequence 10 below retained floor 128; reattach with snapshot", Retryable: true,
			Details: json.RawMessage(`{"requested":10,"floor":128}`)}},
	}
}

func TestGoldenVectors(t *testing.T) {
	dir := "testdata"
	if *update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.MarshalIndent(tc.v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join(dir, tc.name+".json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to generate)", path, err)
			}
			if !bytes.Equal(bytes.TrimRight(want, "\n"), bytes.TrimRight(got, "\n")) {
				t.Errorf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", tc.name, want, got)
			}
		})
	}
}

// TestFrameRoundTrip proves the framing codec is lossless for a control frame
// (header only) and an output frame (header + raw binary body, no base64).
func TestFrameRoundTrip(t *testing.T) {
	header := []byte(`{"type":"event","event":"pty_output","session":"ses-000001","seq":7}`)
	body := []byte{0x1b, '[', '3', '1', 'm', 'h', 'i', 0x00, 0xff} // raw bytes incl NUL and 0xff

	var buf bytes.Buffer
	if err := WriteFrame(&buf, header, body); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, header, nil); err != nil { // control frame, empty body
		t.Fatal(err)
	}

	h1, b1, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(h1, header) || !bytes.Equal(b1, body) {
		t.Fatal("output frame did not round-trip losslessly")
	}
	h2, b2, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(h2, header) || b2 != nil {
		t.Fatal("control frame did not round-trip (body should be nil)")
	}
	if _, _, err := ReadFrame(&buf); err != io.EOF {
		t.Fatalf("clean end of stream must be io.EOF, got %v", err)
	}
}

func TestFrameLimitsFailClosed(t *testing.T) {
	// Oversize header prefix must fail closed before allocation.
	oversize := []byte{0x00, 0x20, 0x00, 0x01} // 0x00200001 > MaxHeaderBytes (0x00100000)
	_, _, err := ReadFrame(bytes.NewReader(oversize))
	fe, ok := err.(*FrameError)
	if !ok || fe.Code != ErrResourceExhausted {
		t.Fatalf("oversize header must return resource_exhausted FrameError, got %v", err)
	}
	// Truncated frame (header prefix promises bytes that never arrive).
	truncated := []byte{0x00, 0x00, 0x00, 0x08, 'a', 'b'}
	if _, _, err := ReadFrame(bytes.NewReader(truncated)); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated frame must be io.ErrUnexpectedEOF, got %v", err)
	}
}

// TestNegotiationMatrix pins the ADR-0003 version-negotiation rules.
func TestNegotiationMatrix(t *testing.T) {
	cases := []struct {
		name                           string
		sMajor, sMinor, cMajor, cMinor int
		wantMajor, wantMinor           int
		wantCode                       ErrorCode
	}{
		{"exact match", 1, 0, 1, 0, 1, 0, ""},
		{"newer server negotiates down", 1, 3, 1, 1, 1, 1, ""},
		{"newer client negotiates down", 1, 1, 1, 4, 1, 1, ""},
		{"unsupported major", 1, 0, 2, 0, 0, 0, ErrUnsupportedVersion},
		{"older major rejected", 2, 0, 1, 0, 0, 0, ErrUnsupportedVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			maj, min, eb := Negotiate(tc.sMajor, tc.sMinor, tc.cMajor, tc.cMinor)
			if tc.wantCode != "" {
				if eb == nil || eb.Code != tc.wantCode {
					t.Fatalf("want code %q, got %v", tc.wantCode, eb)
				}
				return
			}
			if eb != nil {
				t.Fatalf("unexpected error: %v", eb)
			}
			if maj != tc.wantMajor || min != tc.wantMinor {
				t.Fatalf("want %d.%d, got %d.%d", tc.wantMajor, tc.wantMinor, maj, min)
			}
		})
	}
}

// TestUnknownFieldPolicy pins the split decode contract: envelopes tolerate
// additive minor fields; durable payloads reject unknown fields.
func TestUnknownFieldPolicy(t *testing.T) {
	// A newer client's hello carries an unknown additive field. Lenient decode
	// (envelope) must accept it.
	newer := []byte(`{"type":"hello","major":1,"minor":1,"client":"x","future_field":true}`)
	var h Hello
	if err := DecodeLenient(newer, &h); err != nil {
		t.Fatalf("envelope decode must ignore unknown additive fields: %v", err)
	}
	if h.Minor != 1 {
		t.Fatal("known fields must still decode")
	}
	// A durable command payload with an unknown field must be rejected.
	type splitParams struct {
		Workspace string `json:"workspace"`
	}
	var p splitParams
	err := DecodeStrict([]byte(`{"workspace":"w","bogus":1}`), &p)
	if err == nil {
		t.Fatal("durable payload decode must reject unknown fields")
	}
}

// TestErrorTaxonomyFrozen pins the exhaustive error-code set so the contract can
// only change through a reviewed edit here.
func TestErrorTaxonomyFrozen(t *testing.T) {
	want := []ErrorCode{
		"invalid_argument", "not_found", "conflict", "unsupported_version",
		"not_input_lease_holder", "event_gap", "replay_gap", "project_trust_required",
		"hook_grant_required", "hook_grant_stale", "scope_denied", "resource_exhausted",
		"internal",
	}
	if len(AllErrorCodes) != len(want) {
		t.Fatalf("error taxonomy size changed: want %d, got %d", len(want), len(AllErrorCodes))
	}
	for i := range want {
		if AllErrorCodes[i] != want[i] {
			t.Fatalf("error code %d changed: want %q got %q", i, want[i], AllErrorCodes[i])
		}
	}
}
