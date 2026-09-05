package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// serviceData is the template input for `service.tmpl` and
// `service-passthrough.tmpl`. One value is built per DSL method.
type serviceData struct {
	Package     string
	Service     string
	Method      string
	ServiceName string
	// RequestType / RequestPkgAlias rendered together by the
	// template as `*<alias>.<Type>`. Local types use alias `types`;
	// cross-package types use the target package's name and pull
	// the matching Go import in via [ExtraTypesImports].
	RequestType      string
	RequestPkgAlias  string
	ResponseType     string
	ResponsePkgAlias string
	Doc              []string
	HasRequest       bool
	HasResponse      bool
	NeedsTypes       bool
	// RawRequest / RawResponse report which transport sides logic owns
	// (see wire.RawSides); IsPassthrough is both at once and only selects
	// the stub's doc text. Sig is the declaration's parameter and result
	// lists, computed by buildSignature together with the transport call
	// site. RequestContract / ResponseContract are the rendered Go types a
	// raw side documents (OpenAPI + generated types); the stub comment
	// points logic at the shape it must honour, without importing it.
	RawRequest       bool
	RawResponse      bool
	IsPassthrough    bool
	Sig              methodSignature
	RequestContract  string
	ResponseContract string
	TypesImport      string
	SvccontextImport string
	// ExtraTypesImports lists Go imports for cross-package request
	// or response types. Empty when both live in the service's own
	// package.
	ExtraTypesImports []extraImport
}

// GenerateService scaffolds one `<method>.go` per method per service
// under `<output.service>/<servicePackage>/`. Unlike the other generators
// this one runs in **scaffold** mode: existing files are left untouched so
// user-written business logic is never overwritten.
//
// Equivalent to [GenerateServicePackage] with a nil [CrossPkg] context.
func GenerateService(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	return GenerateServicePackage(pkg, cfg, projectRoot, nil)
}

// GenerateServicePackage is the multi-package variant of [GenerateService].
// crossPkg lets the scaffold render `*foo.Cred` for a cross-package
// request/response type rather than the legacy `*types.Cred`.
func GenerateServicePackage(pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateServiceFor(svcName, svc, pkg, cfg, projectRoot, crossPkg); err != nil {
			return err
		}
	}
	return nil
}

// generateServiceFor emits all per-method service scaffold files for a single
// service, skipping any that already exist on disk.
func generateServiceFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, crossPkg CrossPkg) error {
	groups := methodGroups(svc)
	t := tmpl("service.tmpl")
	for _, m := range svc.Methods {
		group := groups[m.Name]
		imps := importPathsForGroup(cfg, pkg, svcName, group)
		dir := serviceOutputDir(projectRoot, cfg.Output.Service, svcName, group, cfg.Output.FileCase)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		filename := idents.FileName(m.Name, cfg.Output.FileCase) + ".go"
		fullPath := filepath.Join(dir, filename)
		if _, err := os.Stat(fullPath); err == nil {
			continue
		}
		data := buildServiceData(pkg.Name, svcName, m, imps, crossPkg)
		formatted, err := renderGo(t, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", filename, err)
		}
		if err := os.WriteFile(fullPath, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// buildServiceData populates the serviceData struct for one DSL method.
func buildServiceData(pkgName, svcName string, m *ast.Method, imps importPaths, crossPkg CrossPkg) serviceData {
	mode := modeOf(m)
	takesReq, returnsResp := mode.StubTakesReq(), mode.StubReturnsResp()
	d := serviceData{
		Package:          ServicePkgName(pkgName, svcName),
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
	// Track which Go imports we've already pinned via [TypesImport]
	// or an extra entry - duplicates would surface as "duplicate
	// import" Go errors otherwise.
	extraSeen := map[string]bool{}
	addExtra := func(extra extraImport) {
		if extra.Path == "" || extraSeen[extra.Path] {
			return
		}
		extraSeen[extra.Path] = true
		d.ExtraTypesImports = append(d.ExtraTypesImports, extra)
	}
	// resolveTypeRef returns only the OUTER ref's import; a generic instance's
	// type-args reach further packages (`genpkg.Box<argpkg.Owner>` →
	// `genpkg.Box[argpkg.Owner]`), so walk the whole ref and add every
	// cross-package import - otherwise the scaffold references `argpkg.Owner`
	// with no import (`undefined: argpkg`). The other emitters already do this
	// via walkCrossPkgImports.
	pathAlias := map[string]string{}
	for alias, path := range crossPkg {
		pathAlias[path] = alias
	}
	addRefExtras := func(ref *ast.NamedTypeRef) {
		set := map[string]bool{}
		walkCrossPkgImports(&ast.TypeRef{Named: ref}, crossPkg, set)
		for path := range set {
			addExtra(extraImport{Alias: pathAlias[path], Path: path})
		}
	}
	// A docs-only contract (the block on a raw side) is rendered for the
	// stub comment but never imported: the stub does not reference it,
	// and an unused import would fail `go build`.
	if mode.HasRequest {
		alias, bare, _ := resolveTypeRef(m.Request, crossPkg)
		d.RequestContract = alias + "." + bare
	}
	if mode.HasResponse {
		alias, bare, _ := resolveTypeRef(m.Response.Type, crossPkg)
		d.ResponseContract = alias + "." + bare
	}
	if takesReq {
		alias, bare, extra := resolveTypeRef(m.Request, crossPkg)
		d.RequestPkgAlias = alias
		d.RequestType = bare
		addExtra(extra)
		addRefExtras(m.Request)
	}
	if returnsResp {
		alias, bare, extra := resolveTypeRef(m.Response.Type, crossPkg)
		d.ResponsePkgAlias = alias
		d.ResponseType = bare
		addExtra(extra)
		addRefExtras(m.Response.Type)
	}
	// The canonical `types` import is needed only when a side the stub
	// names lives in the local package - directly (alias `types`) or as
	// the local type-arg of a cross-package generic (`shared.Page<Order>`
	// renders as `*shared.Page[types.Order]`). Both sides cross-package →
	// drop it so the scaffold compiles.
	usesLocal := func(alias, bare string) bool {
		return alias == "types" || strings.Contains(bare, "types.")
	}
	d.NeedsTypes = (takesReq && usesLocal(d.RequestPkgAlias, d.RequestType)) ||
		(returnsResp && usesLocal(d.ResponsePkgAlias, d.ResponseType))
	d.Sig = buildSignature(mode, d.RequestPkgAlias+"."+d.RequestType, d.ResponsePkgAlias+"."+d.ResponseType)
	return d
}
