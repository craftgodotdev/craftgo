package lsp

import (
	"path/filepath"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// "😀" (U+1F600) is a supplementary character: 4 UTF-8 bytes, 1 rune, but
// TWO UTF-16 code units. "é" (U+00E9) is BMP: 2 UTF-8 bytes, 1 rune, 1
// UTF-16 unit. These are the two cases the byte/rune/UTF-16 confusions
// drifted on.

func TestUTF16Len(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"café", 4}, // é is 1 UTF-16 unit
		{"a😀b", 4},  // emoji is 2 units → 1+2+1
		{"😀😀", 4},   // 2 emoji → 4 units
		{"tên người", 9},
	}
	for _, c := range cases {
		if got := utf16Len(c.s); got != c.want {
			t.Errorf("utf16Len(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestOffsetFromLSP(t *testing.T) {
	src := "a😀b\nxy"
	cases := []struct {
		line, char uint32
		wantOffset int
	}{
		{0, 0, 0}, // start
		{0, 1, 1}, // after 'a'
		{0, 3, 5}, // after the emoji (char 1 + 2 units) → byte 1+4
		{0, 4, 6}, // after 'b'
		{1, 0, 6}, // start of line 1 (after '\n' at byte 5... 'a'1 '😀'4 'b'1 = 6, '\n' at 6, line1 starts at 7)
	}
	// recompute: a(1)😀(4)b(1)=6 bytes, then '\n' at byte 6, line 1 starts byte 7.
	cases[4].wantOffset = 7
	for _, c := range cases {
		if got := offsetFromLSP(src, c.line, c.char); got != c.wantOffset {
			t.Errorf("offsetFromLSP(%q, %d, %d) = %d, want %d", src, c.line, c.char, got, c.wantOffset)
		}
	}
}

func TestUTF16Position(t *testing.T) {
	// Single line "a😀b": rune column 3 (1-indexed) is 'b', which sits at
	// UTF-16 character 3 (a=1 + emoji=2). A naive rune→unit copy would
	// report character 2.
	src := "a😀b"
	got := utf16Position(src, lexer.Position{Line: 1, Column: 3})
	if got.Line != 0 || got.Character != 3 {
		t.Errorf("utf16Position rune col 3 over %q = (%d,%d), want (0,3)", src, got.Line, got.Character)
	}
	// On line 2, the converter must use line 2's text, not line 1's.
	multi := "😀\nabX"
	got = utf16Position(multi, lexer.Position{Line: 2, Column: 3})
	if got.Line != 1 || got.Character != 2 {
		t.Errorf("utf16Position line2 col3 = (%d,%d), want (1,2)", got.Line, got.Character)
	}
	// No source / out-of-range falls back to rune column verbatim.
	fb := utf16Position("", lexer.Position{Line: 5, Column: 4})
	if fb.Line != 4 || fb.Character != 3 {
		t.Errorf("utf16Position fallback = (%d,%d), want (4,3)", fb.Line, fb.Character)
	}
}

func TestIsUnderDesignRoot(t *testing.T) {
	root := filepath.Join("/proj", "design")
	cases := []struct {
		p    string
		want bool
	}{
		{filepath.Join(root, "a.craftgo"), true},
		{filepath.Join(root, "sub", "b.craftgo"), true},
		{root, true}, // exact
		{filepath.Join("/proj", "design2", "x.craftgo"), false}, // sibling sharing a name prefix
		{filepath.Join("/proj", "design_backup", "x.craftgo"), false},
		{filepath.Join("/other", "x.craftgo"), false},
	}
	for _, c := range cases {
		if got := isUnderDesignRoot(c.p, root); got != c.want {
			t.Errorf("isUnderDesignRoot(%q, %q) = %v, want %v", c.p, root, got, c.want)
		}
	}
	if isUnderDesignRoot("/anything", "") {
		t.Error("empty design root must never match")
	}
}

func TestWholeDocumentRangeUTF16(t *testing.T) {
	cases := []struct {
		src      string
		wantLine uint32
		wantChar uint32
	}{
		{"abc", 0, 3},
		{"a\nbé", 1, 2}, // last line "bé": b=1 + é=1 unit
		{"a\nb😀", 1, 3}, // last line "b😀": b=1 + emoji=2 units (byte len would be 5)
		{"x\n", 1, 0},   // trailing newline → empty last line
	}
	for _, c := range cases {
		r := wholeDocumentRange(c.src)
		if r.End.Line != c.wantLine || r.End.Character != c.wantChar {
			t.Errorf("wholeDocumentRange(%q).End = (%d,%d), want (%d,%d)", c.src, r.End.Line, r.End.Character, c.wantLine, c.wantChar)
		}
	}
}
