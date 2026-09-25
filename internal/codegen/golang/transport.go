package golang

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/route"
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
	// BindRequest and WriteResponse report the sides the handler owns ([wire.RawSides]).
	BindRequest   bool
	WriteResponse bool
	Sig           methodSignature
	IsMultipart   bool
	// MultipartMaxMemory spells the ParseMultipartForm memory budget: 32 MiB, or @maxBodySize when larger.
	MultipartMaxMemory string
	PathParams         []paramBinding
	QueryParams        []paramBinding
	HeaderParams       []paramBinding
	CookieParams       []paramBinding
	FormStrings        []paramBinding
	FormFiles          []paramBinding
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

// paramBinding is one field binding in a handler; Bind is its rendered Go statement.
type paramBinding struct {
	DSLName string
	GoName  string
	Bind    string
	// IsArray marks a `file[]` field, bound from every part of its name.
	IsArray bool
}

// generateTransport writes each method's handler to output.transport/<segment>/<method>.go,
// the segment being the @group or the service directory. A nil r resolves local names only.
func generateTransport(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	r = resolverFor(pkg, r)
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		if err := generateTransportFor(svcName, svc, pkg, cfg, projectRoot, r); err != nil {
			return err
		}
	}
	return nil
}

func generateTransportFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	out := outputsOf(cfg)
	for _, m := range svc.Methods {
		seg := route.OutputSegment(svcName, semantic.MethodGroupOf(svc, m), cfg.Output.FileCase)
		data := buildTransportData(m, svc.Decorators(m), out.segmentImports(pkg.Name, seg), pkg, r)
		if err := writeGo(out.transport.sub(seg).at(projectRoot, methodFile(m, cfg.Output.FileCase)), tmpl("transport.tmpl"), data); err != nil {
			return err
		}
	}
	return nil
}

// buildTransportData renders m's handler, decs being the decorators that apply to m.
func buildTransportData(m *ast.Method, decs []*ast.Decorator, imps importPaths, pkg *semantic.Package, r *projectResolver) transportData {
	mode := modeOf(m, decs)
	imports := newImportSet(r.Module, r, goImport{Alias: localAlias, Path: imps.Types}, transportNames)
	imports.use("net/http")
	imports.use(serverImport)
	imports.fixed("service", imps.Service)
	imports.use(imps.Svccontext)
	d := transportData{
		Package:       pkg.Name,
		Method:        m.Name,
		ServiceName:   logicTypeName(m.Name),
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
			const stdlibDefault int64 = 32 << 20
			budget := stdlibDefault
			if n, _ := semantic.SizeArg(firstArg(decs, "maxBodySize")); n > stdlibDefault {
				budget = n
			}
			d.MultipartMaxMemory = formatSizeGo(budget)
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
