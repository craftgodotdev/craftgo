// Package format renders an [ast.File] back to canonical CraftGo source:
// tab indentation, aligned field and enum-value columns, and runs of blank
// lines collapsed to one.
package format

import (
	"bytes"
	"fmt"
	"io"
	"math"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// Format parses src (filename only labels diagnostics) and returns its canonical
// text. With diagnostics it returns src unchanged: src does not parse, or its
// canonical text would not parse or would not hold the same comments in the
// same places.
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
// source in was parsed from: out does not parse, or its comments differ or
// sit elsewhere.
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
	return movedComment(in, outFile)
}

// movedComment reports a comment of in that out holds in another place: after
// another construct, as another construct's doc, or in another scope.
func movedComment(in, out *ast.File) []lexer.Diagnostic {
	left := map[placedComment]int{}
	for _, c := range fileLayout(out).comments {
		left[c]++
	}
	moved := map[string]bool{}
	for _, c := range fileLayout(in).comments {
		if left[c] > 0 {
			left[c]--
			continue
		}
		moved[c.text] = true
	}
	for _, c := range in.Comments {
		if moved[c.Text] {
			return refusal(c.Pos, "formatting would move the comment %q", c.Text)
		}
	}
	return nil
}

// refusal is the single error diagnostic of a Format that returns src unchanged.
func refusal(pos lexer.Position, format string, args ...any) []lexer.Diagnostic {
	return []lexer.Diagnostic{{Pos: pos, Msg: fmt.Sprintf(format, args...)}}
}

// Print writes the canonical text of f to w and returns the first write error.
func Print(w io.Writer, f *ast.File) error {
	pr := newPrinter(w, f)
	pr.File(f)
	return pr.err
}

// newPrinter builds a Printer over the trailing, in-chain and free comments of
// f.
func newPrinter(w io.Writer, f *ast.File) *Printer {
	p := &Printer{w: w, chain: f.ChainComments, codeAfter: fileLayout(f).codeAfterFreeComments()}
	for _, c := range f.Comments {
		if c.Kind == lexer.CommentTrailing {
			p.trailing = append(p.trailing, c)
		}
	}
	return p
}

// Printer holds the state of one render pass; built without newPrinter it
// prints no trailing or in-chain comments.
type Printer struct {
	w     io.Writer
	err   error
	depth int
	// trailing holds the comments that follow code on their line, in source
	// order; the first emitted of them are printed.
	trailing []*ast.Comment
	emitted  int
	// open is set while a code line waits for its newline, so the trailing
	// comments of the source lines it covers can still join it.
	open bool
	// chain maps a source line to the comments above the decorator, name or
	// keyword on it inside a decorator chain.
	chain map[int][]string
	// codeAfter maps the first line of a free comment block to the first
	// line of the construct below it.
	codeAfter map[int]int
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
				p.blank(fcs[0].Pos.Line)
			}
			p.printFreeComment(fcs[0])
			wroteAny = true
			flushed = true
			fcs = fcs[1:]
		}
		return flushed
	}
	if f.Package != nil {
		start := memberStartLine(f.Package.Pos.Line, f.Decorators, len(f.LeadingDoc))
		if flushBefore(start) {
			p.blank(start)
		}
		p.comments(start, f.LeadingDoc)
		p.declDecorators(f.Decorators, f.Package.Pos.Line)
		if flushBefore(f.Package.Pos.Line - len(f.Package.Doc)) {
			p.blank(f.Package.Pos.Line)
		}
		p.comments(f.Package.Pos.Line, f.Package.Doc)
		p.line(f.Package.Pos.Line)
		p.write("package " + f.Package.Name)
		p.endCode()
		wroteAny = true
	}
	for i, imp := range f.Imports {
		// A comment block between imports keeps the blank lines around it.
		if flushBefore(imp.Pos.Line-len(imp.Doc)) || i == 0 {
			p.blank(imp.Pos.Line)
		}
		p.Import(imp)
		wroteAny = true
	}
	for i, d := range f.Decls {
		// Without a package, the file's leading decorators and the comment
		// above them go to the first declaration.
		var doc []string
		if i == 0 && f.Package == nil {
			doc = f.LeadingDoc
		}
		start := declFirstSourceLine(d) - len(doc)
		flushBefore(start)
		if wroteAny {
			p.blank(start)
		}
		p.comments(start, doc)
		p.Decl(d)
		wroteAny = true
	}
	flushBefore(0)
	p.at(math.MaxInt)
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
	return memberStartLine(d.DeclPos().Line, decs, 0)
}

func (p *Printer) Import(imp *ast.Import) {
	p.comments(imp.Pos.Line, imp.Doc)
	p.line(imp.Pos.Line)
	p.write("import ")
	if imp.Alias != "" {
		p.write(imp.Alias + " ")
	}
	p.write(imp.PathText)
	p.endCode()
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
