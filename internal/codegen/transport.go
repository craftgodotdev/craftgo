package codegen

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
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
	// NeedsStrconv tells the template to import "strconv" when a
	// response header / cookie field needs number→string formatting.
	// Request parsing runs through the generic [server] bind helpers, so
	// it no longer pulls strconv into the handler.
	NeedsStrconv bool
	// SuccessStatus is the resolved HTTP success code for this method
	// (see [methodSuccessStatus]). SuccessStatusExpr is the Go source
	// the template emits in `w.WriteHeader(...)` — an `http.StatusXxx`
	// constant for the standard codes, falling back to the decimal
	// literal. The response branch only writes the header when the code
	// is not the implicit 200 the encoder already produces; the
	// no-response branch always writes it (defaulting to 204).
	SuccessStatus     int
	SuccessStatusExpr string
	ServiceImport     string
	TypesImport       string
	SvccontextImport  string
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
	// Required mirrors the field's non-optional state (the inverse of a
	// `?` suffix). Set by [collectFormBindings] so [multipartRequestBody]
	// can list non-optional form/file fields under the multipart schema's
	// `required[]`; without it every uploaded field reads as optional to
	// generated clients even when the server's validator rejects a
	// missing one. Unused by the wire-param bindings (path/query/...),
	// which carry the required flag on the OpenAPI parameter directly.
	Required bool
	// Field is the source AST field, set by [collectFormBindings] so
	// [multipartRequestBody] can build the SAME constrained schema
	// (`@maxLength`, nullability, ...) the JSON body component carries
	// instead of a bare `{type: string}`. Without it a generated client
	// sees the served multipart text field as an unconstrained string
	// and skips the limits the server's validator still enforces. nil for
	// wire-param bindings.
	Field *ast.Field
	// IsArray marks a `file[]` (repeated-part) upload. Set by
	// [collectFormBindings] for file entries so the multipart template binds
	// from `r.MultipartForm.File[name]` (`[]*multipart.FileHeader`) instead of
	// the single-file `r.FormFile`, and [multipartRequestBody] emits an
	// `array` schema. Single-file and text bindings leave it false.
	IsArray bool
}

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
	return GenerateTransportResolved(pkg, cfg, projectRoot, nil)
}

// GenerateTransportWith is the explicit-tables entry for single-package
// tests that build CrossPkg / ScalarTable directly.
// [GenerateTransportResolved] accepts a [ProjectResolver] bundling
// every cross-package table.
func GenerateTransportWith(pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg, scalars ScalarTable) error {
	r := &ProjectResolver{Scalars: scalars, CrossPkg: crossPkg}
	return GenerateTransportResolved(pkg, cfg, projectRoot, r)
}

// GenerateTransportResolved is the canonical entry point. The
// [ProjectResolver] supplies every project-wide lookup the handler
// emit chain may consult — scalar inheritance, cross-package
// enum/type resolution for binding casts, and the Go import paths
// the generated handler file needs when it emits qualified
// identifiers. nil resolver yields the legacy single-package
// behaviour: only `pkg`'s local symbols resolve.
func GenerateTransportResolved(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *ProjectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateTransportFor(svcName, svc, pkg, cfg, projectRoot, r); err != nil {
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
func generateTransportFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, r *ProjectResolver) error {
	groups := methodGroups(svc)
	jsonTpl := tmpl("transport.tmpl")
	passthroughTpl := tmpl("transport-passthrough.tmpl")
	multipartTpl := tmpl("transport-multipart.tmpl")
	for _, m := range svc.Methods {
		group := groups[m.Name]
		imps := importPathsForGroup(cfg, pkg, svcName, group)
		dir := serviceOutputDir(projectRoot, cfg.Output.Transport, svcName, group)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		data, err := buildTransportData(svcName, m, imps, pkg, r)
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
			return fmt.Errorf("render %s transport: %w", idents.KebabCase(m.Name), err)
		}
		filename := idents.KebabCase(m.Name) + ".go"
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
// The resolver drives cross-package request resolution: when a method
// declares `request foo.Cred` and `foo` lives in another DSL package,
// the handler's Go file gets an extra import for that package and
// the generated `var req foo.Cred` line uses the package name as the
// Go alias. Scalar inheritance for cross-package primitive bindings
// (`shared.ID @path`) also flows through the resolver.
func buildTransportData(svcName string, m *ast.Method, imps importPaths, pkg *semantic.Package, r *ProjectResolver) (transportData, error) {
	crossPkg := r.crossPkgMap()
	scalars := r.scalars()
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
		BodyVerb:         wire.IsBodyVerb(m.Verb),
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
		extraSeen := map[string]bool{}
		addExtra := func(e extraImport) {
			if e.Path == "" || extraSeen[e.Path] {
				return
			}
			extraSeen[e.Path] = true
			d.ExtraTypesImports = append(d.ExtraTypesImports, e)
		}
		// Cross-package request → drop the canonical types import; the only
		// types reference in the handler body now resolves via the cross-pkg
		// alias. Edge case: a cross-pkg generic with a LOCAL type-arg
		// (`shared.WrapBag<Item>` → `shared.WrapBag[types.Item]`) still
		// references the canonical `types` alias for the inner arg, so keep
		// the import when the rendered type carries a `types.` reference —
		// mirroring the scaffold-service guard. But when the cross-pkg request
		// package itself is named `types`, its alias IS `types`, so the
		// `types.` references are the cross-pkg ones (covered by `extra`); keep
		// the canonical import too and they collide (`types redeclared`).
		if extra.Path != "" {
			if alias == "types" || !strings.Contains(bare, "types.") {
				d.NeedsTypes = false
			}
			addExtra(extra)
		}
		// The request type's own generic type-args reach further packages
		// (`genpkg.Box<argpkg.Owner>` → `var req genpkg.Box[argpkg.Owner]`),
		// so import every arg package; missing one leaves the handler
		// referencing an unimported package (`undefined: argpkg`).
		argSet := map[string]bool{}
		walkCrossPkgImports(&ast.TypeRef{Named: m.Request}, crossPkg, argSet)
		pathAlias := map[string]string{}
		for a, p := range crossPkg {
			pathAlias[p] = a
		}
		for path := range argSet {
			addExtra(extraImport{Alias: pathAlias[path], Path: path})
		}
		// Wire binders cast cross-package scalar fields to their
		// declared Go type — `req.ID = shared.ID(r.PathValue("id"))`.
		// Without this import-walk the `shared` package never makes
		// it into the file's import block, and the cast compiles to
		// `undefined: shared`. Walk every field type of the request
		// struct so transitively-referenced packages get pulled in.
		fieldImports := collectRequestFieldImports(m, pkg, crossPkg, r)
		for _, alias := range sortedKeys(fieldImports) {
			addExtra(extraImport{Alias: alias, Path: fieldImports[alias]})
		}
		var err error
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams, err = collectBindings(m, pkg, d.RequestPkgAlias, r)
		if err != nil {
			return transportData{}, err
		}
		// collectBindings reads crossPkg/scalars off the resolver
		// itself, so the locals are unused here.
		_ = crossPkg
		_ = scalars
		// JSON body decode is only needed when at least one field is
		// body-bound (default for body verbs unless explicitly tagged).
		d.BodyDecode = wire.IsBodyVerb(m.Verb) && hasUnboundField(m, pkg, r)
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
		forms, files, ferr := collectFormBindings(m, pkg, d.RequestPkgAlias, r)
		if ferr != nil {
			return d, ferr
		}
		if len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
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
		var respStrconv bool
		d.RespHeaders, d.RespCookies, respStrconv = collectResponseBindings(m, pkg, r)
		if respStrconv {
			d.NeedsStrconv = true
		}
	}
	if hasReq {
		d.Defaults = collectDefaults(m, pkg, d.RequestPkgAlias, r)
	}
	// Resolve the success status once so the handler's WriteHeader and
	// the OpenAPI response key stay in lockstep. Passthrough handlers
	// write their own status, so the field is ignored by that template.
	d.SuccessStatus = methodSuccessStatus(m)
	d.SuccessStatusExpr = statusConstExpr(d.SuccessStatus)
	return d, nil
}

// statusConstExpr renders an HTTP status code as the Go source the
// transport template drops into `w.WriteHeader(...)`. Standard 2xx
// success codes map to their `net/http` constant so the generated code
// reads like hand-written code; any other code (an unusual `@status`
// override) falls back to the decimal literal, which always compiles.
func statusConstExpr(code int) string {
	switch code {
	case http.StatusOK:
		return "http.StatusOK"
	case http.StatusCreated:
		return "http.StatusCreated"
	case http.StatusAccepted:
		return "http.StatusAccepted"
	case http.StatusNonAuthoritativeInfo:
		return "http.StatusNonAuthoritativeInfo"
	case http.StatusNoContent:
		return "http.StatusNoContent"
	case http.StatusResetContent:
		return "http.StatusResetContent"
	case http.StatusPartialContent:
		return "http.StatusPartialContent"
	}
	return strconv.Itoa(code)
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
