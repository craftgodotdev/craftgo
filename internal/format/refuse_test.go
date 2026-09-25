package format

import (
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/parser"
)

// Format returns the parser diagnostic for a decorator that follows a mixin on its line.
func TestFormatReportsStrandedDecorator(t *testing.T) {
	_, diags := Format("t.craftgo", "package p\n\ntype A {\n\tuser string S @default(\"\")\n\tname string\n}\n")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic, got none")
	}
}

// Format either returns text that parses and holds the source's comments, or
// returns the source unchanged with a diagnostic.
func TestFormatNeverDamagesTheFile(t *testing.T) {
	for name, src := range map[string]string{
		"escapes and floats": "package app\n\ntype D {\n" +
			"    a string @doc(\"bell \\u{7} here\")\n" +
			"    b float64 @lte(1234567.5)\n" +
			"    c float64 @gte(0.00001)\n" +
			"    d string @pattern(`^\\d+$`)\n" +
			"    e string @doc(\"nul \\u{0} x\")\n" +
			"}\n",
		"comments after package, middleware and brace": "package app // pkg comment\n\n" +
			"middleware Auth // mw comment\n\n" +
			"type T { // brace comment\n    a string\n}\n",
		"comment inside a scalar's decorator chain": "package app\n\n" +
			"@minLength(1)\n// in-chain note\n@maxLength(5)\nscalar Code string\n",
		"comment under a forwarded decorator": "// c\n\n// c\n\n// c\n@doc(\"t\")\n// c\ntype T {\n\ty string\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, diags := Format("t.craftgo", src)
			if len(diags) > 0 {
				if out != src {
					t.Errorf("diagnostics %v came with changed text:\n%s", diags, out)
				}
				return
			}
			p := parser.New("t.craftgo", out)
			p.Parse()
			if len(p.Diagnostics()) > 0 {
				t.Fatalf("formatted text does not parse: %v\n%s", p.Diagnostics(), out)
			}
			if in, got := commentTexts(src), commentTexts(out); strings.Join(in, "\n") != strings.Join(got, "\n") {
				t.Errorf("comments changed:\nsource: %q\noutput: %q\n%s", in, got, out)
			}
		})
	}
}

// Format writes LF line ends for CRLF and CR-only input, trailing comments
// included; "\r\r\n" is two line ends.
func TestFormatDropsCarriageReturns(t *testing.T) {
	for src, want := range map[string]string{
		"package p\r\n\r\n// doc\r\ntype A {\r\n\tx string // note\r\n}\r\n":             "package p\n\n// doc\ntype A {\n\tx string // note\n}\n",
		"package p\r\r// doc\rtype A {\r\tx string // note\r}\r":                         "package p\n\n// doc\ntype A {\n\tx string // note\n}\n",
		"package p\r\r\n\r\r\n// doc\r\r\ntype A {\r\r\n\tx string // note\r\r\n}\r\r\n": "package p\n\n// doc\n\ntype A {\n\tx string // note\n}\n",
	} {
		out, diags := Format("t.craftgo", src)
		if len(diags) > 0 {
			t.Fatalf("%q: diagnostics: %v", src, diags)
		}
		if out != want {
			t.Errorf("%q: got %q, want %q", src, out, want)
		}
	}
}

// Format reads a file that opens with a byte-order mark and writes it without
// the mark.
func TestFormatDropsTheByteOrderMark(t *testing.T) {
	out, diags := Format("t.craftgo", "\ufeff// doc\npackage p\n")
	if len(diags) > 0 || out != "// doc\npackage p\n" {
		t.Errorf("got %q, %v", out, diags)
	}
}

// checkOutput refuses canonical text that fails to parse or whose comments
// differ from the source's, naming the comment at its source position.
func TestCheckOutput(t *testing.T) {
	src := "package p\n\n// doc\ntype A {\n\tx string // note\n}\n"
	for _, c := range []struct {
		name, out, want string
	}{
		{"same comments", "package p\n\n// doc\ntype A {\n\tx string  // note\n}\n", ""},
		{"parse error", "package p\n\n// doc\ntype A {\n\tx string // note\n", "t.craftgo:1:1: the formatted text would not parse"},
		{"dropped", "package p\n\n// doc\ntype A {\n\tx string\n}\n", `t.craftgo:5:11: formatting would drop the comment "note"`},
		{"duplicated", "package p\n\n// doc\n// doc\ntype A {\n\tx string // note\n}\n", `t.craftgo:3:1: formatting would duplicate the comment "doc"`},
		{"invented", "package p\n\n// doc\ntype A {\n\tx string // note\n}\n// new\n", `t.craftgo:1:1: formatting would add the comment "new"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := parser.New("t.craftgo", src)
			f := p.Parse()
			diags := checkOutput("t.craftgo", f, newSource(p.Tokens()), c.out)
			if c.want == "" {
				if len(diags) > 0 {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if len(diags) == 0 || !strings.HasPrefix(diags[0].Error(), c.want) {
				t.Fatalf("diagnostics %v, want one starting %q", diags, c.want)
			}
		})
	}
}

// checkOutput refuses canonical text that holds a comment in another place:
// after another member, or as a doc where the source had a free comment.
func TestCheckOutputRefusesAMovedComment(t *testing.T) {
	src := "package p\n\ntype A {\n\tx string // note\n\ty string\n}\n\n// free\n\ntype B {}\n"
	for _, c := range []struct {
		name, out, want string
	}{
		{"same places", "package p\n\ntype A {\n\tx string  // note\n\n\ty string\n}\n\n// free\n\ntype B {}\n", ""},
		{"trailing comment on the next member", "package p\n\ntype A {\n\tx string\n\ty string // note\n}\n\n// free\n\ntype B {}\n", `t.craftgo:4:11: formatting would move the comment "note"`},
		{"free comment turned doc", "package p\n\ntype A {\n\tx string // note\n\ty string\n}\n\n// free\ntype B {}\n", `t.craftgo:8:1: formatting would move the comment "free"`},
		{"free comment into a body", "package p\n\ntype A {\n\tx string // note\n\ty string\n\n\t// free\n}\n\ntype B {}\n", `t.craftgo:8:1: formatting would move the comment "free"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := parser.New("t.craftgo", src)
			f := p.Parse()
			diags := checkOutput("t.craftgo", f, newSource(p.Tokens()), c.out)
			if c.want == "" {
				if len(diags) > 0 {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if len(diags) == 0 || diags[0].Error() != c.want {
				t.Fatalf("diagnostics %v, want %q", diags, c.want)
			}
		})
	}
}

// commentTexts returns the text of every comment in src, sorted.
func commentTexts(src string) []string {
	p := parser.New("t.craftgo", src)
	var out []string
	for _, c := range p.Parse().Comments {
		out = append(out, c.Text)
	}
	slices.Sort(out)
	return out
}
