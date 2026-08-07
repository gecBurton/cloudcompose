package compiler

import (
	"fmt"
	"strconv"
	"strings"
)

// pyJSON renders a value the way Python's json.dumps(obj) does with its
// default arguments: insertion-order keys (never sorted), and ", "/": "
// separators rather than encoding/json's compact no-space output. This
// matters because every *_policy, *_definitions, and secret_string field in
// this package holds one of these embedded JSON strings verbatim -- and the
// bar for this phase is byte-identical output against Python, not merely
// equivalent-once-parsed output.
//
// encoding/json's map[string]any marshals with keys sorted
// alphabetically, which Python's json.dumps(dict) never does (Python dicts
// preserve insertion order, and none of generator.py's inline dict literals
// happen to be alphabetical) -- confirmed as a real, not theoretical,
// divergence by diffing actual Go output against actual Python output for
// the hello example's assume_role_policy field (2026-08-06). PyOrdered
// exists specifically so every inline policy/definition literal in this
// package can state its keys in the same order Python's dict literal did,
// rather than accepting whatever order a map[string]any happens to
// (alphabetically) produce.
type PyOrdered []PyPair

// PyPair is one key/value entry in a PyOrdered object, in the order it
// should be emitted.
type PyPair struct {
	Key   string
	Value any
}

func p(key string, value any) PyPair { return PyPair{Key: key, Value: value} }

// PyDumps renders value as Python's json.dumps(value) would: PyOrdered
// values keep their stated key order, slices render as JSON arrays in
// order, and everything else falls back to Go's own encoding for scalars
// and already-ordered structures (structs, which encoding/json renders in
// field-declaration order already matching the corresponding Pydantic
// model's declaration order).
func PyDumps(value any) string {
	var b strings.Builder
	writePyValue(&b, value)
	return b.String()
}

// PyDumpsIndent renders value as Python's json.dumps(value, indent=n)
// would: multi-line, n spaces per nesting level, ", "-separated items
// becoming one-per-line with a bare "," at end of line (no trailing
// space), and "key": value using ": " same as the compact form.
//
// Needed for generator_azure.go specifically: unlike generator.py's AWS
// output, generator_azure.py's json.dumps(terraform, indent=2) has no
// sort_keys=True at all -- every level of the *entire* document, not just
// embedded policy strings, has to preserve Python's insertion order, so
// this can't reuse PyDumps' compact single-line rendering and rely on
// encoding/json.Indent to add the newlines afterward (that would still
// need the compact form to already be in the right key order first, which
// it is, but Indent alone doesn't change spacing between "," and the next
// key onto separate lines the way Python's indent mode does -- confirmed
// by diffing actual output against the golden files' exact formatting,
// not assumed).
func PyDumpsIndent(value any, indent int) string {
	var b strings.Builder
	writePyValueIndent(&b, value, indent, 0)
	return b.String()
}

func writePyValueIndent(b *strings.Builder, value any, indent, depth int) {
	switch v := value.(type) {
	case PyOrdered:
		writePyObjectIndent(b, v, indent, depth)
	case []PyOrdered:
		writePyArrayIndent(b, len(v), indent, depth, func(i int) any { return v[i] })
	case []any:
		writePyArrayIndent(b, len(v), indent, depth, func(i int) any { return v[i] })
	case []string:
		writePyArrayIndent(b, len(v), indent, depth, func(i int) any { return v[i] })
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		obj := make(PyOrdered, 0, len(keys))
		for _, k := range keys {
			obj = append(obj, p(k, v[k]))
		}
		writePyObjectIndent(b, obj, indent, depth)
	case string, bool, int, int64, float64, PyFloat, nil:
		writePyValue(b, value)
	default:
		// Structs, pointers-to-struct, and any other map/slice shape not
		// already handled above (e.g. map[string]SomeStruct,
		// []SomeStruct): converted through reflection into PyOrdered/[]any
		// first, then re-dispatched, so struct field declaration order
		// (matching the Pydantic model it mirrors) is preserved rather
		// than lost the way round-tripping through encoding/json would.
		//
		// structToPyOrdered's own default branch dereferences any pointer
		// down to its underlying scalar via reflection (see
		// pyordered_reflect.go) before returning, so recursing back into
		// writePyValueIndent here terminates: the dereferenced value
		// matches one of the concrete cases above on the next call,
		// rather than falling to this default branch again. A version of
		// structToPyOrdered that returned the still-pointer value here
		// instead caused a real, confirmed stack overflow (2026-08-06),
		// not a theoretical risk -- the fix lives in
		// pyordered_reflect.go, not here, but this comment documents why
		// this recursive call is safe rather than assuming it.
		writePyValueIndent(b, structToPyOrdered(value), indent, depth)
	}
}

func writePyObjectIndent(b *strings.Builder, obj PyOrdered, indent, depth int) {
	if len(obj) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	childIndent := strings.Repeat(" ", indent*(depth+1))
	for i, pair := range obj {
		b.WriteString(childIndent)
		writePyString(b, pair.Key)
		b.WriteString(": ")
		writePyValueIndent(b, pair.Value, indent, depth+1)
		if i < len(obj)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat(" ", indent*depth))
	b.WriteByte('}')
}

func writePyArrayIndent(b *strings.Builder, length, indent, depth int, at func(int) any) {
	if length == 0 {
		b.WriteString("[]")
		return
	}
	b.WriteString("[\n")
	childIndent := strings.Repeat(" ", indent*(depth+1))
	for i := 0; i < length; i++ {
		b.WriteString(childIndent)
		writePyValueIndent(b, at(i), indent, depth+1)
		if i < length-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat(" ", indent*depth))
	b.WriteByte(']')
}

func writePyValue(b *strings.Builder, value any) {
	switch v := value.(type) {
	case PyOrdered:
		writePyObject(b, v)
	case []PyOrdered:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyObject(b, item)
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyValue(b, item)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyValue(b, item)
		}
		b.WriteByte(']')
	case map[string]any:
		// Only used where key order genuinely doesn't matter (there are no
		// such call sites left deliberately; kept as a safety net rather
		// than removed, since a future caller reaching for a bare map
		// literal should get *some* defined behavior, not a compile error
		// discovered far from here).
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		obj := make(PyOrdered, 0, len(keys))
		for _, k := range keys {
			obj = append(obj, p(k, v[k]))
		}
		writePyObject(b, obj)
	case string:
		writePyString(b, v)
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case float64:
		b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	case nil:
		b.WriteString("null")
	default:
		// Fall back to fmt for anything unexpected rather than panicking;
		// every real call site in this package passes PyOrdered/string/
		// []string/bool/int, so this branch existing at all would itself
		// be worth investigating if ever hit.
		fmt.Fprintf(b, "%v", v)
	}
}

func writePyObject(b *strings.Builder, obj PyOrdered) {
	b.WriteByte('{')
	for i, pair := range obj {
		if i > 0 {
			b.WriteString(", ")
		}
		writePyString(b, pair.Key)
		b.WriteString(": ")
		writePyValue(b, pair.Value)
	}
	b.WriteByte('}')
}

// writePyString escapes a string exactly the way Python's json.dumps does
// by default: ensure_ascii=True, so anything outside printable ASCII
// becomes a \uXXXX escape, and "<"/">" are never escaped (Python's json
// module has no HTML-safe mode to begin with, unlike encoding/json's
// default).
func writePyString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r > 0x7e {
				if r > 0xffff {
					// Outside the BMP: Python emits a UTF-16 surrogate pair.
					r1, r2 := utf16Surrogates(r)
					fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
				} else {
					fmt.Fprintf(b, `\u%04x`, r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func utf16Surrogates(r rune) (rune, rune) {
	r -= 0x10000
	return 0xd800 + (r >> 10), 0xdc00 + (r & 0x3ff)
}

// PyFloat marshals as Python's json.dumps renders a float: always with a
// decimal point, even for a whole number (70.0, never 70). Plain float64
// values round-tripped through encoding/json lose that distinction --
// Go's encoder treats 70.0 and the integer 70 identically once unmarshalled
// back into a map[string]any, rendering both as "70" -- a real, confirmed
// divergence caught by diffing actual output for the production-stack
// example's autoscaling policy against Python's (2026-08-06), not a
// theoretical one. Every Terraform attribute that Python's model declares
// as a float (target_value on an autoscaling policy, being the one example
// that exists today) needs to go through this type rather than a bare
// float64 to survive the round trip byte-identically.
type PyFloat float64

func (f PyFloat) MarshalJSON() ([]byte, error) {
	s := strconv.FormatFloat(float64(f), 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return []byte(s), nil
}
