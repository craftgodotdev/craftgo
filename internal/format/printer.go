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
//     so decorator chains line up.
//   - The `request` / `response` lines inside a method body share a
//     two-space alignment so the type column matches.
package format

import (
	"bytes"
	"io"
	"strconv"

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
