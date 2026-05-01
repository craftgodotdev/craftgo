package semantic

import (
	"sort"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// declProducer is one (decl → Go-name) pair captured during the
// collision pass. Multiple declProducers per Go-name in the same
// package signal a hard error.
type declProducer struct {
	dslName string
	kind    string // "type" | "error" | "enum" | "scalar" | "middleware"
	pos     lexer.Position
	emitted string // the specific Go name this declProducer emits
}

// checkDeclGoNameCollisions emits an ERROR for any pair of top-level
// declarations in the same package whose codegen output produces the
// same Go identifier. The codegen mangling rules involved:
//
//   - TypeDecl X → emits `type X struct{...}`
//   - EnumDecl X → emits `type X <kind>` + constants
//   - ScalarDecl X → emits `type X = <underlying>`
//   - ErrorDecl X → emits `type XErr struct{...}` and `type XBody
//     struct{...}` (the latter only when the error body is non-empty)
//   - MiddlewareDecl X → emits `type XMiddleware = server.Middleware`
//   - ServiceDecl → no top-level type names (handler/route packages
//     are namespaced separately)
//
// A user writing `type FooErr` AND `error Foo { ... }` would get
// two `type FooErr struct{...}` declarations in the generated
// types package; Go would refuse to compile. Auto-suffixing decls
// is not safe (the user references the name from their own logic),
// so this surfaces at error severity to fail the build early at
// the design layer.
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
		// Anchor the diagnostic at the SECOND declProducer (the dupe)
		// so the IDE squiggle points at the offending later decl
		// while the message still names the first as "first
		// declared at <Pos>". Stable order across runs.
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].pos.Line != ps[j].pos.Line {
				return ps[i].pos.Line < ps[j].pos.Line
			}
			return ps[i].pos.Column < ps[j].pos.Column
		})
		first := ps[0]
		for _, dupe := range ps[1:] {
			d := a.diag(dupe.pos, dupe.pos, lexer.SeverityError, CodeDeclGoNameCollision,
				"%s %q would emit Go identifier %q which already comes from %s %q (%s) — codegen cannot disambiguate decl names; rename one to fix",
				dupe.kind, dupe.dslName, goName, first.kind, first.dslName, describeProducedNames(first))
			d.Related = related(first.pos, "first emitted here")
		}
	}
}

// producedName ties a generated Go identifier back to the DSL decl
// that emits it. Used by [checkDeclGoNameCollisions] only.
type producedName struct {
	goName  string
	dslName string
	kind    string // "type" | "error" | "enum" | "scalar" | "middleware"
	pos     lexer.Position
}

// goNamesProducedBy returns the top-level Go identifiers a decl
// causes the codegen pass to emit. Empty when the decl produces
// nothing at the types-package scope (services live in their own
// package). Mirrors the actual codegen behaviour in
// [internal/codegen/{types,enums,errors,middleware}.go]; whenever
// one of those changes the declProducer set, update this function in
// lock-step.
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
		out := []producedName{{goName: dd.Name + "Err", dslName: dd.Name, kind: "error", pos: dd.Pos}}
		if len(dd.Body) > 0 {
			out = append(out, producedName{goName: dd.Name + "Body", dslName: dd.Name, kind: "error", pos: dd.Pos})
		}
		return out
	}
	// MiddlewareDecl is intentionally excluded — its alias lives in
	// the svccontext package, NOT the types package, so its
	// `<Name>Middleware` emit cannot collide with anything checked
	// here. Middleware-vs-middleware uniqueness is handled via the
	// dedicated `seenMW` map in [collectDecls].
	return nil
}

// describeProducedNames renders a short suffix listing every Go
// name a decl emits. Used inside diagnostic messages so users see
// which mangled output collided when a single DSL decl produces
// more than one name (e.g. ErrorDecl emits both `XErr` and `XBody`).
func describeProducedNames(p declProducer) string {
	switch p.kind {
	case "error":
		return "error decls emit `<Name>Err` and `<Name>Body`"
	case "middleware":
		return "middleware decls emit `<Name>Middleware`"
	}
	return "decl emits its name verbatim"
}

// (helper rendering kept separate for readability — joined on demand
// when the declProducer list becomes a subject of the diagnostic.)
var _ = strings.Join
