// Package format renders an [ast.File] back to canonical CraftGo source:
// tab indentation, aligned field and enum-value columns, and runs of blank
// lines collapsed to one.
package format

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// Format parses src (filename only labels diagnostics) and returns its canonical
// text. With diagnostics it returns src unchanged: src does not parse, or its
// canonical text would not parse or would not hold the same comments.
func Format(filename, src string) (string, []lexer.Diagnostic) {
	p := parser.New(filename, src)
	f := p.Parse()
	if diags := p.Diagnostics(); len(diags) > 0 {
		return src, diags
	}
	var buf bytes.Buffer
	newPrinter(&buf, f).File(f)
	out := buf.String()
	if diags := checkOutput(filename, f, out); len(diags) > 0 {
		return src, diags
	}
	return out, nil
}

// checkOutput reports why out, the canonical text of in, cannot replace the
// source in was parsed from: out does not parse, or its comments differ.
func checkOutput(filename string, in *ast.File, out string) []lexer.Diagnostic {
	fileStart := lexer.Position{Filename: filename, Line: 1, Column: 1}
	p := parser.New(filename, out)
	outFile := p.Parse()
	if diags := p.Diagnostics(); len(diags) > 0 {
		return refusal(fileStart, "the formatted text would not parse (%s on its line %d)", diags[0].Msg, diags[0].Pos.Line)
	}
	extra := map[string]int{}
	for _, c := range outFile.Comments {
		extra[c.Text]++
	}
	for _, c := range in.Comments {
		extra[c.Text]--
	}
	for _, c := range in.Comments {
		switch {
		case extra[c.Text] < 0:
			return refusal(c.Pos, "formatting would drop the comment %q", c.Text)
		case extra[c.Text] > 0:
			return refusal(c.Pos, "formatting would duplicate the comment %q", c.Text)
		}
	}
	for _, c := range outFile.Comments {
		if extra[c.Text] > 0 {
			return refusal(fileStart, "formatting would add the comment %q", c.Text)
		}
	}
	return nil
}

// refusal is the single error diagnostic of a Format that returns src unchanged.
func refusal(pos lexer.Position, format string, args ...any) []lexer.Diagnostic {
	return []lexer.Diagnostic{{Pos: pos, Msg: fmt.Sprintf(format, args...)}}
}

// Print writes the canonical text of f to w and returns the first write error.
// Comments that no AST node holds are read from f.Comments.
func Print(w io.Writer, f *ast.File) error {
	pr := newPrinter(w, f)
	pr.File(f)
	return pr.err
}

// newPrinter builds a Printer with the comment maps derived from f.Comments.
func newPrinter(w io.Writer, f *ast.File) *Printer {
	return &Printer{
		w:        w,
		trailing: buildTrailingFromComments(f),
		interDec: buildInterDecoratorComments(f),
	}
}

// Printer holds the state of one render pass; built without newPrinter it
// prints none of the comments that come from f.Comments.
type Printer struct {
	w     io.Writer
	err   error
	depth int
	// trailing maps a source line to the text of the comment after its code.
	trailing map[int]string
	// interDec maps the source line of a decorator or keyword to the comment
	// block written above it inside a decorator chain; no AST node holds these.
	interDec map[int][]string
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

// File renders f in source order: one blank line between top-level
// declarations, file-scope comment blocks placed by source line.
func (p *Printer) File(f *ast.File) {
	fcs := f.FreeComments
	wroteAny := false
	// flushBefore prints the file-scope comment blocks that start before line
	// (0 means all) and reports whether it printed any.
	flushBefore := func(line int) bool {
		flushed := false
		for len(fcs) > 0 && (line == 0 || fcs[0].Pos.Line < line) {
			if wroteAny {
				p.nl()
			}
			p.printFreeComment(fcs[0])
			wroteAny = true
			flushed = true
			fcs = fcs[1:]
		}
		return flushed
	}
	if len(f.LeadingDoc) > 0 {
		p.Doc(f.LeadingDoc)
		wroteAny = true
	}
	for _, d := range f.Decorators {
		p.Decorator(d)
		p.nl()
		wroteAny = true
	}
	if f.Package != nil {
		if flushBefore(f.Package.Pos.Line - len(f.Package.Doc)) {
			p.nl()
		}
		p.Doc(f.Package.Doc)
		p.write("package ")
		p.write(f.Package.Name)
		p.nl()
		wroteAny = true
	}
	if len(f.Imports) > 0 {
		p.nl()
		for _, imp := range f.Imports {
			flushBefore(imp.Pos.Line - len(imp.Doc))
			p.Import(imp)
			p.nl()
			wroteAny = true
		}
	}
	for _, d := range f.Decls {
		flushBefore(declFirstSourceLine(d))
		if wroteAny {
			p.nl()
		}
		p.Decl(d)
		wroteAny = true
	}
	flushBefore(0)
}

// declFirstSourceLine returns the line of d's first decorator, or of d itself.
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
	case *ast.EventDecl:
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
	case *ast.EventDecl:
		p.EventDecl(v)
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
