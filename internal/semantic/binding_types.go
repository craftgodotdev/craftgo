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
	if f.Type == nil {
		return
	}
	if ast.HasDecorator(f.Decorators, "nullable") {
		for _, d := range f.Decorators {
			switch d.Name {
			case wire.BindingPath, wire.BindingQuery, wire.BindingHeader, wire.BindingCookie, wire.BindingForm:
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
					"@nullable cannot be combined with @%s: a wire parameter is a string with no JSON-null form. Use `?` to make the parameter optional.",
					d.Name)
				return
			}
		}
	}
	if ast.HasDecorator(f.Decorators, "default") {
		for _, d := range f.Decorators {
			if d.Name == wire.BindingPath {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
					"@default cannot be combined with @path: a path segment is always supplied for a matched route, so the default can never apply - drop it.")
				return
			}
		}
	}
	// @cookie and @path refuse every array below.
	if f.Type.ArrayDepth > 1 {
		for _, d := range f.Decorators {
			switch d.Name {
			case wire.BindingQuery, wire.BindingHeader, wire.BindingForm:
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
					"field %s.%s: @%s cannot bind to a multi-dimensional array - a wire parameter carries repeated single values (`?x=1&x=2`), which has no nested form. Move the field to the JSON body or flatten to a single-level array.",
					parent, f.Name, d.Name)
				return
			}
		}
	}
	for _, d := range f.Decorators {
		switch d.Name {
		case wire.BindingPath:
			if a.isPathBindingType(f.Type) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				msgBindPath,
				parent, f.Name, describeTypeRef(f.Type))
			return
		case wire.BindingQuery, wire.BindingHeader, wire.BindingCookie:
			// The wire check below accepts arrays; a cookie carries one value.
			if d.Name == wire.BindingCookie && f.Type.Array {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
					msgBindCookieArray,
					parent, f.Name)
				return
			}
			if a.isWireBindingType(f.Type) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				msgBindWire,
				parent, f.Name, d.Name, describeTypeRef(f.Type))
			return
		case wire.BindingForm:
			if a.isFormBindingType(f.Type) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				msgBindForm,
				parent, f.Name, describeTypeRef(f.Type))
			return
		}
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

// describeTypeRef renders t for diagnostics, such as `int[][]` or `string?`;
// generic arguments are left out.
func describeTypeRef(t *ast.TypeRef) string {
	if t == nil {
		return "(none)"
	}
	name := "?"
	if t.Named != nil {
		name = t.Named.Name.String()
	} else if t.Map != nil {
		key, val := "?", "?"
		if t.Map.Key != nil {
			key = describeTypeRef(t.Map.Key)
		}
		if t.Map.Value != nil {
			val = describeTypeRef(t.Map.Value)
		}
		name = "map<" + key + ", " + val + ">"
	}
	// A hand-built TypeRef may set Array without ArrayDepth.
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		depth = 1
	}
	for i := 0; i < depth; i++ {
		name += "[]"
	}
	if t.Optional {
		name += "?"
	}
	return name
}
