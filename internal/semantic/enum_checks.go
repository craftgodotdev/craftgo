package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

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
