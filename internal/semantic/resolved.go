// Resolved field IR: the single, LAYER-AGNOSTIC view of a
// field's resolved facts - what the field MEANS in the DSL (its category,
// underlying primitive, home package, nilability), independent of how Go
// renders it. The LSP and the semantic checks read these directly; codegen
// derives the Go-specific bits (the *T pointer wrap, the json tag, the Go
// type string) from them. Computing each fact ONCE here is what stops the
// recurring "semantic resolves a scalar one way, codegen another" drift
// (e.g. the cross-package-promoted scalar nilability gap).
package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// NilableScalarPrimitive reports whether a scalar's underlying primitive
// lowers to a Go type that already holds nil (so a scalar over it renders
// without a pointer wrap): the `bytes` slice and the `any` interface. It is
// the single authority both the resolved IR ([ResolveField]) and codegen's
// pointer-wrap decision cite, so the two layers can't disagree on whether a
// scalar field needs a `*T`.
func NilableScalarPrimitive(prim string) bool {
	return prim == "bytes" || prim == "any"
}

// FieldCategory classifies a field's resolved type independent of Go syntax.
type FieldCategory int

const (
	CatUnknown   FieldCategory = iota
	CatPrimitive               // string / int* / uint* / float* / bool
	CatBytes                   // the `bytes` builtin (Go []byte)
	CatAny                     // the `any` builtin (Go interface{})
	CatFile                    // the `file` builtin (Go *multipart.FileHeader)
	CatScalar                  // a `scalar Name <prim>` defined type
	CatEnum                    // an `enum Name { ... }` defined type
	CatStruct                  // a `type Name { ... }` struct
	CatArray                   // an array of any of the above
	CatMap                     // a map
)

// ResolvedField is the layer-agnostic resolved view of one field. It is the
// floor every stage stands on: a fact read from here cannot disagree with
// another stage's, because it was computed once.
type ResolvedField struct {
	// Field is the source field (post generic-substitution / mixin
	// promotion); stages needing the raw decorators or type ref read it here.
	Field *ast.Field

	DSLName  string        // the source field identifier (wire/json base name)
	Category FieldCategory // the resolved type category

	// ResolvedPrim is the underlying DSL primitive: the primitive itself for
	// a primitive/bytes/any field, or the `scalar`'s primitive for a scalar
	// field. "" for enum / struct / array / map / file / unresolved.
	ResolvedPrim string

	// HomePkg is the package the field's named type lives in - the qualifier
	// of a `lib.X` ref, or the package a bare ref was resolved against (which,
	// for a field promoted across a package boundary, is the mixin's home, NOT
	// the using package). "" for a builtin primitive or an unresolved ref.
	HomePkg string

	// IsNilable reports whether the Go type holds nil directly (slice, map,
	// bytes, any, file, or a scalar over a nilable primitive), so an optional
	// `?` / `@nullable` use of it needs no redundant pointer wrap. This is the
	// fact codegen's `*T` decision and the cross-field presence check must
	// agree on.
	IsNilable bool
}

// ResolveField computes the layer-agnostic facts for a single field. pkg is
// the field's HOME package - for a bare named ref it is resolved against pkg,
// so a field promoted from a sibling-package mixin must be resolved with that
// mixin's package as pkg (not the using package). proj resolves a qualified
// `lib.X` ref against its named package.
func ResolveField(f *ast.Field, pkg *Package, proj *Project) ResolvedField {
	rf := ResolvedField{Field: f}
	if f != nil {
		rf.DSLName = f.Name
	}
	if f == nil || f.Type == nil {
		return rf
	}
	t := f.Type
	if t.Array {
		rf.Category = CatArray
		rf.IsNilable = true // a Go slice holds nil directly
		return rf
	}
	if t.Map != nil {
		rf.Category = CatMap
		rf.IsNilable = true // a Go map holds nil directly
		return rf
	}
	if t.Named == nil || t.Named.Name == nil {
		return rf
	}
	parts := t.Named.Name.Parts
	name := parts[len(parts)-1]
	homePkg := pkg
	if len(parts) == 2 && proj != nil {
		rf.HomePkg = parts[0]
		homePkg = proj.Packages[parts[0]]
	} else if pkg != nil {
		rf.HomePkg = pkg.Name
	}

	switch name {
	case "bytes":
		rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg = CatBytes, "bytes", true, ""
		return rf
	case "any":
		rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg = CatAny, "any", true, ""
		return rf
	case "file":
		rf.Category, rf.IsNilable, rf.HomePkg = CatFile, true, ""
		return rf
	}
	if isPrimitiveWireName(name) {
		rf.Category, rf.ResolvedPrim, rf.HomePkg = CatPrimitive, name, ""
		return rf
	}
	if homePkg != nil {
		if sd, ok := homePkg.Scalars[name]; ok && sd != nil {
			rf.Category, rf.ResolvedPrim = CatScalar, sd.Primitive
			rf.IsNilable = NilableScalarPrimitive(sd.Primitive)
			return rf
		}
		if _, ok := homePkg.Enums[name]; ok {
			rf.Category = CatEnum
			return rf
		}
		if _, ok := homePkg.Types[name]; ok {
			rf.Category = CatStruct
			return rf
		}
	}
	return rf // unresolved (e.g. a generic type-param or a missing ref)
}
