package format

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func (p *Printer) TypeDecl(d *ast.TypeDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
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

// writeTrailing writes the trailing comment td after a closing brace.
func (p *Printer) writeTrailing(td []string) {
	if len(td) == 0 {
		return
	}
	p.write("  // ")
	p.write(strings.Join(td, " "))
}

// writeSourceTrailing writes the trailing comment on line, unless a decorator on
// that line already wrote it as its TrailingDoc (decoratorCarriesTrailing).
func (p *Printer) writeSourceTrailing(line int, decoratorCarriesTrailing bool) {
	if decoratorCarriesTrailing {
		return
	}
	if c, ok := p.trailing[line]; ok {
		p.write(" // ")
		p.write(c)
	}
}

// printTypeBody prints body with fields aligned in name and type columns,
// keeping one blank line wherever the source had blank lines.
func (p *Printer) printTypeBody(body []ast.TypeMember) {
	maxName, maxType := 0, 0
	typeStr := make(map[*ast.Field]string, len(body))
	for _, m := range body {
		if f, ok := m.(*ast.Field); ok {
			if n := len(f.Name); n > maxName {
				maxName = n
			}
			ts := p.typeRefString(f.Type)
			// @default makes a field optional, so its type gains `?`; a @path
			// field is exempt because an optional @path is a semantic error.
			if f.Type != nil && !f.Type.Optional && fieldHasDefault(f) && !ast.HasDecorator(f.Decorators, "path") {
				ts += "?"
			}
			typeStr[f] = ts
			if n := len(ts); n > maxType {
				maxType = n
			}
		}
	}
	prevEnd := 0
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			p.blankBetween(prevEnd, memberStartLine(v.Pos.Line, v.Decorators, len(v.Doc)))
			p.alignedField(v, maxName, maxType, typeStr[v])
			prevEnd = v.Pos.Line
		case *ast.Mixin:
			p.blankBetween(prevEnd, v.Pos.Line-len(v.Doc))
			p.printLeadingDoc(v.Doc, v.Pos.Line)
			p.indent()
			p.NamedTypeRef(v.Ref)
			p.nl()
			prevEnd = v.Pos.Line
		case *ast.FreeComment:
			p.blankBetween(prevEnd, v.Pos.Line)
			p.printFreeComment(v)
			prevEnd = v.Pos.Line + len(v.Text) - 1
		}
	}
}

// blankBetween writes one blank line if the source had any between line prevEnd
// and line start; a zero line (first member, or no position) writes none.
func (p *Printer) blankBetween(prevEnd, start int) {
	if prevEnd > 0 && start > prevEnd+1 {
		p.nl()
	}
}

// memberStartLine returns the first source line of a member: its first
// decorator's line when that is above pos, less the doc lines.
func memberStartLine(pos int, decs []*ast.Decorator, docLen int) int {
	if len(decs) > 0 && decs[0].Pos.Line > 0 && decs[0].Pos.Line < pos {
		pos = decs[0].Pos.Line
	}
	return pos - docLen
}

func fieldHasDefault(f *ast.Field) bool {
	return f != nil && ast.HasDecorator(f.Decorators, "default")
}

func (p *Printer) typeRefString(t *ast.TypeRef) string {
	var buf bytes.Buffer
	sub := &Printer{w: &buf}
	sub.TypeRef(t)
	return buf.String()
}

// alignedField prints f's doc, then f on one line padded to the maxName and
// maxType columns.
func (p *Printer) alignedField(f *ast.Field, maxName, maxType int, ts string) {
	p.printFieldDoc(f)
	p.indent()
	p.write(f.Name)
	p.write(strings.Repeat(" ", maxName-len(f.Name)+1))
	p.write(ts)
	var decTrailing []string
	if len(f.Decorators) > 0 {
		p.write(strings.Repeat(" ", maxType-len(ts)+1))
		for i, dec := range f.Decorators {
			if i > 0 {
				p.write(" ")
			}
			// Trailing comments move to the line end: mid-line, one would
			// comment out the decorators after it.
			p.decoratorCore(dec)
			if dec.TrailingDoc != "" {
				decTrailing = append(decTrailing, dec.TrailingDoc)
			}
		}
	}
	if len(decTrailing) > 0 {
		p.write("  // ")
		p.write(strings.Join(decTrailing, " "))
	} else {
		p.writeSourceTrailing(f.Pos.Line, false)
	}
	p.nl()
}

func (p *Printer) printFieldDoc(f *ast.Field) {
	p.printLeadingDoc(f.Doc, f.Pos.Line)
}

// printLeadingDoc prints doc, dropping each line whose source line (assumed to
// be directly above posLine) has a trailing comment.
func (p *Printer) printLeadingDoc(doc []string, posLine int) {
	if len(doc) == 0 {
		return
	}
	if p.trailing == nil {
		p.Doc(doc)
		return
	}
	keep := make([]string, 0, len(doc))
	for i, line := range doc {
		srcLine := posLine - len(doc) + i
		if _, hit := p.trailing[srcLine]; hit {
			continue
		}
		keep = append(keep, line)
	}
	p.Doc(keep)
}

func (p *Printer) EnumDecl(d *ast.EnumDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
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
	prevEnd := 0
	for _, m := range d.Members {
		switch v := m.(type) {
		case *ast.EnumValue:
			p.blankBetween(prevEnd, v.Pos.Line-len(v.Doc))
			p.EnumValue(v, maxName)
			prevEnd = v.Pos.Line
		case *ast.FreeComment:
			p.blankBetween(prevEnd, v.Pos.Line)
			p.printFreeComment(v)
			prevEnd = v.Pos.Line + len(v.Text) - 1
		}
	}
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(d.TrailingDoc)
	p.nl()
}

func (p *Printer) EnumValue(v *ast.EnumValue, maxName int) {
	p.printLeadingDoc(v.Doc, v.Pos.Line)
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
	p.declDecorators(d.Decorators, d.Pos.Line)
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
	p.declDecorators(d.Decorators, d.Pos.Line)
	p.indent()
	p.write("middleware ")
	p.write(d.Name)
	p.nl()
}

func (p *Printer) ServiceDecl(d *ast.ServiceDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
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
	printedAny := false
	prevEnd := 0
	for _, member := range d.Members {
		switch v := member.(type) {
		case *ast.Method:
			p.serviceMemberGap(printedAny, prevEnd, memberStartLine(v.Pos.Line, v.Decorators, len(v.Doc)))
			p.Method(v)
			printedAny, prevEnd = true, endOrStart(v.EndPos.Line, v.Pos.Line)
		case *ast.FreeComment:
			p.serviceMemberGap(printedAny, prevEnd, v.Pos.Line)
			p.printFreeComment(v)
			printedAny = true
			prevEnd = v.Pos.Line + len(v.Text) - 1
		}
	}
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(d.TrailingDoc)
	p.nl()
}

// serviceMemberGap separates service members as the source did, or by one
// blank line when positions are missing.
func (p *Printer) serviceMemberGap(printedAny bool, prevEnd, start int) {
	if !printedAny {
		return
	}
	if prevEnd > 0 && start > 0 {
		p.blankBetween(prevEnd, start)
		return
	}
	p.nl()
}

// endOrStart returns end, or start when end is 0 (no closing-brace position).
func endOrStart(end, start int) int {
	if end == 0 {
		return start
	}
	return end
}

// memberClause is one `<keyword> <type>` line of a method or event body;
// keyword carries the padding that aligns the type column.
type memberClause struct {
	keyword string
	line    int
	ref     *ast.NamedTypeRef
	array   bool // an event payload's `[]` suffix
}

// memberBody prints a method or event body: clauses and free comments in
// source order, then the closing brace; an empty body prints as `{}`.
func (p *Printer) memberBody(clauses []memberClause, comments []*ast.FreeComment, trailing []string) {
	if len(clauses) == 0 && len(comments) == 0 {
		p.write(" {}")
		p.writeTrailing(trailing)
		p.nl()
		return
	}
	p.write(" {")
	p.nl()
	p.depth++
	prevEnd := 0
	flushBefore := func(line int) {
		for len(comments) > 0 && (line == 0 || comments[0].Pos.Line < line) {
			c := comments[0]
			p.blankBetween(prevEnd, c.Pos.Line)
			p.printFreeComment(c)
			prevEnd = c.Pos.Line + len(c.Text) - 1
			comments = comments[1:]
		}
	}
	for _, cl := range clauses {
		flushBefore(cl.line)
		p.blankBetween(prevEnd, cl.line)
		p.indent()
		p.write(cl.keyword)
		p.NamedTypeRef(cl.ref)
		if cl.array {
			p.write("[]")
		}
		p.writeSourceTrailing(cl.line, false)
		p.nl()
		prevEnd = cl.line
	}
	flushBefore(0)
	p.depth--
	p.indent()
	p.write("}")
	p.writeTrailing(trailing)
	p.nl()
}

func (p *Printer) Method(m *ast.Method) {
	p.Doc(m.Doc)
	p.declDecorators(m.Decorators, m.Pos.Line)
	p.indent()
	p.write(m.Verb)
	p.write(" ")
	p.write(m.Name)
	if m.Path != nil {
		p.write(" ")
		p.Path(m.Path)
	}
	var clauses []memberClause
	if m.Request != nil {
		clauses = append(clauses, memberClause{keyword: "request  ", line: m.Request.Pos.Line, ref: m.Request})
	}
	if m.Response != nil {
		clauses = append(clauses, memberClause{keyword: "response ", line: m.Response.Pos.Line, ref: m.Response.Type})
	}
	p.memberBody(clauses, m.BodyComments, m.TrailingDoc)
}

func (p *Printer) EventDecl(e *ast.EventDecl) {
	p.Doc(e.Doc)
	p.declDecorators(e.Decorators, e.Pos.Line)
	p.indent()
	p.write("event ")
	p.write(e.Name)
	var clauses []memberClause
	if e.Payload != nil {
		clauses = append(clauses, memberClause{keyword: "payload ", line: e.Payload.Pos.Line, ref: e.Payload.Type, array: e.Payload.Array})
	}
	p.memberBody(clauses, e.BodyComments, e.TrailingDoc)
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

// declDecorators prints decs one per line with the comments written inside the
// chain; keywordLine is the source line of the keyword after the chain.
func (p *Printer) declDecorators(decs []*ast.Decorator, keywordLine int) {
	for _, d := range decs {
		if block, ok := p.interDec[d.Pos.Line]; ok {
			p.Doc(block)
		}
		p.indent()
		p.Decorator(d)
		p.nl()
	}
	if block, ok := p.interDec[keywordLine]; ok {
		p.Doc(block)
	}
}
