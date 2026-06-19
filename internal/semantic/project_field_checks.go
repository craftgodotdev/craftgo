// Project-level field checks: @default / @example literal validation with
// cross-package scalar and enum resolution.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkProjectFieldDefaults re-validates `@default` on fields whose
// declared type is a qualified cross-package reference. The per-package
// analyser DEFERS those (returns true from [defaultElemSupported]) because
// it lacks the cross-package scalar / enum tables; this pass owns the
// final verdict.
//
// Validation steps for each deferred field:
//
//  1. The qualified prefix must resolve to a package in the project AND
//     the trailing symbol must be a scalar (wrapping a primitive) or
//     an enum declared in that package. Otherwise emit
//     [CodeDecoratorConflict] - same code the per-pkg path uses for
//     "@default is not supported on field X".
//  2. The literal kind must match the resolved primitive (string vs
//     int vs float vs bool) or the enum's value set, mirroring
//     [checkDefaultLiteral].
//
// Single-package projects never trigger this pass - no qualified refs
// exist for it to re-validate.
func (r *refResolver) checkProjectFieldDefaults() {
	scalars, enums := r.buildProjectDecls()
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, td := range pkg.Types {
			if td == nil {
				continue
			}
			r.checkBodyDefaults(td.Body, scalars, enums)
		}
		for _, ed := range pkg.Errors {
			if ed == nil {
				continue
			}
			r.checkBodyDefaults(ed.Body, scalars, enums)
		}
	}
}

// buildProjectDecls returns two project-wide lookup tables keyed by
// the qualified DSL form (`<pkg>.<name>`) - the shape that appears
// in a field's [ast.QualifiedIdent.Parts]. Local (single-segment)
// decls are NOT included; the per-package pass already validates
// those.
func (r *refResolver) buildProjectDecls() (map[string]*ast.ScalarDecl, map[string]*ast.EnumDecl) {
	scalars := map[string]*ast.ScalarDecl{}
	enums := map[string]*ast.EnumDecl{}
	for pkgName, pkg := range r.proj.Packages {
		if pkg == nil || pkgName == "" {
			continue
		}
		for sname, sd := range pkg.Scalars {
			scalars[pkgName+"."+sname] = sd
		}
		for ename, ed := range pkg.Enums {
			enums[pkgName+"."+ename] = ed
		}
	}
	return scalars, enums
}

// checkBodyDefaults visits every Field with `@default` whose declared
// type is a qualified ref (two-segment name) and validates the literal
// against the resolved cross-package scalar / enum.
func (r *refResolver) checkBodyDefaults(members []ast.TypeMember, scalars map[string]*ast.ScalarDecl, enums map[string]*ast.EnumDecl) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		r.checkOneFieldDefaultExample(f, scalars, enums)
	}
}

// checkOneFieldDefaultExample validates @default AND @example literals on a
// field whose type is a CROSS-PACKAGE qualified scalar or enum - the case the
// per-package pass skips (it can't resolve a foreign scalar's primitive).
// Both decorators share the resolution + the literal check (kind, enum
// membership, and - for @default - int-capacity / bytes-file). The @default-
// only "not a valid target" rejects are gated on the decorator name; @example
// is lenient (a poor example is harmless), matching the per-package passes.
func (r *refResolver) checkOneFieldDefaultExample(f *ast.Field, scalars map[string]*ast.ScalarDecl, enums map[string]*ast.EnumDecl) {
	if f == nil || f.Type == nil {
		return
	}
	for _, decName := range [...]string{"default", "example"} {
		dec := ast.FindDecorator(f.Decorators, decName)
		if dec == nil {
			continue
		}
		// Element-of-array follows the same rule as the field itself.
		t := f.Type
		if t.Array {
			t = t.ElemTypeRef()
		}
		if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 2 {
			continue // local type - per-package pass already validated it
		}
		qname := t.Named.Name.Parts[0] + "." + t.Named.Name.Parts[1]
		var prim string
		var edDecl *ast.EnumDecl
		switch {
		case scalars[qname] != nil:
			prim = scalars[qname].Primitive
			if primitiveArgKind(prim) == ArgAny {
				// Scalar wraps a non-primitive: @default rejects it (no literal
				// form), @example just ignores it - matching the per-package pass.
				if decName == "default" {
					r.diag(dec.Pos, lexer.SeverityError, CodeDecoratorConflict,
						"@default is not supported on field %q: scalar %s does not wrap a primitive", f.Name, qname)
				}
				continue
			}
		case enums[qname] != nil:
			edDecl = enums[qname]
		default:
			// Neither a scalar nor an enum (a struct, or unknown). @default is
			// not a valid target here; @example silently does nothing.
			if decName == "default" {
				pkgName := t.Named.Name.Parts[0]
				if pkg := r.proj.Packages[pkgName]; pkg != nil && packageHasSymbol(pkg, t.Named.Name.Parts[1]) {
					r.diag(dec.Pos, lexer.SeverityError, CodeDecoratorConflict,
						"@default is not supported on field %q: only primitives, enums, scalars (wrapping primitives), and arrays of those are allowed", f.Name)
				}
			}
			continue
		}
		// Validate each literal (array elements individually) through the SAME
		// check the per-package pass uses, so a cross-package default/example
		// gets the kind / enum-membership (and, for @default, capacity) verdicts.
		emit := func(p lexer.Position, code, format string, args ...any) {
			r.diag(p, lexer.SeverityError, code, format, args...)
		}
		for _, lit := range defaultArgLiterals(f, dec) {
			if lit.v == nil {
				continue // object-literal example - handled per-package
			}
			checkScalarEnumLiteralValue(decName, f.Name, qname, prim, edDecl, lit.v, lit.pos, emit)
		}
	}
}

// defaultLit is one literal a @default carries, with its position.
type defaultLit struct {
	v   ast.Expr
	pos lexer.Position
}

// defaultArgLiterals returns each literal a @default supplies: the single
// positional value, or every element of an array-literal default.
func defaultArgLiterals(f *ast.Field, dec *ast.Decorator) []defaultLit {
	args := positionalArgs(dec)
	if f.Type != nil && f.Type.Array {
		arr, ok := singleArrayLiteral(args)
		if !ok {
			return nil
		}
		out := make([]defaultLit, 0, len(arr.Elements))
		for _, e := range arr.Elements {
			out = append(out, defaultLit{v: e, pos: e.ExprPos()})
		}
		return out
	}
	if len(args) != 1 {
		return nil
	}
	return []defaultLit{{v: args[0].Value, pos: args[0].Pos}}
}

// singleArrayLiteral asserts that args contains exactly one ArrayLit
// argument, returning it. Defensive guard so a bare value on an array
// field doesn't crash the cross-pkg pass.
func singleArrayLiteral(args []*ast.DecoratorArg) (*ast.ArrayLit, bool) {
	if len(args) != 1 {
		return nil, false
	}
	arr, ok := args[0].Value.(*ast.ArrayLit)
	return arr, ok
}
