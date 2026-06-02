package semantic

// Operation-name resolution. The operationId and the component-schema base
// name a method emits are LANGUAGE facts (derived from the method name, its
// service, and an explicit @operationId override) — not OpenAPI rendering — so
// they are decided here, on the floor both the analyser and codegen read.
// codegen's emit calls [OperationID] / [OperationBaseName]; the analyser's
// [analyzer.checkOperationIDUniqueness] flags duplicates at design time so the
// editor surfaces what would otherwise be a codegen-only error.

import (
	"sort"
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

// checkOperationIDUniqueness flags every method whose operationId collides
// with another's. Runs after services are merged so it sees the full method
// set per service.
func (a *analyzer) checkOperationIDUniqueness() {
	counts := MethodNameCounts(a.pkg)

	type owner struct {
		ref string
		pos lexer.Position
	}
	owners := map[string][]owner{}

	svcNames := make([]string, 0, len(a.pkg.Services))
	for name := range a.pkg.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)
	for _, svcName := range svcNames {
		for _, m := range a.pkg.Services[svcName].Methods {
			id := OperationID(m, OperationBaseName(svcName, m, counts))
			owners[id] = append(owners[id], owner{ref: svcName + "." + m.Name, pos: m.Pos})
		}
	}

	ids := make([]string, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		who := owners[id]
		if len(who) < 2 {
			continue
		}
		refs := make([]string, len(who))
		for i, o := range who {
			refs[i] = o.ref
		}
		joined := strings.Join(refs, ", ")
		for _, o := range who {
			a.diag(o.pos, o.pos, lexer.SeverityError, CodeDuplicateOperation,
				"operationId %q is shared by %s — give each method a distinct @operationId(...)",
				id, joined)
		}
	}
}
