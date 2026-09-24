package ast

// Constructors for test fixtures; positions are left zero.

// Named returns the TypeRef `name`.
func Named(name string) *TypeRef {
	return &TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{name}}}}
}

// NamedOpt returns the TypeRef `name?`.
func NamedOpt(name string) *TypeRef {
	t := Named(name)
	t.Optional = true
	return t
}

// NamedArr returns the TypeRef `name[]`.
func NamedArr(name string) *TypeRef {
	t := Named(name)
	t.Array = true
	t.ArrayDepth = 1
	return t
}

// NamedArrOpt returns the TypeRef `name[]?`.
func NamedArrOpt(name string) *TypeRef {
	t := NamedArr(name)
	t.Optional = true
	return t
}

// Qualified returns the TypeRef naming the dotted parts, e.g. `shared.User`.
func Qualified(parts ...string) *TypeRef {
	return &TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: parts}}}
}

// MapOf returns the TypeRef `map<key, value>`.
func MapOf(key, value string) *TypeRef {
	return &TypeRef{Map: &MapType{Key: Named(key), Value: Named(value)}}
}

// Generic returns the TypeRef `name<args...>`.
func Generic(name string, args ...*TypeRef) *TypeRef {
	return &TypeRef{Named: &NamedTypeRef{
		Name: &QualifiedIdent{Parts: []string{name}},
		Args: args,
	}}
}

// FieldOf returns the field `name typeName`.
func FieldOf(name, typeName string) *Field {
	return &Field{Name: name, Type: Named(typeName)}
}

// FieldT returns the field `name t`.
func FieldT(name string, t *TypeRef) *Field {
	return &Field{Name: name, Type: t}
}

// MixinOf returns the mixin `name`.
func MixinOf(name string) *Mixin {
	return &Mixin{Ref: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{name}}}}
}

// MixinQualified returns the mixin naming the dotted parts, e.g. `shared.Audit`.
func MixinQualified(parts ...string) *Mixin {
	return &Mixin{Ref: &NamedTypeRef{Name: &QualifiedIdent{Parts: parts}}}
}
