package golang

import (
	"bytes"
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// wireParse is how a handler parses a wire string of one primitive kind: the pkg/server parser,
// "" for a string, which binds as it is, and the kind a parse error names.
type wireParse struct {
	parser, label string
}

// wireParses are the kinds a wire string binds as.
var wireParses = map[prims.Kind]wireParse{
	prims.String: {"", "string"},
	prims.Bool:   {"server.ParseBool", "bool"},
	prims.Int:    {"server.ParseSigned", "int"},
	prims.Uint:   {"server.ParseUnsigned", "uint"},
	prims.Float:  {"server.ParseFloat", "float"},
}

// wireSource is how a handler reads the raw strings of one binding.
type wireSource struct {
	single  func(wireName string) string // the value
	array   func(wireName string) string // every value; nil when the source carries one per name
	present func(wireName string) string // whether the key came at all; nil skips the presence check
	cookie  bool                         // read inside `if c, err := r.Cookie(name)`, which supplies c
}

// wireSources are the sources of the bindings a handler reads from strings.
var wireSources = map[wire.Binding]wireSource{
	// A matched route always supplies its segments.
	wire.BindPath: {
		single: func(n string) string { return fmt.Sprintf("r.PathValue(%q)", n) },
	},
	// transport.tmpl declares `_q := r.URL.Query()` once when the method has query params.
	wire.BindQuery: {
		single:  func(n string) string { return fmt.Sprintf("_q.Get(%q)", n) },
		array:   func(n string) string { return fmt.Sprintf("_q[%q]", n) },
		present: func(n string) string { return fmt.Sprintf("_q.Has(%q)", n) },
	},
	wire.BindHeader: {
		single:  func(n string) string { return fmt.Sprintf("r.Header.Get(%q)", n) },
		array:   func(n string) string { return fmt.Sprintf("server.HeaderList(r, %q)", n) },
		present: func(n string) string { return fmt.Sprintf("len(r.Header.Values(%q)) > 0", n) },
	},
	wire.BindCookie: {
		single:  func(string) string { return "c.Value" },
		present: func(n string) string { return fmt.Sprintf("server.CookiePresent(r, %q)", n) },
		cookie:  true,
	},
	wire.BindForm: {
		single: func(n string) string { return fmt.Sprintf("r.FormValue(%q)", n) },
		array:  func(n string) string { return fmt.Sprintf("r.MultipartForm.Value[%q]", n) },
	},
}

// renderWireBindLine renders the statement binding rf from binding's source into req; the
// analyser admits only a field whose type the source carries.
func renderWireBindLine(rf resolvedField, binding wire.Binding, wireName string, pkg *semantic.Package, r *projectResolver, imports *importSet) string {
	src := wireSources[binding]
	primName, declared, _ := wireTarget(rf.ResolvedField, pkg, r)
	sp, _ := prims.Lookup(primName)
	parse := wireParses[sp.Kind]
	cast := ""
	if declared != "" {
		cast = imports.qualify(declared)
	}
	data := wireBindData{
		DSLNameQuoted: strconv.Quote(wireName),
		GoName:        rf.GoName,
		Label:         parse.label,
		SingleSource:  src.single(wireName),
	}
	if src.array != nil {
		data.ArraySource = src.array(wireName)
	}
	if parse.parser != "" {
		data.ParseFn = parse.parser + "[" + cmp.Or(cast, primName) + "]"
	}
	shape := bindShape(rf, parse.parser != "", cast != "")
	// directSingle converts the source expression; every other shape the `_v` it read.
	data.Wrap = castTo(cast, "_v")
	if shape == "directSingle" {
		data.Wrap = castTo(cast, data.SingleSource)
	}
	return guardBind(renderWireBindShape(shape, data), rf, binding, wireName)
}

// wireTarget resolves what one wire string of rf holds, an array's element for an array: the
// primitive it parses as and the scalar or enum it converts to, "" for a primitive; ok is false
// for a type no wire string carries.
func wireTarget(rf semantic.ResolvedField, pkg *semantic.Package, r *projectResolver) (prim, declared string, ok bool) {
	if t := rf.Field.Type; t != nil && t.Array {
		elem := *rf.Field
		elem.Type = t.ElemTypeRef()
		rf = semantic.ResolveField(&elem, pkg, r.Project())
	}
	switch rf.Category {
	case semantic.CatPrimitive:
		return rf.ResolvedPrim, "", prims.IsWireParseable(rf.ResolvedPrim)
	case semantic.CatScalar, semantic.CatEnum:
		return rf.ResolvedPrim, rf.Field.Type.Named.Name.String(), prims.IsWireParseable(rf.ResolvedPrim)
	}
	return "", "", false
}

// bindShape names the transport_wire_bind.tmpl shape binding rf: array or single value, parsed or
// kept a string, converted to a declared type, stored behind a pointer, pre-filled by a default.
func bindShape(rf resolvedField, parsed, cast bool) string {
	array, defaulted := rf.Field.Type.Array, rf.HasDefValue
	switch {
	case array && parsed:
		return "arrayParsed"
	case array && !cast && defaulted:
		return "directSliceDefaulted"
	case array && !cast:
		return "directSlice"
	case array && defaulted:
		return "arrayStringDefaulted"
	case array:
		return "arrayString"
	case parsed && rf.IsPointer:
		return "optionalParsed"
	case parsed:
		return "singleParsed"
	case rf.IsPointer && cast:
		return "optionalStringCast"
	case rf.IsPointer:
		return "optionalStringNoCast"
	case defaulted:
		return "directSingleDefaulted"
	}
	return "directSingle"
}

// castTo converts the Go expression v to type, unless type is "".
func castTo(typ, v string) string {
	if typ == "" {
		return v
	}
	return typ + "(" + v + ")"
}

// guardBind wraps bind in its source's guards: a cookie is read inside `if c, err :=
// r.Cookie(name)`, and a missing required key (no `?`, no @default) answers 400 before it, outside
// the cookie guard so a missing cookie reaches the check. A present empty value passes.
func guardBind(bind string, rf resolvedField, binding wire.Binding, wireName string) string {
	src := wireSources[binding]
	if src.cookie {
		bind = fmt.Sprintf("if c, err := r.Cookie(%q); err == nil {\n%s\n}", wireName, indentLines(bind, "\t"))
	}
	if src.present != nil && !rf.Field.Type.Optional && !rf.HasDefValue {
		bind = fmt.Sprintf("if !server.RequirePresent(w, r, %s, %q, %q) {\nreturn\n}\n", src.present(wireName), wireName, binding) + bind
	}
	return bind
}

// indentLines prepends prefix to every non-empty line of s.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// wireBindData is the input of every shape in transport_wire_bind.tmpl; a shape reads only
// the fields it needs. SingleSource and ArraySource are the src expressions (`_q.Get("x")`, `c.Value`).
type wireBindData struct {
	DSLNameQuoted string
	GoName        string
	Wrap          string
	// ParseFn is the generic parser the bind helper calls, e.g. `server.ParseSigned[types.Cents]`.
	ParseFn      string
	Label        string
	SingleSource string
	ArraySource  string
}

// renderWireBindShape executes the named shape of transport_wire_bind.tmpl; an unknown name panics.
func renderWireBindShape(name string, data wireBindData) string {
	var buf bytes.Buffer
	if err := transportWireBindTemplate.ExecuteTemplate(&buf, name, data); err != nil {
		panic(fmt.Sprintf("codegen: render wire bind shape %q: %v", name, err))
	}
	return buf.String()
}

var transportWireBindTemplate = tmpl("transport_wire_bind.tmpl")
