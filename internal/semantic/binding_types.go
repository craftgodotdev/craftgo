package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// Binding-type diagnostic messages.
const (
	msgBindPath        = "field %s.%s: @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s"
	msgBindWire        = "field %s.%s: @%s requires string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or generic instantiations) - got %s"
	msgBindCookieArray = "field %s.%s: @cookie cannot bind to an array - cookies carry a single value per name"
	msgBindForm        = "field %s.%s: @form requires `file` or string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or file arrays) - got %s"
)

// checkBindingFieldType rejects a wire binding whose field type the binder
// cannot fill, `@nullable` on any wire binding, and `@default` on `@path`.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field) {
	kind, _ := wire.BindingKind(f.Decorators)
	if f.Type == nil || !kind.IsParam() {
		return
	}
	d := ast.FindDecorator(f.Decorators, kind.String())
	switch {
	case ast.HasDecorator(f.Decorators, "nullable"):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
			"@nullable cannot be combined with @%s: a wire parameter is a string with no JSON-null form. Use `?` to make the parameter optional.",
			d.Name)
	case kind == wire.BindPath && ast.HasDecorator(f.Decorators, "default"):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
			"@default cannot be combined with @path: a path segment is always supplied for a matched route, so the default can never apply - drop it.")
	case f.Type.ArrayDepth > 1 && (kind == wire.BindQuery || kind == wire.BindHeader || kind == wire.BindForm):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
			"field %s.%s: @%s cannot bind to a multi-dimensional array - a wire parameter carries repeated single values (`?x=1&x=2`), which has no nested form. Move the field to the JSON body or flatten to a single-level array.",
			parent, f.Name, d.Name)
	case kind == wire.BindPath && !a.isPathBindingType(f.Type):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindPath, parent, f.Name, f.Type)
	case kind == wire.BindCookie && f.Type.Array:
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindCookieArray, parent, f.Name)
	case (kind == wire.BindQuery || kind == wire.BindHeader || kind == wire.BindCookie) && !a.isWireBindingType(f.Type):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindWire, parent, f.Name, d.Name, f.Type)
	case kind == wire.BindForm && !a.isFormBindingType(f.Type):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindForm, parent, f.Name, f.Type)
	}
}

// isQualifiedTypeRef reports whether t names a qualified `pkg.Name` symbol.
func isQualifiedTypeRef(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	return len(t.Named.Name.Parts) >= 2
}

// isPathBindingType reports whether t can bind to `@path`: a wire-bindable
// type that is neither optional nor an array.
func (a *analyzer) isPathBindingType(t *ast.TypeRef) bool {
	return a.pathBindableIn(a.pkg.Name, t)
}

// pathBindableIn is [analyzer.isPathBindingType] with bare type names
// resolved in homePkg.
func (a *analyzer) pathBindableIn(homePkg string, t *ast.TypeRef) bool {
	if t == nil || t.Optional || t.Array {
		return false
	}
	return a.wireBindableIn(homePkg, t)
}

// isWireBindingType reports whether t can bind to a query, header or cookie:
// a parseable primitive, a scalar or enum over one, or a 1-D array of them.
func (a *analyzer) isWireBindingType(t *ast.TypeRef) bool {
	return a.wireBindableIn(a.pkg.Name, t)
}

// wireBindableIn is [analyzer.isWireBindingType] with bare type names
// resolved in homePkg - the package of the type that declares the field.
func (a *analyzer) wireBindableIn(homePkg string, t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Args) > 0 || t.ArrayDepth > 1 {
		return false
	}
	switch rt := a.elemFacts(homePkg, t); rt.Category {
	case CatPrimitive, CatScalar:
		return prims.IsWireParseable(rt.ResolvedPrim)
	case CatEnum:
		return true
	}
	return false
}

// isFormBindingType reports whether t can bind to `@form`: a wire-bindable
// type, a `file`, or a 1-D `file[]`.
func (a *analyzer) isFormBindingType(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil {
		return false
	}
	if a.elemFacts(a.pkg.Name, t).Category == CatFile {
		return t.ArrayDepth <= 1
	}
	return a.isWireBindingType(t)
}

// elemFacts resolves t in homePkg, or the element of t when t is an array.
func (a *analyzer) elemFacts(homePkg string, t *ast.TypeRef) ResolvedField {
	if t.Array {
		t = t.ElemTypeRef()
	}
	return resolveTypeRef(t, false, a.proj.Packages[homePkg], a.proj)
}
