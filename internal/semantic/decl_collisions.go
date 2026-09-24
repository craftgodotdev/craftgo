package semantic

import (
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// declProducer is a decl that emits a given Go name.
type declProducer struct {
	dslName string
	kind    string // "type" | "error" | "enum" | "scalar"
	pos     lexer.Position
	emitted string // the Go name
}

// checkDeclGoNameCollisions rejects two decls of one package that emit the
// same Go type name.
func (a *analyzer) checkDeclGoNameCollisions(files []*ast.File) {
	groups := map[string][]declProducer{}
	order := []string{}
	add := func(goName, dslName, kind string, pos lexer.Position) {
		p := declProducer{dslName: dslName, kind: kind, pos: pos, emitted: goName}
		if _, seen := groups[goName]; !seen {
			order = append(order, goName)
		}
		groups[goName] = append(groups[goName], p)
	}

	for _, f := range files {
		for _, d := range f.Decls {
			for _, n := range goNamesProducedBy(d) {
				add(n.goName, n.dslName, n.kind, n.pos)
			}
		}
	}

	for _, goName := range order {
		ps := groups[goName]
		if len(ps) < 2 {
			continue
		}
		// Every producer after the first by position is reported against it.
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].pos.Line != ps[j].pos.Line {
				return ps[i].pos.Line < ps[j].pos.Line
			}
			return ps[i].pos.Column < ps[j].pos.Column
		})
		first := ps[0]
		for _, dupe := range ps[1:] {
			d := a.diag(dupe.pos, dupe.pos, lexer.SeverityError, CodeDeclGoNameCollision,
				"%s %q would emit Go identifier %q which already comes from %s %q (%s) - codegen cannot disambiguate decl names; rename one to fix",
				dupe.kind, dupe.dslName, goName, first.kind, first.dslName, describeProducedNames(first))
			d.Related = related(first.pos, "first emitted here")
		}
	}
}

// producedName is a Go name a decl emits.
type producedName struct {
	goName  string
	dslName string
	kind    string // "type" | "error" | "enum" | "scalar"
	pos     lexer.Position
}

// errStructName returns the Go struct name of the error decl name.
func errStructName(name string) string {
	if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
		return name
	}
	return name + "Err"
}

// goNamesProducedBy returns the Go type names d emits into the types
// package.
func goNamesProducedBy(d ast.Decl) []producedName {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		if dd.Name == "" {
			return nil
		}
		return []producedName{{goName: dd.Name, dslName: dd.Name, kind: "type", pos: dd.Pos}}
	case *ast.EnumDecl:
		if dd.Name == "" {
			return nil
		}
		return []producedName{{goName: dd.Name, dslName: dd.Name, kind: "enum", pos: dd.Pos}}
	case *ast.ScalarDecl:
		if dd.Name == "" {
			return nil
		}
		return []producedName{{goName: dd.Name, dslName: dd.Name, kind: "scalar", pos: dd.Pos}}
	case *ast.ErrorDecl:
		if dd.Name == "" {
			return nil
		}
		out := []producedName{{goName: errStructName(dd.Name), dslName: dd.Name, kind: "error", pos: dd.Pos}}
		if len(dd.Body) > 0 {
			out = append(out, producedName{goName: dd.Name + "Body", dslName: dd.Name, kind: "error", pos: dd.Pos})
		}
		return out
	}
	// A middleware's alias lives in the svccontext package.
	return nil
}

// describeProducedNames states p's naming rule for the collision message.
func describeProducedNames(p declProducer) string {
	switch p.kind {
	case "error":
		return "error decls emit `<Name>Err` and `<Name>Body`"
	}
	return "decl emits its name verbatim"
}
