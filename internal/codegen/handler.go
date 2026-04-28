package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// handlerData is the template input for `handler.tmpl`. One value is built
// per (service, method) pair.
type handlerData struct {
	Package          string
	Method           string
	Verb             string
	RequestType      string
	Doc              []string
	HasRequest       bool
	HasResponse      bool
	BodyVerb         bool
	BodyDecode       bool
	NeedsTypes       bool
	IsStream         bool
	StreamFormat     string // "sse" / "ndjson" / "" when not a stream method
	StreamCtor       string // "SSE" / "NDJSON" — matches the runtime constructor name
	IsRaw            bool
	IsMultipart      bool
	PathParams       []paramBinding
	QueryParams      []paramBinding
	HeaderParams     []paramBinding
	CookieParams     []paramBinding
	FormStrings      []paramBinding
	FormFiles        []paramBinding
	LogicImport      string
	TypesImport      string
	SvccontextImport string
}

// paramBinding is one row of a handler's parameter-binding table.
// DSLName is the source-side identifier (e.g. the `{id}` segment, the
// query/header/cookie key); GoName is the exported field on the request
// struct that receives the value.
type paramBinding struct {
	DSLName string
	GoName  string
}

// helpersData is the template input for `handler_helpers.tmpl`.
type helpersData struct{ Package string }

// GenerateHandlers emits one `<method>_handler.go` per method per service
// under `<output.handler>/<servicePackage>/`. Each file contains a single
// exported `<Method>Handler(svcCtx) http.HandlerFunc` constructor that
// decodes the request, calls the user's logic, and writes the response.
//
// projectRoot is prepended to `cfg.Output.Handler` so the function can be
// called with paths relative to the manifest's directory.
func GenerateHandlers(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateHandlersFor(svcName, svc, pkg, cfg, projectRoot); err != nil {
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

// generateHandlersFor emits all per-method handler files for a single
// service. Each method becomes a separate file so that user-friendly diffs
// are produced when only one endpoint changes.
func generateHandlersFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	imps := importPathsFor(cfg, pkg, svcName)
	dir := filepath.Join(projectRoot, cfg.Output.Handler, ServiceDir(svcName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	jsonTpl := tmpl("handler.tmpl")
	streamTpl := tmpl("handler-stream.tmpl")
	rawTpl := tmpl("handler-raw.tmpl")
	rawStreamTpl := tmpl("handler-raw-stream.tmpl")
	multipartTpl := tmpl("handler-multipart.tmpl")
	for _, m := range svc.Methods {
		data := buildHandlerData(svcName, m, imps, pkg)
		t := jsonTpl
		switch {
		case data.IsRaw && data.IsStream:
			t = rawStreamTpl
		case data.IsStream:
			t = streamTpl
		case data.IsRaw:
			t = rawTpl
		case data.IsMultipart:
			t = multipartTpl
		}
		formatted, err := renderGo(t, data)
		if err != nil {
			return fmt.Errorf("render %s-handler: %w", kebabCase(m.Name), err)
		}
		filename := kebabCase(m.Name) + "-handler.go"
		if err := os.WriteFile(filepath.Join(dir, filename), formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// buildHandlerData populates the handlerData struct for one DSL method.
func buildHandlerData(svcName string, m *ast.Method, imps importPaths, pkg *semantic.Package) handlerData {
	hasReq := m.Request != nil
	hasResp := m.Response != nil && m.Response.Type != nil
	d := handlerData{
		Package:          ServicePackage(svcName),
		Method:           m.Name,
		Verb:             httpVerb(m.Verb),
		Doc:              m.Doc,
		HasRequest:       hasReq,
		HasResponse:      hasResp,
		BodyVerb:         hasBodyVerb(m.Verb),
		NeedsTypes:       hasReq || hasResp,
		LogicImport:      imps.Logic,
		TypesImport:      imps.Types,
		SvccontextImport: imps.Svccontext,
	}
	// Handler body only references `types.X` for request decoding;
	// the response is passed through to the encoder, so we only need
	// the types import when there's a request type to bind.
	d.NeedsTypes = hasReq
	if hasReq {
		d.RequestType = m.Request.Name.String()
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams = collectBindings(m, pkg)
		// JSON body decode is only needed when at least one field is
		// body-bound (default for body verbs unless explicitly tagged).
		d.BodyDecode = hasBodyVerb(m.Verb) && hasUnboundField(m, pkg)
	}
	if (m.Response != nil && m.Response.Stream) || hasStreamDecorator(m.Decorators) {
		d.IsStream = true
		d.StreamFormat = streamFormat(m)
		d.StreamCtor = streamCtor(d.StreamFormat)
	}
	if hasRawDecorator(m.Decorators) {
		d.IsRaw = true
	}
	if hasReq && !d.IsStream && !d.IsRaw {
		if forms, files := collectFormBindings(m, pkg); len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
		}
	}
	return d
}

// hasRawDecorator reports whether `@raw` is declared on the method.
func hasRawDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d.Name == "raw" {
			return true
		}
	}
	return false
}

// hasStreamDecorator reports whether `@stream` is declared on the
// method. The flag is also implicitly set by `response stream T` in
// the DSL — both forms route the codegen through the streaming
// templates.
func hasStreamDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d.Name == "stream" {
			return true
		}
	}
	return false
}

// collectFormBindings returns the per-field form bindings used by the
// multipart handler. `file`-typed fields land in files; plain string
// fields without an explicit binding fall back to form-string. Fields
// already bound to path/query/header/cookie are skipped — those have
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

// streamFormat reads the `@format(...)` decorator argument; defaults
// to `"sse"` when no format is declared so a bare `@stream` produces
// Server-Sent Events out of the box.
func streamFormat(m *ast.Method) string {
	for _, d := range m.Decorators {
		if d.Name != "format" || len(d.Args) == 0 {
			continue
		}
		switch v := d.Args[0].Value.(type) {
		case *ast.StringLit:
			if v.Value != "" {
				return v.Value
			}
		case *ast.IdentExpr:
			if name := v.Name.String(); name != "" {
				return name
			}
		}
	}
	return "sse"
}

// streamCtor maps a DSL stream-format name to the matching runtime
// constructor in pkg/server. Unknown formats fall back to SSE — same
// rationale as streamFormat's default.
func streamCtor(format string) string {
	switch format {
	case "ndjson", "jsonl":
		return "NDJSON"
	}
	return "SSE"
}

// collectBindings walks the request type's fields and returns per-kind
// binding tables. Path / query / header / cookie checks match the
// runtime contract: only string-typed (non-array, non-optional) fields
// are bound today; anything else is left for user code in logic. Path
// matching also accepts implicit `{name}` segment matches when no
// explicit `@path` decorator is present.
func collectBindings(m *ast.Method, pkg *semantic.Package) (path, query, header, cookie []paramBinding) {
	if m.Request == nil {
		return
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return
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
		if !isPlainStringField(f) {
			continue
		}
		bind := bindingFromDecorators(f.Decorators)
		if bind == "" && pathSegs[f.Name] {
			bind = "path"
		}
		entry := paramBinding{DSLName: f.Name, GoName: GoFieldName(f.Name)}
		switch bind {
		case "path":
			path = append(path, entry)
		case "query":
			query = append(query, entry)
		case "header":
			header = append(header, entry)
		case "cookie":
			cookie = append(cookie, entry)
		}
	}
	return
}

// isPlainStringField reports whether f is a non-array, non-optional
// `string`. The runtime binders for path/query/header/cookie only know
// how to populate strings in v1; richer types stay untouched.
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
		// Implicit path match short-circuits — that's a path field, not body.
		if pathSegs[f.Name] {
			continue
		}
		return true
	}
	return false
}

// GenerateHandlerHelpers writes the small `errors.go` helper used by every
// generated handler in a service package. Kept in a separate file so the
// per-method handler files stay short and the helper can be regenerated
// without touching them.
func GenerateHandlerHelpers(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	t := tmpl("handler_helpers.tmpl")
	for _, svcName := range sortedServices(pkg) {
		dir := filepath.Join(projectRoot, cfg.Output.Handler, ServiceDir(svcName))
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
