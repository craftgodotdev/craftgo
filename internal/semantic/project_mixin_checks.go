// Project-level mixin expansion checks: cross-package mixin resolution,
// duplicate-embed rejection, and promoted-field collision detection.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkProjectMixins runs the unified mixin expansion across every
// type and error body in every package. In project mode the
// per-package pass is gated off (see [Options.skipMixinCheck]) because
// qualified mixin refs (`shared.Timestamps`) can only resolve here,
// where every package's symbol tables are visible at once.
//
// The expansion mirrors [analyzer.collectMixinFields]: walk a host's
// own direct fields into a seen map keyed by field name, then walk
// every mixin (local OR qualified) and either add its fields or fire
// the appropriate diagnostic (cycle, conflict, non-type, arity,
// unresolved). Conflict detection works across the local + qualified
// mixin boundary because they share the same seen map.
func (r *refResolver) checkProjectMixins() {
	pkgsByName := r.proj.Packages
	for currentPkg, pkg := range pkgsByName {
		if pkg == nil {
			continue
		}
		for _, td := range pkg.Types {
			r.checkOneTypeMixinsProject(currentPkg, td.Name, td.Body)
			// Embedding a bare type-parameter (`type Box<T> { T }`) is
			// structural — no cross-package resolution needed — but the
			// per-package pass that would catch it is gated off in project mode,
			// so mirror it here.
			for _, tpm := range findTypeParamMixins(td.TypeParams, td.Body) {
				r.diag(tpm.pos, lexer.SeverityError, CodeMixinConflict, "%s", typeParamMixinMsg(td.Name, tpm.param))
			}
		}
		for _, ed := range pkg.Errors {
			r.checkOneTypeMixinsProject(currentPkg, ed.Name, ed.Body)
		}
	}
}

// checkOneTypeMixinsProject is the project-aware twin of
// [analyzer.checkOneTypeMixins]. Walks host's direct fields into a
// seen map, then resolves and expands every mixin (local OR
// qualified). currentPkg is the host's package name; used to
// disambiguate local-mixin lookups and as the starting label for
// cycle detection.
func (r *refResolver) checkOneTypeMixinsProject(currentPkg, host string, body []ast.TypeMember) {
	seen := map[string]fieldOrigin{}
	expandMixinsAndCheckCollisions(host, body, seen,
		func(mx *ast.Mixin) { r.processProjectMixin(currentPkg, host, mx, seen) },
		func(pos lexer.Position, code, format string, args ...any) *Diagnostic {
			return r.diag(pos, lexer.SeverityError, code, format, args...)
		})
}

// processProjectMixin resolves one mixin reference - local or
// qualified - and expands its fields. Diagnostic codes match the
// per-package pass so IDE quickfix logic doesn't need to learn a new
// vocabulary.
func (r *refResolver) processProjectMixin(currentPkg, host string, mx *ast.Mixin, seen map[string]fieldOrigin) {
	if mx.Ref == nil || mx.Ref.Name == nil {
		return
	}
	parts := mx.Ref.Name.Parts
	if len(parts) == 0 || len(parts) > 2 {
		return
	}
	targetPkg := currentPkg
	targetName := parts[0]
	if len(parts) == 2 {
		targetPkg = parts[0]
		targetName = parts[1]
	}
	pkg := r.proj.Packages[targetPkg]
	if pkg == nil {
		// Qualified prefix didn't resolve - the qualified-ref check
		// in [walkNamedRef] already fired CodeRefUnknownPackage.
		// Silent here to avoid the duplicate.
		return
	}
	td, ok := pkg.Types[targetName]
	if !ok {
		// Resolved to a non-type entity (enum / error / scalar /
		// middleware) - same code as the per-package pass uses.
		kind := ""
		switch {
		case pkg.Enums[targetName] != nil:
			kind = "enum"
		case pkg.Errors[targetName] != nil:
			kind = "error"
		case pkg.Scalars[targetName] != nil:
			kind = "scalar"
		case pkg.Middlewares[targetName] != nil:
			kind = "middleware"
		}
		if kind != "" {
			r.diag(mx.Pos, lexer.SeverityError, CodeMixinNonType,
				"mixin %s is a %s, not a type", mx.Ref.Name.String(), kind)
			return
		}
		// Truly unresolved - same diagnostic CodeRefUnknownSymbol
		// the qualified-ref check would have fired for a regular
		// field type. For local refs the per-package pass already
		// fired CodeMixinUnresolved.
		if len(parts) == 2 {
			r.diag(mx.Pos, lexer.SeverityError, CodeMixinUnresolved,
				"mixin %s is not declared in package %q", mx.Ref.Name.String(), targetPkg)
		}
		return
	}
	if len(mx.Ref.Args) != len(td.TypeParams) {
		r.diag(mx.Pos, lexer.SeverityError, CodeMixinArity,
			"mixin %s expects %d generic argument(s), got %d",
			mx.Ref.Name.String(), len(td.TypeParams), len(mx.Ref.Args))
		return
	}
	visited := map[string]bool{currentPkg + "." + host: true}
	r.collectProjectMixinFields(targetPkg, targetName, mx.Ref.Name.String(), mx.Pos, seen, visited)
}

// collectProjectMixinFields walks the mixin target's body and any
// nested mixins it contains, with cross-package resolution at every
// step. visited keys are qualified names (`pkg.Type`) so a cycle that
// crosses package boundaries is still detected.
func (r *refResolver) collectProjectMixinFields(targetPkg, targetName, sourceLabel string, mixinPos lexer.Position, seen map[string]fieldOrigin, visited map[string]bool) {
	qualified := targetPkg + "." + targetName
	if visited[qualified] {
		r.diag(mixinPos, lexer.SeverityError, CodeMixinCycle,
			"mixin %s forms a cycle", sourceLabel)
		return
	}
	visited[qualified] = true
	defer delete(visited, qualified)
	pkg := r.proj.Packages[targetPkg]
	if pkg == nil {
		return
	}
	td, ok := pkg.Types[targetName]
	if !ok {
		return
	}
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			if prev, dup := seen[v.Name]; dup {
				if prev.from == sourceLabel {
					continue
				}
				diag := r.diag(mixinPos, lexer.SeverityError, CodeMixinConflict,
					"mixin %s adds field %q, which conflicts with %s",
					sourceLabel, v.Name, prev.from)
				diag.Related = related(prev.pos, "first field declared here")
				continue
			}
			seen[v.Name] = fieldOrigin{pos: v.Pos, from: sourceLabel}
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			nestedParts := v.Ref.Name.Parts
			if len(nestedParts) == 1 {
				r.collectProjectMixinFields(targetPkg, nestedParts[0], sourceLabel, mixinPos, seen, visited)
			} else if len(nestedParts) == 2 {
				r.collectProjectMixinFields(nestedParts[0], nestedParts[1], sourceLabel, mixinPos, seen, visited)
			}
		}
	}
}
