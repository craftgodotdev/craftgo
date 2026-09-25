package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// nilableScalarPrimitive reports whether a scalar over prim holds nil
// without a pointer: true for `bytes` and `any`.
func nilableScalarPrimitive(prim string) bool {
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

	Category FieldCategory // the resolved type category

	// ResolvedPrim is the DSL primitive behind a built-in, scalar or enum
	// field, as its declaring package defines it; "" otherwise.
	ResolvedPrim string

	// IsNilable reports whether the Go type holds nil itself, so `?` adds no
	// pointer. A raw field's nil means absent; a JSON null arrives as `null` bytes.
	IsNilable bool

	// Name is the target's selector for the field, [FlatField.Name] under the
	// [LevelNames] passed to [ResolveFields]; empty without them.
	Name string

	// Binding is where the value rides; [RequestFields] applies auto-binding.
	Binding    wire.Binding
	OnWireBody bool // a property of the JSON body
	// AutoBound reports that [RequestFields] bound the field to @path or
	// @query without a binding decorator.
	AutoBound bool

	NeedsNilGuard bool // optional or @nullable
	HasDefValue   bool // carries a @default whose value resolves

	// SpecRequired puts the field in the document's required list (no `?`,
	// no @default); RuntimeEnforced emits a presence check (not optional,
	// not @sensitive).
	SpecRequired    bool
	RuntimeEnforced bool
}

// GoPointer reports whether the field's Go value is a pointer: a `file`, or
// an optional or @nullable value whose type holds no nil itself.
func (rf ResolvedField) GoPointer() bool {
	return rf.Category == CatFile || (rf.NeedsNilGuard && !rf.IsNilable)
}

// Prims returns the category the field offers type-bound decorators: that
// of its built-in or scalar primitive, [PrimArray], [PrimMap] or
// [PrimRawBytes]; 0 for an enum, a struct, `any` or an unresolved type.
func (rf ResolvedField) Prims() Prims {
	switch rf.Category {
	case CatArray:
		return PrimArray
	case CatMap:
		return PrimMap
	case CatRawBytes:
		return PrimRawBytes
	case CatFile:
		return PrimFile
	case CatPrimitive, CatScalar, CatBytes:
		return PrimFromName(rf.ResolvedPrim)
	}
	return 0
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
	var rf ResolvedField
	if f != nil && f.Type != nil {
		rf = resolveTypeRef(f.Type, HasRawFormat(f.Decorators), pkg, proj)
	}
	rf.Field = f
	if f != nil {
		rf.Binding = wire.ExplicitBinding(f)
		_, offBody := wire.NonBodyBindingKind(f)
		rf.OnWireBody = !offBody && !wire.HasSensitive(f.Decorators)
		rf.NeedsNilGuard = FieldIsOptional(f)
		_, rf.HasDefValue = ResolveDefaultValue(f, pkg)
		rf.SpecRequired = FieldIsRequired(f)
		rf.RuntimeEnforced = f.Type != nil && !FieldIsOptional(f) && !wire.HasSensitive(f.Decorators)
	}
	return rf
}

// ResolveTypeRef returns the type facts of t as r's package spells it,
// resolved as [ResolveField] resolves a field's type; a nil r resolves
// builtins, arrays and maps only.
func (r *Resolver) ResolveTypeRef(t *ast.TypeRef) ResolvedField {
	if t == nil {
		return ResolvedField{}
	}
	proj := r.Project()
	var pkg *Package
	if proj != nil {
		pkg = proj.Packages[r.current]
	}
	return resolveTypeRef(t, false, pkg, proj)
}

// resolveTypeRef returns the type facts of t, resolved as [ResolveField]
// resolves a field's type; raw says `@format(raw)` sits on the field.
func resolveTypeRef(t *ast.TypeRef, raw bool, pkg *Package, proj *Project) ResolvedField {
	var rf ResolvedField
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
	if t.Named == nil || t.Named.Name == nil || proj.namesTypeParam(t.Named.Name) {
		return rf
	}
	parts := t.Named.Name.Parts
	if len(parts) == 0 {
		return rf
	}
	name := parts[len(parts)-1]
	homePkg := pkg
	if len(parts) == 2 && proj != nil {
		homePkg = proj.Packages[parts[0]]
	}

	if sp, ok := prims.Lookup(name); ok {
		switch sp.Kind {
		case prims.Bytes:
			if raw {
				rf.Category, rf.ResolvedPrim, rf.IsNilable = CatRawBytes, name, true
				return rf
			}
			rf.Category, rf.ResolvedPrim, rf.IsNilable = CatBytes, name, true
			return rf
		case prims.Any:
			rf.Category, rf.ResolvedPrim, rf.IsNilable = CatAny, name, true
			return rf
		case prims.File:
			rf.Category, rf.ResolvedPrim, rf.IsNilable = CatFile, name, true
			return rf
		case prims.String, prims.Bool, prims.Int, prims.Uint, prims.Float, prims.DateTime:
			rf.Category, rf.ResolvedPrim = CatPrimitive, name
			return rf
		}
	}
	if homePkg != nil {
		if sd, ok := homePkg.Scalars[name]; ok && sd != nil {
			if sd.Primitive == "bytes" && (raw || HasRawFormat(sd.Decorators)) {
				// A scalar over raw bytes is a raw field.
				rf.Category, rf.ResolvedPrim, rf.IsNilable = CatRawBytes, sd.Primitive, true
				return rf
			}
			rf.Category, rf.ResolvedPrim = CatScalar, sd.Primitive
			rf.IsNilable = nilableScalarPrimitive(sd.Primitive)
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

// LevelNames returns the identifiers a target renders one struct level's
// fields with, in body order. Nil leaves [ResolvedField.Name] empty.
type LevelNames func([]ast.TypeMember) []string

// LookupMethodType returns the type ref names through r and the ref's
// package qualifier ("" when bare), the prefix its bare mixins resolve in.
// The type is nil when r does not resolve it.
func LookupMethodType(ref *ast.NamedTypeRef, r *Resolver) (*ast.TypeDecl, string) {
	if ref == nil || ref.Name == nil {
		return nil, ""
	}
	name := ref.Name.String()
	prefix := ""
	if parts := ref.Name.Parts; len(parts) == 2 {
		prefix = parts[0]
	}
	return r.LookupType(name), prefix
}

// ResolveFields resolves every field [FlattenFields] returns for td, prefix,
// r and levelNames; a bare type name resolves in pkg.
func ResolveFields(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	return resolveFlat(FlattenFields(td, prefix, r, levelNames), pkg, r)
}

// ErrorHasJSONMember reports whether a field of ed, a mixin's included, rides
// its JSON body; an error with none is written as the {"code","message"}
// envelope. ed's names resolve through r.
func ErrorHasJSONMember(ed *ast.ErrorDecl, r *Resolver) bool {
	for _, ff := range FlattenFields(&ast.TypeDecl{Body: ed.Body}, "", r, nil) {
		if _, presence := wire.JSONShape(ff.Field); presence != wire.JSONAbsent {
			return true
		}
	}
	return false
}

// resolveFlat resolves each field of flat; a bare type name resolves in pkg.
func resolveFlat(flat []FlatField, pkg *Package, r *Resolver) []ResolvedField {
	out := make([]ResolvedField, 0, len(flat))
	for _, ff := range flat {
		rf := ResolveField(ff.Field, pkg, r.Project())
		rf.Name = ff.Name
		out = append(out, rf)
	}
	return out
}

// ResponseFields resolves m's response fields, the response type's generic
// arguments substituted; nil when the response names no type.
func ResponseFields(m *ast.Method, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	if m == nil || m.Response == nil {
		return nil
	}
	return resolveInstance(m.Response.Type, pkg, r, levelNames)
}

// resolveInstance resolves the fields of the type ref names, mixins included
// and its generic arguments substituted; nil when ref names no type.
func resolveInstance(ref *ast.NamedTypeRef, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	td, prefix := LookupMethodType(ref, r)
	if td == nil {
		return nil
	}
	return resolveFlat(flattenInstance(td, prefix, ref.Args, r, levelNames), pkg, r)
}

// RequestFields resolves m's request fields, the request type's generic
// arguments substituted, and auto-binds each one with no binding decorator
// and no @sensitive: to @path when its name is a route variable (@prefix
// included), else to @query on a body-less verb.
func RequestFields(m *ast.Method, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	if m == nil || m.Request == nil {
		return nil
	}
	fields := resolveInstance(m.Request, pkg, r, levelNames)
	pathNames := methodRoutePathVars(m, pkg.Services)
	bodyVerb := wire.IsBodyVerb(m.Verb)
	for i := range fields {
		rf := &fields[i]
		// An explicit @body also reads as BindBody.
		if _, explicit := wire.BindingKind(rf.Field.Decorators); explicit || rf.Binding != wire.BindBody {
			continue
		}
		rf.Binding, rf.AutoBound = wire.RequestFieldBinding(rf.Field, pathNames, bodyVerb)
		rf.OnWireBody = rf.Binding == wire.BindBody
	}
	return fields
}
