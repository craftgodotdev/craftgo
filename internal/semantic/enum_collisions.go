package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkEnumValueCollisions warns when two values of an enum map to one Go
// const name.
func (a *analyzer) checkEnumValueCollisions(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			if ed, ok := d.(*ast.EnumDecl); ok {
				a.warnEnumValueCollisions(ed)
			}
		}
	}
}

// warnEnumValueCollisions reports every colliding value of ed but the first.
func (a *analyzer) warnEnumValueCollisions(ed *ast.EnumDecl) {
	if ed == nil {
		return
	}
	enumVals := ed.EnumValues()
	if len(enumVals) < 2 {
		return
	}
	names := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
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
	for _, v := range enumVals {
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
				"enum value %q collides with %q in enum %s - both normalise to Go const %q; codegen will emit %q to keep the package compilable, but the wire payloads stay distinct (rename one if this duplication was unintended)",
				dupeName, firstDSL, ed.Name, ed.Name+c.CanonicalGoName, ed.Name+c.ResolvedGoNames[rank+1])
			if first != nil {
				d.Related = related(first.Pos, "first declared here (keeps the canonical const name)")
			}
		}
	}
}

// checkEnums rejects an empty enum, mixed value kinds, and a repeated value
// name or literal.
func (a *analyzer) checkEnums() {
	for _, ed := range a.pkg.Enums {
		values := ed.EnumValues()
		if len(values) == 0 {
			a.diag(ed.Pos, ed.Pos, lexer.SeverityError, CodeEnumEmpty,
				"enum %q has no values; OpenAPI emits `enum: []` which is invalid per JSON Schema 2020-12", ed.Name)
			continue
		}
		seenNames := map[string]lexer.Position{}
		seenInts := map[int64]lexer.Position{}
		seenStrs := map[string]lexer.Position{}
		var firstKind ast.EnumValueKind
		var firstKindPos lexer.Position
		first := true
		for _, v := range values {
			if prev, dup := seenNames[v.Name]; dup {
				d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumDuplicateName,
					"duplicate enum value name %q in %q", v.Name, ed.Name)
				d.Related = related(prev, "first declared here")
			} else {
				seenNames[v.Name] = v.Pos
			}
			if first {
				firstKind = v.Kind
				firstKindPos = v.Pos
				first = false
			} else if v.Kind != firstKind {
				d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumMixedTypes,
					"enum %q has mixed value types (must be all bare, all int, or all string)", ed.Name)
				d.Related = related(firstKindPos, "first value declared here")
			}
			switch v.Kind {
			case ast.EnumInt:
				if prev, dup := seenInts[v.IntValue]; dup {
					d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumDuplicateLiteral,
						"duplicate int value %d in enum %q", v.IntValue, ed.Name)
					d.Related = related(prev, "first used here")
				} else {
					seenInts[v.IntValue] = v.Pos
				}
			case ast.EnumString:
				if prev, dup := seenStrs[v.StrValue]; dup {
					d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumDuplicateLiteral,
						"duplicate string value %q in enum %q", v.StrValue, ed.Name)
					d.Related = related(prev, "first used here")
				} else {
					seenStrs[v.StrValue] = v.Pos
				}
			}
		}
	}
}
