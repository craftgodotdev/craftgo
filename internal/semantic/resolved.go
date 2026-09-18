// Resolved field IR: the single, LAYER-AGNOSTIC view of a
// field's resolved facts - what the field MEANS in the DSL (its category,
// underlying primitive, home package, nilability), independent of how Go
// renders it. The LSP and the semantic checks read these directly; codegen
// derives the Go-specific bits (the *T pointer wrap, the json tag, the Go
// type string) from them. Computing each fact ONCE here is what stops the
// "semantic resolves a scalar one way, codegen another" class of drift.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// NilableScalarPrimitive reports whether a scalar's underlying primitive
// lowers to a Go type that already holds nil (so a scalar over it renders
// without a pointer wrap): the `bytes` slice and the `any` interface. It is
// the single authority both the resolved IR ([ResolveField]) and codegen's
// pointer-wrap decision cite, so the two layers can't disagree on whether a
// scalar field needs a `*T`.
func NilableScalarPrimitive(prim string) bool {
	sp, ok := prims.Lookup(prim)
	return ok && (sp.Kind == prims.Bytes || sp.Kind == prims.Any)
}

// FieldCategory classifies a field's resolved type independent of Go syntax.
type FieldCategory int

const (
	CatUnknown   FieldCategory = iota
	CatPrimitive               // string / int* / uint* / float* / bool
	CatBytes                   // the `bytes` builtin (Go []byte)
	CatAny                     // the `any` builtin (Go interface{})
	CatRawBytes                // `bytes @format(raw)` (Go wire.Raw)
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
	// a primitive/bytes/any field, the `scalar`'s primitive for a scalar
	// field, and the `enum`'s backing primitive for an enum field - resolved
	// through the declaring package, so a `lib.Colour` ref reports what
	// `lib` declared. "" for struct / array / map / file / unresolved.
	ResolvedPrim string

	// HomePkg is the package the field's named type lives in - the qualifier
	// of a `lib.X` ref, or the package a bare ref was resolved against (which,
	// for a field promoted across a package boundary, is the mixin's home, NOT
	// the using package). "" for a builtin primitive or an unresolved ref.
	HomePkg string

	// IsNilable reports whether the Go type holds nil directly (slice, map,
	// bytes, raw, any, file, or a scalar over a nilable primitive), so an
	// optional `?` / `@nullable` use of it needs no redundant pointer wrap.
	// This is the fact codegen's `*T` decision and the cross-field presence
	// check must agree on. [CatRawBytes] is nilable like the slice it is:
	// a nil wire.Raw is the absent value and an explicit `null` arrives as
	// the four bytes `null`, so the two stay apart on their own. A pointer
	// would lose them: encoding/json nils a pointer on a JSON `null`
	// without ever calling UnmarshalJSON.
	IsNilable bool

	// Name is the identifier the target renders the field with, supplied by
	// the target's own dedup rule during flattening so a promoted field
	// keeps the name it has in its declaring struct.
	Name string

	// Binding is where the value rides, after request auto-binding.
	Binding wire.Binding
	// OnWireBody reports whether the field appears as a property of the
	// JSON body.
	OnWireBody bool
	// AutoBound reports that request resolution promoted an un-decorated
	// field to @path / @query rather than the field declaring it. Stages use
	// it to tell an explicit binding that fails to lower (a hard error) from
	// an auto-promoted field that merely cannot ride the wire (skipped
	// silently). Always false for response and explicitly bound fields.
	AutoBound bool

	// NeedsNilGuard reports that a constraint check must guard before
	// len()/deref: the field is optional or @nullable.
	NeedsNilGuard bool

	HasDefault  bool // carries @default
	DefaultWire any  // the resolved default as a wire value (enum member -> wire)
	HasDefValue bool // a default value resolved

	// SpecRequired: the field belongs in the document's required[] (not
	// optional, no @default). RuntimeEnforced: a presence check is emitted
	// for it (not optional, not @sensitive). Stored side by side so each
	// stage reads ONE answer and a test can assert their relationship as a
	// visible invariant rather than an emergent property of separate
	// predicates. They differ by design on @default (excluded from
	// SpecRequired) and on @sensitive (excluded from RuntimeEnforced).
	SpecRequired    bool
	RuntimeEnforced bool
}

// FieldIsOptional reports whether f may be absent: declared `T?` or
// carrying `@nullable`.
func FieldIsOptional(f *ast.Field) bool {
	return f != nil && f.Type != nil && (f.Type.Optional || ast.HasDecorator(f.Decorators, "nullable"))
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
		dv, hasDV := ResolveDefaultValue(f, pkg)
		rf.Binding = wire.ExplicitBinding(f)
		rf.OnWireBody = wire.NonBodyBindingKind(f) == "" && !wire.HasSensitive(f.Decorators)
		rf.NeedsNilGuard = FieldIsOptional(f)
		rf.HasDefault = ast.HasDecorator(f.Decorators, "default")
		rf.DefaultWire = dv
		rf.HasDefValue = hasDV
		rf.SpecRequired = FieldIsRequired(f)
		rf.RuntimeEnforced = f.Type != nil && !FieldIsOptional(f) && !wire.HasSensitive(f.Decorators)
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
	if len(parts) == 0 {
		return rf // a half-typed ref the editor is still holding open
	}
	name := parts[len(parts)-1]
	homePkg := pkg
	if len(parts) == 2 && proj != nil {
		rf.HomePkg = parts[0]
		homePkg = proj.Packages[parts[0]]
	} else if pkg != nil {
		rf.HomePkg = pkg.Name
	}

	if sp, ok := prims.Lookup(name); ok {
		switch sp.Kind {
		case prims.Bytes:
			if HasRawFormat(f.Decorators) {
				rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg = CatRawBytes, name, true, ""
				return rf
			}
			rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg = CatBytes, name, true, ""
			return rf
		case prims.Any:
			rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg = CatAny, name, true, ""
			return rf
		case prims.File:
			rf.Category, rf.IsNilable, rf.HomePkg = CatFile, true, ""
			return rf
		case prims.String, prims.Bool, prims.Int, prims.Uint, prims.Float, prims.DateTime:
			rf.Category, rf.ResolvedPrim, rf.HomePkg = CatPrimitive, name, ""
			return rf
		}
	}
	if homePkg != nil {
		if sd, ok := homePkg.Scalars[name]; ok && sd != nil {
			if sd.Primitive == "bytes" && (HasRawFormat(sd.Decorators) || HasRawFormat(f.Decorators)) {
				// A scalar over raw bytes names the same Go type a bare
				// raw field lowers to, so the field IS raw - the scalar
				// is the design's name for it, not a second type.
				rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg = CatRawBytes, sd.Primitive, true, ""
				return rf
			}
			rf.Category, rf.ResolvedPrim = CatScalar, sd.Primitive
			rf.IsNilable = NilableScalarPrimitive(sd.Primitive)
			return rf
		}
		if ed, ok := homePkg.Enums[name]; ok {
			rf.Category, rf.ResolvedPrim = CatEnum, EnumPrimitive(ed)
			return rf
		}
		if _, ok := homePkg.Types[name]; ok {
			rf.Category = CatStruct
			return rf
		}
	}
	return rf // unresolved (e.g. a generic type-param or a missing ref)
}
