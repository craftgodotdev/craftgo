package format

import "github.com/craftgodotdev/craftgo/internal/ast"

// line starts an output line for the construct that starts on source line
// src: the open code line ends first, then the indent is written.
func (p *Printer) line(src int) {
	p.at(src)
	p.indent()
}

// endCode ends a code line. Its newline waits for the next construct, so the
// trailing comments of the source lines it covers can still join it.
func (p *Printer) endCode() { p.open = true }

// at ends the open code line before the construct that starts on source line
// src; the trailing comments of the source lines before src join it. A line
// holds one: a second is left out, and Format then refuses the output.
func (p *Printer) at(src int) {
	if !p.open {
		return
	}
	for n := 0; p.emitted < len(p.trailing) && p.trailing[p.emitted].Pos.Line < src; n++ {
		if n == 0 {
			p.comment(" ", p.trailing[p.emitted].Text)
		}
		p.emitted++
	}
	p.write("\n")
	p.open = false
}

// comment writes prefix, `//` and text.
func (p *Printer) comment(prefix, text string) {
	p.write(prefix + "//")
	if text != "" {
		p.write(" " + text)
	}
}

// comments writes the lines of a comment block above the construct that
// starts on source line src.
func (p *Printer) comments(src int, lines []string) {
	for _, l := range lines {
		p.line(src)
		p.comment("", l)
		p.write("\n")
	}
}

// blank writes an empty line before the construct that starts on source line
// src.
func (p *Printer) blank(src int) {
	p.at(src)
	p.write("\n")
}

func (p *Printer) printFreeComment(c *ast.FreeComment) {
	p.comments(c.Pos.Line, c.Text)
}

// declDecorators prints decs one per line with the comments inside their
// chain, which ends at the name or keyword on source line last.
func (p *Printer) declDecorators(decs []*ast.Decorator, last int) {
	prev := 0
	for i, d := range decs {
		if i > 0 && d.Pos.Line != prev {
			p.comments(d.Pos.Line, p.chain[d.Pos.Line])
		}
		prev = d.Pos.Line
		p.line(d.Pos.Line)
		p.Decorator(d)
		p.endCode()
	}
	if len(decs) > 0 && last != prev {
		p.comments(last, p.chain[last])
	}
}

// leadingChain returns the decorators of decs written on lines above line,
// which the parser puts first.
func leadingChain(decs []*ast.Decorator, line int) []*ast.Decorator {
	n := 0
	for n < len(decs) && decs[n].Pos.Line < line {
		n++
	}
	return decs[:n]
}

// chainCommented reports whether a comment sits inside the chain decs, which
// ends at the name or keyword on source line last, or after one of its lines;
// such a chain keeps its lines.
func (p *Printer) chainCommented(decs []*ast.Decorator, last int) bool {
	if len(decs) == 0 {
		return false
	}
	first := decs[0].Pos.Line
	for line := range p.chain {
		if line > first && line <= last {
			return true
		}
	}
	for _, c := range p.trailing[p.emitted:] {
		if c.Pos.Line >= last {
			break
		}
		if c.Pos.Line >= first {
			return true
		}
	}
	return false
}
