package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// transportData is the template input for `handler.tmpl`. One value is built
// per (service, method) pair.
type transportData struct {
	Package string
	Method  string
	Verb    string
	// RequestType is the bare DSL identifier of the request type
	// (`Login`), without any package prefix. The template combines
	// it with [RequestPkgAlias] when emitting `var req X.Y`.
	RequestType string
	// RequestPkgAlias is the Go-side alias under which the request
	// type's package is imported. For a local request type the
	// alias is `types` (matching the canonical [TypesImport]
	// import); for a cross-package request the alias is the target
	// package's name (e.g. `shared`) and the matching import lives
	// in [ExtraTypesImports].
	RequestPkgAlias string
	Doc             []string
	HasRequest      bool
	HasResponse     bool
	BodyVerb        bool
	BodyDecode      bool
	NeedsTypes      bool
	IsPassthrough   bool
	IsMultipart     bool
	// MultipartMaxMemory is the byte budget passed to
	// r.ParseMultipartForm in multipart handlers. Defaults to 32 MiB
	// (the stdlib historical pick) unless the method's `@maxBodySize`
	// decorator declares a higher cap, in which case it lifts to that
	// value so uploads up to the declared limit stay in memory
	// without spilling to disk. Only meaningful when IsMultipart.
	MultipartMaxMemory int64
	PathParams         []paramBinding
	QueryParams        []paramBinding
	HeaderParams       []paramBinding
	CookieParams       []paramBinding
	FormStrings        []paramBinding
	FormFiles          []paramBinding
	// Response-side bindings: fields on the response struct tagged with
	// `@header` / `@cookie`. The handler emits them onto the writer
	// before the JSON body is encoded; the matching JSON tag on the
	// generated struct is `json:"-"` so the values do not also leak
	// into the body.
	RespHeaders []paramBinding
	RespCookies []paramBinding
	// Defaults pre-fills the request struct before JSON decode so any
	// field absent in the body keeps its DSL-declared @default value.
	// JSON decode never zeroes fields it doesn't see, so a pre-filled
	// default survives unless the client explicitly sends a value.
	Defaults []defaultBinding
	// NeedsStrconv tells the template to import "strconv" when at
	// least one bound field needed string→int/float/bool parsing.
	NeedsStrconv     bool
	ServiceImport    string
	TypesImport      string
	SvccontextImport string
	// ExtraTypesImports lists Go imports for cross-package request
	// types. Empty for the common case where request lives in the
	// service's own package.
	ExtraTypesImports []extraImport
}

// extraImport is one row in a generated file's "extra Go imports"
// block. Used by handler / logic templates to pull in cross-package
// types when a service request or response type references a sibling
// DSL package.
type extraImport struct {
	Alias string
	Path  string
}

// defaultBinding is one row of the request struct's pre-fill table.
// `Literal` is the Go source for the default value already quoted /
// formatted for direct emission (e.g. `"pending"` for strings, `20`
// for ints, `[]string{"a", "b"}` for arrays, `StatusActive` for
// enums). The handler template writes `req.<GoName> = <Literal>` for
// non-pointer fields; when Ptr is true (Go type is `*T`) the template
// emits a temp + address-of so a pointer field gets `req.X = &tmp`.
type defaultBinding struct {
	GoName  string
	Literal string
	Ptr     bool
}

// paramBinding is one row of a handler's parameter-binding table.
// DSLName is the source-side identifier (e.g. the `{id}` segment, the
// query/header/cookie key); GoName is the exported field on the
// request struct that receives the value. Bind is the pre-rendered Go
// source the template drops verbatim - the codegen pre-computes it
// per-field so the template stays declarative and the type-specific
// parsing (int / float / bool / arrays) lives in one place.
type paramBinding struct {
	DSLName string
	GoName  string
	Bind    string
	// MimeTypes carries the allowlist declared via `@mimeTypes("a/b",
	// "c/d")` on a `file @form` field. Populated only by
	// [collectFormBindings] for file entries so the OpenAPI emitter
	// can surface the contract under multipart `encoding[field].
	// contentType`; non-file bindings leave it nil.
	MimeTypes []string
}

// helpersData is the template input for `handler_helpers.tmpl`.
type helpersData struct{ Package string }

// GenerateTransport emits one `<method>_handler.go` per method per service
// under `<output.handler>/<servicePackage>/`. Each file contains a single
// exported `<Method>Handler(svcCtx) http.HandlerFunc` constructor that
// decodes the request, calls the user's logic, and writes the response.
//
// projectRoot is prepended to `cfg.Output.Transport` so the function can be
// called with paths relative to the manifest's directory.
//
// Equivalent to [GenerateTransportWith] with nil [CrossPkg] and nil
// [ScalarTable] - the convenience entry single-package tests reach
// for. Production CLI flows go straight through [GenerateTransportWith]
// because they always have a project-wide cross-package table to feed
// in.
func GenerateTransport(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	return GenerateTransportWith(pkg, cfg, projectRoot, nil, nil)
}

// GenerateTransportWith is the full-context variant. `scalars`
// supplies the project-wide [ScalarTable] so cross-package scalar
// fields (`shared.ID @path`) can resolve through to their underlying
// primitive — without it `stringBindable` only sees the local
// `pkg.Scalars` map and rejects every cross-package binding.
func GenerateTransportWith(pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg, scalars ScalarTable) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateTransportFor(svcName, svc, pkg, cfg, projectRoot, crossPkg, scalars); err != nil {
			return err
		}
	}
	return nil
}

// sortedServices returns the package's service names in deterministic order.
func sortedServices(pkg *semantic.Package) []string { return sortedKeys(pkg.Services) }

// generateTransportFor emits all per-method handler files for a single
// service. Each method becomes a separate file so that user-friendly diffs
// are produced when only one endpoint changes.
func generateTransportFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg, scalars ScalarTable) error {
	imps := importPathsFor(cfg, pkg, svcName)
	dir := filepath.Join(projectRoot, cfg.Output.Transport, ServiceDir(svcName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	jsonTpl := tmpl("transport.tmpl")
	passthroughTpl := tmpl("transport-passthrough.tmpl")
	multipartTpl := tmpl("transport-multipart.tmpl")
	for _, m := range svc.Methods {
		data, err := buildTransportData(svcName, m, imps, pkg, crossPkg, scalars)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", svcName, m.Name, err)
		}
		t := jsonTpl
		switch {
		case data.IsPassthrough:
			t = passthroughTpl
		case data.IsMultipart:
			t = multipartTpl
		}
		formatted, err := renderGo(t, data)
		if err != nil {
			return fmt.Errorf("render %s transport: %w", kebabCase(m.Name), err)
		}
		filename := kebabCase(m.Name) + ".go"
		if err := os.WriteFile(filepath.Join(dir, filename), formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// buildTransportData populates the transportData struct for one DSL method.
// Returns an error when collectBindings rejects an unsupported binding
// shape (e.g. `@query` on a struct field).
//
// crossPkg drives cross-package request resolution: when a method
// declares `request foo.Cred` and `foo` lives in another DSL package,
// the handler's Go file gets an extra import for that package and
// the generated `var req foo.Cred` line uses the package name as the
// Go alias.
func buildTransportData(svcName string, m *ast.Method, imps importPaths, pkg *semantic.Package, crossPkg CrossPkg, scalars ScalarTable) (transportData, error) {
	hasReq := m.Request != nil
	hasResp := m.Response != nil && m.Response.Type != nil
	// NeedsTypes triggers the `types` import in the template. The
	// handler body only references `types.X` for request decoding —
	// responses pass through to the encoder unchanged — so the gate is
	// strictly "is there a request to bind". Response-only handlers
	// would otherwise pull in an unused import (`go build` would
	// reject) or render the response cast via a different alias when
	// the response type is cross-package (cf. resolveTypeRef below).
	d := transportData{
		Package:          ServicePackage(svcName),
		Method:           m.Name,
		Verb:             httpVerb(m.Verb),
		Doc:              m.Doc,
		HasRequest:       hasReq,
		HasResponse:      hasResp,
		BodyVerb:         hasBodyVerb(m.Verb),
		NeedsTypes:       hasReq,
		ServiceImport:    imps.Service,
		TypesImport:      imps.Types,
		SvccontextImport: imps.Svccontext,
	}
	if hasReq {
		// Resolve the Go-side reference to the request type. Local
		// types render as `types.<X>` (the canonical alias the
		// template imports); cross-package types render as
		// `<targetPkg>.<X>` and contribute an extra Go import.
		alias, bare, extra := resolveTypeRef(m.Request, crossPkg)
		d.RequestPkgAlias = alias
		d.RequestType = bare
		// Cross-package request → drop the canonical types import;
		// the only types reference in the handler body now resolves
		// via the cross-pkg alias.
		if extra.Path != "" {
			d.NeedsTypes = false
			d.ExtraTypesImports = append(d.ExtraTypesImports, extra)
		}
		// Wire binders cast cross-package scalar fields to their
		// declared Go type — `req.ID = shared.ID(r.PathValue("id"))`.
		// Without this import-walk the `shared` package never makes
		// it into the file's import block, and the cast compiles to
		// `undefined: shared`. Walk every field type of the request
		// struct so transitively-referenced packages get pulled in.
		fieldImports := collectRequestFieldImports(m, pkg, crossPkg)
		for _, alias := range sortedKeys(fieldImports) {
			d.ExtraTypesImports = append(d.ExtraTypesImports, extraImport{
				Alias: alias,
				Path:  fieldImports[alias],
			})
		}
		var err error
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams, d.NeedsStrconv, err = collectBindings(m, pkg, d.RequestPkgAlias, scalars)
		if err != nil {
			return transportData{}, err
		}
		// JSON body decode is only needed when at least one field is
		// body-bound (default for body verbs unless explicitly tagged).
		d.BodyDecode = hasBodyVerb(m.Verb) && hasUnboundField(m, pkg)
	}
	if hasPassthroughDecorator(m.Decorators) {
		d.IsPassthrough = true
		// Passthrough endpoints reach into r/w directly, so the
		// handler skips request decoding entirely.
		d.NeedsTypes = false
		d.HasRequest = false
		d.HasResponse = false
		d.BodyDecode = false
		d.PathParams = nil
		d.QueryParams = nil
		d.HeaderParams = nil
		d.CookieParams = nil
	}
	if hasReq && !d.IsPassthrough {
		forms, files, formStrconv, ferr := collectFormBindings(m, pkg, d.RequestPkgAlias)
		if ferr != nil {
			return d, ferr
		}
		if len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
			if formStrconv {
				d.NeedsStrconv = true
			}
			// Match the stdlib historical 32 MiB floor unless the
			// method's `@maxBodySize` declares a higher cap. The
			// MaxBytesReader at the route layer still enforces the
			// declared cap; this knob only governs how much the
			// multipart parser keeps in memory before spilling.
			const stdlibDefault int64 = 32 << 20
			d.MultipartMaxMemory = stdlibDefault
			if n := sizeDecoratorArg(m.Decorators, "maxBodySize"); n > stdlibDefault {
				d.MultipartMaxMemory = n
			}
		}
	}
	if hasResp {
		d.RespHeaders, d.RespCookies = collectResponseBindings(m, pkg)
	}
	if hasReq {
		d.Defaults = collectDefaults(m, pkg, d.RequestPkgAlias)
	}
	return d, nil
}

func hasPassthroughDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "passthrough") }

// hasDeprecatedDecorator reports whether `@deprecated` is declared in
// the chain. Used by OpenAPI codegen to flag operations and schemas,
// and by the types emitter to prepend a Go-style `// Deprecated:` line
// (which `go vet` / `staticcheck` honour).
func hasDeprecatedDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "deprecated") }

// deprecatedReason returns the optional `@deprecated("...")` argument,
// or "" when the decorator carries no message. Both forms are valid:
// `@deprecated` alone is "deprecated, no reason given"; `@deprecated("use Foo")`
// supplies the reason that ends up in `// Deprecated:` comments and
// OpenAPI descriptions.
func deprecatedReason(ds []*ast.Decorator) string {
	return decoratorStringArg(ds, "deprecated")
}

// decoratorStringArg returns the first positional string argument of
// the named decorator, or "" when absent. Used for simple
// "decorator-with-text" forms: `@doc("...")`, `@summary("...")`,
// `@deprecated("...")`. Multiple-argument or object-form decorators
// have their own dedicated extractors.
func decoratorStringArg(ds []*ast.Decorator, name string) string {
	for _, d := range ds {
		if d.Name != name {
			continue
		}
		if len(d.Args) == 0 {
			return ""
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return s.Value
		}
	}
	return ""
}

// resolveDescription returns the OpenAPI description for a node by
// preferring the explicit `@doc("...")` decorator over the leading `//`
// comment block. Both forms are documented in the README; `@doc` wins
// because it's an intentional override, while comments are often
// implementation notes the API consumer doesn't care about.
func resolveDescription(decs []*ast.Decorator, doc []string) string {
	if s := decoratorStringArg(decs, "doc"); s != "" {
		return s
	}
	if len(doc) == 0 {
		return ""
	}
	return strings.Join(doc, "\n")
}

// collectFormBindings returns the per-field form bindings used by the
// multipart handler. `file`-typed fields land in files; plain string
// fields without an explicit binding fall back to form-string. Fields
// already bound to path/query/header/cookie are skipped - those have
// dedicated emission paths in the multipart template.

func pathString(p *ast.Path) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, seg := range p.Segments {
		sb.WriteByte('/')
		if seg.Param {
			sb.WriteByte('{')
			sb.WriteString(seg.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(seg.Literal)
		}
	}
	return sb.String()
}

// queryPrim is the per-primitive recipe for parsing a query-string
// value. Strings short-circuit (no parser); other kinds dispatch to
// strconv. `bits` is forwarded to ParseInt / ParseUint / ParseFloat;
// bool ignores it. `goType` is the cast applied to the parsed numeric
// to land in the target Go field type.

type queryPrim struct {
	parser string // strconv.ParseX function or "" for direct string
	bits   int
	goType string // cast target ("int", "int8", "float64", ...) or "" for direct
	label  string // human-readable kind for error messages
}

var queryPrims = map[string]queryPrim{
	"string":  {label: "string"},
	"bool":    {parser: "strconv.ParseBool", label: "bool"},
	"int":     {parser: "strconv.ParseInt", bits: 64, goType: "int", label: "int"},
	"int8":    {parser: "strconv.ParseInt", bits: 8, goType: "int8", label: "int"},
	"int16":   {parser: "strconv.ParseInt", bits: 16, goType: "int16", label: "int"},
	"int32":   {parser: "strconv.ParseInt", bits: 32, goType: "int32", label: "int"},
	"int64":   {parser: "strconv.ParseInt", bits: 64, goType: "int64", label: "int"},
	"uint":    {parser: "strconv.ParseUint", bits: 64, goType: "uint", label: "uint"},
	"uint8":   {parser: "strconv.ParseUint", bits: 8, goType: "uint8", label: "uint"},
	"uint16":  {parser: "strconv.ParseUint", bits: 16, goType: "uint16", label: "uint"},
	"uint32":  {parser: "strconv.ParseUint", bits: 32, goType: "uint32", label: "uint"},
	"uint64":  {parser: "strconv.ParseUint", bits: 64, goType: "uint64", label: "uint"},
	"float32": {parser: "strconv.ParseFloat", bits: 32, goType: "float32", label: "float"},
	"float64": {parser: "strconv.ParseFloat", bits: 64, goType: "float64", label: "float"},
}

// collectRequestFieldImports walks every WIRE-BOUND field of the
// method's request type (path / query / header / cookie, explicit
// or auto-promoted) and returns the cross-package import paths
// reached through those field types. Result keys the DSL package
// name (= Go alias used in the binder cast) to its full Go import
// path, ready to append to the handler's extra-imports block.
//
// Body-only fields are intentionally skipped: the JSON decoder
// reads them through the request struct's own package, so no
// extra import is needed at the handler-file level. Including them
// would emit unused `import` statements that `go build` rejects.
//
// Without this walk, a request with a cross-package scalar field
// auto-promoted to @path (`id shared.ID` on a `/{id}` route)

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
	kind        string
	singleExpr  func(wireName string) string
	arrayExpr   func(wireName string) string // "" if arrays unsupported for this source
	cookieGuard bool
}

// querySource / headerSource / cookieSource / formSource build the
// wireSource for each of the four supported bindings. Hot path - kept
// allocation-free by capturing the wireName by value at the call site.
func querySource() wireSource {
	return wireSource{
		kind:       "query",
		singleExpr: func(n string) string { return fmt.Sprintf("r.URL.Query().Get(%q)", n) },
		arrayExpr:  func(n string) string { return fmt.Sprintf("r.URL.Query()[%q]", n) },
	}
}

func headerSource() wireSource {
	return wireSource{
		kind:       "header",
		singleExpr: func(n string) string { return fmt.Sprintf("r.Header.Get(%q)", n) },
		arrayExpr:  func(n string) string { return fmt.Sprintf("r.Header.Values(%q)", n) },
	}
}

func cookieSource() wireSource {
	return wireSource{
		kind:        "cookie",
		singleExpr:  func(string) string { return "c.Value" },
		arrayExpr:   func(string) string { return "" },
		cookieGuard: true,
	}
}

func formSource() wireSource {
	return wireSource{
		kind:       "form",
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
func renderWireBindLine(f *ast.Field, pkg *semantic.Package, pkgAlias, wireName string, src wireSource) (string, bool, error) {
	if f.Type == nil {
		return "", false, fmt.Errorf("field %q has no resolved type", f.Name)
	}
	if f.Type.Map != nil {
		return "", false, fmt.Errorf("field %q: map types cannot bind to @%s - only string/bool/int*/uint*/float* and arrays of those", f.Name, src.kind)
	}
	if f.Type.Named == nil {
		return "", false, fmt.Errorf("field %q: anonymous types cannot bind to @%s - only string/bool/int*/uint*/float* and arrays of those", f.Name, src.kind)
	}
	if len(f.Type.Named.Args) > 0 {
		return "", false, fmt.Errorf("field %q: generic type %s<...> cannot bind to @%s - only string/bool/int*/uint*/float* and arrays of those", f.Name, f.Type.Named.Name.String(), src.kind)
	}
	if f.Type.Array && src.arrayExpr(wireName) == "" {
		return "", false, fmt.Errorf("field %q: arrays cannot bind to @%s - this wire format carries a single value per name", f.Name, src.kind)
	}
	declName := f.Type.Named.Name.String()
	prim, ok := queryPrims[declName]
	cast := ""
	if !ok {
		if pkg != nil {
			if sc, scOk := pkg.Scalars[declName]; scOk && sc != nil {
				if p2, pOk := queryPrims[sc.Primitive]; pOk {
					prim = p2
					ok = true
					cast = declName
				}
			}
			if !ok {
				if ed, edOk := pkg.Enums[declName]; edOk && ed != nil {
					switch firstEnumKind(ed) {
					case ast.EnumBare, ast.EnumString:
						prim = queryPrims["string"]
						ok = true
						cast = declName
					case ast.EnumInt:
						prim = queryPrims["int"]
						ok = true
						cast = declName
					}
				}
			}
		}
	}
	if !ok {
		return "", false, fmt.Errorf("field %q: type %s cannot bind to @%s - only string/bool/int*/uint*/float*, scalars/enums, and arrays of those (struct/[]struct must ride the body via a body verb instead)", f.Name, describeFieldType(f), src.kind)
	}
	if cast != "" && pkgAlias != "" {
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
		GoName:        GoFieldName(f.Name),
		Label:         prim.label,
		SingleSource:  singleSrc,
		ArraySource:   arraySrc,
	}
	var shape string
	needsStrconv := false
	if f.Type.Array {
		if prim.parser == "" {
			if cast == "" {
				shape = renderWireBindShape("directSlice", data)
			} else {
				data.Wrap = wrap("_v")
				shape = renderWireBindShape("arrayString", data)
			}
		} else {
			data.ParseCall = parseCall(prim)
			data.Wrap = wrap(castExpr(prim, "_n"))
			shape = renderWireBindShape("arrayParsed", data)
			needsStrconv = true
		}
	} else {
		// Single (non-array). Optional non-string primitives are not
		// supported: `*int` from a wire string would need a tri-state
		// (absent / empty / parsed) and no clean idiom exists yet.
		if f.Type.Optional && prim.parser != "" {
			return "", false, fmt.Errorf("field %q: optional %s cannot bind to @%s — drop the `?` (use 0 / false as the absent sentinel) or move to body", f.Name, prim.label, src.kind)
		}
		if prim.parser == "" {
			if f.Type.Optional {
				if cast == "" {
					shape = renderWireBindShape("optionalStringNoCast", data)
				} else {
					data.Wrap = wrap("_v")
					shape = renderWireBindShape("optionalStringCast", data)
				}
			} else {
				data.Wrap = wrap(singleSrc)
				shape = renderWireBindShape("directSingle", data)
			}
		} else {
			data.ParseCall = parseCall(prim)
			data.Wrap = wrap(castExpr(prim, "_n"))
			shape = renderWireBindShape("singleParsed", data)
			needsStrconv = true
		}
	}
	if src.cookieGuard {
		shape = wrapCookieGuard(wireName, shape)
	}
	return shape, needsStrconv, nil
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
	ParseCall     string
	Label         string
	SingleSource  string
	ArraySource   string
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

// describeFieldType renders a short human-readable form of f's type
// for error messages - `[]Point`, `Page<Book>`, `map<string,int>`,
// etc. Used by the binding-rejection paths so the user sees the
// exact shape that violated the binding contract.

func parseCall(p queryPrim) string {
	switch p.parser {
	case "strconv.ParseBool":
		return "strconv.ParseBool(_v)"
	case "strconv.ParseFloat":
		return fmt.Sprintf("strconv.ParseFloat(_v, %d)", p.bits)
	default: // ParseInt / ParseUint
		return fmt.Sprintf("%s(_v, 10, %d)", p.parser, p.bits)
	}
}

// castExpr wraps the parsed value in the target Go type cast when
// needed. Bool returns the parsed value directly (it's already typed);
// numeric primitives need a cast from int64/uint64/float64 to the
// declared field type.
func castExpr(p queryPrim, varName string) string {
	if p.goType == "" || p.parser == "strconv.ParseBool" {
		return varName
	}
	return fmt.Sprintf("%s(%s)", p.goType, varName)
}

// isPlainStringField reports whether f is a non-array, non-optional
// `string`. Used internally for the auto-promotion safety check (a
// body field that happens to share a name with a path segment is
// silently skipped instead of producing a hard error). Path / header /
// cookie binding goes through [stringBindable] which additionally
// accepts string scalars and string-backed enums.

func GenerateTransportHelpers(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	t := tmpl("transport_helpers.tmpl")
	for _, svcName := range sortedServices(pkg) {
		dir := filepath.Join(projectRoot, cfg.Output.Transport, ServiceDir(svcName))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		formatted, err := renderGo(t, helpersData{Package: ServicePackage(svcName)})
		if err != nil {
			return fmt.Errorf("render handler helpers: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "errors.go"), formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}
