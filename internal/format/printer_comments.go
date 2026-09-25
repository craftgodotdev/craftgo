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
func (p *Printer) endCode() {
	p.open = true
	p.freeEnd = 0
}

// at ends the open code line before the construct that starts on source line
// src. A free comment block prints below the construct it sits in, so before
// one the trailing comments up to the next construct join the line.
func (p *Printer) at(src int) {
	if next, ok := p.codeAfter[src]; ok {
		src = next
	}
	p.endLine(src)
}

// endLine ends the open code line; the trailing comments of the source lines
// before src join it. A line holds one: joined records a second, which is left
// out, and Format then refuses the output.
func (p *Printer) endLine(src int) {
	if !p.open {
		return
	}
	for n := 0; p.emitted < len(p.trailing) && p.trailing[p.emitted].Pos.Line < src; n++ {
		if n == 0 {
			p.comment(" ", p.trailing[p.emitted].Text)
		} else if p.joined[0] == nil {
			p.joined = [2]*ast.Comment{p.trailing[p.emitted-n], p.trailing[p.emitted]}
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

// blankBefore reports whether a blank line goes above the construct that
// starts on source line start, below the one on line prevEnd: the source has
// one there, or a free comment block printed last did not sit right above it,
// which it would then read as its doc.
func (p *Printer) blankBefore(prevEnd, start int) bool {
	if prevEnd == 0 || start <= prevEnd {
		return false
	}
	return !p.code[start-1] || p.freeEnd > 0 && p.freeEnd != start-1
}

func (p *Printer) printFreeComment(c *ast.FreeComment) {
	p.comments(c.Pos.Line, c.Text)
	p.freeEnd = c.Pos.Line + len(c.Text) - 1
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

// trailingDecorators writes decs, the decorators after the code of a member
// that starts on source line first, set off from it by pad: all on the
// member's line or, when a comment sits among them, each later source line's
// on its own line one level deeper, under its comments.
func (p *Printer) trailingDecorators(decs []*ast.Decorator, first int, pad string) {
	if len(decs) == 0 {
		return
	}
	onLine := len(decs)
	if p.chainCommented(first, decs[len(decs)-1].Pos.Line) {
		onLine = 0
		for onLine < len(decs) && decs[onLine].Pos.Line <= first {
			onLine++
		}
	}
	if onLine > 0 {
		p.write(pad)
		p.inlineDecorators(decs[:onLine])
	}
	p.depth++
	for rest := decs[onLine:]; len(rest) > 0; {
		line, n := rest[0].Pos.Line, 1
		for n < len(rest) && rest[n].Pos.Line == line {
			n++
		}
		p.endCode()
		p.comments(line, p.chain[line])
		p.line(line)
		p.inlineDecorators(rest[:n])
		rest = rest[n:]
	}
	p.depth--
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

// chainCommented reports whether a comment sits inside the decorator chain
// that runs from source line first to line last: above one of its later
// lines, or after one of its lines but the last. Such a chain keeps its lines.
func (p *Printer) chainCommented(first, last int) bool {
	for line := range p.chain {
		if line > first && line <= last {
			return true
		}
	}
	return p.trailingBefore(first, last)
}

// trailingBefore reports whether a trailing comment still to print sits on a
// source line from first up to, not including, end.
func (p *Printer) trailingBefore(first, end int) bool {
	for _, c := range p.trailing[p.emitted:] {
		if c.Pos.Line >= end {
			return false
		}
		if c.Pos.Line >= first {
			return true
		}
	}
	return false
}
