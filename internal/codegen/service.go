package codegen

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
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
	IsPassthrough    bool
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
	imps := importPathsFor(cfg, pkg, svcName)
	dir := filepath.Join(projectRoot, cfg.Output.Service, ServiceDir(svcName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	jsonTpl := tmpl("service.tmpl")
	passthroughTpl := tmpl("service-passthrough.tmpl")
	for _, m := range svc.Methods {
		filename := kebabCase(m.Name) + ".go"
		fullPath := filepath.Join(dir, filename)
		if _, err := os.Stat(fullPath); err == nil {
			continue
		}
		data := buildServiceData(svcName, m, imps, crossPkg)
		t := jsonTpl
		if data.IsPassthrough {
			t = passthroughTpl
		}
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
func buildServiceData(svcName string, m *ast.Method, imps importPaths, crossPkg CrossPkg) serviceData {
	hasReq := m.Request != nil
	hasResp := m.Response != nil && m.Response.Type != nil
	d := serviceData{
		Package:          ServicePackage(svcName),
		Service:          svcName,
		Method:           m.Name,
		ServiceName:      m.Name + "Service",
		Doc:              m.Doc,
		HasRequest:       hasReq,
		HasResponse:      hasResp,
		NeedsTypes:       hasReq || hasResp,
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
	if hasReq {
		alias, bare, extra := resolveTypeRef(m.Request, crossPkg)
		d.RequestPkgAlias = alias
		d.RequestType = bare
		addExtra(extra)
	}
	if hasResp {
		alias, bare, extra := resolveTypeRef(m.Response.Type, crossPkg)
		d.ResponsePkgAlias = alias
		d.ResponseType = bare
		addExtra(extra)
	}
	// When BOTH request and response live in cross-pkg packages, the
	// canonical `types` import becomes unused. Drop it so the scaffold
	// compiles. Single-cross-pkg + local-other still keeps the canonical
	// types import for the local one.
	if hasReq && hasResp && d.RequestPkgAlias != "types" && d.ResponsePkgAlias != "types" {
		d.NeedsTypes = false
	} else if hasReq && !hasResp && d.RequestPkgAlias != "types" {
		d.NeedsTypes = false
	} else if !hasReq && hasResp && d.ResponsePkgAlias != "types" {
		d.NeedsTypes = false
	}
	if hasPassthroughDecorator(m.Decorators) {
		d.IsPassthrough = true
		// Passthrough scaffolds don't reference `types.<X>` at all
		// - the entry point takes (w, r) directly - so drop every
		// type-related import to keep the generated file compiling
		// cleanly without manual edits.
		d.NeedsTypes = false
		d.HasRequest = false
		d.HasResponse = false
		d.RequestPkgAlias = ""
		d.RequestType = ""
		d.ResponsePkgAlias = ""
		d.ResponseType = ""
		d.ExtraTypesImports = nil
	}
	return d
}
