// Package format renders an [ast.File] back to canonical CraftGo source.
//
// The printer is round-trip safe for every well-formed input: parsing the
// printer's output produces an AST equal to its input, and a second
// formatting pass is a no-op (idempotency).
//
// Comment recovery is driven entirely from `f.Comments` - the parser
// snapshots every `//` comment (with position and leading/trailing kind)
// from the lexer onto that slice. The printer derives its trailing /
// loose lookup maps from the slice via [buildTrailingFromComments] and
// [buildLooseFromComments]; no source-bytes scan is needed. As a result
// [Format] (which has the source) and [Print] (AST only) produce the
// same output - both rely on the same `f.Comments` data.
//
// Two entry points are provided:
//
//   - [Format] takes a source buffer, parses it, and returns formatted text
//     plus any diagnostics. Suitable for `craftgo fmt` and editor integration.
//   - [Print] takes an [ast.File] and writes formatted text to an [io.Writer].
//     Suitable for callers that already have an AST in hand. Comment
//     recovery works as long as the file's `Comments` slice was populated
//     (which the parser always does).
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

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// Format parses src (filename used only for diagnostics) and returns the
// canonical-formatted text alongside any parser diagnostics. The returned
// text is always non-empty because the parser is error-tolerant - callers
// that want to reject formatting on errors should check len(diags) == 0.
func Format(filename, src string) (string, []lexer.Diagnostic) {
	p := parser.New(filename, src)
	f := p.Parse()
	var buf bytes.Buffer
	pr := newPrinter(&buf, f)
	pr.File(f)
	return buf.String(), p.Diagnostics()
}

// Print writes a canonical render of f to w. Comment recovery is driven
// from `f.Comments` (populated by the parser) so loose blocks and trailing
// notes survive even when the caller has no source buffer in hand.
func Print(w io.Writer, f *ast.File) error {
	pr := newPrinter(w, f)
	pr.File(f)
	return pr.err
}

// newPrinter builds a Printer with trailing / loose comment lookup maps
// derived from `f.Comments`. Centralising construction keeps both
// [Format] and [Print] paths identical so the two entry points produce
// the same output - the only difference is whether the caller already
// has an AST in hand.
func newPrinter(w io.Writer, f *ast.File) *Printer {
	return &Printer{
		w:        w,
		trailing: buildTrailingFromComments(f),
		loose:    buildLooseFromComments(f),
	}
}

// Printer is the internal state for one render pass. The zero value is
// useful as long as w is set; the trailing / loose maps are populated by
// [newPrinter] from the file's `Comments` slice (callers constructing a
// Printer directly via struct literal will simply lose comment recovery).
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
	p.Doc(imp.Doc)
	p.write("import ")
	if imp.Alias != "" {
		p.write(imp.Alias)
		p.write(" ")
	}
	p.write(strconv.Quote(imp.Path))
	if imp.TrailingDoc != "" {
		p.write("  // ")
		p.write(imp.TrailingDoc)
	}
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
	// Skip the source-scan trailing if a decorator's TrailingDoc already
	// emitted the same comment — otherwise the same `// note` would
	// appear twice and grow on each format pass.
	if !decoratorCarriesTrailing {
		if c, ok := p.trailing[f.Pos.Line]; ok {
			p.write(" // ")
			p.write(c)
		}
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
func (p *Printer) printFreeComment(c *ast.FreeComment) {
	for _, line := range c.Text {
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

func (p *Printer) Decorator(d *ast.Decorator) {
	p.write("@")
	p.write(d.Name)
	if d.Args != nil {
		p.write("(")
		for i, a := range d.Args {
			if i > 0 {
				p.write(", ")
			}
			p.DecoratorArg(a)
		}
		p.write(")")
	}
	if d.TrailingDoc != "" {
		p.write("  // ")
		p.write(d.TrailingDoc)
	}
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

// buildTrailingFromComments walks f.Comments and produces the trailing
// lookup map keyed by source line number. Replaces the legacy
// [scanTrailingComments] source-bytes scan now that the parser populates
// f.Comments with every comment + position + kind.
func buildTrailingFromComments(f *ast.File) map[int]string {
	out := map[int]string{}
	if f == nil {
		return out
	}
	for _, c := range f.Comments {
		if c == nil || c.Kind != lexer.CommentTrailing {
			continue
		}
		out[c.Pos.Line] = c.Text
	}
	return out
}

// buildLooseFromComments walks f.Comments to find leading-comment blocks
// separated from the following decl by a blank line - the lexer drops
// those from any AST node's Doc, so without the loose lookup the
// formatter would lose them. Equivalent to [scanLooseComments] but
// driven entirely from the AST so [Print] (which has no source buffer)
// works the same as [Format].
//
// Comments already promoted to a [*ast.FreeComment] body member by the
// parser (e.g. closing notes captured from `rbrace.Doc`) are skipped
// here - otherwise they would also appear above the next anchor decl,
// double-printing the comment.
func buildLooseFromComments(f *ast.File) map[int][]string {
	out := map[int][]string{}
	if f == nil || len(f.Comments) == 0 {
		return out
	}
	anchors := declAnchorLines(f)
	if len(anchors) == 0 {
		return out
	}
	claimed := freeCommentLines(f)
	i := 0
	for i < len(f.Comments) {
		c := f.Comments[i]
		if c == nil || c.Kind != lexer.CommentLeading {
			i++
			continue
		}
		// Capture the contiguous leading run starting at c.
		block := []string{c.Text}
		startLine := c.Pos.Line
		lastLine := c.Pos.Line
		j := i + 1
		for j < len(f.Comments) {
			n := f.Comments[j]
			if n == nil || n.Kind != lexer.CommentLeading || n.Pos.Line != lastLine+1 {
				break
			}
			block = append(block, n.Text)
			lastLine = n.Pos.Line
			j++
		}
		// Skip the whole block when its first line is already owned by
		// a body-level FreeComment. Parser promotes `}.Doc` content into
		// FreeComment members with Pos = the closing brace, but the
		// comment text itself sits one or more lines above the brace -
		// the FreeComment Text length tells us the span.
		if claimed[startLine] {
			i = j
			continue
		}
		// Anchor = first decl line >= lastLine + 1.
		anchor := nextAnchor(anchors, lastLine+1)
		if anchor != 0 && anchor > lastLine+1 {
			// Blank line between block end and anchor → loose.
			if existing, ok := out[anchor]; ok {
				out[anchor] = append(append(existing, ""), block...)
			} else {
				out[anchor] = block
			}
		}
		i = j
	}
	return out
}

// freeCommentLines collects every source line covered by a [*ast.FreeComment]
// body member. The parser sets FreeComment.Pos to the closing `}` of the
// body and FreeComment.Text to the comment lines that immediately precede
// it - so the covered span is `Pos.Line - len(Text) .. Pos.Line - 1`.
// Used by [buildLooseFromComments] to suppress duplicate emission of
// comments that already live inside a body as FreeComment.
func freeCommentLines(f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, d := range f.Decls {
		walkBodyForFreeComments(d, out)
	}
	return out
}

func walkBodyForFreeComments(d ast.Decl, out map[int]bool) {
	switch v := d.(type) {
	case *ast.TypeDecl:
		collectFreeCommentSpans(v.Body, out)
	case *ast.ErrorDecl:
		collectFreeCommentSpans(v.Body, out)
	case *ast.EnumDecl:
		for _, m := range v.Members {
			if fc, ok := m.(*ast.FreeComment); ok {
				markFreeCommentSpan(fc, out)
			}
		}
	case *ast.ServiceDecl:
		for _, m := range v.Members {
			if fc, ok := m.(*ast.FreeComment); ok {
				markFreeCommentSpan(fc, out)
			}
		}
	}
}

func collectFreeCommentSpans(members []ast.TypeMember, out map[int]bool) {
	for _, m := range members {
		if fc, ok := m.(*ast.FreeComment); ok {
			markFreeCommentSpan(fc, out)
		}
	}
}

func markFreeCommentSpan(fc *ast.FreeComment, out map[int]bool) {
	if fc == nil || len(fc.Text) == 0 {
		return
	}
	startLine := fc.Pos.Line - len(fc.Text)
	for ln := startLine; ln < fc.Pos.Line; ln++ {
		out[ln] = true
	}
}

// declAnchorLines returns the source-line anchor of every AST node that
// can claim a preceding `//` block as its leading Doc. Used by the loose-
// comment resolver to decide which blocks the lexer flushed on a blank
// line still have an "owning" code line nearby.
//
// Includes:
//   - imports (Import.Doc captures comments directly above)
//   - top-level decls (each Decl.Doc — see [declFirstSourceLine] for
//     the decorator-aware anchor)
//   - body members that carry leading doc: fields, methods, enum values
//
// Excluding any of these would cause the loose resolver to mis-classify
// a comment as "free-floating" and re-emit it as a section block on the
// next decl, double-printing the comment.
func declAnchorLines(f *ast.File) []int {
	out := make([]int, 0, len(f.Imports)+len(f.Decls)*4)
	if f.Package != nil {
		out = append(out, f.Package.Pos.Line)
	}
	for _, imp := range f.Imports {
		out = append(out, imp.Pos.Line)
	}
	for _, d := range f.Decls {
		out = append(out, declFirstSourceLine(d))
		out = append(out, bodyMemberAnchors(d)...)
	}
	return out
}

// bodyMemberAnchors collects the source-line anchor of every body
// member inside a top-level decl that can carry leading Doc:
// fields/mixins inside type/error bodies, methods inside service
// bodies, and enum values inside enum bodies. FreeComment members
// are skipped - they ARE the comments we're trying to anchor.
func bodyMemberAnchors(d ast.Decl) []int {
	var out []int
	switch v := d.(type) {
	case *ast.TypeDecl:
		for _, m := range v.Body {
			if pos, ok := memberAnchor(m); ok {
				out = append(out, pos)
			}
		}
	case *ast.ErrorDecl:
		for _, m := range v.Body {
			if pos, ok := memberAnchor(m); ok {
				out = append(out, pos)
			}
		}
	case *ast.EnumDecl:
		for _, m := range v.Members {
			if v, ok := m.(*ast.EnumValue); ok {
				out = append(out, v.Pos.Line)
			}
		}
	case *ast.ServiceDecl:
		for _, m := range v.Members {
			if mm, ok := m.(*ast.Method); ok {
				out = append(out, mm.Pos.Line)
			}
		}
	}
	return out
}

// memberAnchor returns the source line of a TypeMember when it can
// claim a preceding leading-doc block. FreeComment is excluded.
func memberAnchor(m ast.TypeMember) (int, bool) {
	switch v := m.(type) {
	case *ast.Field:
		return v.Pos.Line, true
	case *ast.Mixin:
		return v.Pos.Line, true
	}
	return 0, false
}

// nextAnchor returns the smallest entry in anchors that is >= line, or
// 0 when none. anchors is in source order (which is also numeric order
// for valid input). Linear scan is fine - top-level decl counts are
// small (~tens to hundreds, never thousands).
func nextAnchor(anchors []int, line int) int {
	for _, a := range anchors {
		if a >= line {
			return a
		}
	}
	return 0
}
