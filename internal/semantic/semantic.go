// Package semantic performs whole-package validation on parsed [ast.File]
// values and produces a merged, name-indexed [Package] for downstream tools.
//
// Responsibilities (current):
//   - Verify package name is consistent across files.
//   - Build symbol tables for types, enums, errors, scalars, middlewares.
//   - Merge primary and `extend service` declarations into a single service.
//   - Reject duplicate top-level names, fields, enum value names/literals,
//     service methods, and route collisions.
//   - Enforce uniform enum value kinds.
//
// Future work (not yet implemented): mixin field expansion, generic
// instantiation, full decorator compatibility-matrix validation, path
// resolution against [config.OpenAPI].BasePath. The current set is enough
// for codegen of plain types/enums/errors and for catching the most common
// authoring mistakes.
package semantic

import (
	"fmt"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// Diagnostic re-exports [lexer.Diagnostic] so semantic-layer callers do not
// need to import the lexer package directly.
type Diagnostic = lexer.Diagnostic

// Package is the merged result of analysing one or more [ast.File] from the
// same logical package. The maps are keyed by the unqualified declaration
// name; cross-package references are resolved later (currently out of scope).
type Package struct {
	// Name is the package name agreed on by every file with a `package`
	// declaration. Empty when no file has one.
	Name string
	// Types maps `type Name { ... }` declarations by name.
	Types map[string]*ast.TypeDecl
	// Enums maps `enum Name { ... }` declarations by name.
	Enums map[string]*ast.EnumDecl
	// Errors maps `error Cat Name [{ ... }]` declarations by name.
	Errors map[string]*ast.ErrorDecl
	// Scalars maps `scalar Name Primitive` declarations by name.
	Scalars map[string]*ast.ScalarDecl
	// Middlewares maps `middleware Name(...)` declarations by name.
	Middlewares map[string]*ast.MiddlewareDecl
	// Services maps service names to the merged primary + extends bundle.
	Services map[string]*ServiceInfo
}

// ServiceInfo bundles the primary `service` declaration with every `extend
// service` continuation that targets the same name. Methods is the merged
// list in source order.
type ServiceInfo struct {
	Primary *ast.ServiceDecl
	Extends []*ast.ServiceDecl
	Methods []*ast.Method
}

// Analyze validates the supplied AST files as a single package and returns
// the merged [Package] together with every diagnostic found. The Package
// value is always non-nil even when diagnostics were reported, so callers
// (codegen, LSP) can do best-effort downstream work.
func Analyze(files []*ast.File) (*Package, []Diagnostic) {
	a := &analyzer{
		pkg: &Package{
			Types:       map[string]*ast.TypeDecl{},
			Enums:       map[string]*ast.EnumDecl{},
			Errors:      map[string]*ast.ErrorDecl{},
			Scalars:     map[string]*ast.ScalarDecl{},
			Middlewares: map[string]*ast.MiddlewareDecl{},
			Services:    map[string]*ServiceInfo{},
		},
	}
	a.checkPackageName(files)
	a.collectDecls(files)
	a.mergeServices()
	a.checkFieldUniqueness()
	a.checkEnums()
	a.checkServiceMethods()
	a.checkDecoratorDuplicates(files)
	a.checkQualifiedRefs()
	a.checkCombinationRules(files)
	return a.pkg, a.diags
}

// analyzer is the per-call state of [Analyze]. Kept private to discourage
// callers from introspecting partial results.
type analyzer struct {
	pkg   *Package
	diags []Diagnostic
}

// errorf appends a diagnostic at pos.
func (a *analyzer) errorf(pos lexer.Position, format string, args ...any) {
	a.diags = append(a.diags, Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// checkPackageName ensures every file with a `package` declaration agrees on
// the same package name and stores it on a.pkg.Name. Files without an
// explicit `package` are treated as belonging to whatever name the others
// pick.
func (a *analyzer) checkPackageName(files []*ast.File) {
	var name string
	var firstPos lexer.Position
	for _, f := range files {
		if f.Package == nil {
			continue
		}
		if name == "" {
			name = f.Package.Name
			firstPos = f.Package.Pos
			continue
		}
		if name != f.Package.Name {
			a.errorf(f.Package.Pos, "package name %q conflicts with %q at %s", f.Package.Name, name, firstPos)
		}
	}
	a.pkg.Name = name
}

// collectDecls walks every declaration once, populates the Package symbol
// tables, and reports duplicate top-level names. Services are special-cased:
// they merge across files via [ServiceInfo] (see [mergeServices]).
func (a *analyzer) collectDecls(files []*ast.File) {
	seen := map[string]lexer.Position{}
	register := func(name string, pos lexer.Position) bool {
		if prev, ok := seen[name]; ok {
			a.errorf(pos, "duplicate top-level declaration %q (previously at %s)", name, prev)
			return false
		}
		seen[name] = pos
		return true
	}
	for _, f := range files {
		for _, d := range f.Decls {
			switch dd := d.(type) {
			case *ast.TypeDecl:
				if register(dd.Name, dd.Pos) {
					a.pkg.Types[dd.Name] = dd
				}
			case *ast.EnumDecl:
				if register(dd.Name, dd.Pos) {
					a.pkg.Enums[dd.Name] = dd
				}
			case *ast.ErrorDecl:
				if register(dd.Name, dd.Pos) {
					a.pkg.Errors[dd.Name] = dd
				}
			case *ast.ScalarDecl:
				if register(dd.Name, dd.Pos) {
					a.pkg.Scalars[dd.Name] = dd
				}
			case *ast.MiddlewareDecl:
				if register(dd.Name, dd.Pos) {
					a.pkg.Middlewares[dd.Name] = dd
				}
			case *ast.ServiceDecl:
				si, ok := a.pkg.Services[dd.Name]
				if !ok {
					si = &ServiceInfo{}
					a.pkg.Services[dd.Name] = si
				}
				if dd.Extend {
					si.Extends = append(si.Extends, dd)
				} else if si.Primary != nil {
					a.errorf(dd.Pos, "duplicate primary service %q (previously at %s)", dd.Name, si.Primary.Pos)
				} else {
					si.Primary = dd
				}
			}
		}
	}
}

// mergeServices flattens each [ServiceInfo] into a single ordered method
// list. `extend service` declarations may not carry service-level
// decorators (those belong on the primary), and orphan extends without a
// primary are reported.
func (a *analyzer) mergeServices() {
	for name, si := range a.pkg.Services {
		if si.Primary == nil {
			for _, e := range si.Extends {
				a.errorf(e.Pos, "extend service %q has no primary declaration", name)
			}
			continue
		}
		si.Methods = append(si.Methods, si.Primary.Methods...)
		for _, e := range si.Extends {
			if len(e.Decorators) > 0 {
				a.errorf(e.Pos, "extend service %q must not have service-level decorators", name)
			}
			si.Methods = append(si.Methods, e.Methods...)
		}
	}
}

// checkFieldUniqueness enforces that no type or error body has two fields
// with the same name. Mixin members are skipped here — their fields are
// expanded later (when implemented) and clash detection happens then.
func (a *analyzer) checkFieldUniqueness() {
	check := func(name string, members []ast.TypeMember) {
		seen := map[string]lexer.Position{}
		for _, m := range members {
			f, ok := m.(*ast.Field)
			if !ok {
				continue
			}
			if prev, exists := seen[f.Name]; exists {
				a.errorf(f.Pos, "duplicate field %q in %q (previously at %s)", f.Name, name, prev)
			} else {
				seen[f.Name] = f.Pos
			}
		}
	}
	for _, td := range a.pkg.Types {
		check(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		check(ed.Name, ed.Body)
	}
}

// checkEnums verifies (a) value-name uniqueness inside each enum, (b)
// uniform value kind across the enum, and (c) literal-value uniqueness for
// int and string enums.
func (a *analyzer) checkEnums() {
	for _, ed := range a.pkg.Enums {
		seenNames := map[string]bool{}
		seenInts := map[int64]bool{}
		seenStrs := map[string]bool{}
		var firstKind ast.EnumValueKind
		first := true
		for _, v := range ed.Values {
			if seenNames[v.Name] {
				a.errorf(v.Pos, "duplicate enum value name %q in %q", v.Name, ed.Name)
			}
			seenNames[v.Name] = true
			if first {
				firstKind = v.Kind
				first = false
			} else if v.Kind != firstKind {
				a.errorf(v.Pos, "enum %q has mixed value types (must be all bare, all int, or all string)", ed.Name)
			}
			switch v.Kind {
			case ast.EnumInt:
				if seenInts[v.IntValue] {
					a.errorf(v.Pos, "duplicate int value %d in enum %q", v.IntValue, ed.Name)
				}
				seenInts[v.IntValue] = true
			case ast.EnumString:
				if seenStrs[v.StrValue] {
					a.errorf(v.Pos, "duplicate string value %q in enum %q", v.StrValue, ed.Name)
				}
				seenStrs[v.StrValue] = true
			}
		}
	}
}

// checkServiceMethods enforces method-name and route-key uniqueness within
// each service after extends have been merged.
func (a *analyzer) checkServiceMethods() {
	for _, si := range a.pkg.Services {
		seenName := map[string]lexer.Position{}
		seenRoute := map[string]lexer.Position{}
		for _, m := range si.Methods {
			if prev, ok := seenName[m.Name]; ok {
				a.errorf(m.Pos, "duplicate method %q (previously at %s)", m.Name, prev)
			} else {
				seenName[m.Name] = m.Pos
			}
			key := m.Verb + " " + PathString(m.Path)
			if prev, ok := seenRoute[key]; ok {
				a.errorf(m.Pos, "duplicate route %q (previously at %s)", key, prev)
			} else {
				seenRoute[key] = m.Pos
			}
		}
	}
}

// checkDecoratorDuplicates rejects two `@same` decorators in the same
// declaration scope. Decorators are identified by their bare name; arguments
// don't disambiguate (`@tags("a")` + `@tags("b")` is still a duplicate). The
// second occurrence is reported, pointing back at the first for context. We
// walk every scope that can carry decorators: the file header, top-level
// declarations, fields inside type / error bodies, enum values, service
// methods, and middleware-declaration sites.
func (a *analyzer) checkDecoratorDuplicates(files []*ast.File) {
	for _, f := range files {
		a.checkDecoratorScope("file", f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclDecorators(d)
		}
	}
}

// checkDeclDecorators dispatches decorator-uniqueness checks for one
// top-level declaration plus every nested scope it owns (fields, methods,
// enum values).
func (a *analyzer) checkDeclDecorators(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkDecoratorScope("type "+dd.Name, dd.Decorators)
		a.checkFieldDecorators(dd.Name, dd.Body)
	case *ast.EnumDecl:
		a.checkDecoratorScope("enum "+dd.Name, dd.Decorators)
		for _, v := range dd.Values {
			a.checkDecoratorScope("enum value "+dd.Name+"."+v.Name, v.Decorators)
		}
	case *ast.ErrorDecl:
		a.checkDecoratorScope("error "+dd.Name, dd.Decorators)
		a.checkFieldDecorators(dd.Name, dd.Body)
	case *ast.ScalarDecl:
		a.checkDecoratorScope("scalar "+dd.Name, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkDecoratorScope("middleware "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		scope := "service " + dd.Name
		if dd.Extend {
			scope = "extend " + scope
		}
		a.checkDecoratorScope(scope, dd.Decorators)
		for _, m := range dd.Methods {
			a.checkDecoratorScope("method "+dd.Name+"."+m.Name, m.Decorators)
		}
	}
}

// checkFieldDecorators applies the duplicate check to every Field in a type
// or error body. Mixin members carry no decorators and are skipped.
func (a *analyzer) checkFieldDecorators(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkDecoratorScope("field "+parent+"."+f.Name, f.Decorators)
	}
}

// checkDecoratorScope is the leaf check: emit a diagnostic for any decorator
// whose Name appears more than once in decs. The first occurrence is silent;
// every subsequent one is flagged with a back-reference to the first.
func (a *analyzer) checkDecoratorScope(scope string, decs []*ast.Decorator) {
	seen := map[string]lexer.Position{}
	for _, d := range decs {
		if d == nil {
			continue
		}
		if prev, ok := seen[d.Name]; ok {
			a.errorf(d.Pos, "duplicate decorator @%s on %s (previously at %s)", d.Name, scope, prev)
			continue
		}
		seen[d.Name] = d.Pos
	}
}

// checkQualifiedRefs flags any `pkg.Type` reference that the v1 model
// cannot resolve. CraftGo v1 uses a folder-merge import model: every
// `.craftgo` file reachable from the design root contributes to a single
// logical package, so type references should be unqualified. A multi-part
// qualified name (e.g. `shared.User`) parses successfully — the AST keeps it
// so the v2 cross-package resolver has something to work with — but produces
// a Go compile error downstream because no Go-level import is emitted. We
// turn that latent failure into a friendly diagnostic up front.
//
// Mixin references are exempt from the check because the codegen already
// strips qualified prefixes (`emitMixin` uses the trailing segment) and the
// generated Go has no other way to spell the embedded field name.
func (a *analyzer) checkQualifiedRefs() {
	for _, td := range a.pkg.Types {
		a.walkTypeMembers(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.walkTypeMembers(ed.Name, ed.Body)
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			if m.Request != nil {
				a.checkNamedRef("method "+m.Name+" request", m.Request)
			}
			if m.Response != nil && m.Response.Type != nil {
				a.checkNamedRef("method "+m.Name+" response", m.Response.Type)
			}
		}
	}
}

// walkTypeMembers checks every Field type reference in a type or error body
// for a qualified prefix. Mixin members are skipped (see [checkQualifiedRefs]).
func (a *analyzer) walkTypeMembers(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.walkTypeRef("field "+parent+"."+f.Name, f.Type)
	}
}

// walkTypeRef descends into a TypeRef and applies the qualified-name check
// to every NamedTypeRef encountered. Map keys, map values, and generic
// arguments are all visited recursively.
func (a *analyzer) walkTypeRef(scope string, t *ast.TypeRef) {
	if t == nil {
		return
	}
	if t.Map != nil {
		a.walkTypeRef(scope, t.Map.Key)
		a.walkTypeRef(scope, t.Map.Value)
		return
	}
	if t.Named != nil {
		a.checkNamedRef(scope, t.Named)
	}
}

// checkNamedRef reports a diagnostic when n.Name has more than one segment
// and recurses through n.Args so generic arguments are validated too.
func (a *analyzer) checkNamedRef(scope string, n *ast.NamedTypeRef) {
	if n == nil || n.Name == nil {
		return
	}
	if len(n.Name.Parts) > 1 {
		a.errorf(n.Pos, "cross-package qualified reference %q in %s is not supported in v1 (folder-merge model); use the unqualified name", n.Name.String(), scope)
	}
	for _, arg := range n.Args {
		a.walkTypeRef(scope, arg)
	}
}

// checkCombinationRules enforces the decorator-combination contract
// documented in the README §"Combination rules":
//
//   - `@required` cannot coexist with `T?` (an optional type — they
//     contradict each other).
//   - At most one of `@path / @query / @header / @cookie / @body / @form`
//     may appear on a single field; multiple non-body bindings would
//     compete for the same value at runtime.
//   - `@raw` bypasses JSON encoding entirely, so `@format` on the same
//     method has no defined meaning and is rejected.
//
// Diagnostics fire on the second / conflicting decorator so the error
// points at the offending source location, not the (innocent) first
// occurrence.
func (a *analyzer) checkCombinationRules(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclCombinations(d)
		}
	}
}

// checkDeclCombinations dispatches per-declaration: type / error bodies
// for field-level rules, services / methods for method-level rules.
func (a *analyzer) checkDeclCombinations(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
	case *ast.ErrorDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
	case *ast.ServiceDecl:
		for _, m := range dd.Methods {
			a.checkMethodCombinations(dd.Name, m)
		}
	}
}

// checkFieldCombinations applies the per-field combination checks to
// every Field in a type or error body. Mixin members are skipped — they
// have no decorators of their own.
func (a *analyzer) checkFieldCombinations(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkRequiredOptional(parent, f)
		a.checkSingleBinding(parent, f)
	}
}

// checkRequiredOptional rejects `@required` on a `T?` field. The two
// say opposite things ("must be present" vs "may be absent"), and
// silently ignoring one would let a buggy validator pass.
func (a *analyzer) checkRequiredOptional(parent string, f *ast.Field) {
	if f.Type == nil || !f.Type.Optional {
		return
	}
	for _, d := range f.Decorators {
		if d.Name == "required" {
			a.errorf(d.Pos, "field %s.%s: @required is incompatible with optional type %q (drop one)", parent, f.Name, "T?")
			return
		}
	}
}

// checkSingleBinding enforces the "at most one binding" rule. The
// six binding decorators (`@path / @query / @header / @cookie / @body /
// @form`) are mutually exclusive; the first wins, every subsequent one
// gets a diagnostic with a back-reference to the first.
func (a *analyzer) checkSingleBinding(parent string, f *ast.Field) {
	bindings := map[string]bool{
		"path": true, "query": true, "header": true,
		"cookie": true, "body": true, "form": true,
	}
	first := ""
	var firstPos lexer.Position
	for _, d := range f.Decorators {
		if !bindings[d.Name] {
			continue
		}
		if first == "" {
			first = d.Name
			firstPos = d.Pos
			continue
		}
		a.errorf(d.Pos, "field %s.%s: @%s conflicts with @%s at %s (a field must have at most one binding)", parent, f.Name, d.Name, first, firstPos)
	}
}

// checkMethodCombinations enforces method-level rules. Today there is
// only one: `@raw` bypasses the encoder, so `@format` is meaningless and
// rejected. The streaming / raw-stream combination is intentionally
// allowed and not flagged here.
func (a *analyzer) checkMethodCombinations(svcName string, m *ast.Method) {
	hasRaw := false
	var rawPos lexer.Position
	for _, d := range m.Decorators {
		if d.Name == "raw" {
			hasRaw = true
			rawPos = d.Pos
			break
		}
	}
	if !hasRaw {
		return
	}
	for _, d := range m.Decorators {
		if d.Name == "format" {
			a.errorf(d.Pos, "method %s.%s: @format is incompatible with @raw at %s (raw mode bypasses encoding)", svcName, m.Name, rawPos)
		}
	}
}

// PathString renders an [ast.Path] as a printable route string, e.g.
// `/users/{id}/posts`. A nil path renders as the empty string. Used for
// route-collision detection and diagnostics.
func PathString(p *ast.Path) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteByte('{')
			sb.WriteString(s.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}
