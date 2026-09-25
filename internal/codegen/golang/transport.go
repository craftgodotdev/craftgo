package golang

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

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
	Verb    string
	// RequestType is the Go type the handler binds the request into.
	RequestType string
	Doc         []string
	HasRequest  bool
	HasResponse bool
	BodyVerb    bool
	BodyDecode  bool
	// RawRequest and RawResponse report the transport sides logic owns ([wire.RawSides]).
	RawRequest    bool
	RawResponse   bool
	BindRequest   bool
	WriteResponse bool
	Sig           methodSignature
	IsMultipart   bool
	// MultipartMaxMemory is the ParseMultipartForm memory budget: 32 MiB, or @maxBodySize when larger.
	MultipartMaxMemory int64
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
	// NeedsStrconv is set when a response header or cookie formats a non-string value.
	NeedsStrconv bool
	// SuccessStatus is the method's [wire.SuccessStatus]; SuccessStatusExpr spells it in Go.
	SuccessStatus     int
	SuccessStatusExpr string
	ServiceImport     string
	SvccontextImport  string
	// Imports are the packages the request's type, bindings and defaults name.
	Imports []goImport
}

// defaultBinding pre-fills field GoName with the Go expression Literal, through a temp when Ptr.
type defaultBinding struct {
	GoName  string
	Literal string
	Ptr     bool
}

// paramBinding is one field binding in a handler; Bind is its rendered Go statement.
type paramBinding struct {
	DSLName   string
	GoName    string
	Bind      string
	MimeTypes []string
	Required  bool
	Field     *ast.Field
	// IsArray marks a `file[]` field, bound from every part of its name.
	IsArray bool
}

// generateTransport writes each method's handler to output.transport/<segment>/<method>.go,
// the segment being the @group or the service directory. A nil r resolves local names only.
func generateTransport(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
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
	for _, m := range svc.Methods {
		group := semantic.MethodGroupOf(svc, m)
		imps := importPathsForGroup(cfg, pkg, svcName, group)
		dir := serviceOutputDir(projectRoot, cfg.Output.Transport, svcName, group, cfg.Output.FileCase)
		data, err := buildTransportData(svcName, m, imps, pkg, r)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", svcName, m.Name, err)
		}
		if err := writeGo(filepath.Join(dir, idents.FileName(m.Name, cfg.Output.FileCase)+".go"), tmpl("transport.tmpl"), data); err != nil {
			return err
		}
	}
	return nil
}

// buildTransportData fails on a field its binding source cannot carry, such as @query on a struct.
func buildTransportData(svcName string, m *ast.Method, imps importPaths, pkg *semantic.Package, r *projectResolver) (transportData, error) {
	mode := modeOf(m)
	imports := newImportSet(r.CrossPkg, goImport{Alias: localAlias, Path: imps.Types}, transportNames)
	d := transportData{
		Package:          servicePkgName(pkg.Name, svcName),
		Method:           m.Name,
		Verb:             httpVerb(m.Verb),
		Doc:              m.Doc,
		HasRequest:       mode.HasRequest,
		HasResponse:      mode.HasResponse,
		BodyVerb:         wire.IsBodyVerb(m.Verb),
		RawRequest:       mode.RawRequest,
		RawResponse:      mode.RawResponse,
		BindRequest:      mode.BindRequest(),
		WriteResponse:    mode.WriteResponse(),
		ServiceImport:    imps.Service,
		SvccontextImport: imps.Svccontext,
	}
	if d.BindRequest {
		d.RequestType = imports.named(m.Request)
		fields := resolveRequestFields(m, pkg, r)
		binds, err := collectBindings(m, fields, pkg, r, imports)
		if err != nil {
			return transportData{}, err
		}
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams = binds[wire.BindPath], binds[wire.BindQuery], binds[wire.BindHeader], binds[wire.BindCookie]
		d.BodyDecode = wire.IsBodyVerb(m.Verb) && hasBodyField(fields)
		forms, files, ferr := collectFormBindings(m, fields, pkg, r, imports)
		if ferr != nil {
			return d, ferr
		}
		if len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
			// The multipart parser owns the body.
			d.BodyDecode = false
			const stdlibDefault int64 = 32 << 20
			d.MultipartMaxMemory = stdlibDefault
			if n, _ := semantic.SizeArg(firstArg(m.Decorators, "maxBodySize")); n > stdlibDefault {
				d.MultipartMaxMemory = n
			}
		}
		d.Defaults = collectDefaults(fields, r, imports)
	}
	// On a raw response side logic writes its own headers.
	if d.WriteResponse && mode.HasResponse {
		var respStrconv bool
		d.RespHeaders, d.RespCookies, respStrconv = collectResponseBindings(m, pkg, r)
		if respStrconv {
			d.NeedsStrconv = true
		}
	}
	var respRef string
	if mode.StubReturnsResp() {
		// The handler infers resp's type, so the signature names it without importing it.
		respRef = imports.scratch().named(m.Response.Type)
	}
	d.Sig = buildSignature(mode, d.RequestType, respRef)
	d.Imports = imports.imports()
	d.SuccessStatus = wire.SuccessStatus(m)
	d.SuccessStatusExpr = statusConstExpr(d.SuccessStatus)
	return d, nil
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
