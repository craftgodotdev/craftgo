package semantic

// Cross-package import + qualified-ref resolution. Runs after every
// file has been grouped into a package (by `package X` declaration)
// and each package has been individually analysed. For every file we:
//
//  1. Walk `import` declarations, validate the path against the
//     design filesystem, and store the per-file alias map for the LSP.
//  2. Walk every NamedTypeRef (in fields, mixin refs, method
//     request/response, generic args, map keys/values) and resolve
//     multi-part qualified names against the project's package set
//     keyed by the `package X` declaration name.
//
// Aliases (`import alias "path"`) are parsed and recorded but DO NOT
// drive resolution - qualified refs use the bare package name. The
// alias is preserved for IDE tooling that wants to surface "this
// import is referenced under name X".

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// refResolver carries the per-call state for cross-package resolution.
// Kept private - external callers see only the [Project] result.
type refResolver struct {
	proj  *Project
	diags []Diagnostic
}

// processFile validates one file's imports + every qualified ref it
// contains.
func (r *refResolver) processFile(f *ast.File, designRoot string) {
	if f == nil {
		return
	}
	aliases := r.resolveImports(f, designRoot)
	if filename := fileFilename(f); filename != "" {
		r.proj.FileImports[filename] = aliases
	}

	currentPkg := ""
	if f.Package != nil {
		currentPkg = f.Package.Name
	}
	for _, d := range f.Decls {
		r.walkDeclRefs(d, currentPkg)
	}
}

// resolveImports walks f.Imports, validating each path and building
// the file's alias → relative-import-path map. The alias map is
// returned for storage on the project's [Project.FileImports] index;
// resolution itself uses package names, not aliases.
func (r *refResolver) resolveImports(f *ast.File, designRoot string) map[string]string {
	aliases := map[string]string{}
	currentPkg := ""
	if f.Package != nil {
		currentPkg = f.Package.Name
	}
	for _, imp := range f.Imports {
		path := imp.Path
		if path == "" {
			continue
		}
		if isEscapingPath(path) {
			r.diag(imp.Pos, lexer.SeverityError, CodeImportEscape,
				"import %q must be relative to the design root (no leading `/`, `./`, or `..`)", path)
			continue
		}
		if !folderExists(designRoot, path) {
			r.diag(imp.Pos, lexer.SeverityError, CodeImportUnresolved,
				"import %q does not match any folder under the design root", path)
			continue
		}
		// Self-import: `package X` importing a folder whose only
		// `.craftgo` files also declare `package X` - the import is
		// pulling files from itself. Detected when the imported
		// folder's package name matches the current file.
		if currentPkg != "" && currentPkg == folderPkg(path) {
			r.diag(imp.Pos, lexer.SeverityWarning, CodeImportSelf,
				"import %q resolves back into the current package %q (the files are merged anyway)",
				path, currentPkg)
		}
		alias := imp.Alias
		if alias == "" {
			alias = lastSegment(path)
		}
		// First-binding-wins for duplicate aliases - IDE may want to
		// surface the conflict but resolution doesn't depend on it.
		if _, dup := aliases[alias]; !dup {
			aliases[alias] = path
		}
	}
	return aliases
}

// walkDeclRefs descends into a top-level declaration, applying the
// qualified-ref check to every named type reference it contains.
func (r *refResolver) walkDeclRefs(d ast.Decl, currentPkg string) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		r.walkBodyRefs(dd.Body, currentPkg)
	case *ast.ErrorDecl:
		r.walkBodyRefs(dd.Body, currentPkg)
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			if m.Request != nil {
				r.walkNamedRef(m.Request, currentPkg)
			}
			if m.Response != nil && m.Response.Type != nil {
				r.walkNamedRef(m.Response.Type, currentPkg)
			}
		}
	}
}

// walkBodyRefs walks fields + mixin refs in a type/error body.
func (r *refResolver) walkBodyRefs(members []ast.TypeMember, currentPkg string) {
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			r.walkTypeRef(v.Type, currentPkg)
		case *ast.Mixin:
			r.walkNamedRef(v.Ref, currentPkg)
		}
	}
}

// walkTypeRef descends into a TypeRef, recursing through map keys,
// values, and generic arguments.
func (r *refResolver) walkTypeRef(t *ast.TypeRef, currentPkg string) {
	if t == nil {
		return
	}
	if t.Map != nil {
		r.walkTypeRef(t.Map.Key, currentPkg)
		r.walkTypeRef(t.Map.Value, currentPkg)
		return
	}
	if t.Named != nil {
		r.walkNamedRef(t.Named, currentPkg)
	}
}

// walkNamedRef applies the qualified-name validation to one named
// reference and recurses through its generic arguments. Single-part
// names are out of scope here - the per-package analyser already
// resolves them. Multi-part names look up the prefix as a Package
// name in the project; failures emit [CodeRefUnknownPackage] or
// [CodeRefUnknownSymbol].
func (r *refResolver) walkNamedRef(n *ast.NamedTypeRef, currentPkg string) {
	if n == nil || n.Name == nil {
		return
	}
	for _, arg := range n.Args {
		r.walkTypeRef(arg, currentPkg)
	}
	parts := n.Name.Parts
	if len(parts) < 2 {
		return
	}
	if len(parts) > 2 {
		r.diag(n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"qualified reference %q has too many segments (max 1 package prefix)", n.Name.String())
		return
	}
	pkgName, sym := parts[0], parts[1]
	// Self-qualified `currentPkg.Type` is allowed but redundant -
	// resolve it and don't fire any diagnostic.
	target := r.proj.Packages[pkgName]
	if target == nil {
		r.diag(n.Pos, lexer.SeverityError, CodeRefUnknownPackage,
			"package %q is not declared anywhere in the project", pkgName)
		return
	}
	if !packageHasSymbol(target, sym) {
		r.diag(n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"package %q has no symbol %q", pkgName, sym)
		return
	}
	// Arity check for qualified generic refs. The per-package generics
	// pass (checkGenerics) skips qualified names — it only sees the
	// local symbol table — so this is the single site that catches
	// `shared.Page` (declared as `Page<T>`) being used without `<…>`.
	if td := target.Types[sym]; td != nil {
		want := len(td.TypeParams)
		got := len(n.Args)
		switch {
		case want == 0 && got > 0:
			r.diag(n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
				"%s.%s is not a generic type but received %d argument(s)", pkgName, sym, got)
		case want > 0 && got != want:
			r.diag(n.Pos, lexer.SeverityError, CodeGenericArity,
				"%s.%s expects %d generic argument(s), got %d", pkgName, sym, want, got)
		}
	}
}

// checkProjectExtendOrphans walks every package's orphan
// `extend service` decls (those with no primary in the same
// package) and fires a tailored diagnostic. Two outcomes:
//
//   - Primary lives in a SIBLING package → the message names that
//     package and explains the per-package extend rule. The fix is
//     unambiguous (declare the extend inside the owning package).
//   - Primary doesn't exist anywhere → the "no primary declaration"
//     message under the same [CodeServiceExtendOrphan] code.
//
// The per-package pass is muted under [Options.skipExtendOrphanCheck]
// when [AnalyzeProject] runs, so this is the single emit site in
// project mode.
func (r *refResolver) checkProjectExtendOrphans() {
	primaryPkg := map[string]string{}
	primaryPos := map[string]lexer.Position{}
	for pkgName, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary == nil {
				continue
			}
			primaryPkg[name] = pkgName
			primaryPos[name] = si.Primary.Pos
		}
	}
	for _, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary != nil {
				continue
			}
			otherPkg, found := primaryPkg[name]
			for _, e := range si.Extends {
				diag := Diagnostic{
					Pos:      e.Pos,
					End:      e.Pos,
					Severity: lexer.SeverityError,
					Code:     CodeServiceExtendOrphan,
				}
				if found {
					diag.Msg = fmt.Sprintf(
						"extend service %q: primary lives in package %q - extend declarations are per-package, move this block into that package or rename the service",
						name, otherPkg)
					diag.Related = []lexer.Related{{
						Pos: primaryPos[name],
						Msg: "primary service declared here",
					}}
				} else {
					diag.Msg = fmt.Sprintf("extend service %q has no primary declaration", name)
				}
				r.diags = append(r.diags, diag)
			}
		}
	}
}

// checkProjectServiceUniqueness fires when two packages declare a
// primary `service` of the same name. Codegen writes per-service
// scaffolds under `internal/{routes,handler,logic}/<service>/`, so a
// cross-package duplicate would silently overwrite one set of
// scaffolds with the other. Diagnostics fire at every site with
// related entries pointing at the others.
func (r *refResolver) checkProjectServiceUniqueness() {
	type origin struct {
		pkg string
		pos lexer.Position
	}
	groups := map[string][]origin{}
	for pkgName, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary == nil {
				continue
			}
			groups[name] = append(groups[name], origin{pkg: pkgName, pos: si.Primary.Pos})
		}
	}
	for name, occs := range groups {
		if len(occs) < 2 {
			continue
		}
		for i, o := range occs {
			diag := Diagnostic{
				Pos:      o.pos,
				End:      o.pos,
				Severity: lexer.SeverityError,
				Code:     CodeServiceCollision,
				Msg: fmt.Sprintf("service %q is declared in multiple packages - codegen output directories collide; rename one",
					name),
			}
			for j, other := range occs {
				if j == i {
					continue
				}
				diag.Related = append(diag.Related, lexer.Related{
					Pos: other.pos,
					Msg: "also declared in package " + other.pkg,
				})
			}
			r.diags = append(r.diags, diag)
		}
	}
}

// checkProjectMiddlewareUniqueness fires whenever the same middleware
// name is declared in more than one package. Bare cross-package refs
// (`@middlewares(AuthRequired)`) resolve through the global union, so
// a collision would silently pick the first match the iterator hands
// back - the diagnostic forces the author to rename or consolidate.
//
// Diagnostics are emitted at every conflicting declaration, with
// related entries pointing at the other occurrences, so the editor's
// "go to" actions land on each site.
func (r *refResolver) checkProjectMiddlewareUniqueness() {
	type origin struct {
		pkg  string
		decl *ast.MiddlewareDecl
	}
	groups := map[string][]origin{}
	for pkgName, pkg := range r.proj.Packages {
		for name, m := range pkg.Middlewares {
			groups[name] = append(groups[name], origin{pkg: pkgName, decl: m})
		}
	}
	for name, occs := range groups {
		if len(occs) < 2 {
			continue
		}
		for i, o := range occs {
			diag := Diagnostic{
				Pos:      o.decl.Pos,
				End:      o.decl.Pos,
				Severity: lexer.SeverityError,
				Code:     CodeMiddlewareCollision,
				Msg: fmt.Sprintf("middleware %q is declared in multiple packages - names are global; rename or qualify references",
					name),
			}
			for j, other := range occs {
				if j == i {
					continue
				}
				diag.Related = append(diag.Related, lexer.Related{
					Pos: other.decl.Pos,
					Msg: "also declared in package " + other.pkg,
				})
			}
			r.diags = append(r.diags, diag)
		}
	}
}

// checkProjectMiddlewareRefs validates `@middlewares(...)` arguments
// across the entire project. The per-package analyser skips this check
// (under [Options.skipMiddlewareRefCheck]) so a name declared in one
// package can be referenced from another. We accept a name when at
// least one package in the project declares a `middleware Name`; if
// no package does, we report [CodeDecoratorRef] at the reference.
//
// Cross-package middleware references stay UNQUALIFIED - the DSL has
// no syntax for `pkg.MiddlewareName` in decorator argument lists, and
// adding one would force a deeper change to the decorator parser.
// Name collisions across packages are rare enough in practice that
// the framework leans on convention (one canonical declaration per
// name) rather than a strict resolver.
func (r *refResolver) checkProjectMiddlewareRefs(files []*ast.File) {
	declared := map[string]bool{}
	for _, pkg := range r.proj.Packages {
		for name := range pkg.Middlewares {
			declared[name] = true
		}
	}
	for _, f := range files {
		for _, d := range f.Decls {
			s, ok := d.(*ast.ServiceDecl)
			if !ok {
				continue
			}
			r.checkMiddlewareDecorators(s.Decorators, declared)
			for _, m := range s.Methods() {
				r.checkMiddlewareDecorators(m.Decorators, declared)
			}
		}
	}
}

// checkMiddlewareDecorators inspects a decorator slice for any
// `@middlewares(...)` and emits a diagnostic for each argument whose
// value names an undeclared middleware. Two reference forms are
// accepted, in priority order:
//
//  1. Qualified `pkg.Name` - the prefix must match a package in the
//     project AND the trailing segment must be a `middleware Name`
//     declared in that package. This is the canonical form when
//     more than one package declares a middleware with the same
//     bare name (no ambiguity at the call site).
//  2. Bare `Name` - the trailing segment alone must be unique in
//     the union of every package's middleware table. Convenient
//     when names collide-free across packages.
//
// Cross-package lookup is intentional: the per-package analyser
// skips middleware-ref validation under
// [Options.skipMiddlewareRefCheck] so this resolver is the single
// authority on which references are valid.
func (r *refResolver) checkMiddlewareDecorators(decs []*ast.Decorator, declared map[string]bool) {
	for _, d := range decs {
		if d == nil || d.Name != "middlewares" {
			continue
		}
		for _, arg := range collectIdentOrStringArgs(d) {
			if r.middlewareRefResolves(arg.value, declared) {
				continue
			}
			r.diag(arg.pos, lexer.SeverityError, CodeDecoratorRef,
				"@middlewares: %q is not a declared middleware in any package", arg.value)
		}
	}
}

// middlewareRefResolves returns true when value is recognised as a
// valid middleware reference under either the qualified or bare form.
func (r *refResolver) middlewareRefResolves(value string, declared map[string]bool) bool {
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		pkgName := value[:dot]
		bare := value[dot+1:]
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			return false
		}
		_, ok := pkg.Middlewares[bare]
		return ok
	}
	return declared[value]
}

// checkProjectErrorRefs validates every `@errors(...)` target against the
// project-wide error table. The per-package pass skips @errors along with
// the other cross-package decorator refs (middleware / security) because
// a target may be qualified (`shared.UnauthorizedErr`) and resolve in
// another package; without this project-level pass a typo like
// `@errors(NotFounds)` slips silently to codegen, which then emits no
// response for it. Mirrors [checkProjectMiddlewareRefs].
func (r *refResolver) checkProjectErrorRefs(files []*ast.File) {
	declared := map[string]bool{}
	for _, pkg := range r.proj.Packages {
		for name := range pkg.Errors {
			declared[name] = true
		}
	}
	for _, f := range files {
		for _, d := range f.Decls {
			s, ok := d.(*ast.ServiceDecl)
			if !ok {
				continue
			}
			r.checkErrorDecorators(s.Decorators, declared)
			for _, m := range s.Methods() {
				r.checkErrorDecorators(m.Decorators, declared)
			}
		}
	}
}

func (r *refResolver) checkErrorDecorators(decs []*ast.Decorator, declared map[string]bool) {
	for _, d := range decs {
		if d == nil || d.Name != "errors" {
			continue
		}
		for _, arg := range collectIdentOrStringArgs(d) {
			if r.errorRefResolves(arg.value, declared) {
				continue
			}
			r.diag(arg.pos, lexer.SeverityError, CodeDecoratorRef,
				"@errors: %q is not a declared error type in any package", arg.value)
		}
	}
}

// errorRefResolves mirrors [middlewareRefResolves] for error types: a
// qualified `pkg.Name` resolves against that package's error table, a
// bare name against the project-wide declared set.
func (r *refResolver) errorRefResolves(value string, declared map[string]bool) bool {
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		pkgName, bare := value[:dot], value[dot+1:]
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			return false
		}
		_, ok := pkg.Errors[bare]
		return ok
	}
	return declared[value]
}

// packageHasSymbol reports whether sym is declared in pkg's symbol
// tables. We accept any kind (type, enum, error, scalar) - DSL
// resolution doesn't distinguish at the reference site.
func packageHasSymbol(pkg *Package, sym string) bool {
	if pkg == nil {
		return false
	}
	if _, ok := pkg.Types[sym]; ok {
		return true
	}
	if _, ok := pkg.Enums[sym]; ok {
		return true
	}
	if _, ok := pkg.Errors[sym]; ok {
		return true
	}
	if _, ok := pkg.Scalars[sym]; ok {
		return true
	}
	return false
}

// folderPkg returns the conventional `package X` name a folder is
// expected to declare - by convention, the last path segment. Used
// for self-import detection without re-parsing the folder's files.
// A folder whose actual `package X` declaration diverges from this
// convention will not trip the warning, which is fine: the
// declaration is the source of truth and the import is informational.
func folderPkg(importPath string) string {
	return lastSegment(importPath)
}

// isEscapingPath reports whether the import path uses syntax that
// would escape the design root or signal an unsupported absolute
// reference.
func isEscapingPath(p string) bool {
	if len(p) == 0 {
		return false
	}
	if p[0] == '/' {
		return true
	}
	if len(p) >= 2 && p[0] == '.' && p[1] == '/' {
		return true
	}
	if len(p) >= 3 && p[0] == '.' && p[1] == '.' && p[2] == '/' {
		return true
	}
	return p == ".." || p == "."
}

// checkProjectFieldDefaults re-validates `@default` on fields whose
// declared type is a qualified cross-package reference. The per-package
// analyser DEFERS those (returns true from [defaultElemSupported]) because
// it lacks the cross-package scalar / enum tables; this pass owns the
// final verdict.
//
// Validation steps for each deferred field:
//
//  1. The qualified prefix must resolve to a package in the project AND
//     the trailing symbol must be a scalar (wrapping a primitive) or
//     an enum declared in that package. Otherwise emit
//     [CodeDecoratorConflict] - same code the per-pkg path uses for
//     "@default is not supported on field X".
//  2. The literal kind must match the resolved primitive (string vs
//     int vs float vs bool) or the enum's value set, mirroring
//     [checkDefaultLiteral].
//
// Single-package projects never trigger this pass — no qualified refs
// exist for it to re-validate.
func (r *refResolver) checkProjectFieldDefaults() {
	scalars, enums := r.buildProjectDecls()
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, td := range pkg.Types {
			if td == nil {
				continue
			}
			r.checkBodyDefaults(td.Body, scalars, enums)
		}
		for _, ed := range pkg.Errors {
			if ed == nil {
				continue
			}
			r.checkBodyDefaults(ed.Body, scalars, enums)
		}
	}
}

// buildProjectDecls returns two project-wide lookup tables keyed by
// the qualified DSL form (`<pkg>.<name>`) - the shape that appears
// in a field's [ast.QualifiedIdent.Parts]. Local (single-segment)
// decls are NOT included; the per-package pass already validates
// those.
func (r *refResolver) buildProjectDecls() (map[string]*ast.ScalarDecl, map[string]*ast.EnumDecl) {
	scalars := map[string]*ast.ScalarDecl{}
	enums := map[string]*ast.EnumDecl{}
	for pkgName, pkg := range r.proj.Packages {
		if pkg == nil || pkgName == "" {
			continue
		}
		for sname, sd := range pkg.Scalars {
			scalars[pkgName+"."+sname] = sd
		}
		for ename, ed := range pkg.Enums {
			enums[pkgName+"."+ename] = ed
		}
	}
	return scalars, enums
}

// checkBodyDefaults visits every Field with `@default` whose declared
// type is a qualified ref (two-segment name) and validates the literal
// against the resolved cross-package scalar / enum.
func (r *refResolver) checkBodyDefaults(members []ast.TypeMember, scalars map[string]*ast.ScalarDecl, enums map[string]*ast.EnumDecl) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		r.checkOneFieldDefault(f, scalars, enums)
	}
}

func (r *refResolver) checkOneFieldDefault(f *ast.Field, scalars map[string]*ast.ScalarDecl, enums map[string]*ast.EnumDecl) {
	if f == nil || f.Type == nil {
		return
	}
	dec := defaultDecorator(f)
	if dec == nil {
		return
	}
	// Element-of-array follows the same rule as the field itself.
	t := f.Type
	if t.Array {
		t = arrayElemTypeRef(t)
	}
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 2 {
		// Per-package pass already validated this case.
		return
	}
	qname := t.Named.Name.Parts[0] + "." + t.Named.Name.Parts[1]
	if sd, ok := scalars[qname]; ok {
		want := primKindFor(sd.Primitive)
		if want == ArgAny {
			// Scalar wraps something we don't classify as a primitive
			// (custom type, generic, ...) - reject the same way the
			// per-package pass does for unsupported wrappers.
			r.diag(dec.Pos, lexer.SeverityError, CodeDecoratorConflict,
				"@default is not supported on field %q: scalar %s does not wrap a primitive", f.Name, qname)
			return
		}
		r.validateDefaultLiteralKind(f, dec, qname, want)
		return
	}
	if ed, ok := enums[qname]; ok {
		r.validateDefaultEnumLiteral(f, dec, ed)
		return
	}
	// Resolves to neither a scalar nor an enum (could be a struct type,
	// or simply unknown). The qualified-ref pass already flagged unknown
	// symbols with [CodeRefUnknownPackage] / [CodeRefUnknownSymbol]; we
	// only need to surface the @default-specific message when the
	// symbol exists but isn't a valid @default target.
	pkgName := t.Named.Name.Parts[0]
	if pkg := r.proj.Packages[pkgName]; pkg != nil && packageHasSymbol(pkg, t.Named.Name.Parts[1]) {
		r.diag(dec.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"@default is not supported on field %q: only primitives, enums, scalars (wrapping primitives), and arrays of those are allowed", f.Name)
	}
}

// validateDefaultLiteralKind checks one positional literal against an
// expected [ArgKind]. Multi-arg / array literals are out of scope
// (covered by the per-package pass for array fields whose ELEMENT is
// local; for array fields whose element is a QUALIFIED ref the loop
// below handles each element).
func (r *refResolver) validateDefaultLiteralKind(f *ast.Field, dec *ast.Decorator, qname string, want ArgKind) {
	args := positionalArgs(dec)
	// Array field: each element must match the scalar's primitive.
	if f.Type != nil && f.Type.Array {
		arr, ok := singleArrayLiteral(args)
		if !ok {
			return
		}
		for _, e := range arr.Elements {
			if !exprMatchesKind(e, want) {
				r.diag(e.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
					"@default on field %q (%s) requires a %s literal", f.Name, qname, want)
			}
		}
		return
	}
	if len(args) != 1 {
		return
	}
	if !exprMatchesKind(args[0].Value, want) {
		r.diag(args[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@default on field %q (%s) requires a %s literal", f.Name, qname, want)
	}
}

// validateDefaultEnumLiteral mirrors the enum branch of
// [checkDefaultLiteral] for cross-package enum fields.
func (r *refResolver) validateDefaultEnumLiteral(f *ast.Field, dec *ast.Decorator, ed *ast.EnumDecl) {
	args := positionalArgs(dec)
	// Element-wise check for array fields.
	if f.Type != nil && f.Type.Array {
		arr, ok := singleArrayLiteral(args)
		if !ok {
			return
		}
		for _, e := range arr.Elements {
			r.checkEnumIdent(f, e, e.ExprPos(), ed)
		}
		return
	}
	if len(args) != 1 {
		return
	}
	r.checkEnumIdent(f, args[0].Value, args[0].Pos, ed)
}

func (r *refResolver) checkEnumIdent(f *ast.Field, v ast.Expr, pos lexer.Position, ed *ast.EnumDecl) {
	ident, ok := v.(*ast.IdentExpr)
	if !ok {
		r.diag(pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@default on enum field %q must reference an enum value by name (one of %s)", f.Name, enumValueList(ed))
		return
	}
	if ident.Name == nil || len(ident.Name.Parts) != 1 {
		r.diag(pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@default on enum field %q must be one of %s", f.Name, enumValueList(ed))
		return
	}
	want := ident.Name.Parts[0]
	for _, ev := range ed.EnumValues() {
		if ev.Name == want {
			return
		}
	}
	r.diag(pos, lexer.SeverityError, CodeDecoratorArgValue,
		"@default %q is not a value of enum %s; expected one of %s", want, ed.Name, enumValueList(ed))
}

// defaultDecorator returns the first `@default` decorator on f, or nil.
func defaultDecorator(f *ast.Field) *ast.Decorator { return ast.FindDecorator(f.Decorators, "default") }

// primKindFor is a tiny alias for [defaultPrimitiveKind] when no scalar
// indirection is required (the scalar is already resolved upstream).
func primKindFor(primName string) ArgKind {
	switch primName {
	case "string", "bytes":
		return ArgString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return ArgInt
	case "float32", "float64":
		return ArgNumber
	case "bool":
		return ArgBool
	}
	return ArgAny
}

// singleArrayLiteral asserts that args contains exactly one ArrayLit
// argument, returning it. Defensive guard so a bare value on an array
// field doesn't crash the cross-pkg pass.
func singleArrayLiteral(args []*ast.DecoratorArg) (*ast.ArrayLit, bool) {
	if len(args) != 1 {
		return nil, false
	}
	arr, ok := args[0].Value.(*ast.ArrayLit)
	return arr, ok
}

// checkProjectMixins runs the unified mixin expansion across every
// type and error body in every package. In project mode the
// per-package pass is gated off (see [Options.skipMixinCheck]) because
// qualified mixin refs (`shared.Timestamps`) can only resolve here,
// where every package's symbol tables are visible at once.
//
// The expansion mirrors [analyzer.collectMixinFields]: walk a host's
// own direct fields into a seen map keyed by field name, then walk
// every mixin (local OR qualified) and either add its fields or fire
// the appropriate diagnostic (cycle, conflict, non-type, arity,
// unresolved). Conflict detection works across the local + qualified
// mixin boundary because they share the same seen map.
func (r *refResolver) checkProjectMixins() {
	pkgsByName := r.proj.Packages
	for currentPkg, pkg := range pkgsByName {
		if pkg == nil {
			continue
		}
		for _, td := range pkg.Types {
			r.checkOneTypeMixinsProject(currentPkg, td.Name, td.Body)
		}
		for _, ed := range pkg.Errors {
			r.checkOneTypeMixinsProject(currentPkg, ed.Name, ed.Body)
		}
	}
}

// checkOneTypeMixinsProject is the project-aware twin of
// [analyzer.checkOneTypeMixins]. Walks host's direct fields into a
// seen map, then resolves and expands every mixin (local OR
// qualified). currentPkg is the host's package name; used to
// disambiguate local-mixin lookups and as the starting label for
// cycle detection.
func (r *refResolver) checkOneTypeMixinsProject(currentPkg, host string, body []ast.TypeMember) {
	seen := map[string]fieldOrigin{}
	for _, m := range body {
		if f, ok := m.(*ast.Field); ok {
			if _, dup := seen[f.Name]; dup {
				continue
			}
			seen[f.Name] = fieldOrigin{pos: f.Pos, from: host}
		}
	}
	for _, m := range body {
		mx, ok := m.(*ast.Mixin)
		if !ok {
			continue
		}
		r.processProjectMixin(currentPkg, host, mx, seen)
	}
	for _, c := range fieldEmbedClashes(body) {
		r.diag(c.pos, lexer.SeverityError, CodeMixinConflict,
			"field %q collides with the embedded mixin %q: both become the Go field %q in the generated struct. Rename the field.",
			c.field, c.mixin, c.goName)
	}
}

// processProjectMixin resolves one mixin reference - local or
// qualified - and expands its fields. Diagnostic codes match the
// per-package pass so IDE quickfix logic doesn't need to learn a new
// vocabulary.
func (r *refResolver) processProjectMixin(currentPkg, host string, mx *ast.Mixin, seen map[string]fieldOrigin) {
	if mx.Ref == nil || mx.Ref.Name == nil {
		return
	}
	parts := mx.Ref.Name.Parts
	if len(parts) == 0 || len(parts) > 2 {
		return
	}
	targetPkg := currentPkg
	targetName := parts[0]
	if len(parts) == 2 {
		targetPkg = parts[0]
		targetName = parts[1]
	}
	pkg := r.proj.Packages[targetPkg]
	if pkg == nil {
		// Qualified prefix didn't resolve - the qualified-ref check
		// in [walkNamedRef] already fired CodeRefUnknownPackage.
		// Silent here to avoid the duplicate.
		return
	}
	td, ok := pkg.Types[targetName]
	if !ok {
		// Resolved to a non-type entity (enum / error / scalar /
		// middleware) - same code as the per-package pass uses.
		kind := ""
		switch {
		case pkg.Enums[targetName] != nil:
			kind = "enum"
		case pkg.Errors[targetName] != nil:
			kind = "error"
		case pkg.Scalars[targetName] != nil:
			kind = "scalar"
		case pkg.Middlewares[targetName] != nil:
			kind = "middleware"
		}
		if kind != "" {
			r.diag(mx.Pos, lexer.SeverityError, CodeMixinNonType,
				"mixin %s is a %s, not a type", mx.Ref.Name.String(), kind)
			return
		}
		// Truly unresolved - same diagnostic CodeRefUnknownSymbol
		// the qualified-ref check would have fired for a regular
		// field type. For local refs the per-package pass already
		// fired CodeMixinUnresolved.
		if len(parts) == 2 {
			r.diag(mx.Pos, lexer.SeverityError, CodeMixinUnresolved,
				"mixin %s is not declared in package %q", mx.Ref.Name.String(), targetPkg)
		}
		return
	}
	if len(mx.Ref.Args) != len(td.TypeParams) {
		r.diag(mx.Pos, lexer.SeverityError, CodeMixinArity,
			"mixin %s expects %d generic argument(s), got %d",
			mx.Ref.Name.String(), len(td.TypeParams), len(mx.Ref.Args))
		return
	}
	visited := map[string]bool{currentPkg + "." + host: true}
	r.collectProjectMixinFields(targetPkg, targetName, mx.Ref.Name.String(), mx.Pos, seen, visited)
}

// collectProjectMixinFields walks the mixin target's body and any
// nested mixins it contains, with cross-package resolution at every
// step. visited keys are qualified names (`pkg.Type`) so a cycle that
// crosses package boundaries is still detected.
func (r *refResolver) collectProjectMixinFields(targetPkg, targetName, sourceLabel string, mixinPos lexer.Position, seen map[string]fieldOrigin, visited map[string]bool) {
	qualified := targetPkg + "." + targetName
	if visited[qualified] {
		r.diag(mixinPos, lexer.SeverityError, CodeMixinCycle,
			"mixin %s forms a cycle", sourceLabel)
		return
	}
	visited[qualified] = true
	defer delete(visited, qualified)
	pkg := r.proj.Packages[targetPkg]
	if pkg == nil {
		return
	}
	td, ok := pkg.Types[targetName]
	if !ok {
		return
	}
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			if prev, dup := seen[v.Name]; dup {
				if prev.from == sourceLabel {
					continue
				}
				diag := r.diag(mixinPos, lexer.SeverityError, CodeMixinConflict,
					"mixin %s adds field %q, which conflicts with %s",
					sourceLabel, v.Name, prev.from)
				diag.Related = related(prev.pos, "first field declared here")
				continue
			}
			seen[v.Name] = fieldOrigin{pos: v.Pos, from: sourceLabel}
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			nestedParts := v.Ref.Name.Parts
			if len(nestedParts) == 1 {
				r.collectProjectMixinFields(targetPkg, nestedParts[0], sourceLabel, mixinPos, seen, visited)
			} else if len(nestedParts) == 2 {
				r.collectProjectMixinFields(nestedParts[0], nestedParts[1], sourceLabel, mixinPos, seen, visited)
			}
		}
	}
}

// diag is a thin wrapper that appends a diagnostic with End = Pos
// and returns a pointer to the freshly-stored entry so callers can
// attach Related links inline (matching [analyzer.diag]). Cross-pkg
// diagnostics don't have a clean trailing position the way decorator
// names do; the LSP renders an empty range as a single column
// underline.
//
// Do not retain the returned pointer past the next r.diag call;
// slice growth invalidates it.
func (r *refResolver) diag(pos lexer.Position, sev lexer.Severity, code, format string, args ...any) *Diagnostic {
	r.diags = append(r.diags, Diagnostic{
		Pos:      pos,
		End:      pos,
		Severity: sev,
		Code:     code,
		Msg:      fmt.Sprintf(format, args...),
	})
	return &r.diags[len(r.diags)-1]
}

// lastSegment returns the trailing path segment, used as the default
// alias for `import "auth/types"` (alias = "types").
func lastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
