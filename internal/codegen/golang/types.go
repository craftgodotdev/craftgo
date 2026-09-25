package golang

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

const (
	// rawImportPath and rawGoType name the type of a `bytes @format(raw)`
	// field, whose bytes the codec embeds as they are.
	rawImportPath = "github.com/craftgodotdev/craftgo/pkg/wire"
	rawGoType     = "wire.Raw"
)

// generateTypes writes outDir/<pkg>/types.go with pkg's scalars and structs,
// generic ones included; a package with neither writes nothing.
func generateTypes(pkg *semantic.Package, outDir string, r *projectResolver) error {
	if !pkgDeclaresTypes(pkg) {
		return nil
	}
	return writeGoSource(filepath.Join(outDir, pkg.Name, "types.go"), buildTypesGo(pkg, resolverFor(pkg, r)))
}

// pkgDeclaresTypes reports whether pkg has a struct or scalar for types.go.
func pkgDeclaresTypes(pkg *semantic.Package) bool {
	return len(pkg.Types) > 0 || len(pkg.Scalars) > 0
}

// buildTypesGo returns the unformatted source of pkg's types.go.
func buildTypesGo(pkg *semantic.Package, r *projectResolver) string {
	imports := newImportSet(r.Module, r, goImport{}, typesNames)
	var decls []string
	if scs := renderScalars(pkg, imports); scs != "" {
		decls = append(decls, scs)
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Types)) {
		decls = append(decls, renderType(pkg.Types[name], pkg, r, imports))
	}
	parts := []string{"package " + pkg.Name + "\n"}
	if decl := imports.decl(); decl != "" {
		parts = append(parts, decl)
	}
	return strings.Join(append(parts, decls...), "\n")
}

// renderScalars declares each scalar as a defined type over its Go primitive,
// so it can carry a Validate() method.
func renderScalars(pkg *semantic.Package, imports *importSet) string {
	if len(pkg.Scalars) == 0 {
		return ""
	}
	names := slices.Sorted(maps.Keys(pkg.Scalars))
	const tmpl = "// %s is a DSL scalar over %s; its declared validators live on its Validate() method and are inherited by every field of this type.\ntype %s %s\n\n"
	// A raw scalar is an alias: a defined type would drop wire.Raw's codec methods.
	const rawTmpl = "// %s is a DSL scalar over bytes @format(raw): an alias for the runtime's pass-through type, whose codec methods carry the bytes untouched.\ntype %s = %s\n\n"
	parts := make([]string, len(names))
	for i, n := range names {
		sd := pkg.Scalars[n]
		head := renderDoc(docHead(semantic.DescriptionLines(sd.Decorators, sd.Doc)), "")
		if semantic.HasRawFormat(sd.Decorators) {
			imports.use(rawImportPath)
			parts[i] = head + fmt.Sprintf(rawTmpl, sd.Name, sd.Name, rawGoType)
			continue
		}
		imports.importBuiltin(sd.Primitive)
		parts[i] = head + fmt.Sprintf(tmpl, sd.Name, sd.Primitive, sd.Name, scalarPrimitiveGo(sd.Primitive))
	}
	return strings.Join(parts, "")
}

// scalarPrimitiveGo returns the Go type of DSL primitive name.
func scalarPrimitiveGo(name string) string {
	sp, _ := prims.Lookup(name)
	return sp.Go
}

// renderType returns td's Go struct with its doc and any deprecation notice.
func renderType(td *ast.TypeDecl, pkg *semantic.Package, r *projectResolver, imports *importSet) string {
	doc := renderDoc(semantic.DescriptionLines(td.Decorators, td.Doc), "")
	doc += renderDeprecatedDoc(td.Decorators, "")
	body := renderTypeBody(td.Body, pkg, r, imports)
	header := "type " + td.Name + renderTypeParams(td.TypeParams) + " struct {\n" + body + "}\n"
	return doc + header
}

// renderDeprecatedDoc returns the `Deprecated:` doc paragraph for a @deprecated
// decorator chain, or "".
func renderDeprecatedDoc(decs []*ast.Decorator, indent string) string {
	if !semantic.IsDeprecated(decs) {
		return ""
	}
	reason := semantic.DeprecatedReason(decs)
	if reason == "" {
		reason = "this entity is deprecated and may be removed in a future release."
	}
	return indent + "//\n" + indent + "// Deprecated: " + reason + "\n"
}

// renderTypeParams returns "[T any, U any]" for a generic decl, or ""
// when the decl has no type parameters.
func renderTypeParams(params []string) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p + " any"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// renderTypeBody returns the tab-indented fields and mixin embeds of a struct
// body; colliding Go names get `_2`, `_3` suffixes.
func renderTypeBody(members []ast.TypeMember, pkg *semantic.Package, r *projectResolver, imports *importSet) string {
	resolved := resolvedGoFieldNames(members)
	parts := make([]string, 0, len(members))
	fieldIdx := 0
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			parts = append(parts, renderField(v, resolved[fieldIdx], pkg, r, imports))
			fieldIdx++
		case *ast.Mixin:
			parts = append(parts, "\t"+imports.named(v.Ref)+"\n")
		}
	}
	return strings.Join(parts, "")
}

// resolvedGoFieldNames returns the deduped Go name of each field in members, in
// source order.
func resolvedGoFieldNames(members []ast.TypeMember) []string {
	var dslNames []string
	for _, f := range ast.Fields(members) {
		dslNames = append(dslNames, f.Name)
	}
	resolved, _ := idents.DedupGoFieldNames(dslNames)
	return resolved
}

// renderField returns one tab-indented struct field named goName, with its doc.
func renderField(f *ast.Field, goName string, pkg *semantic.Package, r *projectResolver, imports *importSet) string {
	return renderDoc(semantic.DescriptionLines(f.Decorators, f.Doc), "\t") +
		renderDeprecatedDoc(f.Decorators, "\t") +
		fmt.Sprintf("\t%s %s `%s`\n", goName, goFieldType(f, pkg, r, imports), structTag(f))
}

// goFieldType returns f's Go type as imports spells it: wire.Raw for a raw
// field, and `*T` for an optional or @nullable field whose type does not
// already hold nil.
func goFieldType(f *ast.Field, pkg *semantic.Package, r *projectResolver, imports *importSet) string {
	if f == nil || f.Type == nil {
		return ""
	}
	rf := semantic.ResolveField(f, pkg, r.Project())
	var s string
	if rf.Category == semantic.CatRawBytes {
		imports.use(rawImportPath)
		s = rawGoType
	} else {
		// The pointer below decides the `*`, so the `?` is dropped here.
		clone := *f.Type
		clone.Optional = false
		s = imports.goType(&clone)
	}
	// A file's Go type spells its pointer already.
	if rf.GoPointer() && rf.Category != semantic.CatFile {
		s = "*" + s
	}
	return s
}

// goType spells t in Go: a builtin as its Go type, a declared type by its name
// as declared spells it (nil keeps the name as written), a generic instance
// with its arguments; an optional is a pointer unless res resolves its type to
// one that holds nil.
func goType(t *ast.TypeRef, res *semantic.Resolver, declared func(name string) string) string {
	if t == nil {
		return ""
	}
	var s string
	if t.Map != nil {
		s = "map[" + goType(t.Map.Key, res, declared) + "]" + goType(t.Map.Value, res, declared)
	} else {
		s = goNamedType(t.Named, res, declared)
	}
	s = strings.Repeat("[]", t.ArrayDepth) + s
	if t.Optional && !res.ResolveTypeRef(t).IsNilable {
		s = "*" + s
	}
	return s
}

// goNamedType is [goType] for a named type.
func goNamedType(n *ast.NamedTypeRef, res *semantic.Resolver, declared func(name string) string) string {
	if n == nil || n.Name == nil {
		return ""
	}
	name := n.Name.String()
	if sp, ok := prims.Lookup(name); ok {
		return sp.Go
	}
	if declared != nil {
		name = declared(name)
	}
	if len(n.Args) == 0 {
		return name
	}
	args := make([]string, len(n.Args))
	for i, a := range n.Args {
		args[i] = goType(a, res, declared)
	}
	return name + "[" + strings.Join(args, ", ") + "]"
}

// structTag returns f's struct tag: the json key, plus for a path, query,
// header or cookie field a documentation-only key naming its wire location.
func structTag(f *ast.Field) string {
	tag := "json:" + strconv.Quote(jsonTag(f))
	if kind, ok := wire.NonBodyBindingKind(f); ok {
		tag += " " + kind.String() + ":" + strconv.Quote(wire.WireName(f, kind))
	}
	return tag
}

// jsonTag returns f's json tag from [wire.JSONShape]: "-" for a field off the
// JSON body, `,omitempty` added for an optional `?` field.
func jsonTag(f *ast.Field) string {
	name, presence := wire.JSONShape(f)
	switch presence {
	case wire.JSONAbsent:
		return "-"
	case wire.JSONOptional:
		return name + ",omitempty"
	}
	return name
}
