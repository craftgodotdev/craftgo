package golang

import (
	"fmt"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
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

// generateService writes each method's gen-once logic scaffold to
// output.service/<segment>/<method>.go. A nil r resolves local names only.
func generateService(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	r = resolverFor(pkg, r)
	crossPkg := r.CrossPkg
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		if err := generateServiceFor(svcName, svc, pkg, cfg, projectRoot, crossPkg); err != nil {
			return err
		}
	}
	return nil
}

func generateServiceFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg crossPkg) error {
	for _, m := range svc.Methods {
		group := semantic.MethodGroupOf(svc, m)
		imps := importPathsForGroup(cfg, pkg, svcName, group)
		dir := serviceOutputDir(projectRoot, cfg.Output.Service, svcName, group, cfg.Output.FileCase)
		filename := idents.FileName(m.Name, cfg.Output.FileCase) + ".go"
		if err := writeGoOnce(filepath.Join(dir, filename), tmpl("service.tmpl"), buildServiceData(pkg.Name, svcName, m, imps, crossPkg)); err != nil {
			return err
		}
	}
	return nil
}

func buildServiceData(pkgName, svcName string, m *ast.Method, imps importPaths, crossPkg crossPkg) serviceData {
	mode := modeOf(m)
	imports := newImportSet(crossPkg, goImport{Alias: localAlias, Path: imps.Types}, serviceNames)
	var reqRef, respRef string
	if mode.BindRequest() {
		reqRef = imports.named(m.Request)
	}
	if mode.StubReturnsResp() {
		respRef = imports.named(m.Response.Type)
	}
	d := serviceData{
		Package:          servicePkgName(pkgName, svcName),
		Service:          svcName,
		Method:           m.Name,
		ServiceName:      m.Name + "Service",
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
