package semantic

// Project-level binding-type check. Mirror of
// [analyzer.checkBindingFieldType] but uses the full
// [Project.Packages] map so qualified refs like `shared.Email @path`
// resolve through to the foreign-package scalar / enum, instead of
// false-rejecting at the per-package layer where only the local
// pkg.Scalars / pkg.Enums maps are in scope.
//
// The per-package analyzer sets [Options.skipBindingTypeCheckQualified]
// in project mode so qualified-ref binding fields skip the local
// diagnostic; this pass re-runs the same shape rules against the
// project-wide view and emits [CodeBindingType] when a cross-package
// binding turns out to violate one.

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (r *refResolver) checkProjectBindings() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, td := range pkg.Types {
			r.checkBindingsInBody(td.Name, td.Body)
		}
		for _, si := range pkg.Services {
			for _, m := range si.Methods {
				if m.Request != nil {
					if td := r.lookupTypeDecl(m.Request); td != nil {
						r.checkBindingsInBody(td.Name, td.Body)
					}
				}
			}
		}
	}
}

func (r *refResolver) checkBindingsInBody(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		if !isQualifiedTypeRef(f.Type) {
			continue
		}
		r.checkBindingsOnQualifiedField(parent, f)
	}
}

func (r *refResolver) checkBindingsOnQualifiedField(parent string, f *ast.Field) {
	for _, d := range f.Decorators {
		switch d.Name {
		case "path":
			if r.qualifiedIsStringBindable(f.Type, false) {
				continue
			}
			r.diagBinding(d, "field %s.%s: @path requires a non-optional string-backed field (string, string scalar, or string enum) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
		case "query", "header", "cookie":
			if d.Name == "cookie" && f.Type.Array {
				r.diagBinding(d, "field %s.%s: @cookie cannot bind to an array - cookies carry a single value per name",
					parent, f.Name)
				continue
			}
			if r.qualifiedIsWireBindable(f.Type) {
				continue
			}
			r.diagBinding(d, "field %s.%s: @%s requires string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or generic instantiations) - got %s",
				parent, f.Name, d.Name, describeTypeRef(f.Type))
		case "form":
			if r.qualifiedIsFormBindable(f.Type) {
				continue
			}
			r.diagBinding(d, "field %s.%s: @form requires `file` or string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or file arrays) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
		}
	}
}

// qualifiedIsStringBindable is the cross-package twin of
// [isStringBindingType]. The same shape rules apply; the only
// difference is the scalar / enum lookup walks the project's
// package map.
func (r *refResolver) qualifiedIsStringBindable(t *ast.TypeRef, allowOptional bool) bool {
	if t == nil || t.Array || t.Map != nil || t.Named == nil {
		return false
	}
	if t.Optional && !allowOptional {
		return false
	}
	if sc := r.lookupScalar(t.Named); sc != nil {
		return sc.Primitive == "string"
	}
	if ed := r.lookupEnum(t.Named); ed != nil {
		return enumIsStringBacked(ed)
	}
	return false
}

func (r *refResolver) qualifiedIsWireBindable(t *ast.TypeRef) bool {
	if t == nil || t.Map != nil || t.Named == nil || len(t.Named.Args) > 0 {
		return false
	}
	if sc := r.lookupScalar(t.Named); sc != nil {
		return isPrimitiveWireName(sc.Primitive)
	}
	if ed := r.lookupEnum(t.Named); ed != nil {
		return enumWireKindOK(ed)
	}
	return false
}

func (r *refResolver) qualifiedIsFormBindable(t *ast.TypeRef) bool {
	// `file` is never qualified — bare primitive only — so the form
	// check on a qualified ref collapses to the wire rules.
	return r.qualifiedIsWireBindable(t)
}

// enumIsStringBacked returns true when the enum's first member is
// bare or string-typed — the only kinds the @path / @query string
// gate accepts.
func enumIsStringBacked(ed *ast.EnumDecl) bool {
	for _, m := range ed.Members {
		if v, ok := m.(*ast.EnumValue); ok {
			return v.Kind == ast.EnumBare || v.Kind == ast.EnumString
		}
	}
	return false
}

// enumWireKindOK returns true when the enum's first member is one of
// the wire-bindable kinds (bare / string / int).
func enumWireKindOK(ed *ast.EnumDecl) bool {
	for _, m := range ed.Members {
		if v, ok := m.(*ast.EnumValue); ok {
			switch v.Kind {
			case ast.EnumBare, ast.EnumString, ast.EnumInt:
				return true
			}
		}
	}
	return false
}

// lookupScalar resolves a qualified `pkg.Name` ref into the
// foreign package's scalar decl, or returns nil when the package
// or symbol is unknown.
func (r *refResolver) lookupScalar(n *ast.NamedTypeRef) *ast.ScalarDecl {
	pkgName, sym := splitQualified(n)
	if pkgName == "" {
		return nil
	}
	pkg := r.proj.Packages[pkgName]
	if pkg == nil {
		return nil
	}
	return pkg.Scalars[sym]
}

func (r *refResolver) lookupEnum(n *ast.NamedTypeRef) *ast.EnumDecl {
	pkgName, sym := splitQualified(n)
	if pkgName == "" {
		return nil
	}
	pkg := r.proj.Packages[pkgName]
	if pkg == nil {
		return nil
	}
	return pkg.Enums[sym]
}

// lookupTypeDecl resolves either a local-bare or cross-pkg-qualified
// type ref into the owning [ast.TypeDecl]. Used by request-type
// resolution so the binding-type check walks the request body even
// when `request foo.Cred` is qualified.
func (r *refResolver) lookupTypeDecl(n *ast.NamedTypeRef) *ast.TypeDecl {
	if n == nil || n.Name == nil || len(n.Name.Parts) == 0 {
		return nil
	}
	parts := n.Name.Parts
	if len(parts) == 1 {
		for _, pkg := range r.proj.Packages {
			if pkg != nil {
				if td := pkg.Types[parts[0]]; td != nil {
					return td
				}
			}
		}
		return nil
	}
	pkg := r.proj.Packages[parts[0]]
	if pkg == nil {
		return nil
	}
	return pkg.Types[parts[1]]
}

func splitQualified(n *ast.NamedTypeRef) (string, string) {
	if n == nil || n.Name == nil {
		return "", ""
	}
	parts := n.Name.Parts
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func (r *refResolver) diagBinding(d *ast.Decorator, format string, args ...any) {
	r.diag(d.Pos, lexer.SeverityError, CodeBindingType, format, args...)
}

// Keep `strings` import used by file in case future helpers need it.
var _ = strings.Contains
