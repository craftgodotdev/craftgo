package server

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
)

// contentTypeJSON is the Content-Type of the framework's JSON responses.
const contentTypeJSON = "application/json; charset=utf-8"

// JSONCodec encodes and decodes the framework's JSON; install one with [SetGlobalJSONCodec].
type JSONCodec interface {
	Encode(w io.Writer, v any) error
	Decode(r io.Reader, v any) error
}

// StrictDecoder is the optional codec method [SetStrictJSON] needs: DecodeStrict decodes like
// Decode but fails on an unknown field ("<field>: unknown field") and on data after the JSON
// value (see [TrailingData]).
type StrictDecoder interface {
	DecodeStrict(r io.Reader, v any) error
}

// defaultCodec is the encoding/json codec; it reports a value of the wrong JSON type as
// "<json path>: <reason>".
type defaultCodec struct{}

func (defaultCodec) Encode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// Decode ignores unknown fields and anything after the value.
func (defaultCodec) Decode(r io.Reader, v any) error {
	return wireDecodeError(json.NewDecoder(r).Decode(v), v)
}

func (defaultCodec) DecodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if field, ok := unknownFieldOf(err); ok {
			return fmt.Errorf("%s: unknown field", field)
		}
		return wireDecodeError(err, v)
	}
	return TrailingData(dec.Buffered(), r)
}

// decodeError is a decode failure worded for the client; it wraps the decoder's error.
type decodeError struct {
	msg string
	err error
}

func (e *decodeError) Error() string { return e.msg }
func (e *decodeError) Unwrap() error { return e.err }

// wireDecodeError rewords a *json.UnmarshalTypeError in err, which names Go structs and types,
// as "<json path>: <reason>", the path into v and "body" at its root; other errors pass.
func wireDecodeError(err error, v any) error {
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return err
	}
	path := wirePath(reflect.TypeOf(v), te)
	if path == "" {
		path = "body"
	}
	return &decodeError{msg: path + ": " + mismatch(te), err: err}
}

// wirePath spells te.Field, encoding/json's path into t, by JSON names alone: the Go names of
// the embedded structs whose fields an object holds as its own drop out.
func wirePath(t reflect.Type, te *json.UnmarshalTypeError) string {
	if te.Field == "" {
		return ""
	}
	p := pathReader{segs: strings.Split(te.Field, "."), last: te.Struct}
	p.reads = make(map[pathState]pathRead, len(p.segs))
	root := structOf(t)
	if root == nil || !p.read(pathState{obj: root, cur: root}).ok {
		return strings.Join(greedyPath(t, p.segs), ".")
	}
	names := make([]string, 0, len(p.segs))
	for st := (pathState{obj: root, cur: root}); ; {
		r := p.reads[st]
		if r.key {
			names = append(names, p.segs[st.pos])
		}
		if st.pos == len(p.segs)-1 {
			return strings.Join(names, ".")
		}
		st = r.next
	}
}

// pathReader reads encoding/json's error path: at each object, the Go names of the embedded
// structs it went through, then the field's JSON key.
type pathReader struct {
	segs  []string
	last  string // the Name of the struct type of the innermost object, UnmarshalTypeError.Struct
	reads map[pathState]pathRead
}

// pathState is segment pos read inside cur, which is obj, the struct type of the segment's
// object, or a struct obj embeds.
type pathState struct {
	obj, cur reflect.Type
	pos      int
}

// pathRead is how the segments from a pathState on read: ok when they all do, last when the
// innermost object's type is the error's struct, key when the segment is a JSON key.
type pathRead struct {
	ok, last, key bool
	next          pathState
}

// read returns how the segments from st on read, preferring a reading whose innermost object
// is the error's struct, then an embedded struct's name over a key.
func (p *pathReader) read(st pathState) pathRead {
	if r, ok := p.reads[st]; ok {
		return r
	}
	var best pathRead
	seg, final := p.segs[st.pos], st.pos == len(p.segs)-1
	if e, ok := embeddedStruct(st.cur, seg); ok && !final {
		next := pathState{obj: st.obj, cur: indirect(e), pos: st.pos + 1}
		if r := p.read(next); r.ok {
			best = pathRead{ok: true, last: r.last, next: next}
		}
	}
	if f, ok := jsonField(st.cur, seg); ok && !best.last {
		r := pathRead{key: true}
		if final {
			r.ok, r.last = true, st.obj.Name() == p.last
		} else if s := structOf(f); s != nil {
			r.next = pathState{obj: s, cur: s, pos: st.pos + 1}
			nr := p.read(r.next)
			r.ok, r.last = nr.ok, nr.last
		}
		if r.ok && (!best.ok || r.last) {
			best = r
		}
	}
	p.reads[st] = best
	return best
}

// greedyPath spells segs, a path into t, when no reading of them reaches the end: a segment is
// an embedded struct's name before it is a key, and an unknown one keeps the rest as written.
func greedyPath(t reflect.Type, segs []string) []string {
	names := make([]string, 0, len(segs))
	for i, seg := range segs {
		s := structOf(t)
		if s == nil {
			return append(names, segs[i:]...)
		}
		if e, ok := embeddedStruct(s, seg); ok {
			t = e
			continue
		}
		names = append(names, seg)
		var ok bool
		if t, ok = jsonField(s, seg); !ok {
			return append(names, segs[i+1:]...)
		}
	}
	return names
}

// structOf returns the struct type t holds through pointers, slices, arrays and maps, or nil.
func structOf(t reflect.Type) reflect.Type {
	for t != nil {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
		case reflect.Struct:
			return t
		default:
			return nil
		}
	}
	return nil
}

// jsonField returns the type of t's field whose JSON key is name, of the fields encoding/json
// decodes into.
func jsonField(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		sf := t.Field(i)
		if isFlattened(sf) || !decodesInto(sf) {
			continue
		}
		if tag := jsonTagName(sf); tag == name || tag == "" && sf.Name == name {
			return sf.Type, true
		}
	}
	return nil, false
}

// decodesInto reports whether encoding/json decodes into sf: it is not tagged "-", and is
// exported or an embedded struct.
func decodesInto(sf reflect.StructField) bool {
	if sf.Tag.Get("json") == "-" {
		return false
	}
	return sf.IsExported() || sf.Anonymous && indirect(sf.Type).Kind() == reflect.Struct
}

// embeddedStruct returns the type of t's flattened embedded struct called name.
func embeddedStruct(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		if sf := t.Field(i); sf.Name == name && isFlattened(sf) {
			return sf.Type, true
		}
	}
	return nil, false
}

// isFlattened reports whether sf is an embedded struct without a JSON name, whose fields the
// object holds as its own.
func isFlattened(sf reflect.StructField) bool {
	return sf.Anonymous && jsonTagName(sf) == "" && indirect(sf.Type).Kind() == reflect.Struct
}

// jsonTagName returns the name sf's json tag gives it, or "".
func jsonTagName(sf reflect.StructField) string {
	name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
	return name
}

// indirect returns the type t points to, through any number of pointers.
func indirect(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// mismatch words te: the JSON type the field takes and the value the body held.
func mismatch(te *json.UnmarshalTypeError) string {
	want := jsonKind(te.Type)
	if lit, ok := strings.CutPrefix(te.Value, "number "); ok {
		switch {
		case !isJSONNumber(lit):
			article := "a "
			if want == "integer" {
				article = "an "
			}
			return strconv.Quote(lit) + " is not " + article + want
		case want == "number", want == "integer" && !strings.ContainsAny(lit, ".eE"):
			return lit + " is out of range"
		}
	}
	got := te.Value
	if got == "bool" {
		got = "boolean"
	}
	return "expected " + want + ", got " + got
}

// isJSONNumber reports whether s is a JSON number literal.
func isJSONNumber(s string) bool {
	return s != "" && (s[0] == '-' || s[0] >= '0' && s[0] <= '9') && json.Valid([]byte(s))
}

// textUnmarshaler is the interface of a type encoding/json decodes from a JSON string.
var textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()

// jsonKind names the JSON type a value of t decodes from.
func jsonKind(t reflect.Type) string {
	t = indirect(t)
	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		return "string"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string"
		}
		return "array"
	case reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return "value"
}

// unknownFieldOf extracts the name from encoding/json's `json: unknown field "name"` error.
func unknownFieldOf(err error) (string, bool) {
	const prefix = `json: unknown field `
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	name, uerr := strconv.Unquote(msg[len(prefix):])
	return name, uerr == nil
}

// TrailingData fails when anything but JSON whitespace remains in buffered (a decoder's
// read-ahead) or rest; a [StrictDecoder] calls it after decoding a value.
func TrailingData(buffered, rest io.Reader) error {
	var b [1]byte
	for _, src := range [...]io.Reader{buffered, rest} {
		for {
			n, err := src.Read(b[:])
			if n == 1 && !isJSONSpace(b[0]) {
				return errors.New("body: unexpected data after the JSON value")
			}
			if err != nil {
				break
			}
		}
	}
	return nil
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// strictCodec is the installed codec with Decode routed through its
// DecodeStrict.
type strictCodec struct {
	JSONCodec
	strict StrictDecoder
}

func (c strictCodec) Decode(r io.Reader, v any) error { return c.strict.DecodeStrict(r, v) }

// codecHolder is the installed codec plus the strict switch. active is
// what [JSON] hands out: base itself, or base behind strictCodec.
type codecHolder struct {
	base   JSONCodec
	strict bool
	active JSONCodec
}

// globalJSON holds the current codecHolder.
var globalJSON atomic.Pointer[codecHolder]

func init() { globalJSON.Store(&codecHolder{base: defaultCodec{}, active: defaultCodec{}}) }

func currentCodec() *codecHolder { return globalJSON.Load() }

// installCodec stores base with the strict switch, refusing a strict
// setting base cannot honour.
func installCodec(base JSONCodec, strict bool) error {
	h := &codecHolder{base: base, strict: strict, active: base}
	if strict {
		sd, ok := base.(StrictDecoder)
		if !ok {
			return fmt.Errorf("strict JSON is on but codec %T has no DecodeStrict", base)
		}
		h.active = strictCodec{JSONCodec: base, strict: sd}
	}
	globalJSON.Store(h)
	return nil
}

// SetGlobalJSONCodec installs c as the codec [JSON] returns; nil restores the built-in one.
// While strict JSON is on, c must implement [StrictDecoder], or the call fails and changes nothing.
func SetGlobalJSONCodec(c JSONCodec) error {
	if c == nil {
		c = defaultCodec{}
	}
	return installCodec(c, currentCodec().strict)
}

// SetStrictJSON switches strict decoding, off by default: while on, [JSON] decodes through
// the codec's [StrictDecoder]. Turning it on fails, changing nothing, when the codec has none.
func SetStrictJSON(strict bool) error {
	return installCodec(currentCodec().base, strict)
}

// JSON returns the codec in effect: the one [SetGlobalJSONCodec] installed, encoding/json by
// default, decoding strictly while [SetStrictJSON] is on.
func JSON() JSONCodec { return currentCodec().active }
