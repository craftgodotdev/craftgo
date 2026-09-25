package golang

import (
	"fmt"
	"go/format"
	"maps"
	"os"
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
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	if !pkgDeclaresTypes(pkg) {
		return nil
	}
	r = resolverFor(pkg, r)
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	src := buildTypesGo(pkg, r)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format types.go: %w\n--- source ---\n%s", err, src)
	}
	return os.WriteFile(filepath.Join(pkgDir, "types.go"), formatted, 0o644)
}

// pkgDeclaresTypes reports whether pkg has a struct or scalar for types.go.
func pkgDeclaresTypes(pkg *semantic.Package) bool {
	return len(pkg.Types) > 0 || len(pkg.Scalars) > 0
}

// buildTypesGo returns the unformatted source of pkg's types.go.
func buildTypesGo(pkg *semantic.Package, r *projectResolver) string {
	parts := []string{
		generatedHeader + "\n",
		"package " + pkg.Name + "\n",
	}
	if imps := collectImports(pkg, r); len(imps) > 0 {
		parts = append(parts, renderImports(imps))
	}
	if scs := renderScalars(pkg); scs != "" {
		parts = append(parts, scs)
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Types)) {
		parts = append(parts, renderType(pkg.Types[name], pkg, r))
	}
	return strings.Join(parts, "\n")
}

// renderScalars declares each scalar as a defined type over its Go primitive,
// so it can carry a Validate() method.
func renderScalars(pkg *semantic.Package) string {
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
		if semantic.HasRawFormat(sd.Decorators) {
			parts[i] = fmt.Sprintf(rawTmpl, sd.Name, sd.Name, rawGoType)
			continue
		}
		parts[i] = fmt.Sprintf(tmpl, sd.Name, sd.Primitive, sd.Name, scalarPrimitiveGo(sd.Primitive))
	}
	return strings.Join(parts, "")
}

// scalarPrimitiveGo returns the Go type of DSL primitive name; other names pass through.
func scalarPrimitiveGo(name string) string {
	if sp, ok := prims.Lookup(name); ok && sp.Go != "" {
		return sp.Go
	}
	return name
}

// renderImports returns the `import (...)` block for imps.
func renderImports(imps []string) string {
	lines := make([]string, len(imps))
	for i, imp := range imps {
		lines[i] = "\t" + strconv.Quote(imp)
	}
	return fmt.Sprintf("import (\n%s\n)\n", strings.Join(lines, "\n"))
}

// collectBodyImports adds to imports every Go import the fields and mixins of a
// type or error body reach, generic arguments included.
func collectBodyImports(body []ast.TypeMember, pkg *semantic.Package, r *projectResolver, imports map[string]bool) {
	addCrossPkg := r.CrossPkg.importsInto(imports)
	visit := func(n *ast.NamedTypeRef) {
		addBuiltinImport(n, imports)
		addCrossPkg(n)
	}
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			if isRawBytesField(v, pkg, r) {
				// The field renders as wire.Raw, so a scalar it names (`shared.RawDoc`) adds no import.
				imports[rawImportPath] = true
				continue
			}
			v.Type.WalkNamedRefs(visit)
		case *ast.Mixin:
			// A generic argument can be a builtin with an import (`Box<file>`).
			v.Ref.WalkNamedRefs(visit)
		}
	}
}

func collectImports(pkg *semantic.Package, r *projectResolver) []string {
	imports := map[string]bool{}
	for _, td := range pkg.Types {
		collectBodyImports(td.Body, pkg, r, imports)
	}
	for _, sd := range pkg.Scalars {
		if semantic.HasRawFormat(sd.Decorators) {
			// The scalar itself is an alias for the runtime type.
			imports[rawImportPath] = true
		}
	}
	return slices.Sorted(maps.Keys(imports))
}

// addBuiltinImport adds to set the stdlib import of the builtin n names, if it
// has one (`file`, `datetime`).
func addBuiltinImport(n *ast.NamedTypeRef, set map[string]bool) {
	switch n.Name.String() {
	case "file":
		set["mime/multipart"] = true
	case "datetime":
		set["time"] = true
	}
}

// renderType returns td's Go struct with its doc and any deprecation notice.
func renderType(td *ast.TypeDecl, pkg *semantic.Package, r *projectResolver) string {
	doc := renderDoc(td.Doc, "")
	doc += renderDeprecatedDoc(td.Decorators, "")
	body := renderTypeBody(td.Body, pkg, r)
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
func renderTypeBody(members []ast.TypeMember, pkg *semantic.Package, r *projectResolver) string {
	resolved := resolvedGoFieldNames(members)
	parts := make([]string, 0, len(members))
	fieldIdx := 0
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			parts = append(parts, renderField(v, resolved[fieldIdx], pkg, r))
			fieldIdx++
		case *ast.Mixin:
			parts = append(parts, renderMixin(v))
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
func renderField(f *ast.Field, goName string, pkg *semantic.Package, r *projectResolver) string {
	return renderDoc(f.Doc, "\t") +
		renderDeprecatedDoc(f.Decorators, "\t") +
		fmt.Sprintf("\t%s %s `%s`\n", goName, goFieldType(f, pkg, r), structTag(f))
}

// goFieldType returns f's Go type: wire.Raw for a raw field, and `*T` for an
// optional or @nullable field whose type does not already hold nil.
func goFieldType(f *ast.Field, pkg *semantic.Package, r *projectResolver) string {
	if f == nil || f.Type == nil {
		return ""
	}
	// goFieldPointerWrap decides the `*`, so the `?` is dropped here.
	clone := *f.Type
	clone.Optional = false
	s := goTypeRef(&clone)
	if isRawBytesField(f, pkg, r) {
		s = rawGoType
	}
	if goFieldPointerWrap(f, pkg, r) {
		s = "*" + s
	}
	return s
}

// isRawBytesField reports whether f is the raw-bytes shape - `bytes
// @format(raw)`, or a scalar over one - whose Go type is [rawGoType].
func isRawBytesField(f *ast.Field, pkg *semantic.Package, r *projectResolver) bool {
	return semantic.ResolveField(f, pkg, r.Project()).Category == semantic.CatRawBytes
}

// goFieldPointerWrap reports whether [goFieldType] prepends `*`: f's Go value
// is a pointer its type does not spell already, as `file` does.
func goFieldPointerWrap(f *ast.Field, pkg *semantic.Package, r *projectResolver) bool {
	rf := semantic.ResolveField(f, pkg, r.Project())
	return rf.GoPointer() && rf.Category != semantic.CatFile
}

// goFieldIsPointer reports whether f's Go value is a pointer.
func goFieldIsPointer(f *ast.Field, pkg *semantic.Package, r *projectResolver) bool {
	return semantic.ResolveField(f, pkg, r.Project()).GoPointer()
}

// renderMixin returns the embed line for m with its package qualifier and
// generic arguments.
func renderMixin(m *ast.Mixin) string {
	return "\t" + goNamedType(m.Ref) + "\n"
}

// goTypeRef returns the Go type of t; an optional gets `*` unless the type
// already holds nil.
func goTypeRef(t *ast.TypeRef) string {
	if t == nil {
		return ""
	}
	var s string
	if t.Map != nil {
		s = "map[" + goTypeRef(t.Map.Key) + "]" + goTypeRef(t.Map.Value)
	} else if t.Named != nil {
		s = goNamedType(t.Named)
	}
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		// A hand-built node may set Array without ArrayDepth.
		depth = 1
	}
	for i := 0; i < depth; i++ {
		s = "[]" + s
	}
	if t.Optional && !isNilableGoType(s) {
		s = "*" + s
	}
	return s
}

// isNilableGoType reports whether the Go type spelt s holds nil, judged from
// the spelling alone.
func isNilableGoType(s string) bool {
	if s == "" {
		return false
	}
	switch {
	case strings.HasPrefix(s, "[]"),
		strings.HasPrefix(s, "map["),
		strings.HasPrefix(s, "*"),
		strings.HasPrefix(s, "chan "),
		strings.HasPrefix(s, "func("):
		return true
	}
	switch s {
	case "any", "interface{}", "error":
		return true
	}
	return false
}

// goNamedType returns the Go form of n: a builtin's Go type, or the name with
// its generic arguments.
func goNamedType(n *ast.NamedTypeRef) string {
	name := n.Name.String()
	if sp, ok := prims.Lookup(name); ok && sp.Go != "" {
		return sp.Go
	}
	if len(n.Args) > 0 {
		var parts []string
		for _, a := range n.Args {
			parts = append(parts, goTypeRef(a))
		}
		return name + "[" + strings.Join(parts, ", ") + "]"
	}
	return name
}

// goFieldName returns the exported Go identifier for a DSL field name.
func goFieldName(name string) string {
	return idents.GoFieldName(name)
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
