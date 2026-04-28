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
