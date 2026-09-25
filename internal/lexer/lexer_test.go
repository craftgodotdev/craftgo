package lexer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPositionString(t *testing.T) {
	if got := (Position{Filename: "x.craftgo", Line: 2, Column: 3}).String(); got != "x.craftgo:2:3" {
		t.Errorf("with filename: got %q", got)
	}
	if got := (Position{Line: 1, Column: 1}).String(); got != "1:1" {
		t.Errorf("without filename: got %q", got)
	}
	if got := (Position{Filename: "craftgo.design.yaml"}).String(); got != "craftgo.design.yaml" {
		t.Errorf("a file without a line: got %q", got)
	}
}

func TestPositionIsValid(t *testing.T) {
	if !(Position{Line: 1}).IsValid() {
		t.Error("expected valid")
	}
	if (Position{}).IsValid() {
		t.Error("expected invalid")
	}
}

func TestKindString(t *testing.T) {
	if EOF.String() != "EOF" {
		t.Error("EOF")
	}
	if LBrace.String() != "{" {
		t.Error("LBrace")
	}
	if got := Kind(99999).String(); got != "Kind(99999)" {
		t.Errorf("unknown: got %q", got)
	}
}

func TestTokenString(t *testing.T) {
	tok := Token{Kind: Ident, Text: "foo", Pos: Position{Line: 1, Column: 1}}
	s := tok.String()
	if !strings.Contains(s, "Ident") || !strings.Contains(s, "foo") {
		t.Errorf("got %q", s)
	}
}

func TestDiagnosticError(t *testing.T) {
	d := Diagnostic{Pos: Position{Line: 1, Column: 1}, Msg: "bad"}
	if !strings.Contains(d.Error(), "bad") {
		t.Error("expected message in Error()")
	}
}

func TestSeverityString(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityError, "error"},
		{SeverityWarning, "warning"},
		{SeverityInfo, "info"},
		{SeverityHint, "hint"},
		{Severity(99), "error"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Severity(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestDiagnosticStructuredFields(t *testing.T) {
	d := Diagnostic{
		Pos:      Position{Line: 2, Column: 3},
		End:      Position{Line: 2, Column: 9},
		Severity: SeverityWarning,
		Code:     "decorator/placement",
		Msg:      "bad place",
		Related: []Related{
			{Pos: Position{Line: 1, Column: 1}, Msg: "first occurrence"},
		},
	}
	if d.Severity != SeverityWarning {
		t.Error("severity")
	}
	if d.Code != "decorator/placement" {
		t.Error("code")
	}
	if d.Msg != "bad place" {
		t.Error("msg")
	}
	if d.End.Column != 9 {
		t.Error("end")
	}
	if len(d.Related) != 1 || d.Related[0].Msg != "first occurrence" {
		t.Error("related")
	}
}

func first(t *testing.T, src string) Token {
	t.Helper()
	return New("", src).Next()
}

func TestEOF(t *testing.T) {
	if first(t, "").Kind != EOF {
		t.Error()
	}
}

func TestWhitespace(t *testing.T) {
	if first(t, " \t\r\n").Kind != EOF {
		t.Error()
	}
}

func TestLineComment(t *testing.T) {
	l := New("", "// hello\nfoo")
	tok := l.Next()
	if tok.Kind != Ident || tok.Text != "foo" || tok.Pos.Line != 2 {
		t.Errorf("got %+v", tok)
	}
}

func TestLineCommentToEOF(t *testing.T) {
	if first(t, "// no newline").Kind != EOF {
		t.Error()
	}
}

func TestKeywords(t *testing.T) {
	cases := map[string]Kind{
		"package": KwPackage, "import": KwImport,
		"type": KwType, "enum": KwEnum, "error": KwError,
		"scalar": KwScalar, "service": KwService, "extend": KwExtend,
		"middleware": KwMiddleware, "request": KwRequest, "response": KwResponse,
		"map":  KwMap,
		"true": KwTrue, "false": KwFalse, "null": KwNull,
		"get": VerbGet, "post": VerbPost, "put": VerbPut, "patch": VerbPatch,
		"delete": VerbDelete, "head": VerbHead, "options": VerbOptions,
	}
	for src, want := range cases {
		if got := first(t, src).Kind; got != want {
			t.Errorf("%q: got %s want %s", src, got, want)
		}
	}
}

func TestIdent(t *testing.T) {
	tok := first(t, "MyType")
	if tok.Kind != Ident || tok.Text != "MyType" {
		t.Errorf("got %+v", tok)
	}
}

func TestIdentUnderscore(t *testing.T) {
	if first(t, "_foo").Kind != Ident {
		t.Error()
	}
}

func TestIdentMixed(t *testing.T) {
	if first(t, "foo_123").Text != "foo_123" {
		t.Error()
	}
}

func TestInt(t *testing.T) {
	tok := first(t, "42")
	if tok.Kind != Int || tok.Text != "42" {
		t.Errorf("got %+v", tok)
	}
}

func TestFloat(t *testing.T) {
	tok := first(t, "3.14")
	if tok.Kind != Float || tok.Text != "3.14" {
		t.Errorf("got %+v", tok)
	}
}

func TestNumberDotNotFloat(t *testing.T) {
	l := New("", "3.foo")
	if t1 := l.Next(); t1.Kind != Int {
		t.Errorf("first: %s", t1.Kind)
	}
	if t2 := l.Next(); t2.Kind != Dot {
		t.Errorf("second: %s", t2.Kind)
	}
}

func TestNumberDotAtEOF(t *testing.T) {
	l := New("", "3.")
	if l.Next().Kind != Int {
		t.Error("first not Int")
	}
	if l.Next().Kind != Dot {
		t.Error("second not Dot")
	}
}

func TestDuration(t *testing.T) {
	for _, c := range []string{"1ns", "2us", "1µs", "100ms", "30s", "5m", "1h"} {
		if first(t, c).Kind != Duration {
			t.Errorf("%q: not duration", c)
		}
	}
}

func TestDurationFloat(t *testing.T) {
	if first(t, "1.5s").Kind != Duration {
		t.Error()
	}
}

func TestSize(t *testing.T) {
	for _, c := range []string{"1B", "1KB", "5MB", "10GB"} {
		if first(t, c).Kind != Size {
			t.Errorf("%q", c)
		}
	}
}

func TestBadNumberSuffix(t *testing.T) {
	l := New("", "1xyz")
	if l.Next().Kind != Error {
		t.Error()
	}
	if len(l.Diagnostics()) == 0 {
		t.Error()
	}
}

func TestString(t *testing.T) {
	tok := first(t, `"hello"`)
	if tok.Kind != String || tok.Text != `"hello"` {
		t.Errorf("got %+v", tok)
	}
}

func TestStringEscapes(t *testing.T) {
	for _, c := range []string{`"a\nb"`, `"a\tb"`, `"a\rb"`, `"a\"b"`, `"a\\b"`} {
		if first(t, c).Kind != String {
			t.Errorf("%q", c)
		}
	}
}

func TestStringUnicodeEscape(t *testing.T) {
	for _, c := range []string{`"\u{1F600}"`, `"\u{a}"`, `"\u{D7FF}"`, `"\u{E000}"`, `"\u{10FFFF}"`} {
		if first(t, c).Kind != String {
			t.Errorf("%q", c)
		}
	}
}

func TestStringUnterminated(t *testing.T) {
	if first(t, `"hello`).Kind != Error {
		t.Error()
	}
}

func TestStringNewline(t *testing.T) {
	if first(t, "\"hello\n\"").Kind != Error {
		t.Error()
	}
}

func TestStringInvalidEscape(t *testing.T) {
	if first(t, `"\x"`).Kind != Error {
		t.Error()
	}
}

func TestStringEOFAfterBackslash(t *testing.T) {
	if first(t, `"\`).Kind != Error {
		t.Error()
	}
}

func TestStringBadUnicodeEscape(t *testing.T) {
	cases := []string{
		`"\u abc"`,     // not opening {
		`"\u{}"`,       // empty
		`"\u{xyz}"`,    // bad hex
		`"\u{1234567}`, // 7 chars, no closing }
		`"\u{D800}"`,   // surrogate
		`"\u{dfff}"`,   // surrogate
		`"\u{110000}"`, // above U+10FFFF
		`"\u{123456}"`, // above U+10FFFF
	}
	for _, c := range cases {
		if first(t, c).Kind != Error {
			t.Errorf("%q", c)
		}
	}
}

// Each reserved word lexes to its kind, whose String is the word; IsKeyword
// holds for exactly the reserved words and IsVerb for the seven HTTP verbs.
func TestKeywordPredicates(t *testing.T) {
	verbs := 0
	for k := EOF; k <= Dash; k++ {
		_, reserved := keywords[k.String()]
		if k.IsKeyword() != reserved {
			t.Errorf("%v: IsKeyword = %v, reserved = %v", k, k.IsKeyword(), reserved)
		}
		if reserved {
			if tok := first(t, k.String()); tok.Kind != k || tok.Text != k.String() {
				t.Errorf("%q lexes as %+v", k.String(), tok)
			}
		}
		if k.IsVerb() {
			verbs++
			if !k.IsKeyword() {
				t.Errorf("verb %v is not a keyword", k)
			}
		}
	}
	if verbs != 7 {
		t.Errorf("%d verbs, want 7", verbs)
	}
}

// IsIdent accepts exactly what lexes as one Ident token.
func TestIsIdent(t *testing.T) {
	for s, want := range map[string]bool{
		"email": true, "_x9": true, "X": true, "uuid": true,
		"": false, "9x": false, "a-b": false, "a b": false, "héllo": false,
		"null": false, "true": false, "service": false, "get": false,
	} {
		if got := IsIdent(s); got != want {
			t.Errorf("IsIdent(%q) = %v, want %v", s, got, want)
		}
		toks := New("", s).Tokenize()
		lexesAsIdent := len(toks) == 2 && toks[0].Kind == Ident && toks[0].Text == s
		if lexesAsIdent != want {
			t.Errorf("%q lexes as %v", s, toks)
		}
	}
}

// Unquote decodes the DSL's escapes, keeps a raw literal's content, and names
// the first escape it rejects.
func TestUnquote(t *testing.T) {
	for _, c := range []struct {
		in, want, err string
	}{
		{`""`, "", ""},
		{`"abc"`, "abc", ""},
		{`"a\nb\tc\rd"`, "a\nb\tc\rd", ""},
		{`"a\"b\\c"`, "a\"b\\c", ""},
		{`"\u{61}\u{7}\u{0}"`, "a\a\x00", ""},
		{`"\u{1F600}"`, "\U0001F600", ""},
		{`"\u{D7FF}\u{E000}\u{10FFFF}"`, "\ud7ff\ue000\U0010FFFF", ""},
		{"\"zero\u200bwidth\"", "zero\u200bwidth", ""},
		{"`^\\d+$`", `^\d+$`, ""},
		{"`a\nb`", "a\nb", ""},
		{`"\zbad"`, "", `invalid escape sequence \z`},
		{`"\é"`, "", `invalid escape sequence \é`},
		{`"\u nobrace"`, "", "invalid unicode escape"},
		{`"\u{nobrace"`, "", "invalid unicode escape"},
		{`"\u{}"`, "", "invalid unicode escape"},
		{`"\u{ZZ}"`, "", "invalid unicode escape"},
		{`"\u{1234567}"`, "", "invalid unicode escape"},
		{`"\u{D800}"`, "", `unicode escape \u{D800} is outside the valid code points (0-D7FF, E000-10FFFF)`},
		{`"a\u{dfff}"`, "", `unicode escape \u{dfff} is outside the valid code points (0-D7FF, E000-10FFFF)`},
		{`"\u{110000}"`, "", `unicode escape \u{110000} is outside the valid code points (0-D7FF, E000-10FFFF)`},
		{`"a\"`, "", "unterminated escape sequence"},
		{`"`, "", `"\"" is not a string literal`},
		{`abc`, "", `"abc" is not a string literal`},
	} {
		got, err := Unquote(c.in)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("Unquote(%q) error = %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("Unquote(%q) = %q, %v, want %q", c.in, got, err, c.want)
		}
	}
}

// A String token's text is what Unquote accepts, escapes as written.
func TestStringTextIsTheSource(t *testing.T) {
	src := `"a\u{7}\"b"`
	if tok := first(t, src); tok.Kind != String || tok.Text != src {
		t.Errorf("got %+v", tok)
	}
}

func TestRawString(t *testing.T) {
	if first(t, "`hello`").Kind != RawString {
		t.Error()
	}
}

func TestRawStringMultiline(t *testing.T) {
	if first(t, "`a\nb`").Kind != RawString {
		t.Error()
	}
}

func TestRawStringUnterminated(t *testing.T) {
	if first(t, "`hello").Kind != Error {
		t.Error()
	}
}

func TestPunct(t *testing.T) {
	cases := map[string]Kind{
		"{": LBrace, "}": RBrace, "(": LParen, ")": RParen,
		"[": LBracket, "]": RBracket, "<": LAngle, ">": RAngle,
		",": Comma, ":": Colon, "=": Equal, "?": Question,
		".": Dot, "/": Slash, "@": At, "-": Dash,
	}
	for src, want := range cases {
		if got := first(t, src).Kind; got != want {
			t.Errorf("%q: got %s want %s", src, got, want)
		}
	}
}

func TestUnknownChar(t *testing.T) {
	l := New("", "$")
	if l.Next().Kind != Error {
		t.Error()
	}
	if len(l.Diagnostics()) == 0 {
		t.Error()
	}
}

func TestPositionTracking(t *testing.T) {
	l := New("test.craftgo", "foo\n  bar")
	t1 := l.Next()
	if t1.Pos.Line != 1 || t1.Pos.Column != 1 {
		t.Errorf("first: %+v", t1.Pos)
	}
	t2 := l.Next()
	if t2.Pos.Line != 2 || t2.Pos.Column != 3 {
		t.Errorf("second: %+v", t2.Pos)
	}
	if t2.Pos.Filename != "test.craftgo" {
		t.Error("filename not preserved")
	}
}

func TestTokenize(t *testing.T) {
	toks := New("", "type Foo").Tokenize()
	if len(toks) != 3 {
		t.Errorf("count: %d", len(toks))
	}
	if toks[0].Kind != KwType {
		t.Error("token[0]")
	}
	if toks[1].Kind != Ident {
		t.Error("token[1]")
	}
	if toks[2].Kind != EOF {
		t.Error("token[2]")
	}
}

func TestGoldenSample(t *testing.T) {
	path, err := filepath.Abs("testdata/sample.craftgo")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l := New("sample.craftgo", string(src))
	toks := l.Tokenize()
	if len(toks) < 30 {
		t.Errorf("expected many tokens, got %d", len(toks))
	}
	if d := l.Diagnostics(); len(d) > 0 {
		t.Errorf("expected no diagnostics, got %d: %v", len(d), d)
	}
}

// TestLineCommentStripsCarriageReturn pins that a CRLF comment reaches Doc
// without its '\r'.
func TestLineCommentStripsCarriageReturn(t *testing.T) {
	tok := New("", "// hello\r\nfoo").Next()
	if tok.Text != "foo" {
		t.Fatalf("expected `foo` token, got %q", tok.Text)
	}
	if len(tok.Doc) != 1 || tok.Doc[0] != "hello" {
		t.Errorf("CRLF comment must not leave a trailing CR; got Doc %q", tok.Doc)
	}
}

// A comment after a token is recorded as trailing without a CRLF's '\r', in
// order with the leading ones; a lone '\r' ends the line before a comment.
func TestTrailingCommentStripsCarriageReturn(t *testing.T) {
	l := New("", "foo // one\r\n// two\r\nbar \r// three\r\n")
	toks := l.Tokenize()
	if len(toks[1].Doc) != 1 || toks[1].Doc[0] != "two" {
		t.Errorf("doc of bar = %q", toks[1].Doc)
	}
	var got []string
	for _, c := range l.Comments() {
		got = append(got, c.Kind.String()+":"+c.Text)
	}
	if want := "trailing:one leading:two leading:three"; strings.Join(got, " ") != want {
		t.Errorf("comments = %q, want %q", got, want)
	}
}

// A lone '\r' ends a line as '\n' and "\r\n" do: a source with any of the
// three line ends lexes into the same tokens, docs and comments, at the same
// lines and columns.
func TestLoneCarriageReturnEndsALine(t *testing.T) {
	lf := "package demo\ntype T {\n\tid string // why\n}\n// note\ntype U {\n\tname string\n}\n\n// detached\n\n// doc\ntype V {\n\tpath string @pattern(`a\nb`)\n}\n"
	want := lexed(lf)
	for name, src := range map[string]string{
		"CR":    strings.ReplaceAll(lf, "\n", "\r"),
		"CRLF":  strings.ReplaceAll(lf, "\n", "\r\n"),
		"mixed": strings.Replace(strings.ReplaceAll(lf, "\n", "\r"), "\r", "\n", 3),
	} {
		t.Run(name, func(t *testing.T) {
			if got := lexed(src); got != want {
				t.Errorf("lexed:\n%s\nwant:\n%s", got, want)
			}
		})
	}
	if tok := first(t, "\"a\rb\""); tok.Kind != Error || tok.Text != "newline in string literal" {
		t.Errorf("a string across a lone CR: %v", tok)
	}
}

// A UTF-8 byte-order mark at the start of the source is skipped: the first
// token is at line 1, column 1, and nothing is reported. Anywhere else it is
// an unexpected character.
func TestByteOrderMarkIsSkipped(t *testing.T) {
	l := New("", "\ufeff// doc\npackage p")
	tok := l.Next()
	if tok.Kind != KwPackage || tok.Pos.Line != 2 || tok.Pos.Column != 1 || len(tok.Doc) != 1 || tok.Doc[0] != "doc" {
		t.Errorf("first token %+v", tok)
	}
	if c := l.Comments()[0]; c.Pos.Line != 1 || c.Pos.Column != 1 || c.Pos.Offset != len("\ufeff") {
		t.Errorf("comment at %+v", c.Pos)
	}
	if d := l.Diagnostics(); len(d) > 0 {
		t.Errorf("diagnostics: %v", d)
	}
	l = New("", "p \ufeff")
	l.Tokenize()
	if len(l.Diagnostics()) != 1 {
		t.Errorf("a mark after the start: diagnostics %v", l.Diagnostics())
	}
}

// lexed renders the tokens of src with their lines, columns and docs, then its
// comments; a line end inside a token reads as "\n".
func lexed(src string) string {
	l := New("", src)
	lineEnds := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	var b strings.Builder
	for _, tok := range l.Tokenize() {
		fmt.Fprintf(&b, "%s %q %d:%d %q\n", tok.Kind, lineEnds.Replace(tok.Text), tok.Pos.Line, tok.Pos.Column, tok.Doc)
	}
	for _, c := range l.Comments() {
		fmt.Fprintf(&b, "%s %q %d:%d\n", c.Kind, c.Text, c.Pos.Line, c.Pos.Column)
	}
	for _, d := range l.Diagnostics() {
		fmt.Fprintf(&b, "diagnostic %s\n", d.Error())
	}
	return b.String()
}
