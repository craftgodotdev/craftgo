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
	Package          string
	Service          string
	Method           string
	ServiceName      string
	RequestType      string
	RequestPkgAlias  string
	ResponseType     string
	ResponsePkgAlias string
	Doc              []string
	// Notes follow Doc on the entry point: a streaming RPC's usage hint.
	Notes       []string
	HasRequest  bool
	HasResponse bool
	NeedsTypes  bool
	// RawRequest and RawResponse report the transport sides logic owns ([wire.RawSides]).
	RawRequest    bool
	RawResponse   bool
	IsPassthrough bool
	Sig           methodSignature
	// RequestContract and ResponseContract name a raw side's documented type in the stub's doc.
	RequestContract  string
	ResponseContract string
	TypesImport      string
	SvccontextImport string
	// ExtraTypesImports are the other DSL packages the stub's signature references.
	ExtraTypesImports []extraImport
	// PBImports are the pb packages of a gRPC scaffold's messages.
	PBImports []extraImport
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
		if err := writeScaffoldOnce(filepath.Join(dir, filename), "service.tmpl", buildServiceData(pkg.Name, svcName, m, imps, crossPkg)); err != nil {
			return err
		}
	}
	return nil
}

func buildServiceData(pkgName, svcName string, m *ast.Method, imps importPaths, crossPkg crossPkg) serviceData {
	mode := modeOf(m)
	takesReq, returnsResp := mode.StubTakesReq(), mode.StubReturnsResp()
	d := serviceData{
		Package:          servicePkgName(pkgName, svcName),
		Service:          svcName,
		Method:           m.Name,
		ServiceName:      m.Name + "Service",
		Doc:              m.Doc,
		HasRequest:       mode.HasRequest,
		HasResponse:      mode.HasResponse,
		RawRequest:       mode.RawRequest,
		RawResponse:      mode.RawResponse,
		IsPassthrough:    mode.RawRequest && mode.RawResponse,
		TypesImport:      imps.Types,
		SvccontextImport: imps.Svccontext,
	}
	extraSeen := map[string]bool{}
	addExtra := func(extra extraImport) {
		if extra.Path == "" || extraSeen[extra.Path] {
			return
		}
		extraSeen[extra.Path] = true
		d.ExtraTypesImports = append(d.ExtraTypesImports, extra)
	}
	// resolveTypeRef imports only the outer type's package; its generic arguments can reach others.
	pathAlias := map[string]string{}
	for alias, path := range crossPkg {
		pathAlias[path] = alias
	}
	addRefExtras := func(ref *ast.NamedTypeRef) {
		set := map[string]bool{}
		ref.WalkNamedRefs(crossPkg.importsInto(set))
		for path := range set {
			addExtra(extraImport{Alias: pathAlias[path], Path: path})
		}
	}
	// A contract appears only in the stub's doc, so it adds no import.
	if mode.HasRequest {
		alias, bare, _, _ := resolveTypeRef(m.Request, crossPkg)
		d.RequestContract = alias + "." + bare
	}
	if mode.HasResponse {
		alias, bare, _, _ := resolveTypeRef(m.Response.Type, crossPkg)
		d.ResponseContract = alias + "." + bare
	}
	var reqUse, respUse typeRefUse
	if takesReq {
		alias, bare, extra, use := resolveTypeRef(m.Request, crossPkg)
		d.RequestPkgAlias = alias
		d.RequestType = bare
		reqUse = use
		addExtra(extra)
		addRefExtras(m.Request)
	}
	if returnsResp {
		alias, bare, extra, use := resolveTypeRef(m.Response.Type, crossPkg)
		d.ResponsePkgAlias = alias
		d.ResponseType = bare
		respUse = use
		addExtra(extra)
		addRefExtras(m.Response.Type)
	}
	// The types import serves a local named side or a local type argument (`*shared.Page[types.Order]`).
	d.NeedsTypes = (takesReq && reqUse.LocalTypes) || (returnsResp && respUse.LocalTypes)
	d.Sig = buildSignature(mode, d.RequestPkgAlias+"."+d.RequestType, d.ResponsePkgAlias+"."+d.ResponseType)
	return d
}
