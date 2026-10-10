package server

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// defaultCodec is the encoding/json codec; it words a body it cannot decode for the client, as
// "<json path>: <reason>" for a value the body holds.
type defaultCodec struct{}

func (defaultCodec) Encode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// Decode ignores unknown fields and anything after the value.
func (defaultCodec) Decode(r io.Reader, v any) error { return decodeBody(r, v, false) }

func (defaultCodec) DecodeStrict(r io.Reader, v any) error { return decodeBody(r, v, true) }

// decodeBody decodes the first JSON value of r into v, failing on an unknown field and on data
// after the value when strict. When v can hold a [selfDecoding] value, whose failure names no
// path, the decoder reads through a recorder, so that the value can be found again.
func decodeBody(r io.Reader, v any, strict bool) error {
	src := r
	var rec *bodyRecorder
	if t := reflect.TypeOf(v); t != nil && holdsSelfDecoding(t) {
		rec = newBodyRecorder(r)
		defer rec.release()
		src = rec
	}
	dec := json.NewDecoder(src)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		return decodeFailure(err, v, strict, rec)
	}
	if strict {
		return TrailingData(dec.Buffered(), r)
	}
	return nil
}

// decodeFailure words err, the decoder's failure to decode a body into v, for the client and
// keeps it in the chain: an empty body is required, and a value of the wrong JSON type, or one
// its own decoder refuses, is named by its JSON path. Malformed JSON and a failed read pass. rec
// holds what the decoder read, or is nil.
func decodeFailure(err error, v any, strict bool, rec *bodyRecorder) error {
	if errors.Is(err, io.EOF) {
		return &decodeError{msg: "body: required", err: err}
	}
	if field, ok := unknownFieldOf(err); ok && strict {
		return fmt.Errorf("%s: unknown field", field)
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return wireDecodeError(err, te, v)
	}
	var syntax *json.SyntaxError
	if rec == nil || errors.Is(err, rec.err) || errors.As(err, &syntax) || errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if path, why, ok := refusedValue(rec.buf.Bytes(), reflect.TypeOf(v), err); ok {
		return &decodeError{msg: path + ": " + why, err: err}
	}
	return err
}

// decodeError is a decode failure worded for the client; it wraps the decoder's error.
type decodeError struct {
	msg string
	err error
}

func (e *decodeError) Error() string { return e.msg }
func (e *decodeError) Unwrap() error { return e.err }

// wireDecodeError words te, the *json.UnmarshalTypeError in err, which names Go structs and
// types, as "<json path>: <reason>", the path into v and "body" at its root.
func wireDecodeError(err error, te *json.UnmarshalTypeError, v any) error {
	path := wirePath(reflect.TypeOf(v), te)
	if path == "" {
		path = "body"
	}
	reason := mismatch(te)
	if errors.As(err, new(base64.CorruptInputError)) {
		reason = "not a valid base64"
	}
	return &decodeError{msg: path + ": " + reason, err: err}
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
	for sf := range t.Fields() {
		if isFlattened(sf) || !decodesInto(sf) {
			continue
		}
		if tag := jsonTagName(sf); tag == name || tag == "" && sf.Name == name {
			return sf.Type, true
		}
	}
	return nil, false
}

// embeddedStruct returns the type of t's flattened embedded struct called name.
func embeddedStruct(t reflect.Type, name string) (reflect.Type, bool) {
	for sf := range t.Fields() {
		if sf.Name == name && isFlattened(sf) {
			return sf.Type, true
		}
	}
	return nil, false
}

// bodyRecorder keeps a copy of what is read from r, and the error of the first failed read.
type bodyRecorder struct {
	r   io.Reader
	buf *bytes.Buffer
	err error
}

// recordBufs holds the buffers of the bodyRecorders.
var recordBufs = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func newBodyRecorder(r io.Reader) *bodyRecorder {
	return &bodyRecorder{r: r, buf: recordBufs.Get().(*bytes.Buffer)}
}

func (b *bodyRecorder) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.buf.Write(p[:n])
	if err != nil && err != io.EOF && b.err == nil {
		b.err = err
	}
	return n, err
}

// release returns the buffer to the pool unless it grew past 64 KiB.
func (b *bodyRecorder) release() {
	if b.buf.Cap() <= 64<<10 {
		b.buf.Reset()
		recordBufs.Put(b.buf)
	}
	b.buf = nil
}

var (
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	timeType        = reflect.TypeFor[time.Time]()
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
)

// wirePackage is the path of the package of wire.Raw, which takes any JSON value as it is.
const wirePackage = "github.com/craftgodotdev/craftgo/pkg/wire"

// decodesItself reports whether encoding/json hands a value of type t to t's own UnmarshalJSON
// or UnmarshalText.
func decodesItself(t reflect.Type) bool {
	p := reflect.PointerTo(t)
	return p.Implements(jsonUnmarshaler) || p.Implements(textUnmarshaler)
}

// isBase64 reports whether encoding/json decodes a value of type t from base64.
func isBase64(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 && !decodesItself(t)
}

// selfDecoding reports whether a value of type t is decoded by code that fails without naming
// where, such as a datetime's own method or base64 bytes; a raw value, which takes any JSON
// value, is not.
func selfDecoding(t reflect.Type) bool {
	if t == rawMessageType || t.PkgPath() == wirePackage && t.Name() == "Raw" {
		return false
	}
	return decodesItself(t) || isBase64(t)
}

// selfDecodingHolders caches [holdsSelfDecoding] by type.
var selfDecodingHolders sync.Map

// holdsSelfDecoding reports whether a value of type t is, or holds, a [selfDecoding] value.
func holdsSelfDecoding(t reflect.Type) bool {
	if holds, ok := selfDecodingHolders.Load(t); ok {
		return holds.(bool)
	}
	holds := holdsSelfDecodingIn(t, map[reflect.Type]bool{})
	selfDecodingHolders.Store(t, holds)
	return holds
}

// holdsSelfDecodingIn is [holdsSelfDecoding] for a t not among seen.
func holdsSelfDecodingIn(t reflect.Type, seen map[reflect.Type]bool) bool {
	t = indirect(t)
	if seen[t] {
		return false
	}
	seen[t] = true
	if selfDecoding(t) {
		return true
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return holdsSelfDecodingIn(t.Elem(), seen)
	case reflect.Struct:
		for _, f := range objectFields(t) {
			if holdsSelfDecodingIn(f.typ, seen) {
				return true
			}
		}
	}
	return false
}

// refusedValue reads data, the JSON value decoded into a value of type t, for the first
// [selfDecoding] value whose decoding fails as want did, and returns its JSON path, "body" at the
// root, and why it fails.
func refusedValue(data []byte, t reflect.Type, want error) (path, why string, ok bool) {
	f := valueFinder{dec: json.NewDecoder(bytes.NewReader(data)), want: want.Error(), fields: fieldCache{}}
	if f.value(t); f.why == "" {
		return "", "", false
	}
	if len(f.path) == 0 {
		return "body", f.why, true
	}
	return strings.Join(f.path, "."), f.why, true
}

// valueFinder walks a JSON document beside the Go type it decodes into.
type valueFinder struct {
	dec    *json.Decoder
	want   string // the decoding failure to find
	fields fieldCache
	path   []string // the JSON names of the struct fields down to the value read
	why    string   // why the value found fails, once found
}

// value reads the next value as one of type t and reports whether the walk stops: at the value
// to find, or where the document ends.
func (f *valueFinder) value(t reflect.Type) bool {
	t = indirect(t)
	switch {
	case !holdsSelfDecoding(t):
		return f.skip()
	case selfDecoding(t):
		return f.selfDecoded(t)
	case t.Kind() == reflect.Struct:
		return f.object(func(key string) bool {
			fd, ok := f.fields.lookup(t, key)
			if !ok {
				return f.skip()
			}
			f.path = append(f.path, fd.name)
			if f.value(fd.typ) {
				return true
			}
			f.path = f.path[:len(f.path)-1]
			return false
		})
	case t.Kind() == reflect.Map:
		return f.object(func(string) bool { return f.value(t.Elem()) })
	}
	return f.array(t.Elem())
}

// selfDecoded reads a [selfDecoding] value of type t and decodes it alone, stopping the walk when
// it fails as the document did.
func (f *valueFinder) selfDecoded(t reflect.Type) bool {
	var raw json.RawMessage
	if f.dec.Decode(&raw) != nil {
		return true
	}
	err := json.Unmarshal(raw, reflect.New(t).Interface())
	if err == nil || err.Error() != f.want {
		return false
	}
	f.why = refusal(t, raw, err)
	return true
}

// object reads an object, calling each at each member's value with its key until each stops the
// walk; any other value is read whole.
func (f *valueFinder) object(each func(key string) bool) bool {
	if opened, stop := f.open('{'); !opened {
		return stop
	}
	for f.dec.More() {
		tok, err := f.dec.Token()
		key, isKey := tok.(string)
		if err != nil || !isKey || each(key) {
			return true
		}
	}
	_, err := f.dec.Token()
	return err != nil
}

// array reads an array of elem values; any other value is read whole.
func (f *valueFinder) array(elem reflect.Type) bool {
	if opened, stop := f.open('['); !opened {
		return stop
	}
	for f.dec.More() {
		if f.value(elem) {
			return true
		}
	}
	_, err := f.dec.Token()
	return err != nil
}

// open reads the first token of the next value, reporting whether it is want; another value is
// read whole. stop reports the end of the document.
func (f *valueFinder) open(want json.Delim) (opened, stop bool) {
	tok, err := f.dec.Token()
	if err != nil {
		return false, true
	}
	if d, ok := tok.(json.Delim); ok {
		if d == want {
			return true, false
		}
		for depth := 1; depth > 0; {
			if tok, err = f.dec.Token(); err != nil {
				return false, true
			}
			switch tok {
			case json.Delim('{'), json.Delim('['):
				depth++
			case json.Delim('}'), json.Delim(']'):
				depth--
			}
		}
	}
	return false, false
}

// skip reads the next value whole.
func (f *valueFinder) skip() bool {
	var raw json.RawMessage
	return f.dec.Decode(&raw) != nil
}

// refusal words why raw, a value of type t, fails to decode with err: a datetime and base64
// bytes as the format checks word them, another type in its own words.
func refusal(t reflect.Type, raw []byte, err error) string {
	switch {
	case t == timeType && raw[0] != '"':
		return "expected string, got " + rawKind(raw)
	case t == timeType:
		return "not a valid RFC 3339 datetime"
	case isBase64(t):
		return "not a valid base64"
	}
	return err.Error()
}

// rawKind names the JSON type of raw, a JSON value.
func rawKind(raw []byte) string {
	switch raw[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	return "number"
}

// field is a struct field encoding/json decodes an object member into.
type field struct {
	name   string // the member's key
	typ    reflect.Type
	index  []int // the field's index sequence through the embedded structs
	tagged bool  // the json tag names it
}

// fieldCache holds [objectFields] by struct type.
type fieldCache map[reflect.Type][]field

func (c fieldCache) of(t reflect.Type) []field {
	fields, ok := c[t]
	if !ok {
		fields = objectFields(t)
		c[t] = fields
	}
	return fields
}

// lookup returns the field of struct t a member keyed key decodes into: the one of that name,
// else the first whose name differs in case alone.
func (c fieldCache) lookup(t reflect.Type, key string) (field, bool) {
	fields := c.of(t)
	for _, f := range fields {
		if f.name == key {
			return f, true
		}
	}
	for _, f := range fields {
		if strings.EqualFold(f.name, key) {
			return f, true
		}
	}
	return field{}, false
}

// objectFields returns the fields of struct t encoding/json decodes object members into, in
// declaration order: t's own and those of the embedded structs whose fields it holds as its own,
// where a shallower field hides a deeper one of its name and, at one depth, a tagged one an
// untagged one. A name two fields still share is no field's.
func objectFields(t reflect.Type) []field {
	type embedded struct {
		typ   reflect.Type
		index []int
		count int // the embeddings at one depth that reach typ
	}
	var all []field
	visited := map[reflect.Type]bool{}
	for level := []embedded{{typ: t, count: 1}}; len(level) > 0; {
		var next []embedded
		queued := map[reflect.Type]int{}
		for _, e := range level {
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			for i := range e.typ.NumField() {
				sf := e.typ.Field(i)
				if !decodesInto(sf) {
					continue
				}
				index := append(slices.Clip(e.index), i)
				if isFlattened(sf) {
					et := indirect(sf.Type)
					if at, ok := queued[et]; ok {
						next[at].count++
						continue
					}
					queued[et] = len(next)
					next = append(next, embedded{typ: et, index: index, count: 1})
					continue
				}
				f := field{name: jsonTagName(sf), typ: sf.Type, index: index, tagged: true}
				if f.name == "" {
					f.name, f.tagged = sf.Name, false
				}
				for range e.count {
					all = append(all, f)
				}
			}
		}
		level = next
	}
	return dominantFields(all)
}

// dominantFields keeps, of the fields that share a name, the one encoding/json decodes into: the
// shallowest, then a tagged one; when two tie, none. The result is in index order.
func dominantFields(all []field) []field {
	slices.SortStableFunc(all, func(a, b field) int {
		switch {
		case a.name != b.name:
			return strings.Compare(a.name, b.name)
		case len(a.index) != len(b.index):
			return len(a.index) - len(b.index)
		case a.tagged != b.tagged && a.tagged:
			return -1
		case a.tagged != b.tagged:
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	out := make([]field, 0, len(all))
	for i := 0; i < len(all); {
		j := i + 1
		for j < len(all) && all[j].name == all[i].name {
			j++
		}
		if j-i == 1 || len(all[i].index) != len(all[i+1].index) || all[i].tagged != all[i+1].tagged {
			out = append(out, all[i])
		}
		i = j
	}
	slices.SortFunc(out, func(a, b field) int { return slices.Compare(a.index, b.index) })
	return out
}

// decodesInto reports whether encoding/json decodes into sf: it is not tagged "-", and is
// exported or an embedded struct.
func decodesInto(sf reflect.StructField) bool {
	if sf.Tag.Get("json") == "-" {
		return false
	}
	return sf.IsExported() || sf.Anonymous && indirect(sf.Type).Kind() == reflect.Struct
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
// read-ahead) or rest, and returns the error of a failed read, such as the
// *http.MaxBytesError of a body past its cap; a [StrictDecoder] calls it after decoding a value.
func TrailingData(buffered, rest io.Reader) error {
	var b [512]byte
	for _, src := range [...]io.Reader{buffered, rest} {
		for {
			n, err := src.Read(b[:])
			for _, c := range b[:n] {
				if !isJSONSpace(c) {
					return errors.New("body: unexpected data after the JSON value")
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
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
