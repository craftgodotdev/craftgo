// Per-declaration print methods + type-ref rendering.
package format

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func (p *Printer) TypeDecl(d *ast.TypeDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators)
	p.indent()
	p.write("type ")
	p.write(d.Name)
	if len(d.TypeParams) > 0 {
		p.write("<")
		for i, tp := range d.TypeParams {
			if i > 0 {
				p.write(", ")
			}
			p.write(tp)
		}
		p.write(">")
	}
	p.write(" {")
	p.nl()
	p.depth++
	p.printTypeBody(d.Body)
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(d.TrailingDoc)
	p.nl()
}

// writeTrailing emits a `// note` after the just-written close brace
// when the decl carries a trailing doc captured by the parser. Multiple
// lines are joined with a space — multi-line trailing comments on a
// single closing line are rare; the AST exposes []string for symmetry
// with Doc rather than because we expect more than one entry.
func (p *Printer) writeTrailing(td []string) {
	if len(td) == 0 {
		return
	}
	p.write("  // ")
	p.write(strings.Join(td, " "))
}

// writeSourceTrailing emits the trailing `// comment` captured from the
// source line, but ONLY when no decorator on the same line already
// emitted it via its own TrailingDoc. The lexer attaches a line-trailing
// comment to BOTH the last decorator's TrailingDoc AND the source-scan
// map (p.trailing, built by scanTrailingComments), so any decl that
// prints its decorators must suppress the source-scan copy here.
// Otherwise the same `// note` is written twice and DOUBLES on every
// subsequent format pass — `craftgo fmt` becomes non-idempotent and
// format-on-save balloons the comment (1→2→4→8…). All three decorated
// print sites (fields, enum values, scalars) route through here so the
// guard can never drift out of sync again.
func (p *Printer) writeSourceTrailing(line int, decoratorCarriesTrailing bool) {
	if decoratorCarriesTrailing {
		return
	}
	if c, ok := p.trailing[line]; ok {
		p.write(" // ")
		p.write(c)
	}
}

// printTypeBody prints a slice of TypeMember with column-aligned fields.
// Mixins are printed un-aligned on their own lines. Doc lines that the
// parser misattributed (i.e. trailing comments on the previous field's
// source line) are filtered out and re-emitted as trailing comments on
// the correct field - this avoids losing them and avoids printing them
// in the wrong place.
func (p *Printer) printTypeBody(body []ast.TypeMember) {
	maxName, maxType := 0, 0
	typeStr := make(map[*ast.Field]string, len(body))
	for _, m := range body {
		if f, ok := m.(*ast.Field); ok {
			if n := len(f.Name); n > maxName {
				maxName = n
			}
			ts := p.typeRefString(f.Type)
			// Auto-fix: a field carrying `@default(...)` is conceptually
			// optional - the default fires when the value is absent. If
			// the author hasn't typed `?`, the formatter adds it on save
			// so the source matches the runtime contract (and the
			// `decorator/default-needs-optional` warning clears).
			if f.Type != nil && !f.Type.Optional && fieldHasDefault(f) {
				ts += "?"
			}
			typeStr[f] = ts
			if n := len(ts); n > maxType {
				maxType = n
			}
		}
	}
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			p.alignedField(v, maxName, maxType, typeStr[v])
		case *ast.Mixin:
			p.indent()
			p.NamedTypeRef(v.Ref)
			p.nl()
		case *ast.FreeComment:
			p.printFreeComment(v)
		}
	}
}

// fieldHasDefault reports whether f carries a `@default(...)` decorator.
// Used by the type-body printer to auto-add `?` to the rendered type
// when the author hasn't marked the field optional, since `@default`
// only fires when the value is absent.
func fieldHasDefault(f *ast.Field) bool {
	if f == nil {
		return false
	}
	for _, d := range f.Decorators {
		if d != nil && d.Name == "default" {
			return true
		}
	}
	return false
}

// typeRefString renders a TypeRef to a string by reusing the printer.
func (p *Printer) typeRefString(t *ast.TypeRef) string {
	var buf bytes.Buffer
	sub := &Printer{w: &buf}
	sub.TypeRef(t)
	return buf.String()
}

// alignedField prints a single Field padded to share columns with its
// siblings. Decorators and the optional trailing comment follow the
// padded type column.
func (p *Printer) alignedField(f *ast.Field, maxName, maxType int, ts string) {
	p.printFieldDoc(f)
	p.indent()
	p.write(f.Name)
	p.write(strings.Repeat(" ", maxName-len(f.Name)+1))
	p.write(ts)
	decoratorCarriesTrailing := false
	if len(f.Decorators) > 0 {
		p.write(strings.Repeat(" ", maxType-len(ts)+1))
		for i, dec := range f.Decorators {
			if i > 0 {
				p.write(" ")
			}
			p.Decorator(dec)
			if dec.TrailingDoc != "" {
				decoratorCarriesTrailing = true
			}
		}
	}
	p.writeSourceTrailing(f.Pos.Line, decoratorCarriesTrailing)
	p.nl()
}

// printFieldDoc emits the field's leading doc comments, filtering out
// any line the lexer actually picked up from the previous field's
// trailing `//`. The lexer attaches every contiguous `//` block above
// a token to that token's Doc - so when a field above ends with a
// trailing comment AND the field below has its own leading block, the
// trailing line ends up at the FRONT of the field-below's Doc. Without
// this filter the trailing would be re-emitted as a leading comment on
// the wrong field.
//
// The preceding field's printer pulls its trailing text from
// p.trailing (built in [scanTrailingComments]), so dropping the
// misattributed entry here does not lose information — it lands on
// the correct field by way of the trailing map.
func (p *Printer) printFieldDoc(f *ast.Field) {
	if len(f.Doc) == 0 {
		return
	}
	if p.trailing == nil {
		p.Doc(f.Doc)
		return
	}
	docCount := len(f.Doc)
	keep := make([]string, 0, docCount)
	for i, line := range f.Doc {
		// Doc lines occupy the |Doc|-many source lines immediately
		// above f. Index i maps back to source line
		// f.Pos.Line - docCount + i.
		srcLine := f.Pos.Line - docCount + i
		if _, hit := p.trailing[srcLine]; hit {
			continue
		}
		keep = append(keep, line)
	}
	p.Doc(keep)
}

func (p *Printer) EnumDecl(d *ast.EnumDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators)
	p.indent()
	p.write("enum ")
	p.write(d.Name)
	p.write(" {")
	p.nl()
	p.depth++
	maxName := 0
	for _, m := range d.Members {
		if v, ok := m.(*ast.EnumValue); ok {
			if v.Kind != ast.EnumBare && len(v.Name) > maxName {
				maxName = len(v.Name)
			}
		}
	}
	for _, m := range d.Members {
		switch v := m.(type) {
		case *ast.EnumValue:
			p.EnumValue(v, maxName)
		case *ast.FreeComment:
			p.printFreeComment(v)
		}
	}
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(d.TrailingDoc)
	p.nl()
}

// printFreeComment renders a free-floating comment block at the current
// indentation depth. Each line gets the canonical `// ` prefix.

func (p *Printer) EnumValue(v *ast.EnumValue, maxName int) {
	p.indent()
	p.write(v.Name)
	switch v.Kind {
	case ast.EnumInt:
		p.write(strings.Repeat(" ", maxName-len(v.Name)+1))
		p.write("= ")
		p.write(strconv.FormatInt(v.IntValue, 10))
	case ast.EnumString:
		p.write(strings.Repeat(" ", maxName-len(v.Name)+1))
		p.write("= ")
		p.write(strconv.Quote(v.StrValue))
	}
	decoratorCarriesTrailing := false
	for _, dec := range v.Decorators {
		p.write(" ")
		p.Decorator(dec)
		if dec.TrailingDoc != "" {
			decoratorCarriesTrailing = true
		}
	}
	p.writeSourceTrailing(v.Pos.Line, decoratorCarriesTrailing)
	p.nl()
}

func (p *Printer) ErrorDecl(d *ast.ErrorDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators)
	p.indent()
	p.write("error ")
	p.write(d.Category)
	p.write(" ")
	p.write(d.Name)
	if !d.HasBody {
		p.nl()
		return
	}
	p.write(" {")
	p.nl()
	p.depth++
	p.printTypeBody(d.Body)
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(d.TrailingDoc)
	p.nl()
}

func (p *Printer) ScalarDecl(d *ast.ScalarDecl) {
	p.Doc(d.Doc)
	p.indent()
	p.write("scalar ")
	p.write(d.Name)
	p.write(" ")
	p.write(d.Primitive)
	decoratorCarriesTrailing := false
	for _, dec := range d.Decorators {
		p.write(" ")
		p.Decorator(dec)
		if dec.TrailingDoc != "" {
			decoratorCarriesTrailing = true
		}
	}
	p.writeSourceTrailing(d.Pos.Line, decoratorCarriesTrailing)
	p.nl()
}

func (p *Printer) MiddlewareDecl(d *ast.MiddlewareDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators)
	p.indent()
	p.write("middleware ")
	p.write(d.Name)
	p.nl()
}

func (p *Printer) ServiceDecl(d *ast.ServiceDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators)
	p.indent()
	if d.Extend {
		p.write("extend service ")
	} else {
		p.write("service ")
	}
	p.write(d.Name)
	p.write(" {")
	p.nl()
	p.depth++
	first := true
	for _, member := range d.Members {
		switch v := member.(type) {
		case *ast.Method:
			if !first {
				p.nl()
			}
			p.Method(v)
			first = false
		case *ast.FreeComment:
			if !first {
				p.nl()
			}
			p.printFreeComment(v)
			first = false
		}
	}
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(d.TrailingDoc)
	p.nl()
}

func (p *Printer) Method(m *ast.Method) {
	p.Doc(m.Doc)
	p.declDecorators(m.Decorators)
	p.indent()
	p.write(m.Verb)
	p.write(" ")
	p.write(m.Name)
	if m.Path != nil {
		p.write(" ")
		p.Path(m.Path)
	}
	if m.Request == nil && m.Response == nil {
		// The grammar always wraps the method with `{ ... }` even
		// when both sides are absent (e.g. a `@passthrough` method),
		// so emit an empty body literal to keep round-trip parity.
		p.write(" {}")
		p.nl()
		return
	}
	p.write(" {")
	p.nl()
	p.depth++
	if m.Request != nil {
		p.indent()
		p.write("request  ")
		p.NamedTypeRef(m.Request)
		p.nl()
	}
	if m.Response != nil {
		p.indent()
		p.write("response ")
		p.NamedTypeRef(m.Response.Type)
		p.nl()
	}
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(m.TrailingDoc)
	p.nl()
}

func (p *Printer) Path(path *ast.Path) {
	p.write("/")
	first := true
	for _, seg := range path.Segments {
		if seg.Param {
			if !first {
				p.write("/")
			}
			p.write("{")
			p.write(seg.Literal)
			p.write("}")
			first = false
		} else if seg.Literal != "" {
			if !first {
				p.write("/")
			}
			p.write(seg.Literal)
			first = false
		}
	}
}

func (p *Printer) TypeRef(t *ast.TypeRef) {
	if t.Map != nil {
		p.write("map<")
		p.TypeRef(t.Map.Key)
		p.write(", ")
		p.TypeRef(t.Map.Value)
		p.write(">")
	} else if t.Named != nil {
		p.NamedTypeRef(t.Named)
	}
	for i := 0; i < t.ArrayDepth; i++ {
		p.write("[]")
	}
	if t.Optional {
		p.write("?")
	}
}

func (p *Printer) NamedTypeRef(n *ast.NamedTypeRef) {
	p.write(n.Name.String())
	if len(n.Args) > 0 {
		p.write("<")
		for i, a := range n.Args {
			if i > 0 {
				p.write(", ")
			}
			p.TypeRef(a)
		}
		p.write(">")
	}
}

func (p *Printer) declDecorators(decs []*ast.Decorator) {
	for _, d := range decs {
		p.indent()
		p.Decorator(d)
		p.nl()
	}
}
