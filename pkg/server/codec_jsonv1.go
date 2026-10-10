//go:build !goexperiment.jsonv2

package server

import (
	"encoding/json"
	"reflect"
	"strings"
)

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
