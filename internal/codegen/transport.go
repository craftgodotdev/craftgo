package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
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
	PathParams      []paramBinding
	QueryParams     []paramBinding
	HeaderParams    []paramBinding
	CookieParams    []paramBinding
	FormStrings     []paramBinding
	FormFiles       []paramBinding
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
// Equivalent to [GenerateTransportPackage] with a nil [CrossPkg]
// context - kept so single-package callers / tests stay unchanged.
func GenerateTransport(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	return GenerateTransportPackage(pkg, cfg, projectRoot, nil)
}

// GenerateTransportPackage is the multi-package variant of
// [GenerateTransport]. crossPkg supplies the alias→Go-import-path
// table so a method whose request type lives in a sibling DSL
// package (`request shared.Cred`) renders the correct Go reference
// and import statements.
func GenerateTransportPackage(pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateTransportFor(svcName, svc, pkg, cfg, projectRoot, crossPkg); err != nil {
			return err
		}
	}
	return nil
}

// sortedServices returns the package's service names in deterministic order.
func sortedServices(pkg *semantic.Package) []string {
	out := make([]string, 0, len(pkg.Services))
	for n := range pkg.Services {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// generateTransportFor emits all per-method handler files for a single
// service. Each method becomes a separate file so that user-friendly diffs
// are produced when only one endpoint changes.
func generateTransportFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg) error {
	imps := importPathsFor(cfg, pkg, svcName)
	dir := filepath.Join(projectRoot, cfg.Output.Transport, ServiceDir(svcName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	jsonTpl := tmpl("transport.tmpl")
	passthroughTpl := tmpl("transport-passthrough.tmpl")
	multipartTpl := tmpl("transport-multipart.tmpl")
	for _, m := range svc.Methods {
		data, err := buildTransportData(svcName, m, imps, pkg, crossPkg)
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
func buildTransportData(svcName string, m *ast.Method, imps importPaths, pkg *semantic.Package, crossPkg CrossPkg) (transportData, error) {
	hasReq := m.Request != nil
	hasResp := m.Response != nil && m.Response.Type != nil
	d := transportData{
		Package:          ServicePackage(svcName),
		Method:           m.Name,
		Verb:             httpVerb(m.Verb),
		Doc:              m.Doc,
		HasRequest:       hasReq,
		HasResponse:      hasResp,
		BodyVerb:         hasBodyVerb(m.Verb),
		NeedsTypes:       hasReq || hasResp,
		ServiceImport:    imps.Service,
		TypesImport:      imps.Types,
		SvccontextImport: imps.Svccontext,
	}
	// Handler body only references `types.X` for request decoding;
	// the response is passed through to the encoder, so we only need
	// the types import when there's a request type to bind.
	d.NeedsTypes = hasReq
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
		var err error
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams, d.NeedsStrconv, err = collectBindings(m, pkg)
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
		if forms, files := collectFormBindings(m, pkg); len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
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

// collectDefaults walks the request type's fields and returns one
// [defaultBinding] per field that carries `@default(value)`. Only plain
// (non-array, non-optional, non-map) primitive fields are filled - the
// pre-fill semantics are uncertain on collections, and pointer-typed
// optional fields would still get nil-overwritten by the JSON decoder
// for absent keys. Unknown literal kinds are skipped silently.
func collectDefaults(m *ast.Method, pkg *semantic.Package, pkgAlias string) []defaultBinding {
	if m.Request == nil {
		return nil
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return nil
	}
	var out []defaultBinding
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		if f.Type == nil || f.Type.Map != nil {
			continue
		}
		lit := defaultLiteral(f, pkg, pkgAlias)
		if lit == "" {
			continue
		}
		out = append(out, defaultBinding{
			GoName:  GoFieldName(f.Name),
			Literal: lit,
			Ptr:     goFieldIsPointer(f),
		})
	}
	return out
}

// defaultLiteral returns the Go-source form of a `@default(...)`
// value, or "" when the decorator is absent or unrenderable. The
// supported shapes are: string / int / float / bool literals,
// IdentExpr (resolved to an enum constant), and ArrayLit (rendered
// recursively as a Go slice literal). Map / struct / generic field
// types fall through to "" - the semantic phase has already flagged
// the unsupported combination.
func defaultLiteral(f *ast.Field, pkg *semantic.Package, pkgAlias string) string {
	for _, d := range f.Decorators {
		if d.Name != "default" || len(d.Args) != 1 {
			continue
		}
		return renderDefault(f.Type, d.Args[0].Value, pkg, pkgAlias)
	}
	return ""
}

// renderDefault produces the Go source for one `@default(...)` value
// against the field's resolved type. Recurses through array / array
// elements; returns "" when the value can't be rendered (mixed kind,
// unknown enum, struct element, etc.). pkgAlias is the Go-side
// alias of the types package used by the request struct (e.g.
// "types") so named-type references (enums, scalars) emit as
// `<alias>.<Name>` and stay valid in the handler's own package.
func renderDefault(t *ast.TypeRef, v ast.Expr, pkg *semantic.Package, pkgAlias string) string {
	if t == nil {
		return ""
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			return ""
		}
		elemT := arrayElemTypeRef(t)
		elemGo := qualifyNamed(GoTypeRef(elemT), elemT, pkg, pkgAlias)
		if elemGo == "" {
			return ""
		}
		parts := make([]string, 0, len(arr.Elements))
		for _, e := range arr.Elements {
			p := renderDefault(elemT, e, pkg, pkgAlias)
			if p == "" {
				return ""
			}
			parts = append(parts, p)
		}
		return "[]" + elemGo + "{" + strings.Join(parts, ", ") + "}"
	}
	switch lit := v.(type) {
	case *ast.StringLit:
		return strconv.Quote(lit.Value)
	case *ast.IntLit:
		return strconv.FormatInt(lit.Value, 10)
	case *ast.FloatLit:
		return strconv.FormatFloat(lit.Value, 'g', -1, 64)
	case *ast.BoolLit:
		if lit.Value {
			return "true"
		}
		return "false"
	case *ast.IdentExpr:
		return enumDefaultConst(t, pkg, lit, pkgAlias)
	}
	return ""
}

// qualifyNamed prefixes a Go type reference with `<pkgAlias>.` when
// the underlying TypeRef points at a project-defined named type
// (enum or scalar) - those constants live in the types package and
// the handler file's package needs the alias to reach them.
// Primitives stay bare.
func qualifyNamed(goName string, t *ast.TypeRef, pkg *semantic.Package, pkgAlias string) string {
	if pkgAlias == "" || goName == "" {
		return goName
	}
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 1 {
		return goName
	}
	name := t.Named.Name.Parts[0]
	if _, ok := pkg.Enums[name]; ok {
		return pkgAlias + "." + goName
	}
	if _, ok := pkg.Scalars[name]; ok {
		return pkgAlias + "." + goName
	}
	return goName
}

// arrayElemTypeRef returns the element TypeRef of an array. Drops
// the Array marker and decrements ArrayDepth so nested-array
// elements (rare but legal) collapse one level at a time.
func arrayElemTypeRef(t *ast.TypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	clone := *t
	clone.Array = false
	if clone.ArrayDepth > 0 {
		clone.ArrayDepth--
	}
	if clone.ArrayDepth > 0 {
		clone.Array = true
	}
	return &clone
}

// enumDefaultConst resolves an `@default(<Ident>)` reference to its
// emitted Go constant name. The semantic phase has already validated
// that the field type is an enum and the ident matches a declared
// value; this function reproduces buildEnumView's dedup so the
// const-name lookup hits the same identifier even when value names
// differ only in case.
func enumDefaultConst(t *ast.TypeRef, pkg *semantic.Package, v *ast.IdentExpr, pkgAlias string) string {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	if len(t.Named.Name.Parts) != 1 {
		return ""
	}
	enumName := t.Named.Name.Parts[0]
	ed, ok := pkg.Enums[enumName]
	if !ok || v.Name == nil || len(v.Name.Parts) != 1 {
		return ""
	}
	valueName := v.Name.Parts[0]
	idx := -1
	enumVals := ed.EnumValues()
	dslNames := make([]string, len(enumVals))
	for i, val := range enumVals {
		dslNames[i] = val.Name
		if val.Name == valueName {
			idx = i
		}
	}
	if idx < 0 {
		return ""
	}
	resolved, _ := idents.DedupGoFieldNames(dslNames)
	bare := enumName + resolved[idx]
	if pkgAlias != "" {
		return pkgAlias + "." + bare
	}
	return bare
}

// collectResponseBindings walks the response type's fields and returns the
// `@header` / `@cookie` bindings that should be written to the
// http.ResponseWriter before the JSON body. Both kinds accept plain string
// fields only - richer types (slices, maps, structs) stay in the body
// where the JSON encoder can handle them.
func collectResponseBindings(m *ast.Method, pkg *semantic.Package) (headers, cookies []paramBinding) {
	if m.Response == nil || m.Response.Type == nil {
		return nil, nil
	}
	td, ok := pkg.Types[m.Response.Type.Name.String()]
	if !ok {
		return nil, nil
	}
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		if !isPlainStringField(f) {
			continue
		}
		entry := paramBinding{DSLName: f.Name, GoName: GoFieldName(f.Name)}
		switch bindingFromDecorators(f.Decorators) {
		case "header":
			headers = append(headers, entry)
		case "cookie":
			cookies = append(cookies, entry)
		}
	}
	return headers, cookies
}

// hasPassthroughDecorator reports whether `@passthrough` is declared
// on the method. Passthrough methods bypass the framework entirely:
// codegen emits a thin `http.HandlerFunc` that delegates to logic
// without parsing, validating, or encoding anything.
func hasPassthroughDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d.Name == "passthrough" {
			return true
		}
	}
	return false
}

// hasDeprecatedDecorator reports whether `@deprecated` is declared in
// the chain. Used by OpenAPI codegen to flag operations and schemas,
// and by the types emitter to prepend a Go-style `// Deprecated:` line
// (which `go vet` / `staticcheck` honour).
func hasDeprecatedDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d.Name == "deprecated" {
			return true
		}
	}
	return false
}

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
func collectFormBindings(m *ast.Method, pkg *semantic.Package) (strings, files []paramBinding) {
	if m.Request == nil {
		return nil, nil
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return nil, nil
	}
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		switch bindingFromDecorators(f.Decorators) {
		case "path", "query", "header", "cookie":
			continue
		}
		if pathSegs[f.Name] {
			continue
		}
		entry := paramBinding{DSLName: f.Name, GoName: GoFieldName(f.Name)}
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "file" {
			files = append(files, entry)
			continue
		}
		if isPlainStringField(f) {
			strings = append(strings, entry)
		}
	}
	return strings, files
}

// collectBindings walks the request type's fields and returns per-kind
// binding tables (path, query, header, cookie) plus a flag noting
// whether any emitted block reaches into `strconv`.
//
// Resolution order for a field's bucket:
//  1. Explicit `@path` / `@query` / `@header` / `@cookie` decorator wins.
//  2. A name match against a `{param}` segment in the method path
//     promotes the field to `path` (so `id string` on `/users/{id}`
//     auto-binds without a decorator).
//  3. For non-body verbs (GET / DELETE / HEAD / OPTIONS) any leftover
//     unbound field defaults to `query` - the README's "Default
//     binding theo verb" rule wired up at last.
//
// Path / header / cookie still require string-typed fields (URLs and
// HTTP headers carry strings on the wire). Query supports the full
// numeric / float / bool / array matrix; the per-field [Bind] is
// pre-rendered Go that the handler template emits verbatim.
//
// Unsupported binding shapes (struct/[]struct/map on @query, non-string
// on @path/@header/@cookie) return a non-nil error. Silent skips were
// removed in favour of fail-fast feedback at `craftgo gen` time.
func collectBindings(m *ast.Method, pkg *semantic.Package) (path, query, header, cookie []paramBinding, needsStrconv bool, err error) {
	if m.Request == nil {
		return
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return
	}
	reqName := m.Request.Name.String()
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	autoQuery := !hasBodyVerb(m.Verb)
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		bind := bindingFromDecorators(f.Decorators)
		auto := false
		if bind == "" && pathSegs[f.Name] {
			bind = "path"
			auto = true
		}
		if bind == "" && autoQuery {
			bind = "query"
			auto = true
		}
		switch bind {
		case "path":
			if !isPlainStringField(f) {
				if auto {
					// Auto-promoted from a path segment match - silently skip
					// so a body field that happens to share a name with a
					// segment doesn't break the build. Explicit @path is
					// strict (handled above by entering this case via the
					// decorator scan); auto-promotion is permissive.
					continue
				}
				err = fmt.Errorf("%s.%s: @path requires a non-array, non-optional string field - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			path = append(path, paramBinding{
				DSLName: f.Name,
				GoName:  GoFieldName(f.Name),
				Bind:    fmt.Sprintf("req.%s = r.PathValue(%q)", GoFieldName(f.Name), f.Name),
			})
		case "query":
			line, needs, lerr := renderQueryBindLine(f)
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			query = append(query, paramBinding{
				DSLName: f.Name,
				GoName:  GoFieldName(f.Name),
				Bind:    line,
			})
		case "header":
			if !isPlainStringField(f) {
				err = fmt.Errorf("%s.%s: @header requires a non-array, non-optional string field - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			header = append(header, paramBinding{
				DSLName: f.Name,
				GoName:  GoFieldName(f.Name),
				Bind:    fmt.Sprintf("req.%s = r.Header.Get(%q)", GoFieldName(f.Name), f.Name),
			})
		case "cookie":
			if !isPlainStringField(f) {
				err = fmt.Errorf("%s.%s: @cookie requires a non-array, non-optional string field - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			cookie = append(cookie, paramBinding{
				DSLName: f.Name,
				GoName:  GoFieldName(f.Name),
				Bind: fmt.Sprintf(`if c, err := r.Cookie(%q); err == nil {
	req.%s = c.Value
}`, f.Name, GoFieldName(f.Name)),
			})
		}
	}
	return
}

// pathString re-renders a method's path for error messages
// (`/books/{id}/cancel`); empty string when m has no path block.
//
// Hot path (called per method during routes-go emission): Builder
// keeps the per-segment append allocation-free.
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

// renderQueryBindLine returns the Go source that binds one field from
// the URL query string. Shape varies by field type:
//   - string single → `req.X = r.URL.Query().Get("x")`
//   - numeric/bool single → `if v := ...; v != "" { parse + cast }`
//   - []string → `req.X = r.URL.Query()["x"]`
//   - []numeric/bool → `for ... { parse + append }`
//
// Returns a non-nil error for unsupported field shapes (structs,
// []struct, maps, generics, ...) so the codegen surfaces the
// mistake at `craftgo gen` time instead of silently producing a
// handler that leaves the field zero-valued. The second return
// value is true when the rendered code references "strconv" so
// the caller can flip the import flag once.
func renderQueryBindLine(f *ast.Field) (string, bool, error) {
	if f.Type == nil {
		return "", false, fmt.Errorf("field %q has no resolved type", f.Name)
	}
	if f.Type.Map != nil {
		return "", false, fmt.Errorf("field %q: map types cannot bind to query - only string/bool/int*/uint*/float* and arrays of those", f.Name)
	}
	if f.Type.Named == nil {
		return "", false, fmt.Errorf("field %q: anonymous types cannot bind to query - only string/bool/int*/uint*/float* and arrays of those", f.Name)
	}
	if len(f.Type.Named.Args) > 0 {
		return "", false, fmt.Errorf("field %q: generic type %s<...> cannot bind to query - only string/bool/int*/uint*/float* and arrays of those", f.Name, f.Type.Named.Name.String())
	}
	prim, ok := queryPrims[f.Type.Named.Name.String()]
	if !ok {
		return "", false, fmt.Errorf("field %q: type %s cannot bind to query - only string/bool/int*/uint*/float* and arrays of those (struct/[]struct must ride the body via a body verb instead)", f.Name, describeFieldType(f))
	}
	dslName := f.Name
	goName := GoFieldName(f.Name)
	if f.Type.Array {
		if prim.parser == "" {
			// []string - direct slice assignment from query map.
			return fmt.Sprintf("req.%s = r.URL.Query()[%q]", goName, dslName), false, nil
		}
		// []numeric / []bool - loop, parse each, append to slice.
		return fmt.Sprintf(`for _, _v := range r.URL.Query()[%q] {
	_n, _err := %s
	if _err != nil {
		http.Error(w, %q+": invalid %s value: "+_err.Error(), http.StatusBadRequest)
		return
	}
	req.%s = append(req.%s, %s)
}`, dslName, parseCall(prim), dslName, prim.label, goName, goName, castExpr(prim, "_n")), true, nil
	}
	// Single (non-array). Optional non-string primitives are not
	// supported in v1 - `*int` from query would need a tri-state
	// (absent / empty / parsed) we don't have a clean idiom for yet.
	if f.Type.Optional && prim.parser != "" {
		return "", false, fmt.Errorf("field %q: optional %s cannot bind to query in v1 - drop the `?` (use 0 / false as the absent sentinel) or move to body", f.Name, prim.label)
	}
	if prim.parser == "" {
		// string single
		access := "req." + goName
		if f.Type.Optional {
			// `*string`: store the queried value via address.
			return fmt.Sprintf(`if _v := r.URL.Query().Get(%q); _v != "" {
	%s = &_v
}`, dslName, access), false, nil
		}
		return fmt.Sprintf("%s = r.URL.Query().Get(%q)", access, dslName), false, nil
	}
	// numeric / bool single, gated by non-empty query value.
	return fmt.Sprintf(`if _v := r.URL.Query().Get(%q); _v != "" {
	_n, _err := %s
	if _err != nil {
		http.Error(w, %q+": invalid %s value: "+_err.Error(), http.StatusBadRequest)
		return
	}
	req.%s = %s
}`, dslName, parseCall(prim), dslName, prim.label, goName, castExpr(prim, "_n")), true, nil
}

// describeFieldType renders a short human-readable form of f's type
// for error messages - `[]Point`, `Page<Book>`, `map<string,int>`,
// etc. Used by the binding-rejection paths so the user sees the
// exact shape that violated the binding contract.
func describeFieldType(f *ast.Field) string {
	if f == nil || f.Type == nil {
		return "<unresolved>"
	}
	t := f.Type
	switch {
	case t.Map != nil:
		return "map<...>"
	case t.Named == nil:
		return "<anonymous>"
	}
	name := t.Named.Name.String()
	if len(t.Named.Args) > 0 {
		name += "<...>"
	}
	if t.Array {
		name = "[]" + name
	}
	if t.Optional {
		name += "?"
	}
	return name
}

// parseCall renders the strconv.ParseX(_v, ...) expression for a
// numeric / bool primitive. Bool ignores bits; ParseFloat takes only
// (s, bits); ParseInt / ParseUint take (s, base, bits).
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
// `string`. Path / header / cookie binders still require this shape
// in v1 - those wire formats carry only strings, and lifting the
// restriction would just push parsing into every handler. Query is
// the broad path; see [renderQueryBindLine].
func isPlainStringField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Optional {
		return false
	}
	return f.Type.Named != nil && f.Type.Named.Name.String() == "string"
}

// hasUnboundField reports whether the request type has at least one
// field that does NOT carry an explicit @path/@query/@header/@cookie
// (or @body/@form) decorator and is not implicitly path-bound. The
// handler only emits the JSON body decode block when one or more body
// fields exist, so a request whose every field is parameter-bound
// skips the decode entirely.
func hasUnboundField(m *ast.Method, pkg *semantic.Package) bool {
	if m.Request == nil {
		return false
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return false
	}
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		switch bindingFromDecorators(f.Decorators) {
		case "path", "query", "header", "cookie":
			continue
		case "body", "form":
			return true
		}
		// Implicit path match short-circuits - that's a path field, not body.
		if pathSegs[f.Name] {
			continue
		}
		return true
	}
	return false
}

// GenerateTransportHelpers writes the small `errors.go` helper used by every
// generated handler in a service package. Kept in a separate file so the
// per-method handler files stay short and the helper can be regenerated
// without touching them.
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
