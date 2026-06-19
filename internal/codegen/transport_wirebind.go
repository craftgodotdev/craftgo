// Wire-bind rendering: the per-source (query/header/cookie/path/form)
// binding descriptors, the primitive parse table, and the Go-source
// generator for one field's bind line.
package codegen

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

type queryPrim struct {
	parser string // strconv.ParseX function or "" for direct string
	goType string // type-argument for the bind helper ("int", "float64", ...) or "" for bool/string
	label  string // human-readable kind for error messages
}

var queryPrims = map[string]queryPrim{
	"string":  {label: "string"},
	"bool":    {parser: "strconv.ParseBool", label: "bool"},
	"int":     {parser: "strconv.ParseInt", goType: "int", label: "int"},
	"int8":    {parser: "strconv.ParseInt", goType: "int8", label: "int"},
	"int16":   {parser: "strconv.ParseInt", goType: "int16", label: "int"},
	"int32":   {parser: "strconv.ParseInt", goType: "int32", label: "int"},
	"int64":   {parser: "strconv.ParseInt", goType: "int64", label: "int"},
	"uint":    {parser: "strconv.ParseUint", goType: "uint", label: "uint"},
	"uint8":   {parser: "strconv.ParseUint", goType: "uint8", label: "uint"},
	"uint16":  {parser: "strconv.ParseUint", goType: "uint16", label: "uint"},
	"uint32":  {parser: "strconv.ParseUint", goType: "uint32", label: "uint"},
	"uint64":  {parser: "strconv.ParseUint", goType: "uint64", label: "uint"},
	"float32": {parser: "strconv.ParseFloat", goType: "float32", label: "float"},
	"float64": {parser: "strconv.ParseFloat", goType: "float64", label: "float"},
}

// wireSource describes a binding's HTTP wire source. Different bindings
// extract the raw string differently but share the same downstream
// parse / cast / wrap logic, so we abstract the source extraction
// behind these closures and dispatch through [renderWireBindLine].
//
// Cookie is special-cased: `r.Cookie(name)` returns (cookie, error)
// rather than a bare string, so the renderer wraps the whole produced
// block in `if c, err := r.Cookie(name); err == nil { ... }` when
// cookieGuard is true. SingleExpr / arrayExpr for cookie return
// `c.Value` / "" - the wrap supplies `c`.
type wireSource struct {
	kind         string
	singleExpr   func(wireName string) string
	arrayExpr    func(wireName string) string // "" if arrays unsupported for this source
	presenceExpr func(wireName string) string // Go bool expr: key present? nil = no presence check for this source
	cookieGuard  bool
}

// querySource / headerSource / cookieSource / formSource build the
// wireSource for each of the four supported bindings. Hot path - kept
// allocation-free by capturing the wireName by value at the call site.
func querySource() wireSource {
	// Reads come off `_q`, the `url.Values` the handler parses ONCE via
	// `_q := r.URL.Query()` (the template emits it when QueryParams is
	// non-empty). r.URL.Query() reparses RawQuery and allocates a fresh
	// map on every call, so binding N query fields off one `_q` instead
	// of N `r.URL.Query()` calls is N× fewer parses + maps. The %q is the
	// WIRE name (honours `@query("x-q")`), not the Go field name.
	return wireSource{
		kind:         wire.BindingQuery,
		singleExpr:   func(n string) string { return fmt.Sprintf("_q.Get(%q)", n) },
		arrayExpr:    func(n string) string { return fmt.Sprintf("_q[%q]", n) },
		presenceExpr: func(n string) string { return fmt.Sprintf("_q.Has(%q)", n) },
	}
}

func headerSource() wireSource {
	return wireSource{
		kind:         wire.BindingHeader,
		singleExpr:   func(n string) string { return fmt.Sprintf("r.Header.Get(%q)", n) },
		arrayExpr:    func(n string) string { return fmt.Sprintf("r.Header.Values(%q)", n) },
		presenceExpr: func(n string) string { return fmt.Sprintf("len(r.Header.Values(%q)) > 0", n) },
	}
}

func cookieSource() wireSource {
	return wireSource{
		kind:         wire.BindingCookie,
		singleExpr:   func(string) string { return "c.Value" },
		arrayExpr:    func(string) string { return "" },
		cookieGuard:  true,
		presenceExpr: func(n string) string { return fmt.Sprintf("server.CookiePresent(r, %q)", n) },
	}
}

// pathSource reads a single segment via `r.PathValue("id")`. A path has
// no multi-value form, so arrayExpr returns "" and [renderWireBindLine]
// rejects an array-typed @path field. A matched route always supplies
// the segment, so the value is treated as present (the semantic layer
// rejects an optional @path field, so only the required shapes -
// directSingle / singleParsed - are ever emitted here).
func pathSource() wireSource {
	return wireSource{
		kind:       wire.BindingPath,
		singleExpr: func(n string) string { return fmt.Sprintf("r.PathValue(%q)", n) },
		arrayExpr:  func(string) string { return "" },
	}
}

func formSource() wireSource {
	return wireSource{
		kind:       wire.BindingForm,
		singleExpr: func(n string) string { return fmt.Sprintf("r.FormValue(%q)", n) },
		arrayExpr:  func(n string) string { return fmt.Sprintf("r.MultipartForm.Value[%q]", n) },
	}
}

// renderWireBindLine renders the per-field binding statement for any
// of the four HTTP wire-string sources (query / header / cookie / form).
// The source-extraction expressions come from src; the rest of the
// pipeline (primitive resolution, scalar / enum cast, parse + 400 on
// failure, optional pointer wrap, array loop) is shared.
//
// Returns the rendered Go code, a flag indicating whether the line
// needs `strconv` imported, and an error describing why a particular
// field shape cannot ride the wire (cookies have no array form, maps
// and structs ride only `@body`, etc.).
func renderWireBindLine(f *ast.Field, pkg *semantic.Package, r *ProjectResolver, pkgAlias, wireName, goName string, src wireSource) (string, error) {
	if f.Type == nil {
		return "", fmt.Errorf("field %q has no resolved type", f.Name)
	}
	if f.Type.Map != nil {
		return "", fmt.Errorf("field %q: map types cannot bind to @%s - only string/bool/int*/uint*/float* and arrays of those", f.Name, src.kind)
	}
	if f.Type.Named == nil {
		return "", fmt.Errorf("field %q: anonymous types cannot bind to @%s - only string/bool/int*/uint*/float* and arrays of those", f.Name, src.kind)
	}
	if len(f.Type.Named.Args) > 0 {
		return "", fmt.Errorf("field %q: generic type %s<...> cannot bind to @%s - only string/bool/int*/uint*/float* and arrays of those", f.Name, f.Type.Named.Name.String(), src.kind)
	}
	if f.Type.Array && src.arrayExpr(wireName) == "" {
		return "", fmt.Errorf("field %q: arrays cannot bind to @%s - this wire format carries a single value per name", f.Name, src.kind)
	}
	declName := f.Type.Named.Name.String()
	prim, ok := queryPrims[declName]
	cast := ""
	if !ok {
		// Local first (cheap, matches the pre-resolver shape), then
		// project-wide for qualified `pkg.X` refs. For cross-pkg the
		// declName is already the full qualified name (`xshared.XEmail`),
		// which is also the correct Go cast - no extra prefix needed.
		if pkg != nil {
			if sc, scOk := pkg.Scalars[declName]; scOk && sc != nil {
				if p2, pOk := queryPrims[sc.Primitive]; pOk {
					prim = p2
					ok = true
					cast = declName
				}
			}
		}
		if !ok {
			if sc := r.LookupScalar(declName); sc != nil {
				if p2, pOk := queryPrims[sc.Primitive]; pOk {
					prim = p2
					ok = true
					cast = declName
				}
			}
		}
		if !ok {
			if pkg != nil {
				if ed, edOk := pkg.Enums[declName]; edOk && ed != nil {
					prim = queryPrims[enumWirePrim(ed)]
					ok = true
					cast = declName
				}
			}
		}
		if !ok {
			if ed := r.LookupEnum(declName); ed != nil {
				prim = queryPrims[enumWirePrim(ed)]
				ok = true
				cast = declName
			}
		}
	}
	if !ok {
		return "", fmt.Errorf("field %q: type %s cannot bind to @%s - only string/bool/int*/uint*/float*, scalars/enums, and arrays of those (struct/[]struct must ride the body via a body verb instead)", f.Name, describeFieldType(f), src.kind)
	}
	// Local refs get the request-pkg alias prefix (`Email` →
	// `xrefs.Email`). Qualified refs already carry their pkg
	// (`xshared.XEmail`) and pass through untouched. Detect by the
	// presence of `.` - declName from a bare `*ast.QualifiedIdent`
	// is dotless.
	if cast != "" && pkgAlias != "" && !strings.Contains(cast, ".") {
		cast = pkgAlias + "." + cast
	}
	wrap := func(s string) string {
		if cast == "" {
			return s
		}
		return cast + "(" + s + ")"
	}
	singleSrc := src.singleExpr(wireName)
	arraySrc := src.arrayExpr(wireName)
	data := wireBindData{
		DSLNameQuoted: strconv.Quote(wireName),
		GoName:        goName,
		Label:         prim.label,
		SingleSource:  singleSrc,
		ArraySource:   arraySrc,
	}
	// Parsed primitives bind through the generic [server] helpers; the
	// type argument is the scalar cast when present, else the builtin
	// Go type (bool has no goType entry, so fall back to the DSL name).
	if prim.parser != "" {
		bindType := cast
		if bindType == "" {
			bindType = prim.goType
		}
		if bindType == "" {
			bindType = declName
		}
		data.ParseFn = bindParseFamily(prim.parser) + "[" + bindType + "]"
	}
	var shape string
	if f.Type.Array {
		// An array @default pre-fills the slice; the binding must REPLACE it
		// when the key is present and PRESERVE it when absent. The parsed
		// path's server.BindValues already does both; the string-slice paths
		// otherwise overwrite-with-nil (direct) or append (cast), destroying
		// or polluting the default - so they use presence-guarded variants.
		// The has-default test uses resolveDefaultValue - the same oracle the
		// prefill emits from - so an enum-member array default (`[Red, Blue]`,
		// which defaultValue can't resolve) is seen as a default here too.
		_, hasDef := resolveDefaultValue(f, pkg)
		if prim.parser == "" {
			if cast == "" {
				if hasDef {
					shape = renderWireBindShape("directSliceDefaulted", data)
				} else {
					shape = renderWireBindShape("directSlice", data)
				}
			} else {
				data.Wrap = wrap("_v")
				if hasDef {
					shape = renderWireBindShape("arrayStringDefaulted", data)
				} else {
					shape = renderWireBindShape("arrayString", data)
				}
			}
		} else {
			shape = renderWireBindShape("arrayParsed", data)
		}
	} else {
		// Single (non-array). An absent param and a present-but-empty one
		// (`?x=`) both leave the field unset: nil for an optional pointer,
		// the zero value for a required field.
		if prim.parser == "" {
			if goFieldIsPointer(f, pkg, r) {
				if cast == "" {
					shape = renderWireBindShape("optionalStringNoCast", data)
				} else {
					data.Wrap = wrap("_v")
					shape = renderWireBindShape("optionalStringCast", data)
				}
			} else if _, hasDef := resolveDefaultValue(f, pkg); hasDef {
				// A string-backed param carrying @default: only overwrite
				// the pre-filled default when the param is actually present,
				// mirroring the parsed path's `raw != ""` guard. An
				// unconditional assign would clobber the default with "" on
				// an absent (or `?x=`) request.
				data.Wrap = wrap("_v")
				shape = renderWireBindShape("directSingleDefaulted", data)
			} else {
				data.Wrap = wrap(singleSrc)
				shape = renderWireBindShape("directSingle", data)
			}
		} else {
			if goFieldIsPointer(f, pkg, r) {
				shape = renderWireBindShape("optionalParsed", data)
			} else {
				shape = renderWireBindShape("singleParsed", data)
			}
		}
	}
	if src.cookieGuard {
		shape = wrapCookieGuard(wireName, shape)
	}
	// A required param (non-optional, no @default) on a source that can
	// distinguish present from absent gets a presence check: the OpenAPI
	// advertises required:true, so the runtime 400s on a missing key instead
	// of silently accepting the zero value. This covers arrays too (a
	// required array @query / @header 400s when the key is absent), matching
	// the required:true the spec carries. A present-but-empty value (`?q=`)
	// passes - the test is on the key, not the value. @default fields are
	// exempt (the default covers absence). The check sits OUTSIDE the
	// cookie-guard wrap so an absent required cookie 400s rather than
	// skipping silently.
	if src.presenceExpr != nil && !f.Type.Optional {
		if _, hasDef := resolveDefaultValue(f, pkg); !hasDef {
			guard := fmt.Sprintf("if !server.RequirePresent(w, r, %s, %q, %q) {\nreturn\n}", src.presenceExpr(wireName), wireName, src.kind)
			shape = guard + "\n" + shape
		}
	}
	return shape, nil
}

// wrapCookieGuard wraps a rendered shape in the
// `if c, err := r.Cookie(name); err == nil { ... }` prelude. Cookie
// retrieval returns (Cookie, error); we surface a missing-cookie state
// the same way other wire bindings handle empty values - the field
// stays at its zero value (or nil pointer for optional shapes).
//
// The inner body is indented one tab so the produced code stays
// gofmt-clean without a post-render pass.
func wrapCookieGuard(wireName, inner string) string {
	indented := indentLines(inner, "\t")
	return fmt.Sprintf("if c, err := r.Cookie(%q); err == nil {\n%s\n}", wireName, indented)
}

// indentLines prepends prefix to every non-blank line of s. Used by
// the cookie-guard wrap so the inner block sits one level deeper.
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

// wireBindData is the payload threaded through every named block in
// transport_wire_bind.tmpl. Fields that a particular shape does not
// reference stay empty - the template only slots what it asks for so
// unused entries are harmless.
//
// `SingleSource` / `ArraySource` are the binding-specific source
// extraction expressions (e.g. `r.URL.Query().Get("x")` for query,
// `c.Value` for cookie). They are supplied by the caller's
// [wireSource]; the template stays unaware of which wire format it is
// emitting for.
type wireBindData struct {
	DSLNameQuoted string
	GoName        string
	Wrap          string
	// ParseFn is the generic parse function the bind helpers receive,
	// e.g. `server.ParseSigned[int]` or `server.ParseSigned[types.Cents]`.
	ParseFn      string
	Label        string
	SingleSource string
	ArraySource  string
}

// bindParseFamily maps a strconv parser to the matching generic
// [server] parse helper. The type argument (appended by the caller)
// carries the per-type bit width and any scalar conversion.
func bindParseFamily(parser string) string {
	switch parser {
	case "strconv.ParseBool":
		return "server.ParseBool"
	case "strconv.ParseFloat":
		return "server.ParseFloat"
	case "strconv.ParseUint":
		return "server.ParseUnsigned"
	default: // strconv.ParseInt
		return "server.ParseSigned"
	}
}

// renderWireBindShape executes one named block from
// transport_wire_bind.tmpl. The shape name is a compile-time constant
// at every call site so a typo would fail the next test run with a
// clear "template not found" panic.
func renderWireBindShape(name string, data wireBindData) string {
	var buf bytes.Buffer
	if err := transportWireBindTemplate.ExecuteTemplate(&buf, name, data); err != nil {
		panic(fmt.Sprintf("codegen: render wire bind shape %q: %v", name, err))
	}
	return buf.String()
}

// transportWireBindTemplate is parsed once at first use; subsequent
// renders are pure ExecuteTemplate dispatches by name. The template
// holds the catalogue of shapes (see file header comment) so adding a
// new wire-bound primitive is a template-only change once the Go
// dispatcher knows which name to pick.
var transportWireBindTemplate = tmpl("transport_wire_bind.tmpl")
