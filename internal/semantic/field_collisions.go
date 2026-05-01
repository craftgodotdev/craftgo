package semantic

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/idents"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// checkFieldNameCollisions emits a warning for each type / error body
// in which two or more DSL field names normalise to the same Go
// identifier under [idents.GoFieldName]. The canonical example:
//
//	type User {
//	    user_id string  // → Go field UserID
//	    userId  string  // → Go field UserID — same name!
//	}
//
// Without intervention, codegen would emit two `UserID` fields and
// the resulting Go file would not compile. The codegen pass in
// [internal/codegen] disambiguates by appending `_2`, `_3`, ...
// suffixes — but that's a silent fix that hides a schema-level
// duplication: BOTH fields keep their DSL spelling on the JSON wire,
// so the API contract carries two distinct keys for what the user
// almost certainly meant as one.
//
// Surfaced as a warning (not an error) so existing projects with
// intentional aliasing keep building; the squiggle in the IDE is
// loud enough to flag accidental cases without breaking CI.
func (a *analyzer) checkFieldNameCollisions(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkOneDeclFieldCollisions(d)
		}
	}
}

// checkOneDeclFieldCollisions inspects a single TypeDecl / ErrorDecl
// body. Other decl shapes have no field collisions to warn about.
func (a *analyzer) checkOneDeclFieldCollisions(d ast.Decl) {
	var (
		parent  string
		members []ast.TypeMember
	)
	switch dd := d.(type) {
	case *ast.TypeDecl:
		parent, members = "type "+dd.Name, dd.Body
	case *ast.ErrorDecl:
		parent, members = "error "+dd.Name, dd.Body
	default:
		return
	}
	a.warnFieldCollisions(parent, members)
}

// warnFieldCollisions runs the dedup over the body's field names and
// emits one diagnostic per collision group, anchored at the first
// duplicate's position so the IDE highlights the offending field
// rather than the (innocent) original.
func (a *analyzer) warnFieldCollisions(parent string, members []ast.TypeMember) {
	var fields []*ast.Field
	for _, m := range members {
		if f, ok := m.(*ast.Field); ok && f.Name != "" {
			fields = append(fields, f)
		}
	}
	if len(fields) < 2 {
		return
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	_, collisions := idents.DedupGoFieldNames(names)
	if len(collisions) == 0 {
		return
	}
	// Index by DSL spelling → field for fast lookup of a position
	// to anchor each diagnostic at.
	byName := map[string]*ast.Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	for _, c := range collisions {
		if len(c.DSLNames) < 2 {
			continue
		}
		firstDSL := c.DSLNames[0]
		first := byName[firstDSL]
		// Emit one diagnostic per DUPLICATE — anchor at each dupe's
		// own position so all colliding fields get a squiggle in the
		// editor, not just the first one in the group. The message
		// names the canonical "winner" so the user sees which DSL
		// spelling keeps the bare Go name vs. which receives the
		// `_N` suffix.
		for rank, dupeName := range c.DSLNames[1:] {
			anchor := byName[dupeName]
			if anchor == nil {
				continue
			}
			d := a.diag(anchor.Pos, anchor.Pos, lexer.SeverityWarning, CodeFieldNameCollision,
				"field name %q collides with %q in %s — both normalise to Go identifier %q; codegen will emit %q to keep the struct compilable, but the JSON wire shape carries BOTH DSL spellings (rename one if this duplication was unintended)",
				dupeName, firstDSL, parent, c.CanonicalGoName, c.ResolvedGoNames[rank+1])
			if first != nil {
				d.Related = related(first.Pos, "first declared here (keeps the canonical Go name)")
			}
		}
	}
}
