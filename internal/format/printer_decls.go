package format

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func (p *Printer) TypeDecl(d *ast.TypeDecl) {
	p.comments(memberStartLine(d.Pos.Line, d.Decorators, 0), d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
	p.line(d.Pos.Line)
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
	p.endCode()
	p.depth++
	p.printTypeBody(d.Body)
	p.depth--
	p.closeBrace(d.EndPos.Line)
}

// closeBrace prints the `}` on source line src.
func (p *Printer) closeBrace(src int) {
	p.line(src)
	p.write("}")
	p.endCode()
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
			prevEnd = memberEndLine(v.Pos.Line, v.Decorators)
		case *ast.Mixin:
			p.blankBetween(prevEnd, v.Pos.Line-len(v.Doc))
			p.comments(v.Pos.Line, v.Doc)
			p.line(v.Pos.Line)
			p.NamedTypeRef(v.Ref)
			p.endCode()
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
		p.blank(start)
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

// memberEndLine returns the last source line of a member on line pos: its
// last decorator's line when that is below pos.
func memberEndLine(pos int, decs []*ast.Decorator) int {
	if n := len(decs); n > 0 && decs[n-1].Pos.Line > pos {
		return decs[n-1].Pos.Line
	}
	return pos
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
// maxType columns. Decorators above or after f join its line unless a comment
// pins them to their own lines.
func (p *Printer) alignedField(f *ast.Field, maxName, maxType int, ts string) {
	start := memberStartLine(f.Pos.Line, f.Decorators, 0)
	p.comments(start, f.Doc)
	decs := f.Decorators
	if lead := leadingChain(decs, f.Pos.Line); len(lead) > 0 && p.chainCommented(lead[0].Pos.Line, f.Pos.Line) {
		p.declDecorators(lead, f.Pos.Line)
		decs, start = decs[len(lead):], f.Pos.Line
	}
	p.line(start)
	p.write(f.Name)
	p.write(strings.Repeat(" ", maxName-len(f.Name)+1))
	p.write(ts)
	p.trailingDecorators(decs, f.Pos.Line, strings.Repeat(" ", maxType-len(ts)+1))
	p.endCode()
}

// inlineDecorators writes decs on the current line, separated by spaces.
func (p *Printer) inlineDecorators(decs []*ast.Decorator) {
	for i, d := range decs {
		if i > 0 {
			p.write(" ")
		}
		p.Decorator(d)
	}
}

func (p *Printer) EnumDecl(d *ast.EnumDecl) {
	p.comments(memberStartLine(d.Pos.Line, d.Decorators, 0), d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
	p.line(d.Pos.Line)
	p.write("enum ")
	p.write(d.Name)
	p.write(" {")
	p.endCode()
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
			prevEnd = memberEndLine(v.Pos.Line, v.Decorators)
		case *ast.FreeComment:
			p.blankBetween(prevEnd, v.Pos.Line)
			p.printFreeComment(v)
			prevEnd = v.Pos.Line + len(v.Text) - 1
		}
	}
	p.depth--
	p.closeBrace(d.EndPos.Line)
}

func (p *Printer) EnumValue(v *ast.EnumValue, maxName int) {
	p.comments(v.Pos.Line, v.Doc)
	p.line(v.Pos.Line)
	p.write(v.Name)
	switch v.Kind {
	case ast.EnumInt:
		p.write(strings.Repeat(" ", maxName-len(v.Name)+1))
		p.write("= ")
		p.write(strconv.FormatInt(v.IntValue, 10))
	case ast.EnumString:
		p.write(strings.Repeat(" ", maxName-len(v.Name)+1))
		p.write("= ")
		p.write(v.StrText)
	}
	p.trailingDecorators(v.Decorators, v.Pos.Line, " ")
	p.endCode()
}

func (p *Printer) ErrorDecl(d *ast.ErrorDecl) {
	p.comments(memberStartLine(d.Pos.Line, d.Decorators, 0), d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
	p.line(d.Pos.Line)
	p.write("error ")
	p.write(d.Category)
	p.write(" ")
	p.write(d.Name)
	if !d.HasBody {
		p.endCode()
		return
	}
	p.write(" {")
	p.endCode()
	p.depth++
	p.printTypeBody(d.Body)
	p.depth--
	p.closeBrace(d.EndPos.Line)
}

// ScalarDecl prints `scalar Name primitive` with its decorators on the line;
// a chain above the keyword keeps its lines when a comment pins it.
func (p *Printer) ScalarDecl(d *ast.ScalarDecl) {
	start := memberStartLine(d.Pos.Line, d.Decorators, 0)
	p.comments(start, d.Doc)
	decs := d.Decorators
	if lead := leadingChain(decs, d.Pos.Line); len(lead) > 0 && p.chainCommented(lead[0].Pos.Line, d.Pos.Line) {
		p.declDecorators(lead, d.Pos.Line)
		decs, start = decs[len(lead):], d.Pos.Line
	}
	p.line(start)
	p.write("scalar ")
	p.write(d.Name)
	p.write(" ")
	p.write(d.Primitive)
	if len(decs) > 0 {
		p.write(" ")
		p.inlineDecorators(decs)
	}
	p.endCode()
}

func (p *Printer) MiddlewareDecl(d *ast.MiddlewareDecl) {
	p.comments(memberStartLine(d.Pos.Line, d.Decorators, 0), d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
	p.line(d.Pos.Line)
	p.write("middleware ")
	p.write(d.Name)
	p.endCode()
}

func (p *Printer) ServiceDecl(d *ast.ServiceDecl) {
	p.comments(memberStartLine(d.Pos.Line, d.Decorators, 0), d.Doc)
	p.declDecorators(d.Decorators, d.Pos.Line)
	p.line(d.Pos.Line)
	if d.Extend {
		p.write("extend service ")
	} else {
		p.write("service ")
	}
	p.write(d.Name)
	p.write(" {")
	p.endCode()
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
	p.closeBrace(d.EndPos.Line)
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
	p.blank(start)
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

// memberBody prints a method or event body closed on source line end: clauses
// and free comments in source order, then the closing brace; an empty body
// prints as `{}`.
func (p *Printer) memberBody(clauses []memberClause, comments []*ast.FreeComment, end int) {
	if len(clauses) == 0 && len(comments) == 0 {
		p.write(" {}")
		p.endCode()
		return
	}
	p.write(" {")
	p.endCode()
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
		p.line(cl.line)
		p.write(cl.keyword)
		p.NamedTypeRef(cl.ref)
		if cl.array {
			p.write("[]")
		}
		p.endCode()
		prevEnd = cl.line
	}
	flushBefore(0)
	p.depth--
	p.closeBrace(end)
}

func (p *Printer) Method(m *ast.Method) {
	p.comments(memberStartLine(m.Pos.Line, m.Decorators, 0), m.Doc)
	p.declDecorators(m.Decorators, m.Pos.Line)
	p.line(m.Pos.Line)
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
	p.memberBody(clauses, m.BodyComments, m.EndPos.Line)
}

func (p *Printer) EventDecl(e *ast.EventDecl) {
	p.comments(memberStartLine(e.Pos.Line, e.Decorators, 0), e.Doc)
	p.declDecorators(e.Decorators, e.Pos.Line)
	p.line(e.Pos.Line)
	p.write("event ")
	p.write(e.Name)
	var clauses []memberClause
	if e.Payload != nil {
		clauses = append(clauses, memberClause{keyword: "payload ", line: e.Payload.Pos.Line, ref: e.Payload.Type, array: e.Payload.Array})
	}
	p.memberBody(clauses, e.BodyComments, e.EndPos.Line)
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
