package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// decoratorSite is one place the design writes decorators: a file header, a
// declaration, an enum value, a field or a method.
type decoratorSite struct {
	level Level
	// label names the site in diagnostics, such as "field User.name".
	label string
	decs  []*ast.Decorator
	// decl is the declaration holding the site; nil at a file header.
	decl ast.Decl
	// field is the field of a field site.
	field *ast.Field
	// inherited holds, at a method of an extend block, the block's
	// decorators the method inherits.
	inherited []*ast.Decorator
}

// extendBlock reports whether s is the decorator list of an `extend
// service` block.
func (s decoratorSite) extendBlock() bool {
	sd, ok := s.decl.(*ast.ServiceDecl)
	return ok && sd.Extend && s.level == LvlService
}

// allows reports whether decorator name may sit at s.
func (s decoratorSite) allows(name string) bool {
	if s.extendBlock() {
		return ExtendAllows(name)
	}
	spec, ok := DecoratorSpec(name)
	return ok && spec.Levels&s.level != 0
}

// decoratorSites returns every decorator site of files once, in source
// order; a method of an extend block holds only the decorators it writes.
func decoratorSites(files []*ast.File) []decoratorSite {
	var out []decoratorSite
	for _, f := range files {
		out = append(out, decoratorSite{level: LvlFile, label: LvlFile.Name(), decs: f.Decorators})
		for _, d := range f.Decls {
			out = appendDeclSites(out, d)
		}
	}
	return out
}

// appendDeclSites appends the sites of d and of every scope nested in it.
func appendDeclSites(out []decoratorSite, d ast.Decl) []decoratorSite {
	site := func(level Level, subject string, decs []*ast.Decorator) decoratorSite {
		return decoratorSite{level: level, label: level.Name() + " " + subject, decs: decs, decl: d}
	}
	fields := func(level Level, parent string, body []ast.TypeMember) {
		for _, f := range ast.Fields(body) {
			s := site(level, parent+"."+f.Name, f.Decorators)
			s.field = f
			out = append(out, s)
		}
	}
	switch dd := d.(type) {
	case *ast.TypeDecl:
		out = append(out, site(LvlType, dd.Name, dd.Decorators))
		fields(LvlField, dd.Name, dd.Body)
	case *ast.EnumDecl:
		out = append(out, site(LvlEnum, dd.Name, dd.Decorators))
		for _, v := range dd.EnumValues() {
			out = append(out, site(LvlEnumValue, dd.Name+"."+v.Name, v.Decorators))
		}
	case *ast.ErrorDecl:
		out = append(out, site(LvlError, dd.Name, dd.Decorators))
		fields(LvlErrorField, dd.Name, dd.Body)
	case *ast.ScalarDecl:
		out = append(out, site(LvlScalar, dd.Name, dd.Decorators))
	case *ast.MiddlewareDecl:
		out = append(out, site(LvlMiddleware, dd.Name, dd.Decorators))
	case *ast.EventDecl:
		out = append(out, site(LvlEvent, dd.Name, dd.Decorators))
	case *ast.ServiceDecl:
		block := site(LvlService, dd.Name, dd.Decorators)
		if dd.Extend {
			block.label = "extend " + block.label
		}
		out = append(out, block)
		inherited := blockInherited(dd)
		for _, m := range dd.Methods() {
			s := site(LvlMethod, dd.Name+"."+m.Name, m.Decorators)
			s.inherited = inherited
			out = append(out, s)
		}
	}
	return out
}

// checkDecoratorSites runs the decorator rules at every site of files.
func (a *analyzer) checkDecoratorSites(files []*ast.File) {
	for _, s := range decoratorSites(files) {
		a.checkDecoratorDuplicates(s)
		a.checkPlacement(s)
		a.checkDecoratorArgs(s)
		a.checkDecoratorRefs(s)
		if f := s.field; f != nil {
			a.checkFieldDefault(f)
			a.checkFieldExample(f)
			a.checkSensitiveConflicts(f)
			a.checkJSONDecorator(f)
		}
	}
}

// checkPlacement reports each decorator at s that is unknown, removed or not
// allowed there.
func (a *analyzer) checkPlacement(s decoratorSite) {
	for _, d := range s.decs {
		spec, known := DecoratorSpec(d.Name)
		switch {
		case !known:
			if note, gone := RemovedDecorator(d.Name); gone {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorRemoved,
					"@%s on %s is no longer a craftgo decorator. %s", d.Name, s.label, note)
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorUnknown,
				"unknown decorator @%s on %s (not in the framework registry)", d.Name, s.label)
		case s.allows(d.Name):
		case s.extendBlock() && namesOneOperation(d.Name):
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeExtendDecoratorNotMethod,
				"@%s on extend service %q would give every method of the block the same one; put it on each method", d.Name, s.decl.(*ast.ServiceDecl).Name)
		case s.extendBlock():
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeExtendDecoratorNotMethod,
				"decorator @%s on extend service %q is not valid on a method; move it to the primary service", d.Name, s.decl.(*ast.ServiceDecl).Name)
		default:
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorPlacement,
				"@%s is not allowed on %s; valid sites: %s", d.Name, s.label, spec.Levels)
		}
	}
}

// checkDecoratorDuplicates rejects a non-repeatable decorator written twice
// at s; one an extend block gives a method counts as written first there.
func (a *analyzer) checkDecoratorDuplicates(s decoratorSite) {
	seen := map[string]lexer.Position{}
	for _, d := range s.inherited {
		if _, dup := seen[d.Name]; !dup {
			seen[d.Name] = d.Pos
		}
	}
	for _, d := range s.decs {
		if spec, _ := DecoratorSpec(d.Name); spec.Repeatable {
			continue
		}
		if prev, ok := seen[d.Name]; ok {
			diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorDuplicate,
				"duplicate decorator @%s on %s", d.Name, s.label)
			diag.Related = related(prev, "first decorator here")
			continue
		}
		seen[d.Name] = d.Pos
	}
}

// checkSensitiveConflicts rejects `@sensitive` beside any field decorator
// that is not [Spec.Metadata].
func (a *analyzer) checkSensitiveConflicts(f *ast.Field) {
	if !ast.HasDecorator(f.Decorators, "sensitive") {
		return
	}
	for _, d := range f.Decorators {
		if d.Name == "sensitive" {
			continue
		}
		spec, ok := DecoratorSpec(d.Name)
		if !ok || spec.Metadata || spec.Levels&(LvlField|LvlErrorField) == 0 {
			continue
		}
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
			CodeDecoratorConflict,
			"@%s cannot be combined with @sensitive: sensitive fields never cross the wire",
			d.Name)
	}
}

// checkJSONDecorator rejects `@json` off the body, and a `@json` key that is
// empty or holds whitespace, a quote, a comma or a backslash.
func (a *analyzer) checkJSONDecorator(f *ast.Field) {
	for _, d := range f.Decorators {
		if d.Name != wire.DecoratorJSON {
			continue
		}
		if kind, ok := wire.BindingKind(f.Decorators); ok && kind != wire.BindBody {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
				"@json cannot be combined with @%s: a field bound off the body has no JSON key - name the wire location in @%s(...) instead", kind, kind)
		}
		if len(d.Args) == 0 {
			continue
		}
		s, ok := d.Args[0].Value.(*ast.StringLit)
		if !ok {
			continue
		}
		if s.Value == "" || strings.ContainsAny(s.Value, " \t\n\",\\") {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgValue,
				"@json on field %q must be a non-empty key without whitespace, quotes, commas or backslashes", f.Name)
		}
	}
}
