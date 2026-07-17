package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Error is a located configuration error. Line and Col are 1-based and byte
// oriented, computed against the ORIGINAL JSONC text: the comment stripper
// replaces comment bytes with spaces (keeping newlines), so every decoder
// offset in the stripped text maps 1:1 onto the file the user is looking at.
type Error struct {
	// Path is the config file path when known; Load fills it, Parse leaves it
	// empty.
	Path string
	// Line is the 1-based line of the error in the original text.
	Line int
	// Col is the 1-based byte column of the error in the original text.
	Col int
	// Msg is the human diagnostic. Like the api/v1 error taxonomy, it is a
	// diagnostic, never an automation contract (ADR-0003).
	Msg string
	// Err is the underlying cause, when any.
	Err error
}

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("config: %s:%d:%d: %s", e.Path, e.Line, e.Col, e.Msg)
	}
	return fmt.Sprintf("config: %d:%d: %s", e.Line, e.Col, e.Msg)
}

func (e *Error) Unwrap() error { return e.Err }

// newError builds a located *Error for the byte AT 0-based offset off in src.
func newError(src []byte, off int, msg string, cause error) *Error {
	line, col := lineCol(src, off)
	return &Error{Line: line, Col: col, Msg: msg, Err: cause}
}

// lineCol maps a 0-based byte offset in src to a 1-based line and byte column.
func lineCol(src []byte, off int) (line, col int) {
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	line = 1
	lastNL := -1
	for i := range off {
		if src[i] == '\n' {
			line++
			lastNL = i
		}
	}
	return line, off - lastNL
}

// stripComments returns a copy of src with // line comments and /* block */
// comments replaced by spaces. Comment markers inside JSON strings are left
// untouched (a JSONC comment never starts inside a string), newlines inside
// block comments are preserved, and the output has exactly the input's
// length — so byte offsets, lines, and columns in the stripped text are the
// same as in the original (the property every located error depends on).
//
// Only comments are added on top of strict JSON: trailing commas remain
// invalid and are rejected by the decoder with a located syntax error (spec
// "JSONC configuration with explicit schema version" — JSONC, not JSON5).
// An unterminated block comment is a located error; an unterminated string
// is left for the JSON decoder to reject.
func stripComments(src []byte) ([]byte, error) {
	out := make([]byte, len(src))
	copy(out, src)
	const (
		stCode = iota
		stString
		stLine
		stBlock
	)
	state := stCode
	blockStart := 0
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch state {
		case stString:
			switch c {
			case '\\':
				i++ // skip the escaped byte; a trailing backslash simply ends the scan
			case '"':
				state = stCode
			}
		case stLine:
			if c == '\n' {
				state = stCode
			} else {
				out[i] = ' '
			}
		case stBlock:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				i++
				state = stCode
			} else if c != '\n' {
				out[i] = ' '
			}
		default: // stCode
			switch c {
			case '"':
				state = stString
			case '/':
				if i+1 < len(src) {
					switch src[i+1] {
					case '/':
						out[i], out[i+1] = ' ', ' '
						i++
						state = stLine
					case '*':
						blockStart = i
						out[i], out[i+1] = ' ', ' '
						i++
						state = stBlock
					}
				}
				// A lone '/' stays put: the JSON decoder rejects it with a
				// located syntax error at exactly this offset.
			}
		}
	}
	if state == stBlock {
		return nil, newError(src, blockStart, "unterminated /* block comment", nil)
	}
	return out, nil
}

// decodeJSONC strictly decodes JSONC src into v. It is the config-file twin of
// api/v1.DecodeStrict (ADR-0003 unknown-field policy): a config file is a
// durable boundary, so unknown fields, trailing commas, and trailing data are
// all rejected — and every rejection carries a line:column in the original
// text so the user can fix the file without guessing.
func decodeJSONC(src []byte, v any) error {
	stripped, err := stripComments(src)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(stripped))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return locateDecodeError(src, stripped, v, err)
	}
	// dec.More() is not enough here: it reports false for a trailing '}' or
	// ']'. Scan the remainder ourselves so ANY trailing byte fails closed.
	if off := firstNonSpace(stripped, int(dec.InputOffset())); off >= 0 {
		return newError(src, off, "trailing data after the configuration value", nil)
	}
	return nil
}

// locateDecodeError maps an encoding/json error onto the original text.
// json.SyntaxError and json.UnmarshalTypeError carry byte offsets (valid
// against the original because stripComments preserves length); an unknown-
// field error carries only the field name, so locateUnknownField re-walks the
// token stream against v's schema to find the offending key.
func locateDecodeError(src, stripped []byte, v any, err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		// Offset is one past the offending byte; point at the byte itself.
		return newError(src, clampOffset(stripped, syn.Offset-1), syn.Error(), err)
	case errors.As(err, &typ):
		// Offset is one past the offending value; point at its last byte.
		msg := fmt.Sprintf("invalid value for field %q: cannot decode JSON %s into %s", typ.Field, typ.Value, typ.Type)
		if typ.Field == "" {
			msg = fmt.Sprintf("invalid value: cannot decode JSON %s into %s", typ.Value, typ.Type)
		}
		return newError(src, clampOffset(stripped, typ.Offset-1), msg, err)
	case errors.Is(err, io.EOF):
		return newError(src, 0, "empty configuration: an explicit schema_version is required", err)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return newError(src, len(stripped)-1, "unexpected end of input", err)
	}
	if name, ok := unknownFieldName(err); ok {
		msg := fmt.Sprintf("unknown field %q: unknown fields are rejected at durable boundaries (ADR-0003)", name)
		if off, found := locateUnknownField(stripped, reflect.TypeOf(v), name); found {
			return newError(src, off, msg, err)
		}
		return newError(src, 0, msg, err)
	}
	return fmt.Errorf("config: decode: %w", err)
}

// clampOffset clamps a decoder offset into a valid index of b.
func clampOffset(b []byte, off int64) int {
	i := int(off)
	if i < 0 {
		i = 0
	}
	if i >= len(b) && len(b) > 0 {
		i = len(b) - 1
	}
	return i
}

// firstNonSpace returns the index of the first non-JSON-whitespace byte of b
// at or after from, or -1 when only whitespace remains.
func firstNonSpace(b []byte, from int) int {
	for i := from; i < len(b); i++ {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
		default:
			return i
		}
	}
	return -1
}

// unknownFieldName extracts the field name from an encoding/json
// DisallowUnknownFields error (`json: unknown field "name"`). That error is an
// unexported type with no offset, hence this parse-and-relocate step.
func unknownFieldName(err error) (string, bool) {
	const prefix = `json: unknown field "`
	msg := err.Error()
	if strings.HasPrefix(msg, prefix) && strings.HasSuffix(msg, `"`) {
		return msg[len(prefix) : len(msg)-1], true
	}
	return "", false
}

// knownFieldSets derives, from the decode target's struct tags, the set of
// accepted keys for every object path ("" is the top level, "replay" the
// replay object, and so on). Deriving from the same type the decoder uses
// keeps a single source of truth for what is "known".
func knownFieldSets(t reflect.Type) map[string]map[string]bool {
	m := make(map[string]map[string]bool)
	var walk func(t reflect.Type, path string)
	walk = func(t reflect.Type, path string) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		if _, done := m[path]; done {
			return
		}
		set := make(map[string]bool)
		m[path] = set
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Name
			if tag := f.Tag.Get("json"); tag != "" {
				base, _, _ := strings.Cut(tag, ",")
				if base == "-" && tag == "-" {
					continue
				}
				if base != "" {
					name = base
				}
			}
			set[name] = true
			walk(f.Type, childPath(path, name))
		}
	}
	walk(t, "")
	return m
}

func childPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// fieldKnown mirrors encoding/json's key matching: exact first, then
// case-insensitive fallback, so this locator never flags a key the decoder
// would in fact have accepted.
func fieldKnown(set map[string]bool, key string) bool {
	if set[key] {
		return true
	}
	for k := range set {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// locateUnknownField token-walks stripped (already known to be well-formed:
// Decode buffers the full value before unmarshalling) tracking the object
// path, and returns the byte offset of the opening quote of the first key
// named name that the target schema does not accept at its path. Path
// tracking matters: a key can be known in one object and unknown in another.
func locateUnknownField(stripped []byte, target reflect.Type, name string) (int, bool) {
	known := knownFieldSets(target)
	dec := json.NewDecoder(bytes.NewReader(stripped))
	type frame struct {
		object  bool   // '{' frame (vs '[')
		path    string // dot-joined key path of this container
		key     string // last key read, pending its value
		wantKey bool   // next string token is a key
	}
	var stack []frame
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].object {
			stack[n-1].wantKey = true
		}
	}
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return 0, false
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				path := ""
				if n := len(stack); n > 0 {
					parent := stack[n-1]
					if parent.object {
						path = childPath(parent.path, parent.key)
					} else {
						path = childPath(parent.path, "[]")
					}
				}
				stack = append(stack, frame{object: t == '{', path: path, wantKey: t == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				valueDone()
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].wantKey {
				fr := &stack[n-1]
				fr.key = t
				fr.wantKey = false
				if fields, checked := known[fr.path]; checked && t == name && !fieldKnown(fields, t) {
					// The key token starts at the first '"' after the previous
					// token (only commas and whitespace can intervene).
					if rel := bytes.IndexByte(stripped[before:], '"'); rel >= 0 {
						return int(before) + rel, true
					}
					return 0, false
				}
			} else {
				valueDone()
			}
		default: // number, bool, or null value
			valueDone()
		}
	}
}
