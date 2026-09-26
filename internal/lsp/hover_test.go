package lsp

import (
	"strings"
	"testing"
)

// Hovering `@length` shows the decorator and its field level.
func TestHoverDecorator(t *testing.T) {
	v := mustHoverAt(t, "test.craftgo", testDSL, "length")
	if !strings.Contains(v, "@length") {
		t.Errorf("hover should mention: %q", v)
	}
	if !strings.Contains(v, "field") {
		t.Errorf("hover should mention legal level 'field': %q", v)
	}
}

// Hovering `string` shows the built-in's doc.
func TestHoverBuiltinType(t *testing.T) {
	v := mustHoverAt(t, "test.craftgo", testDSL, "string")
	if !strings.Contains(v, "UTF-8") {
		t.Errorf("string hover should mention UTF-8: %q", v)
	}
}

// Hovering `raw` in `@format(raw)` explains the raw shape, and hovering
// `bytes` points at it.
func TestHoverFormatRawOnABytesField(t *testing.T) {
	const src = `package design

type Hook {
    payload bytes @format(raw)
}
`
	hov := mustHoverAt(t, "test.craftgo", src, "raw")
	if !strings.Contains(hov, "the bytes ARE the value") || !strings.Contains(hov, "wire.Raw") {
		t.Errorf("hovering `raw` did not explain the shape: %q", hov)
	}
	if got := mustHoverAt(t, "test.craftgo", src, "bytes"); !strings.Contains(got, "@format(raw)") {
		t.Errorf("hovering `bytes` does not point at the raw form: %q", got)
	}
}

// Hovering a type reference shows the declaration line and doc.
func TestHoverUserType(t *testing.T) {
	v := hoverAt(t, "", strings.Replace(testDSL, "request  Greeter", "request  Gree"+cursorMark+"ter", 1))
	if !strings.Contains(v, "type Greeter") {
		t.Errorf("hover should include `type Greeter`: %q", v)
	}
	if !strings.Contains(v, "sample type") {
		t.Errorf("hover should include doc comment: %q", v)
	}
}

// A field's name shows the field even when it is spelt like a built-in, a verb or a keyword.
func TestHoverFieldNamedLikeAKeyword(t *testing.T) {
	const src = `package design

type Req {
	file string
	delete bool
	type int
}
`
	for _, name := range []string{"file", "delete", "type"} {
		if v := hoverAt(t, "", strings.Replace(src, "\t"+name+" ", "\t"+name[:1]+cursorMark+name[1:]+" ", 1)); !strings.Contains(v, "field `Req."+name+"`") {
			t.Errorf("hover on field %s = %q, want the field", name, v)
		}
	}
}
