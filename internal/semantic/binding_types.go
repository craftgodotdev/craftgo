// Binding type-shape rules: which field types may ride @path / @query /
// @header / @cookie / @form, and the human-readable type description used
// in their diagnostics.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkBindingFieldType vets the type compatibility of `@path`,
// `@header`, `@cookie`, and `@form` bindings up front so the codegen
// never has to produce uncompilable Go.
//
// Per-decorator rules (mirrors the wire-bind codegen in
// `internal/codegen.renderWireBindLine`):
//
//   - `@path`              - the same wire-bindable shapes as @query
//     (string / bool / int* / uint* / float*, or a scalar / enum over
//     one), but never optional or array. Path segments are mandatory by
//     definition (the route matched or it didn't), so optional makes no
//     semantic sense, and a path carries one value per segment. A
//     numeric segment is parsed via the same server.Parse* helper a
//     numeric @query field uses.
//   - `@query` / `@header` / `@cookie` - string + numeric + bool +
//     scalars/enums + arrays of those. Optional string-shaped is
//     accepted (binder emits `*T`); optional numerics use the
//     zero-value sentinel because tri-state pointers off a string-
//     wire are not unambiguous.
//   - `@form`              - same as @query plus the `file` type
//     (multipart upload path). Arrays of file are still rejected
//     because the binder writes a single `*multipart.FileHeader`.
//
// Anything outside these categories raises [CodeBindingType] with a
// message that names the offending shape so the author can repair
// without consulting docs.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field) {
	if f.Type == nil {
		return
	}
	// `@nullable` marks a JSON-body field as accepting an explicit null.
	// A wire parameter (path / query / header / cookie / form) is a string
	// on the wire with no JSON-null form, and the Go field it lowers to
	// would be a pointer the wire binder can't assign - so the pairing is
	// rejected outright. `?` is the way to make a parameter optional.
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
	// A path segment of a matched route is ALWAYS supplied, so @default can
	// never apply. The auto-@path form is rejected by checkAutoPathField;
	// reject the explicit @path form here too so the two forms agree
	// (otherwise codegen emits a dead prefill and the OpenAPI param carries
	// both required:true and a default).
	if ast.HasDecorator(f.Decorators, "default") {
		for _, d := range f.Decorators {
			if d.Name == "path" {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
					"@default cannot be combined with @path: a path segment is always supplied for a matched route, so the default can never apply - drop it.")
				return
			}
		}
	}
	// A wire-string source (@query / @header / @form) encodes an array
	// as repeated scalar params (`?x=1&x=2`) - inherently one-dimensional.
	// A nested array (`int[][]`) has no wire form, so reject it
	// structurally here, before the qualified-ref skip below: array depth
	// is independent of the element type, so the check is the same whether
	// the element is local or cross-package. Without this, codegen emits a
	// 1-D binder against an N-D field that won't compile. (@cookie / @path
	// reject every array shape outright elsewhere.)
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
	// In project mode the per-package pass defers qualified-ref
	// binding-type checks to the post-pass resolver, which has the
	// full project symbol table. Without the skip a cross-pkg scalar
	// like `id shared.Email @path` false-rejects because the local
	// pkg.Scalars map can't see `shared.Email`. See
	// [refResolver.checkProjectBindings] for the cross-pkg-aware
	// re-check.
	if a.opts.skipBindingTypeCheckQualified && isQualifiedTypeRef(f.Type) {
		return
	}
	for _, d := range f.Decorators {
		switch d.Name {
		case "path":
			if isPathBindingType(f.Type, a.pkg) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				"field %s.%s: @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
			return
		case wire.BindingQuery, wire.BindingHeader, wire.BindingCookie:
			// Cookie has no multi-value shape; reject arrays with
			// the source-specific message BEFORE the general wire
			// check (which accepts arrays for query / header).
			if d.Name == "cookie" && f.Type.Array {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
					"field %s.%s: @cookie cannot bind to an array - cookies carry a single value per name",
					parent, f.Name)
				return
			}
			if isWireBindingType(f.Type, a.pkg) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				"field %s.%s: @%s requires string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or generic instantiations) - got %s",
				parent, f.Name, d.Name, describeTypeRef(f.Type))
			return
		case "form":
			if isFormBindingType(f.Type, a.pkg) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				"field %s.%s: @form requires `file` or string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or file arrays) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
			return
		}
	}
}

// isQualifiedTypeRef reports whether t names a cross-package symbol
// (`pkg.Name` with 2 segments). Array / optional wrappers don't
// affect the named ref inside - strip those down to the head ref.
func isQualifiedTypeRef(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	return len(t.Named.Name.Parts) >= 2
}

// isPathBindingType reports whether t can bind to `@path`. A path
// segment is parsed the same way as a `@query` value (string / bool /
// int* / uint* / float*, or a scalar / enum wrapping one), so the
// accepted set is exactly [isWireBindingType] MINUS two shapes a URL
// path can't carry:
//   - optional: a matched route always supplies the segment, so a
//     nilable path field is meaningless.
//   - array: a path carries a single value per segment, with no
//     repeated form.
//
// Numeric path IDs (`/users/{id}` with `id int`) are the common REST
// case; the binder parses the segment via the same server.Parse* helper
// a numeric @query field uses.
func isPathBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Optional || t.Array {
		return false
	}
	return isWireBindingType(t, pkg)
}

// isWireBindingType reports whether t is acceptable as a `@query`,
// `@header`, or `@cookie` field. The shared set covers every primitive
// the codegen's wire-bind shape catalogue can parse:
//
//   - string                              → directSingle / optionalStringNoCast
//   - bool / int* / uint* / float*        → singleParsed
//   - string-backed scalar / enum         → directSingle / optionalStringCast with cast
//   - numeric scalar / int enum           → singleParsed with cast
//   - array of any of the above           → directSlice / arrayString / arrayParsed
//   - optional of any string-shaped item  → optionalString*
//
// Optional numerics are accepted too (the binder writes a `*T` and leaves
// it nil when the key is absent). Rejects: maps, structs, generic
// instantiations, and the `file` type (which only `@form` accepts).
func isWireBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Map != nil || t.Named == nil || len(t.Named.Args) > 0 {
		return false
	}
	// A wire-string source encodes an array as repeated single values
	// (`?x=1&x=2`); a nested array has no wire form. Reject at the shared
	// predicate so every consumer - the explicit `@query`/`@header` check,
	// the auto-@query promotion on a body-less verb, and the @form set -
	// agrees, instead of leaving the depth guard on one path only.
	if t.ArrayDepth > 1 {
		return false
	}
	name := t.Named.Name.String()
	if name == "file" {
		return false
	}
	if isPrimitiveWireName(name) {
		return true
	}
	if pkg == nil {
		return false
	}
	if sc, ok := pkg.Scalars[name]; ok && sc != nil {
		return isPrimitiveWireName(sc.Primitive)
	}
	if ed, ok := pkg.Enums[name]; ok && ed != nil {
		for _, m := range ed.Members {
			if v, ok := m.(*ast.EnumValue); ok {
				switch v.Kind {
				case ast.EnumBare, ast.EnumString, ast.EnumInt:
					return true
				}
			}
		}
	}
	return false
}

// isPrimitiveWireName lists the Go builtin types the wire-bind codegen
// can parse from a single HTTP string. Delegates to
// [idents.IsWireParseable] so semantic-time and gen-time rejections
// share one source of truth - the codegen's `queryPrims` table mirrors
// the same set (semantic mustn't import codegen, so the canonical
// table lives in the type-neutral idents package).
func isPrimitiveWireName(name string) bool {
	return idents.IsWireParseable(name)
}

// isFormBindingType is the wire-bind set plus the `file` type, which
// only multipart supports. `file?` and bare `file` are equivalent
// (the renderer drops the pointer wrap on already-nilable types);
// `file[]` is rejected because the multipart binder writes a single
// `*multipart.FileHeader` slot, not a slice.
func isFormBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Named == nil {
		return false
	}
	if t.Named.Name.String() == "file" {
		// A single `file` or a 1-D `file[]` (repeated multipart parts) binds;
		// a map or a multi-dimensional `file[][]` has no multipart encoding.
		return t.Map == nil && t.ArrayDepth <= 1
	}
	return isWireBindingType(t, pkg)
}

// describeTypeRef renders a short human label for a TypeRef so binding
// diagnostics can say `got string?` / `got string[]` / `got int`. Kept
// minimal - the diagnostic only needs to point at the mismatch.
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
	// Render one `[]` per array dimension so a multi-dim field reads as
	// `int[][]`. ArrayDepth is authoritative; fall back to a single `[]`
	// for any hand-built TypeRef that set only the Array flag.
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

// namedTypeRefs returns the leaf names of every named type referenced by t -
// the named type itself, its generic arguments, and map key / value types.
func namedTypeRefs(t *ast.TypeRef) []string {
	if t == nil {
		return nil
	}
	if t.Map != nil {
		return append(namedTypeRefs(t.Map.Key), namedTypeRefs(t.Map.Value)...)
	}
	var out []string
	if t.Named != nil && t.Named.Name != nil {
		out = append(out, t.Named.Name.String())
		for _, arg := range t.Named.Args {
			out = append(out, namedTypeRefs(arg)...)
		}
	}
	return out
}
