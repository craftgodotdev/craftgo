//go:build goexperiment.jsonv2

package server

import (
	"encoding/json"
	"reflect"
	"strings"
)

// wirePath spells te.Field, which encoding/json fills under the jsonv2 experiment with the JSON
// pointer of the value, its tokens joined by ".", by the JSON names of the struct fields on the
// way alone, as without the experiment: array indexes and map keys drop out, and a member whose
// key differs from its field's name in case alone reads as the field's name.
func wirePath(t reflect.Type, te *json.UnmarshalTypeError) string {
	if te.Field == "" {
		return ""
	}
	p := pointerReader{segs: strings.Split(te.Field, "."), want: te.Type, fields: fieldCache{}}
	p.reads = make(map[pointerState]pointerRead, len(p.segs))
	p.budget = 64 * (len(p.segs) + 1)
	root := pointerState{t: t}
	if !p.read(root).ok {
		return strings.Join(unescapeTokens(p.segs), ".")
	}
	names := make([]string, 0, len(p.segs))
	for st := root; st.pos < len(p.segs); st = p.reads[st].next {
		if name := p.reads[st].name; name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ".")
}

// pointerReader reads the segments of a JSON pointer joined by ".": a struct field's key, which
// may span segments, an array index, or a map key, which may span any number.
type pointerReader struct {
	segs   []string
	want   reflect.Type // the type of the value the pointer reaches, UnmarshalTypeError.Type
	fields fieldCache
	reads  map[pointerState]pointerRead
	budget int // the readings left to try before giving up
}

// pointerState is segment pos read inside a value of type t.
type pointerState struct {
	t   reflect.Type
	pos int
}

// pointerRead is how the segments from a pointerState on read: ok when they all do, match when
// the value they reach has the error's type, name the field the segment starts, if any.
type pointerRead struct {
	ok, match bool
	name      string
	next      pointerState
}

// read returns how the segments from st on read, preferring a reading that reaches a value of
// the error's type, then a struct field matched by its exact name, then a shorter map key.
func (p *pointerReader) read(st pointerState) pointerRead {
	if r, ok := p.reads[st]; ok {
		return r
	}
	var best pointerRead
	if p.budget--; p.budget >= 0 {
		best = p.readFresh(st)
	}
	p.reads[st] = best
	return best
}

// readFresh is [pointerReader.read] for a state not read yet.
func (p *pointerReader) readFresh(st pointerState) pointerRead {
	t := indirect(st.t)
	if st.pos == len(p.segs) {
		return pointerRead{ok: true, match: t == indirect(p.want)}
	}
	var best pointerRead
	// consider keeps r when it reads better than best, and reports whether nothing can.
	consider := func(r pointerRead) bool {
		if r.ok && (!best.ok || r.match && !best.match) {
			best = r
		}
		return best.match
	}
	switch t.Kind() {
	case reflect.Struct:
		for _, fold := range [...]bool{false, true} {
			for _, f := range p.fields.of(t) {
				n, ok := p.keyAt(st.pos, f.name, fold)
				if !ok {
					continue
				}
				next := pointerState{t: f.typ, pos: st.pos + n}
				r := p.read(next)
				if consider(pointerRead{ok: r.ok, match: r.match, name: f.name, next: next}) {
					return best
				}
			}
		}
	case reflect.Slice, reflect.Array:
		if isIndex(p.segs[st.pos]) {
			next := pointerState{t: t.Elem(), pos: st.pos + 1}
			r := p.read(next)
			consider(pointerRead{ok: r.ok, match: r.match, next: next})
		}
	case reflect.Map:
		for end := st.pos + 1; end <= len(p.segs); end++ {
			next := pointerState{t: t.Elem(), pos: end}
			r := p.read(next)
			// A key the map cannot take is the end of the pointer.
			match := r.match || end == len(p.segs) && t.Key() == p.want
			if consider(pointerRead{ok: r.ok, match: match, next: next}) {
				return best
			}
		}
	}
	return best
}

// keyAt reports how many segments from pos the key name spans, its case aside when fold.
func (p *pointerReader) keyAt(pos int, name string, fold bool) (int, bool) {
	parts := strings.Split(pointerEscaper.Replace(name), ".")
	if pos+len(parts) > len(p.segs) {
		return 0, false
	}
	for i, part := range parts {
		seg := p.segs[pos+i]
		if seg != part && (!fold || !strings.EqualFold(seg, part)) {
			return 0, false
		}
	}
	return len(parts), true
}

// pointerEscaper escapes a key as a JSON pointer token.
var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// isIndex reports whether seg is an array index.
func isIndex(seg string) bool {
	return seg != "" && strings.Trim(seg, "0123456789") == ""
}

// unescapeTokens returns segs with their JSON pointer escapes undone.
func unescapeTokens(segs []string) []string {
	unescaper := strings.NewReplacer("~1", "/", "~0", "~")
	out := make([]string, len(segs))
	for i, seg := range segs {
		out[i] = unescaper.Replace(seg)
	}
	return out
}
