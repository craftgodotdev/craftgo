package golang

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

type queryPrim struct {
	parser string // strconv.ParseX function, "" for a string
	goType string // bind helper type argument ("int", "float64", ...), "" for bool and string
	label  string // kind named in a parse error
}

// wirePrim returns the binder metadata for a wire-parseable primitive.
func wirePrim(name string) (queryPrim, bool) {
	sp, ok := prims.Lookup(name)
	if !ok || !prims.IsWireParseable(name) {
		return queryPrim{}, false
	}
	q := queryPrim{parser: sp.Parser}
	switch sp.Kind {
	case prims.String:
		q.label = "string"
	case prims.Bool:
		q.label = "bool"
	case prims.Int:
		q.label, q.goType = "int", name
	case prims.Uint:
		q.label, q.goType = "uint", name
	case prims.Float:
		q.label, q.goType = "float", name
	}
	return q, true
}

// wireSource is how a handler reads the raw strings of one binding source.
type wireSource struct {
	kind         wire.Binding
	singleExpr   func(wireName string) string
	arrayExpr    func(wireName string) string // "" when the source has no multi-value form
	presenceExpr func(wireName string) string // key-present expression; nil skips the presence check
	cookieGuard  bool                         // wrap in `if c, err := r.Cookie(name)`, which supplies c
}

func querySource() wireSource {
	// transport.tmpl declares `_q := r.URL.Query()` once when the method has query params.
	return wireSource{
		kind:         wire.BindQuery,
		singleExpr:   func(n string) string { return fmt.Sprintf("_q.Get(%q)", n) },
		arrayExpr:    func(n string) string { return fmt.Sprintf("_q[%q]", n) },
		presenceExpr: func(n string) string { return fmt.Sprintf("_q.Has(%q)", n) },
	}
}

func headerSource() wireSource {
	return wireSource{
		kind:         wire.BindHeader,
		singleExpr:   func(n string) string { return fmt.Sprintf("r.Header.Get(%q)", n) },
		arrayExpr:    func(n string) string { return fmt.Sprintf("r.Header.Values(%q)", n) },
		presenceExpr: func(n string) string { return fmt.Sprintf("len(r.Header.Values(%q)) > 0", n) },
	}
}

func cookieSource() wireSource {
	return wireSource{
		kind:         wire.BindCookie,
		singleExpr:   func(string) string { return "c.Value" },
		arrayExpr:    func(string) string { return "" },
		cookieGuard:  true,
		presenceExpr: func(n string) string { return fmt.Sprintf("server.CookiePresent(r, %q)", n) },
	}
}

// pathSource has no presence check: a matched route always supplies the segment.
func pathSource() wireSource {
	return wireSource{
		kind:       wire.BindPath,
		singleExpr: func(n string) string { return fmt.Sprintf("r.PathValue(%q)", n) },
		arrayExpr:  func(string) string { return "" },
	}
}

func formSource() wireSource {
	return wireSource{
		kind:       wire.BindForm,
		singleExpr: func(n string) string { return fmt.Sprintf("r.FormValue(%q)", n) },
		arrayExpr:  func(n string) string { return fmt.Sprintf("r.MultipartForm.Value[%q]", n) },
	}
}

// renderWireBindLine renders the statement binding field f from src into req.goName, or an error
// for a shape src cannot carry (a map, a struct, an array on a single-value source).
func renderWireBindLine(f *ast.Field, pkg *semantic.Package, r *projectResolver, pkgAlias, wireName, goName string, src wireSource) (string, error) {
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
	prim, ok := wirePrim(declName)
	cast := ""
	if !ok {
		// A scalar or enum casts to its declared name, already qualified when cross-package.
		if sc := r.LookupScalar(declName); sc != nil {
			if p2, pOk := wirePrim(sc.Primitive); pOk {
				prim = p2
				ok = true
				cast = declName
			}
		}
		if !ok {
			if ed := r.LookupEnum(declName); ed != nil {
				prim, _ = wirePrim(enumWirePrim(ed))
				ok = true
				cast = declName
			}
		}
	}
	if !ok {
		return "", fmt.Errorf("field %q: type %s cannot bind to @%s - only string/bool/int*/uint*/float*, scalars/enums, and arrays of those (struct/[]struct must ride the body via a body verb instead)", f.Name, f.Type, src.kind)
	}
	// A local cast gets the request package's alias.
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
	// The parser's type argument is the cast, else the Go type, else (for bool) the DSL name.
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
		// A present key replaces an array @default and an absent key keeps it: server.BindValues
		// does both, and the string-slice shapes have *Defaulted variants for it.
		_, hasDef := semantic.ResolveDefaultValue(f, pkg)
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
		// An absent and an empty (`?x=`) value both leave a single field unset.
		if prim.parser == "" {
			if goFieldIsPointer(f, pkg, r) {
				if cast == "" {
					shape = renderWireBindShape("optionalStringNoCast", data)
				} else {
					data.Wrap = wrap("_v")
					shape = renderWireBindShape("optionalStringCast", data)
				}
			} else if _, hasDef := semantic.ResolveDefaultValue(f, pkg); hasDef {
				// Only a non-empty value overwrites the pre-filled @default.
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
	// A missing required key (non-optional, no @default) answers 400; a present empty value passes.
	// The check sits outside the cookie guard, so a missing cookie reaches it.
	if src.presenceExpr != nil && !f.Type.Optional {
		if _, hasDef := semantic.ResolveDefaultValue(f, pkg); !hasDef {
			guard := fmt.Sprintf("if !server.RequirePresent(w, r, %s, %q, %q) {\nreturn\n}", src.presenceExpr(wireName), wireName, src.kind)
			shape = guard + "\n" + shape
		}
	}
	return shape, nil
}

// wrapCookieGuard runs inner only when the request carries the cookie.
func wrapCookieGuard(wireName, inner string) string {
	indented := indentLines(inner, "\t")
	return fmt.Sprintf("if c, err := r.Cookie(%q); err == nil {\n%s\n}", wireName, indented)
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

// bindParseFamily maps a strconv parser to the generic pkg/server parser a handler calls.
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

// renderWireBindShape executes the named shape of transport_wire_bind.tmpl; an unknown name panics.
func renderWireBindShape(name string, data wireBindData) string {
	var buf bytes.Buffer
	if err := transportWireBindTemplate.ExecuteTemplate(&buf, name, data); err != nil {
		panic(fmt.Sprintf("codegen: render wire bind shape %q: %v", name, err))
	}
	return buf.String()
}

var transportWireBindTemplate = tmpl("transport_wire_bind.tmpl")
