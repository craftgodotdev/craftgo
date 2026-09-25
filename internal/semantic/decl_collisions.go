package semantic

import (
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// goName is a Go identifier a declaration emits.
type goName struct {
	name string
	// source names what emits it, such as `error "UserGone"`.
	source string
	// role says what the identifier is to its source, such as "its code constant".
	role string
	pos  lexer.Position
}

// checkDeclGoNameCollisions rejects two declarations of one package that
// emit the same Go identifier into its types package or its events package.
func (a *analyzer) checkDeclGoNameCollisions(files []*ast.File) {
	var types, events []goName
	for _, f := range files {
		for _, d := range f.Decls {
			if d.DeclName() == "" {
				continue
			}
			if ev, ok := d.(*ast.EventDecl); ok {
				events = append(events, eventGoNames(ev)...)
				continue
			}
			types = append(types, typesGoNames(d)...)
		}
	}
	a.reportGoNameCollisions(types)
	a.reportGoNameCollisions(events)
}

// reportGoNameCollisions reports each identifier of one Go package that
// names emits more than once, every later one by position against the first.
func (a *analyzer) reportGoNameCollisions(names []goName) {
	groups := map[string][]goName{}
	var order []string
	for _, n := range names {
		if _, seen := groups[n.name]; !seen {
			order = append(order, n.name)
		}
		groups[n.name] = append(groups[n.name], n)
	}
	for _, name := range order {
		ns := groups[name]
		if len(ns) < 2 {
			continue
		}
		slices.SortStableFunc(ns, func(x, y goName) int { return comparePos(x.pos, y.pos) })
		first := ns[0]
		for _, dupe := range ns[1:] {
			d := a.diag(dupe.pos, dupe.pos, lexer.SeverityError, CodeDeclGoNameCollision,
				"%s would emit Go identifier %q as %s, which %s already emits as %s - codegen cannot disambiguate decl names; rename one to fix",
				dupe.source, name, dupe.role, first.source, first.role)
			d.Related = related(first.pos, "first emitted here")
		}
	}
}

// typesGoNames returns the Go identifiers d emits into its package's types
// package; a middleware's live in the svccontext package.
func typesGoNames(d ast.Decl) []goName {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		return []goName{{name: dd.Name, source: `type "` + dd.Name + `"`, role: "its type", pos: dd.Pos}}
	case *ast.ScalarDecl:
		return []goName{{name: dd.Name, source: `scalar "` + dd.Name + `"`, role: "its type", pos: dd.Pos}}
	case *ast.EnumDecl:
		out := []goName{{name: dd.Name, source: `enum "` + dd.Name + `"`, role: "its type", pos: dd.Pos}}
		values := dd.EnumValues()
		names := make([]string, len(values))
		for i, v := range values {
			names[i] = v.Name
		}
		for i, c := range idents.EnumConstNames(dd.Name, names) {
			if v := values[i]; v.Name != "" {
				out = append(out, goName{name: c, source: `enum value "` + dd.Name + "." + v.Name + `"`, role: "its constant", pos: v.Pos})
			}
		}
		return out
	case *ast.ErrorDecl:
		source := `error "` + dd.Name + `"`
		out := []goName{
			{name: idents.ErrorTypeName(dd.Name), source: source, role: "its type", pos: dd.Pos},
			{name: idents.ErrorCodeName(dd.Name), source: source, role: "its code constant", pos: dd.Pos},
			{name: idents.ErrorConstructorName(dd.Name), source: source, role: "its constructor", pos: dd.Pos},
		}
		if len(ast.Members(dd.Body)) > 0 {
			out = append(out, goName{name: idents.ErrorBodyName(dd.Name), source: source, role: "its body struct", pos: dd.Pos})
		}
		return out
	}
	return nil
}

// eventGoNames returns the Go identifiers ev emits into its package's events
// package.
func eventGoNames(ev *ast.EventDecl) []goName {
	source := `event "` + ev.Name + `"`
	return []goName{
		{name: ev.Name, source: source, role: "its descriptor", pos: ev.Pos},
		{name: idents.EventContractName(ev.Name), source: source, role: "its contract constant", pos: ev.Pos},
	}
}
