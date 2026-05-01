// Package format renders an [ast.File] back to canonical CraftGo source.
//
// The printer is round-trip safe for every well-formed input: parsing the
// printer's output produces an AST equal to its input, and a second
// formatting pass is a no-op (idempotency). Trailing inline comments
// (`// note` on the same line as a field) are preserved by reading them
// straight from the source buffer rather than relying on the AST - the
// AST's Doc field captures only leading runs of `//`.
//
// Two entry points are provided:
//
//   - [Format] takes a source buffer, parses it, and returns formatted text
//     plus any diagnostics. Suitable for `craftgo fmt` and editor integration.
//   - [Print] takes an [ast.File] and writes formatted text to an [io.Writer].
//     Suitable for callers that already have an AST in hand. Trailing
//     comments cannot be recovered through this entry point because there
//     is no source buffer to scan.
//
// Output conventions:
//
//   - Tabs for indentation (Go-ecosystem convention; editors render the
//     visual width via their own settings).
//   - Field rows inside a type body are column-aligned by name and type
//     so decorator chains line up - this is the single most-read shape
//     in any service definition and aligning it pays for itself.
//   - The `request` / `response` lines inside a method body share a
//     two-space alignment so the type column matches.
package format

import (
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
	"github.com/dropship-dev/craftgo/internal/parser"
)

// Format parses src (filename used only for diagnostics) and returns the
// canonical-formatted text alongside any parser diagnostics. The returned
// text is always non-empty because the parser is error-tolerant - callers
// that want to reject formatting on errors should check len(diags) == 0.
func Format(filename, src string) (string, []lexer.Diagnostic) {
	p := parser.New(filename, src)
	f := p.Parse()
	var buf bytes.Buffer
	pr := &Printer{
		w:        &buf,
		trailing: scanTrailingComments(src),
		loose:    scanLooseComments(src),
	}
	pr.File(f)
	return buf.String(), p.Diagnostics()
}

// Print writes a canonical render of f to w. Trailing comments are not
// recovered via this entry point - callers that want them must use
// [Format] instead, which has access to the source buffer.
func Print(w io.Writer, f *ast.File) error {
	pr := &Printer{w: w}
	pr.File(f)
	return pr.err
}

// Printer is the internal state for one render pass. The zero value is
// useful as long as w is set; trailing is optional and only populated when
// the source buffer was available (i.e. callers that came in via [Format]).
type Printer struct {
	w     io.Writer
	err   error
	depth int
	// trailing maps 1-indexed source line numbers to the text of any
	// `// note` comment found on that line after non-whitespace code.
	// A line with no trailing comment is absent from the map.
	trailing map[int]string
	// loose holds free-floating `//` blocks that the lexer dropped
	// because a blank line separated them from the next token. The
	// key is the 1-indexed source line of the next code construct
	// (top-level decl) the block precedes; values are the
	// already-stripped comment text per line. Multiple consecutive
	// loose blocks targeting the same anchor are merged with a
	// blank entry between them.
	loose map[int][]string
}

func (p *Printer) write(s string) {
	if p.err != nil {
		return
	}
	_, p.err = io.WriteString(p.w, s)
}

func (p *Printer) indent() {
	for i := 0; i < p.depth; i++ {
		p.write("\t")
	}
}

func (p *Printer) nl() { p.write("\n") }

// File renders the entire source file: file-level decorators, the package
// line, imports, and every top-level declaration in source order. A blank
// line separates each major section so the output reads like the canonical
// hand-written form.
func (p *Printer) File(f *ast.File) {
	p.Doc(f.LeadingDoc)
	for _, d := range f.Decorators {
		p.Decorator(d)
		p.nl()
	}
	if f.Package != nil {
		p.Doc(f.Package.Doc)
		p.write("package ")
		p.write(f.Package.Name)
		p.nl()
	}
	if len(f.Imports) > 0 {
		p.nl()
		for _, imp := range f.Imports {
			p.Import(imp)
			p.nl()
		}
	}
	for _, d := range f.Decls {
		p.nl()
		if loose, ok := p.loose[declFirstSourceLine(d)]; ok {
			p.Doc(loose)
			p.nl()
		}
		p.Decl(d)
	}
}

// declFirstSourceLine returns the 1-indexed source line where this
// declaration first appears - the line of its first decorator if it has
// any, otherwise the line of the keyword itself. Used as the anchor key
// for loose comment lookup so a `// section header` block above a
// decorated type lands above the decorator chain, not between decorators
// and the keyword.
func declFirstSourceLine(d ast.Decl) int {
	var decs []*ast.Decorator
	switch v := d.(type) {
	case *ast.TypeDecl:
		decs = v.Decorators
	case *ast.EnumDecl:
		decs = v.Decorators
	case *ast.ErrorDecl:
		decs = v.Decorators
	case *ast.ScalarDecl:
		decs = v.Decorators
	case *ast.MiddlewareDecl:
		decs = v.Decorators
	case *ast.ServiceDecl:
		decs = v.Decorators
	}
	if len(decs) > 0 {
		return decs[0].Pos.Line
	}
	return d.DeclPos().Line
}

func (p *Printer) Import(imp *ast.Import) {
	p.write("import ")
	if imp.Alias != "" {
		p.write(imp.Alias)
		p.write(" ")
	}
	p.write(strconv.Quote(imp.Path))
}

// Decl dispatches to the concrete printer for each top-level declaration.
func (p *Printer) Decl(d ast.Decl) {
	switch v := d.(type) {
	case *ast.TypeDecl:
		p.TypeDecl(v)
	case *ast.EnumDecl:
		p.EnumDecl(v)
	case *ast.ErrorDecl:
		p.ErrorDecl(v)
	case *ast.ScalarDecl:
		p.ScalarDecl(v)
	case *ast.MiddlewareDecl:
		p.MiddlewareDecl(v)
	case *ast.ServiceDecl:
		p.ServiceDecl(v)
	}
}

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
	p.nl()
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
		}
	}
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
	if len(f.Decorators) > 0 {
		p.write(strings.Repeat(" ", maxType-len(ts)+1))
		for i, dec := range f.Decorators {
			if i > 0 {
				p.write(" ")
			}
			p.Decorator(dec)
		}
	}
	if c, ok := p.trailing[f.Pos.Line]; ok {
		p.write(" // ")
		p.write(c)
	}
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
// The previous field's printer pulls trailing text from p.trailing
// (built in [scanTrailingComments]), so dropping the misattributed
// entry here does not lose information - it lands on the correct
// field below by way of the trailing map.
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
	for _, v := range d.Values {
		if v.Kind != ast.EnumBare && len(v.Name) > maxName {
			maxName = len(v.Name)
		}
	}
	for _, v := range d.Values {
		p.EnumValue(v, maxName)
	}
	p.depth--
	p.indent()
	p.write("}")
	p.nl()
}

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
	for _, dec := range v.Decorators {
		p.write(" ")
		p.Decorator(dec)
	}
	if c, ok := p.trailing[v.Pos.Line]; ok {
		p.write(" // ")
		p.write(c)
	}
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
	p.nl()
}

func (p *Printer) ScalarDecl(d *ast.ScalarDecl) {
	p.Doc(d.Doc)
	p.indent()
	p.write("scalar ")
	p.write(d.Name)
	p.write(" ")
	p.write(d.Primitive)
	for _, dec := range d.Decorators {
		p.write(" ")
		p.Decorator(dec)
	}
	if c, ok := p.trailing[d.Pos.Line]; ok {
		p.write(" // ")
		p.write(c)
	}
	p.nl()
}

func (p *Printer) MiddlewareDecl(d *ast.MiddlewareDecl) {
	p.Doc(d.Doc)
	p.declDecorators(d.Decorators)
	p.indent()
	p.write("middleware ")
	p.write(d.Name)
	if d.Params != nil {
		p.write("(")
		for i, par := range d.Params {
			if i > 0 {
				p.write(", ")
			}
			p.write(par.Name)
			p.write(": ")
			p.TypeRef(par.Type)
			if par.Default != nil {
				p.write(" = ")
				p.Expr(par.Default)
			}
		}
		p.write(")")
	}
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
	for i, m := range d.Methods {
		if i > 0 {
			p.nl()
		}
		p.Method(m)
	}
	p.depth--
	p.indent()
	p.write("}")
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

func (p *Printer) Decorator(d *ast.Decorator) {
	p.write("@")
	p.write(d.Name)
	if d.Args == nil {
		return
	}
	p.write("(")
	for i, a := range d.Args {
		if i > 0 {
			p.write(", ")
		}
		p.DecoratorArg(a)
	}
	p.write(")")
}

func (p *Printer) DecoratorArg(a *ast.DecoratorArg) {
	if a.Named {
		p.write(a.Name)
		p.write(": ")
	}
	switch {
	case a.Nested != nil:
		p.Decorator(a.Nested)
	case a.Object != nil:
		p.write("{")
		for i, f := range a.Object {
			if i > 0 {
				p.write(", ")
			}
			p.write(f.Name)
			p.write(": ")
			p.Expr(f.Value)
		}
		p.write("}")
	default:
		p.Expr(a.Value)
	}
}

func (p *Printer) Expr(e ast.Expr) {
	switch v := e.(type) {
	case *ast.StringLit:
		p.write(strconv.Quote(v.Value))
	case *ast.IntLit:
		p.write(strconv.FormatInt(v.Value, 10))
	case *ast.FloatLit:
		p.write(strconv.FormatFloat(v.Value, 'g', -1, 64))
	case *ast.BoolLit:
		if v.Value {
			p.write("true")
		} else {
			p.write("false")
		}
	case *ast.NullLit:
		p.write("null")
	case *ast.DurationLit:
		p.write(v.Text)
	case *ast.SizeLit:
		p.write(v.Text)
	case *ast.IdentExpr:
		p.write(v.Name.String())
	case *ast.ArrayLit:
		p.write("[")
		for i, el := range v.Elements {
			if i > 0 {
				p.write(", ")
			}
			p.Expr(el)
		}
		p.write("]")
	}
}

func (p *Printer) Doc(lines []string) {
	for _, line := range lines {
		p.indent()
		if line == "" {
			p.write("//")
		} else {
			p.write("// ")
			p.write(line)
		}
		p.nl()
	}
}

func (p *Printer) declDecorators(decs []*ast.Decorator) {
	for _, d := range decs {
		p.indent()
		p.Decorator(d)
		p.nl()
	}
}

// scanLooseComments finds every `//` block in src whose run is followed
// by a blank line - that is the lexer signal that the block is NOT
// attached to the next token's Doc and would otherwise be silently
// dropped. We anchor each loose block to the source line of the next
// code (the first non-blank, non-`//` line after the trailing blanks);
// the formatter looks up that anchor and re-emits the block above the
// matching declaration so section dividers and stand-alone notes
// survive the round trip.
//
// Trailing `//` comments (those that share a line with code) are NOT
// loose - they are picked up by [scanTrailingComments] separately.
func scanLooseComments(src string) map[int][]string {
	rawLines := strings.Split(src, "\n")
	out := map[int][]string{}
	i := 0
	for i < len(rawLines) {
		_, ok := leadingCommentText(rawLines[i])
		if !ok {
			i++
			continue
		}
		// Capture the full block.
		var block []string
		for i < len(rawLines) {
			t, ok := leadingCommentText(rawLines[i])
			if !ok {
				break
			}
			block = append(block, t)
			i++
		}
		// `i` now points at the first non-comment line. Determine
		// whether the block is loose: the next line is blank OR the
		// block was the last thing in the file.
		looseEnd := false
		if i >= len(rawLines) {
			looseEnd = true
		} else if strings.TrimSpace(rawLines[i]) == "" {
			looseEnd = true
		}
		if !looseEnd {
			// Attached - lexer / parser captured it; skip.
			continue
		}
		anchor := nextCodeLine(rawLines, i)
		if anchor == 0 {
			// No following code - drop with no anchor (file-trailing
			// comment, currently not preserved).
			continue
		}
		if existing, ok := out[anchor]; ok {
			existing = append(existing, "")
			existing = append(existing, block...)
			out[anchor] = existing
		} else {
			out[anchor] = block
		}
	}
	return out
}

// leadingCommentText reports whether line is a pure (leading) `//`
// comment line and returns the text after the `// ` marker. Lines
// where the `//` follows non-whitespace code (trailing comments) are
// rejected.
func leadingCommentText(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "//") {
		return "", false
	}
	rest := trimmed[2:]
	rest = strings.TrimPrefix(rest, " ")
	return rest, true
}

// nextCodeLine returns the 1-indexed line number of the first line at
// or after fromIdx (0-indexed) that is neither blank nor a `//` comment.
// Returns 0 when no such line exists.
func nextCodeLine(lines []string, fromIdx int) int {
	for j := fromIdx; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		return j + 1
	}
	return 0
}

// scanTrailingComments walks src line by line and records the text of any
// `// ...` that follows non-whitespace code on that line. Lines that
// consist entirely of a `//` comment (with optional leading whitespace)
// are NOT recorded - those reach AST nodes via the parser's Doc field
// and are emitted through the normal leading-comment path.
//
// String/raw-string literals are tracked across the line so a `//` inside
// quotes is not misread as a comment.
func scanTrailingComments(src string) map[int]string {
	out := map[int]string{}
	line := 1
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			scanOneLine(src[start:i], line, out)
			line++
			start = i + 1
		}
	}
	return out
}

func scanOneLine(line string, lineNum int, out map[int]string) {
	inDouble, inRaw := false, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case !inRaw && ch == '\\' && i+1 < len(line):
			// skip escaped char inside string
			i++
		case !inRaw && ch == '"':
			inDouble = !inDouble
		case !inDouble && ch == '`':
			inRaw = !inRaw
		case !inDouble && !inRaw && ch == '/' && i+1 < len(line) && line[i+1] == '/':
			prefix := strings.TrimSpace(line[:i])
			if prefix == "" {
				return
			}
			out[lineNum] = strings.TrimSpace(line[i+2:])
			return
		}
	}
}
