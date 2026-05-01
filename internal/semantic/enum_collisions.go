package semantic

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/idents"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// checkEnumValueCollisions emits a warning for each enum whose value
// names normalise to the same Go const name under
// [idents.GoFieldName]. The codegen const-name shape is
// `<EnumName><GoFieldName(value.Name)>`, so two value names that
// produce the same `GoFieldName` collide as a Go duplicate const.
//
// Canonical example:
//
//	enum TaskStatus {
//	    created = "okok"   // → const TaskStatusCreated
//	    Created = "okok1"  // → const TaskStatusCreated  ← collide!
//	}
//
// The codegen pass auto-suffixes duplicates with `_2`, `_3`, ... so
// the package compiles, but the WIRE payload of the two values
// (`"okok"` vs `"okok1"`) stays distinct — a quiet duplication the
// user almost certainly did not intend. Surfaced as warning so
// existing projects with intentional aliasing keep building.
func (a *analyzer) checkEnumValueCollisions(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			if ed, ok := d.(*ast.EnumDecl); ok {
				a.warnEnumValueCollisions(ed)
			}
		}
	}
}

// warnEnumValueCollisions runs the dedup over an enum's value names
// and emits one diagnostic per duplicate, anchored at each duplicate's
// own position so the IDE highlights every offending value.
func (a *analyzer) warnEnumValueCollisions(ed *ast.EnumDecl) {
	if ed == nil || len(ed.Values) < 2 {
		return
	}
	names := make([]string, 0, len(ed.Values))
	for _, v := range ed.Values {
		if v == nil || v.Name == "" {
			continue
		}
		names = append(names, v.Name)
	}
	if len(names) < 2 {
		return
	}
	_, collisions := idents.DedupGoFieldNames(names)
	if len(collisions) == 0 {
		return
	}
	byName := map[string]*ast.EnumValue{}
	for _, v := range ed.Values {
		if v != nil {
			byName[v.Name] = v
		}
	}
	for _, c := range collisions {
		if len(c.DSLNames) < 2 {
			continue
		}
		firstDSL := c.DSLNames[0]
		first := byName[firstDSL]
		for rank, dupeName := range c.DSLNames[1:] {
			anchor := byName[dupeName]
			if anchor == nil {
				continue
			}
			d := a.diag(anchor.Pos, anchor.Pos, lexer.SeverityWarning, CodeEnumValueCollision,
				"enum value %q collides with %q in enum %s — both normalise to Go const %q; codegen will emit %q to keep the package compilable, but the wire payloads stay distinct (rename one if this duplication was unintended)",
				dupeName, firstDSL, ed.Name, ed.Name+c.CanonicalGoName, ed.Name+c.ResolvedGoNames[rank+1])
			if first != nil {
				d.Related = related(first.Pos, "first declared here (keeps the canonical const name)")
			}
		}
	}
}
