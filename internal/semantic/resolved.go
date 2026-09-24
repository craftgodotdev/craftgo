package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// NilableScalarPrimitive reports whether a scalar over prim holds nil
// without a pointer: true for `bytes` and `any`.
func NilableScalarPrimitive(prim string) bool {
	sp, ok := prims.Lookup(prim)
	return ok && (sp.Kind == prims.Bytes || sp.Kind == prims.Any)
}

// FieldCategory classifies a field's resolved type independent of Go syntax.
type FieldCategory int

const (
	CatUnknown   FieldCategory = iota
	CatPrimitive               // string / int* / uint* / float* / bool / datetime
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

// ResolvedField is what one field means in the DSL, independent of any
// target's syntax.
type ResolvedField struct {
	// Field is the source field, after generic substitution and mixin promotion.
	Field *ast.Field

	DSLName  string        // the source field identifier (wire/json base name)
	Category FieldCategory // the resolved type category

	// ResolvedPrim is the DSL primitive behind a primitive, bytes, any,
	// scalar or enum field, as its declaring package defines it; "" otherwise.
	ResolvedPrim string

	// HomePkg is the package a named type resolves in: the qualifier of a
	// `lib.X` ref, else the resolving package; "" for a built-in or raw field.
	HomePkg string

	// IsNilable reports whether the Go type holds nil itself, so `?` adds no
	// pointer. A raw field's nil means absent; a JSON null arrives as `null` bytes.
	IsNilable bool

	// Name is the target's identifier for the field, from the [LevelNames]
	// passed to [ResolveFields]; empty without one.
	Name string

	// Binding is where the value rides; [RequestFields] applies auto-binding.
	Binding    wire.Binding
	OnWireBody bool // a property of the JSON body
	// AutoBound reports that [RequestFields] bound the field to @path or
	// @query without a binding decorator.
	AutoBound bool

	NeedsNilGuard bool // optional or @nullable

	HasDefault  bool // carries @default
	DefaultWire any  // the resolved default as a wire value (enum member -> wire)
	HasDefValue bool // a default value resolved

	// SpecRequired puts the field in the document's required list (no `?`,
	// no @default); RuntimeEnforced emits a presence check (not optional,
	// not @sensitive).
	SpecRequired    bool
	RuntimeEnforced bool
}

// FieldIsOptional reports whether f may be absent: declared `T?` or
// carrying `@nullable`.
func FieldIsOptional(f *ast.Field) bool {
	return f != nil && f.Type != nil && (f.Type.Optional || ast.HasDecorator(f.Decorators, "nullable"))
}

// ResolveField resolves f's facts. A bare type name resolves in pkg, so a
// field promoted from another package's mixin needs that package; a
// qualified `lib.X` resolves through proj.
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
		rf.IsNilable = true
		return rf
	}
	if t.Map != nil {
		rf.Category = CatMap
		rf.IsNilable = true
		return rf
	}
	if t.Named == nil || t.Named.Name == nil {
		return rf
	}
	parts := t.Named.Name.Parts
	if len(parts) == 0 {
		return rf
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
				// A scalar over raw bytes is a raw field.
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
	return rf // unresolved: a type parameter or a missing ref
}
