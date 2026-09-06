package semantic

// Operation-name resolution. The operationId and the component-schema base
// name a method emits are LANGUAGE facts (derived from the method name, its
// service, and an explicit @operationId override) - not OpenAPI rendering - so
// they are decided here, on the floor both the analyser and codegen read.
// codegen's emit calls [OperationID] / [OperationBaseName];
// [refResolver.checkProjectOperationIDUniqueness] flags duplicates at design
// time so the editor surfaces what would otherwise be a codegen-only error.

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// MethodNameCounts counts how many times each method name appears across every
// service. A name shared by two services must be service-qualified in the
// emitted operationId / component names so they stay globally unique.
func MethodNameCounts(pkg *Package) map[string]int {
	counts := map[string]int{}
	for _, svc := range pkg.Services {
		for _, m := range svc.Methods {
			counts[m.Name]++
		}
	}
	return counts
}

// OperationBaseName is the collision-free base for a method's component schema
// names (`<base>ReqBody`, `<base>RespBody`) and its default operationId: bare
// when the method name is unique project-wide, service-prefixed when shared.
func OperationBaseName(svcName string, m *ast.Method, counts map[string]int) string {
	if counts[m.Name] >= 2 {
		return svcName + m.Name
	}
	return m.Name
}

// OperationID returns a method's operationId: an explicit, non-empty
// `@operationId("...")` override when present, otherwise base.
func OperationID(m *ast.Method, base string) string {
	for _, d := range m.Decorators {
		if d == nil || d.Name != "operationId" || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
			return s.Value
		}
	}
	return base
}

// checkProjectOperationIDUniqueness flags every method whose operationId
// collides with another's anywhere in the project. The single emitted
// OpenAPI document merges every package's services, so method-name counts
// are taken PROJECT-WIDE (matching the merged document): an auto id shared
// by services in different packages is service-prefixed and does not
// clash; an explicit @operationId override is taken verbatim and can. Runs
// after services are merged so it sees the full method set per service.
func (r *refResolver) checkProjectOperationIDUniqueness() {
	counts := map[string]int{}
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				counts[m.Name]++
			}
		}
	}
	type owner struct {
		ref string
		pkg string
		pos lexer.Position
	}
	owners := map[string][]owner{}
	pkgNames := slices.Sorted(maps.Keys(r.proj.Packages))
	for _, pkgName := range pkgNames {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		svcNames := slices.Sorted(maps.Keys(pkg.Services))
		for _, svcName := range svcNames {
			si := pkg.Services[svcName]
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				id := OperationID(m, OperationBaseName(svcName, m, counts))
				owners[id] = append(owners[id], owner{ref: svcName + "." + m.Name, pkg: pkgName, pos: m.Pos})
			}
		}
	}
	ids := slices.Sorted(maps.Keys(owners))
	for _, id := range ids {
		who := owners[id]
		if len(who) < 2 {
			continue
		}
		pkgs := map[string]bool{}
		for _, o := range who {
			pkgs[o.pkg] = true
		}
		refs := make([]string, len(who))
		for i, o := range who {
			refs[i] = o.ref
			if len(pkgs) > 1 {
				refs[i] = o.pkg + "." + o.ref
			}
		}
		joined := strings.Join(refs, ", ")
		msg := "operationId %q is shared by %s - give each method a distinct @operationId(...)"
		if len(pkgs) > 1 {
			msg = "operationId %q is shared across packages by %s - give each method a distinct @operationId(...)"
		}
		for _, o := range who {
			r.diag(o.pos, lexer.SeverityError, CodeDuplicateOperation, msg, id, joined)
		}
	}
}
