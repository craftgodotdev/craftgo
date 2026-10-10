package golang

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// transportData is the template input for transport.tmpl, one value per method.
type transportData struct {
	Package string
	Method  string
	// ServiceName is the logic type the handler calls.
	ServiceName string
	Verb        string
	// RequestType is the Go type the handler binds the request into.
	RequestType string
	// Doc heads the handler's doc comment ([docHead]).
	Doc        []string
	BodyDecode bool
	// BodyWire is the wire struct the body decodes into while presence is checked, "" when the
	// body decodes straight into the request.
	BodyWire string
	// BindRequest and WriteResponse report the sides the handler owns ([wire.RawSides]).
	BindRequest   bool
	WriteResponse bool
	Sig           methodSignature
	IsMultipart   bool
	// PartLimits are the @maxSize limits of the file parts, which the handler holds each part to
	// while the multipart parser reads it.
	PartLimits   []partLimit
	PathParams   []paramBinding
	QueryParams  []paramBinding
	HeaderParams []paramBinding
	CookieParams []paramBinding
	FormStrings  []paramBinding
	FormFiles    []paramBinding
	// RespHeaders and RespCookies write the response's @header and @cookie fields before the body.
	RespHeaders []paramBinding
	RespCookies []paramBinding
	// Defaults pre-fill the request before binding, so an absent field keeps its @default.
	Defaults []defaultBinding
	// SuccessStatus is the method's [wire.SuccessStatus]; SuccessStatusExpr spells it in Go.
	SuccessStatus     int
	SuccessStatusExpr string
	ImportDecl        string
}

// defaultBinding pre-fills field GoName with the Go expression Literal, through a temp when Ptr.
type defaultBinding struct {
	GoName  string
	Literal string
	Ptr     bool
}

// partLimit is the @maxSize of the file parts named Name, a quoted Go string, as a Go literal.
type partLimit struct {
	Name, Limit string
}

// paramBinding is one field binding in a handler; Bind is its rendered Go statement.
type paramBinding struct {
	DSLName string
	GoName  string
	Bind    string
	// IsArray marks a `file[]` field, bound from every part of its name.
	IsArray bool
	// MaxSize is a file field's @maxSize in bytes, 0 for none.
	MaxSize int64
}

// generateTransport writes each method's handler to output.transport/<segment>/<method>.go,
// the segment being the @group or the service directory. A nil r resolves local names only.
func generateTransport(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	r = resolverFor(pkg, r)
	out := outputsOf(cfg)
	for s := range segments(pkg, cfg.Output.FileCase) {
		bodies := segmentBodies(s, r, out)
		dir := out.transport.sub(s.dir)
		for m := range s.methods() {
			data := buildTransportData(m, s.svc.Decorators(m), out.segmentImports(pkg.Name, s.dir), pkg, r, bodies.names()...)
			data.BodyWire = bodies.wireFor(m)
			if err := writeGo(dir.at(projectRoot, methodFile(m, cfg.Output.FileCase)), tmpl("transport.tmpl"), data); err != nil {
				return err
			}
		}
		if src := bodies.source(pkg.Name); src != "" {
			if err := writeGoSource(dir.at(projectRoot, bodyFile), src); err != nil {
				return err
			}
		}
	}
	return nil
}

// segmentBodies weighs the JSON request bodies s's handlers decode.
func segmentBodies(s segment, r *projectResolver, out outputs) *bodyPlan {
	p := newBodyPlan(r, s.pkg.Name, out.types.sub(s.pkg.Name).pkg)
	for m := range s.methods() {
		if fields, ok := bodyFields(m, s.svc.Decorators(m), s.pkg, r); ok {
			p.add(m, fields)
		}
	}
	p.finish()
	return p
}

// bodyFields returns m's request fields when its handler decodes a JSON body: it binds the
// request on a body verb and a member rides the body, and no part is a file, which leaves the
// body to the multipart parser.
func bodyFields(m *ast.Method, decs []*ast.Decorator, pkg *semantic.Package, r *projectResolver) ([]resolvedField, bool) {
	if !modeOf(m, decs).BindRequest() || !wire.IsBodyVerb(m.Verb) {
		return nil, false
	}
	fields := resolveRequestFields(m, pkg, r)
	if !hasBodyField(fields) {
		return nil, false
	}
	resolved := make([]semantic.ResolvedField, len(fields))
	for i, rf := range fields {
		resolved[i] = rf.ResolvedField
	}
	if _, files := semantic.FormParts(resolved); len(files) > 0 {
		return nil, false
	}
	return fields, true
}

// buildTransportData renders m's handler, decs being the decorators that apply to m; reserved
// are the package-level names its segment declares beside the handlers.
func buildTransportData(m *ast.Method, decs []*ast.Decorator, imps importPaths, pkg *semantic.Package, r *projectResolver, reserved ...string) transportData {
	mode := modeOf(m, decs)
	imports := newImportSet(r.Module, r, goImport{Alias: localAlias, Path: imps.Types}, slices.Concat(transportNames, reserved))
	imports.use("net/http")
	imports.use(serverImport)
	imports.fixed("service", imps.Service)
	imports.use(imps.Svccontext)
	d := transportData{
		Package:       pkg.Name,
		Method:        m.Name,
		ServiceName:   idents.LogicTypeName(m.Name),
		Verb:          strings.ToUpper(m.Verb),
		Doc:           docHead(semantic.DescriptionLines(decs, m.Doc)),
		BindRequest:   mode.BindRequest(),
		WriteResponse: mode.WriteResponse(),
	}
	if d.BindRequest {
		d.RequestType = imports.named(m.Request)
		fields := resolveRequestFields(m, pkg, r)
		binds := collectBindings(fields, pkg, r, imports)
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams = binds[wire.BindPath], binds[wire.BindQuery], binds[wire.BindHeader], binds[wire.BindCookie]
		d.BodyDecode = wire.IsBodyVerb(m.Verb) && hasBodyField(fields)
		forms, files := collectFormBindings(fields, pkg, r, imports)
		if len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
			// The multipart parser owns the body.
			d.BodyDecode = false
			for _, f := range files {
				if f.MaxSize > 0 {
					d.PartLimits = append(d.PartLimits, partLimit{Name: strconv.Quote(f.DSLName), Limit: formatSizeGo(f.MaxSize)})
				}
			}
		}
		d.Defaults = collectDefaults(fields, r, imports)
	}
	// On a raw response side logic writes its own headers.
	if d.WriteResponse && mode.HasResponse {
		var respStrconv bool
		d.RespHeaders, d.RespCookies, respStrconv = collectResponseBindings(m, pkg, r)
		if respStrconv {
			imports.use("strconv")
		}
	}
	var respRef string
	if mode.StubReturnsResp() {
		// The handler infers resp's type, so the signature names it without importing it.
		respRef = imports.scratch().named(m.Response.Type)
	}
	d.Sig = buildSignature(mode, d.RequestType, respRef)
	d.ImportDecl = imports.decl()
	d.SuccessStatus = wire.SuccessStatus(m, decs)
	d.SuccessStatusExpr = statusConstExpr(d.SuccessStatus)
	return d
}

// statusConstExpr spells a 2xx code as its net/http constant and any other code as a literal.
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
