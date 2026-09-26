package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// serviceData is the template input for service.tmpl, one value per method or RPC.
type serviceData struct {
	Package     string
	Service     string
	Method      string
	ServiceName string
	// Doc heads the entry point's doc comment ([docHead]); Entry is the rest.
	Doc   []string
	Entry []string
	// RawResponse reports that logic writes the response ([wire.RawSides]).
	RawResponse bool
	Sig         methodSignature
	ImportDecl  string
}

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
	imports := newImportSet(r.Module, r, goImport{Alias: localAlias, Path: imps.Types}, serviceNames)
	var reqRef, respRef string
	if mode.BindRequest() {
		reqRef = imports.named(m.Request)
	}
	if mode.StubReturnsResp() {
		respRef = imports.named(m.Response.Type)
	}
	sig := buildSignature(mode, reqRef, respRef)
	// A contract appears only in the stub's doc, so it adds no import.
	doc := imports.scratch()
	var reqContract, respContract string
	if mode.HasRequest {
		reqContract = doc.named(m.Request)
	}
	if mode.HasResponse {
		respContract = doc.named(m.Response.Type)
	}
	return serviceData{
		Package:     pkgName,
		Service:     svcName,
		Method:      m.Name,
		ServiceName: idents.LogicTypeName(m.Name),
		Doc:         docHead(semantic.DescriptionLines(decs, m.Doc)),
		Entry:       stubEntry(svcName, m.Name, mode, sig, reqContract, respContract),
		RawResponse: mode.RawResponse,
		Sig:         sig,
		ImportDecl:  stubImportDecl(imports, sig, imps.Svccontext),
	}
}

// stubEntry returns the generated doc lines of an HTTP method's logic entry point: what it does
// in mode, then the type its design documents on a raw side, reqContract or respContract.
func stubEntry(svc, method string, mode methodMode, sig methodSignature, reqContract, respContract string) []string {
	var does string
	switch {
	case mode.RawRequest && mode.RawResponse:
		does = method + " reads r and writes the response to w; a returned error goes to server.WriteError."
	case mode.RawResponse:
		does = method + " writes the response to w; a returned error goes to server.WriteError."
	case mode.RawRequest && sig.HasResult:
		does = method + " reads the request from r; craftgo encodes the response it returns."
	case mode.RawRequest:
		does = method + " reads the request from r."
	default:
		return []string{method + " implements " + svc + "." + method + "."}
	}
	var contract string
	switch req, resp := mode.RawRequest && mode.HasRequest, mode.RawResponse && mode.HasResponse; {
	case req && resp:
		contract = "Its request is documented as " + reqContract + " and its response as " + respContract + "."
	case req:
		contract = "Its request is documented as " + reqContract + ", whose Validate checks it."
	case resp:
		contract = "Its response is documented as " + respContract + "."
	default:
		return []string{does}
	}
	return []string{does, contract}
}

// stubImportDecl renders a logic stub's imports: those its signature names, already in imports,
// and the packages service.tmpl writes.
func stubImportDecl(imports *importSet, sig methodSignature, svccontext string) string {
	imports.use("context")
	if sig.NeedsHTTP {
		imports.use("net/http")
	}
	imports.use(logImport)
	if sig.NeedsGRPC {
		imports.use(grpcImport)
	}
	imports.use(svccontext)
	return imports.decl()
}
