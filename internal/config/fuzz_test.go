package config

import "testing"

// FuzzDecodeJSONC pins two properties of the durable-boundary decoder:
// it never panics on arbitrary bytes, and when the comment stripper accepts
// an input it preserves length and every newline position, so line:column
// locations computed against the original text stay exact.
func FuzzDecodeJSONC(f *testing.F) {
	seeds := []string{
		`{"schema_version": 1}`,
		"// c\n{\n  \"schema_version\": 1, /* x */\n  \"log_level\": \"debug\"\n}\n",
		"{\n  \"schema_version\": 1,\n}",
		`{"schema_version": 1, "bogus": true}`,
		`{"schema_version": 1, "log_level": "http://not//a//comment"}`,
		"{ /* unterminated",
		`{"replay": {"per_surface_bytes": -9223372036854775808}}`,
		"\xff\xfe{",
		"",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Bound the work per input; real config files are tiny and the frame
		// discipline elsewhere in Amux caps hostile inputs before allocation.
		if len(data) > 1<<20 {
			t.Skip("input beyond 1 MiB bound")
		}
		stripped, err := stripComments(data)
		if err == nil {
			if len(stripped) != len(data) {
				t.Fatalf("stripComments changed length: %d != %d", len(stripped), len(data))
			}
			for i := range data {
				if (data[i] == '\n') != (stripped[i] == '\n') {
					t.Fatalf("stripComments moved a newline at offset %d", i)
				}
			}
		}
		_, _ = Parse(data) // must never panic
	})
}
