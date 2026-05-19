package lexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ----- Position -----

func TestPositionString(t *testing.T) {
	if got := (Position{Filename: "x.craftgo", Line: 2, Column: 3}).String(); got != "x.craftgo:2:3" {
		t.Errorf("with filename: got %q", got)
	}
	if got := (Position{Line: 1, Column: 1}).String(); got != "1:1" {
		t.Errorf("without filename: got %q", got)
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

// ----- Kind -----

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

// ----- Token -----

func TestTokenString(t *testing.T) {
	tok := Token{Kind: Ident, Text: "foo", Pos: Position{Line: 1, Column: 1}}
	s := tok.String()
	if !strings.Contains(s, "Ident") || !strings.Contains(s, "foo") {
		t.Errorf("got %q", s)
	}
}

// ----- Diagnostic -----

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
		{Severity(99), "error"}, // unknown falls back to error
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

// ----- Helpers -----

func first(t *testing.T, src string) Token {
	t.Helper()
	return New("", src).Next()
}

// ----- Basic flow -----

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

// ----- Keywords -----

func TestKeywords(t *testing.T) {
	cases := map[string]Kind{
		"package": KwPackage, "import": KwImport,
		"type": KwType, "enum": KwEnum, "error": KwError,
		"scalar": KwScalar, "service": KwService, "extend": KwExtend,
		"middleware": KwMiddleware, "request": KwRequest, "response": KwResponse,
		"map": KwMap,
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

// ----- Identifiers -----

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

// ----- Numbers -----

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

// ----- Strings -----

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
	for _, c := range []string{`"\u{1F600}"`, `"\u{a}"`, `"\u{123456}"`} {
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
	}
	for _, c := range cases {
		if first(t, c).Kind != Error {
			t.Errorf("%q", c)
		}
	}
}

// ----- Raw string -----

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

// ----- Punctuation -----

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

// ----- Position tracking -----

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

// ----- Tokenize -----

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

// ----- Golden file -----

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
