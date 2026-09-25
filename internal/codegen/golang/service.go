package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// serviceData is the template input for service.tmpl, one value per method or RPC.
type serviceData struct {
	Package     string
	Service     string
	Method      string
	ServiceName string
	Doc         []string
	// Notes follow Doc on the entry point: a streaming RPC's usage hint.
	Notes       []string
	HasRequest  bool
	HasResponse bool
	// NeedsTypes reports that the stub imports its own package's types, which open their group.
	NeedsTypes bool
	// RawRequest and RawResponse report the transport sides logic owns ([wire.RawSides]).
	RawRequest    bool
	RawResponse   bool
	IsPassthrough bool
	Sig           methodSignature
	// RequestContract and ResponseContract name a raw side's documented type in the stub's doc.
	RequestContract  string
	ResponseContract string
	SvccontextImport string
	// Imports are the packages the stub's signature names.
	Imports []goImport
	// PBImports are the pb packages of a gRPC scaffold's messages.
	PBImports []goImport
}

// logicTypeName is the name of a method's or an RPC's logic struct.
func logicTypeName(method string) string { return method + "Service" }

// generateService writes each method's gen-once logic scaffold to
// output.service/<segment>/<method>.go. A nil r resolves local names only.
func generateService(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	r = resolverFor(pkg, r)
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		if err := generateServiceFor(svcName, svc, pkg, cfg, projectRoot, r); err != nil {
			return err
		}
	}
	return nil
}

func generateServiceFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	out := outputsOf(cfg)
	for _, m := range svc.Methods {
		seg := route.OutputSegment(svcName, semantic.MethodGroupOf(svc, m), cfg.Output.FileCase)
		data := buildServiceData(pkg.Name, svcName, m, svc.Decorators(m), out.segmentImports(pkg.Name, seg), r)
		if err := writeGoOnce(out.service.sub(seg).at(projectRoot, methodFile(m, cfg.Output.FileCase)), tmpl("service.tmpl"), data); err != nil {
			return err
		}
	}
	return nil
}

func buildServiceData(pkgName, svcName string, m *ast.Method, decs []*ast.Decorator, imps importPaths, r *projectResolver) serviceData {
	mode := modeOf(m, decs)
	imports := newImportSet(r, goImport{Alias: localAlias, Path: imps.Types}, serviceNames)
	var reqRef, respRef string
	if mode.BindRequest() {
		reqRef = imports.named(m.Request)
	}
	if mode.StubReturnsResp() {
		respRef = imports.named(m.Response.Type)
	}
	d := serviceData{
		Package:          pkgName,
		Service:          svcName,
		Method:           m.Name,
		ServiceName:      logicTypeName(m.Name),
		Doc:              m.Doc,
		HasRequest:       mode.HasRequest,
		HasResponse:      mode.HasResponse,
		NeedsTypes:       imports.has(imps.Types),
		RawRequest:       mode.RawRequest,
		RawResponse:      mode.RawResponse,
		IsPassthrough:    mode.RawRequest && mode.RawResponse,
		Sig:              buildSignature(mode, reqRef, respRef),
		SvccontextImport: imps.Svccontext,
		Imports:          imports.imports(),
	}
	// A contract appears only in the stub's doc, so it adds no import.
	doc := imports.scratch()
	if mode.HasRequest {
		d.RequestContract = doc.named(m.Request)
	}
	if mode.HasResponse {
		d.ResponseContract = doc.named(m.Response.Type)
	}
	return d
}
