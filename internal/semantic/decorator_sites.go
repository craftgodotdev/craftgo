package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

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
	spec, ok := Lookup(name)
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
		var inherited []*ast.Decorator
		if dd.Extend {
			block.label = "extend " + block.label
			inherited = inheritedFrom(dd)
		}
		out = append(out, block)
		for _, m := range dd.Methods() {
			s := site(LvlMethod, dd.Name+"."+m.Name, ownDecorators(m, inherited))
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
