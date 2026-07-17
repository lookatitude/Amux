package config

import (
	"errors"
	"strings"
	"testing"
)

// tDoc is a decode-layer target unrelated to the Config schema, so the JSONC
// decoder tests stay independent of schema validation.
type tDoc struct {
	URL string `json:"url"`
	N   int    `json:"n"`
	Sub *tSub  `json:"sub"`
}

type tSub struct {
	A int `json:"a"`
}

func TestStripComments(t *testing.T) {
	sp := func(n int) string { return strings.Repeat(" ", n) }
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no comments", `{"a": 1}`, `{"a": 1}`},
		{"trailing line comment without newline", `{"a": 1} // tail`, `{"a": 1} ` + sp(7)},
		{"leading line comment", "// head\n{}", sp(7) + "\n{}"},
		{"block comment", "{ /* x */ }", "{ " + sp(7) + " }"},
		{"block comment preserves newlines", "/* a\nb */{}", sp(4) + "\n" + sp(4) + "{}"},
		{"line-comment slashes inside string", `{"url": "http://x"}`, `{"url": "http://x"}`},
		{"block marker inside string", `{"a": "/* keep */"}`, `{"a": "/* keep */"}`},
		{"escaped quote keeps string open", `{"a": "\" // in string"}`, `{"a": "\" // in string"}`},
		{"comment after escaped backslash string", `{"a": "\\"} // c`, `{"a": "\\"} ` + sp(4)},
		{"lone slash is left for the JSON decoder", `{"a": 1 / }`, `{"a": 1 / }`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stripComments([]byte(tc.in))
			if err != nil {
				t.Fatalf("stripComments(%q): unexpected error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Fatalf("stripComments(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
			if len(got) != len(tc.in) {
				t.Fatalf("stripComments changed length: %d != %d", len(got), len(tc.in))
			}
		})
	}
}

func TestStripCommentsUnterminatedBlock(t *testing.T) {
	_, err := stripComments([]byte("{\n  \"a\": 1\n} /* oops"))
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if ce.Line != 3 || ce.Col != 3 {
		t.Fatalf("unterminated block comment located at %d:%d, want 3:3 (%v)", ce.Line, ce.Col, err)
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("error should mention the unterminated comment: %v", err)
	}
}

func TestDecodeJSONCValid(t *testing.T) {
	src := `
// leading comment
{
  /* block
     comment */
  "url": "http://example.com/a", // slashes in the string survive
  "n": 3,
  "sub": { "a": 1 }
}
`
	var d tDoc
	if err := decodeJSONC([]byte(src), &d); err != nil {
		t.Fatalf("decodeJSONC: %v", err)
	}
	if d.URL != "http://example.com/a" || d.N != 3 || d.Sub == nil || d.Sub.A != 1 {
		t.Fatalf("decoded %+v", d)
	}
}

func TestDecodeJSONCLocatedErrors(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		line   int
		col    int
		substr string
	}{
		{
			name: "trailing comma rejected",
			src:  "{\n  \"n\": 1,\n}\n",
			line: 3, col: 1,
			substr: "invalid character",
		},
		{
			name: "unknown top-level field",
			src:  "{\n  \"n\": 1,\n  \"nn\": 2\n}\n",
			line: 3, col: 3,
			substr: `unknown field "nn"`,
		},
		{
			name: "unknown nested field after comments",
			src:  "// c\n{\n  /* c */\n  \"sub\": {\n    \"a\": 1,\n    \"bogus\": true\n  }\n}\n",
			line: 6, col: 5,
			substr: `unknown field "bogus"`,
		},
		{
			name: "unknown nested field shadowed by known top-level name",
			src:  "{\n  \"sub\": {\"a\": 1, \"url\": 2},\n  \"url\": \"x\"\n}\n",
			line: 2, col: 19,
			substr: `unknown field "url"`,
		},
		{
			name: "wrong value type",
			src:  "{\n  \"n\": \"1\"\n}\n",
			line: 2, col: 10,
			substr: `"n"`,
		},
		{
			name: "truncated input",
			src:  "{\n  \"n\": 1",
			line: 2, col: 8,
			substr: "unexpected end",
		},
		{
			name: "stray slash",
			src:  `{"n": 1 / }`,
			line: 1, col: 9,
			substr: "invalid character",
		},
		{
			name: "trailing second value",
			src:  "{\"n\": 1}\n{\"n\": 2}\n",
			line: 2, col: 1,
			substr: "trailing data",
		},
		{
			name: "trailing close brace",
			src:  `{"n": 1}}`,
			line: 1, col: 9,
			substr: "trailing data",
		},
		{
			name: "comment-only input",
			src:  "// only a comment\n",
			line: 1, col: 1,
			substr: "empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d tDoc
			err := decodeJSONC([]byte(tc.src), &d)
			if err == nil {
				t.Fatalf("decodeJSONC(%q): want error, got nil", tc.src)
			}
			var ce *Error
			if !errors.As(err, &ce) {
				t.Fatalf("want *Error, got %T: %v", err, err)
			}
			if ce.Line != tc.line || ce.Col != tc.col {
				t.Fatalf("error located at %d:%d, want %d:%d (%v)", ce.Line, ce.Col, tc.line, tc.col, err)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.substr)
			}
		})
	}
}
