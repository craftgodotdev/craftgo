package ast

// Test-friendly constructors for AST literals. Hand-typing
// `&TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{"X"}}}}`
// at every test site bloats fixtures with positional cruft; these
// helpers collapse the common cases into one-liners.
//
// Constructors set position info to the zero value - the equality
// helpers ([Equal] methods on TypeRef / NamedTypeRef / ...) ignore
// positions anyway. Reuse from production code is not intended.

// Named builds a TypeRef for a single-segment named type
// (`Named("string")` → DSL `string`). Use [Qualified] for dotted refs.
func Named(name string) *TypeRef {
	return &TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{name}}}}
}

// NamedOpt is `Named` + Optional flag (`string?`).
func NamedOpt(name string) *TypeRef {
	t := Named(name)
	t.Optional = true
	return t
}

// NamedArr is `Named` + Array flag (`string[]`).
func NamedArr(name string) *TypeRef {
	t := Named(name)
	t.Array = true
	t.ArrayDepth = 1
	return t
}

// NamedArrOpt is array + optional (`string[]?`).
func NamedArrOpt(name string) *TypeRef {
	t := NamedArr(name)
	t.Optional = true
	return t
}

// Qualified builds a multi-segment named ref (`Qualified("shared", "User")`
// → DSL `shared.User`). Returns a TypeRef ready to slot into a field.
func Qualified(parts ...string) *TypeRef {
	return &TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: parts}}}
}

// MapOf builds a `map<K, V>` TypeRef from two single-segment names.
// Use [MapOfTypes] when key or value need a more complex shape.
func MapOf(key, value string) *TypeRef {
	return &TypeRef{Map: &MapType{Key: Named(key), Value: Named(value)}}
}

// MapOfTypes is the general form taking already-built TypeRefs.
func MapOfTypes(key, value *TypeRef) *TypeRef {
	return &TypeRef{Map: &MapType{Key: key, Value: value}}
}

// Generic builds a generic-instantiation TypeRef
// (`Generic("Page", Named("Order"))` → DSL `Page<Order>`).
func Generic(name string, args ...*TypeRef) *TypeRef {
	return &TypeRef{Named: &NamedTypeRef{
		Name: &QualifiedIdent{Parts: []string{name}},
		Args: args,
	}}
}

// FieldOf is shorthand for a Field with a single-segment named type
// (`FieldOf("id", "string")` → DSL `id string`).
func FieldOf(name, typeName string) *Field {
	return &Field{Name: name, Type: Named(typeName)}
}

// FieldT is the general form: arbitrary TypeRef.
func FieldT(name string, t *TypeRef) *Field {
	return &Field{Name: name, Type: t}
}

// MixinOf builds a Mixin for a single-segment ref
// (`MixinOf("Profile")` → DSL bare `Profile`).
func MixinOf(name string) *Mixin {
	return &Mixin{Ref: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{name}}}}
}

// MixinQualified builds a Mixin for a multi-segment qualified ref
// (`MixinQualified("shared", "Audit")` → DSL `shared.Audit`).
func MixinQualified(parts ...string) *Mixin {
	return &Mixin{Ref: &NamedTypeRef{Name: &QualifiedIdent{Parts: parts}}}
}
