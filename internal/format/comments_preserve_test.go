package format

import (
	"strings"
	"testing"
)

// formatExact requires Format(src) to equal want and to format to itself.
func formatExact(t *testing.T, src, want string) {
	t.Helper()
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if out != want {
		t.Errorf("output mismatch.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
	out2, diags := Format("t.craftgo", out)
	if len(diags) > 0 {
		t.Fatalf("re-parse diagnostics: %v", diags)
	}
	if out2 != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
}

// TestFormatTypeBodyCommentPlacement pins that a type body's docs, section
// comments, closing note and blank lines format to themselves.
func TestFormatTypeBodyCommentPlacement(t *testing.T) {
	canonical := `package demo

// User doc comment attached to the decl.
type User {
	// Section: Identity

	id    string @path
	name  string

	// Section: Contact
	email string

	// Section: floating with blank lines around

	phone string

	// closing note before brace
}

// Between-decls floating block.

// Order doc.
type Order {
	sku string // trailing note
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatServiceBodyCommentPlacement pins that comments in service and
// method bodies, including trailing and closing-brace notes, format to themselves.
func TestFormatServiceBodyCommentPlacement(t *testing.T) {
	canonical := `package demo

type Req {
	id string @path
}

type Resp {
	ok bool
}

service Things {
	// section: reads

	// doc for list
	get list /things {
		// above request
		request  Req
		response Resp // trailing on response

		// above method rbrace
	}

	// detached inside service, blank both sides

	post create /things {
		request  Req

		// detached inside method body

		response Resp
	}
	// closing note above service rbrace
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatBlankRunsCollapse pins that a run of blank lines collapses to one.
func TestFormatBlankRunsCollapse(t *testing.T) {
	src := `package demo

type User {
	id string



	// far-away section


	name string
}
`
	want := `package demo

type User {
	id   string

	// far-away section

	name string
}
`
	formatExact(t, src, want)
}

// TestFormatNoBlankInsertedAfterBrace pins that a comment directly under an
// opening brace gets no blank line above it.
func TestFormatNoBlankInsertedAfterBrace(t *testing.T) {
	canonical := `package demo

type User {
	// Section: Identity

	id string
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatEmptyMethodTrailingNote pins the trailing comment after an empty
// method body `{}`.
func TestFormatEmptyMethodTrailingNote(t *testing.T) {
	canonical := `package demo

@passthrough
service Raw {
	get stream /stream {}  // bypasses the JSON codec
}
`
	out, diags := Format("t.craftgo", canonical)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if !strings.Contains(out, "// bypasses the JSON codec") {
		t.Errorf("empty-body trailing note dropped:\n%s", out)
	}
	out2, _ := Format("t.craftgo", out)
	if out2 != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
}

// TestFormatTopLevelBlocksKeepBlankSeparation pins that two file-scope comment
// blocks separated by a blank line stay two blocks.
func TestFormatTopLevelBlocksKeepBlankSeparation(t *testing.T) {
	canonical := `package demo

// First commented-out probe:
//
// scalar OptionalEmail string? @format(email)

// Second block explaining the decl below.
type User {
	id string
}

// end of file note
`
	formatExact(t, canonical, canonical)
}

// TestFormatDetachedCommentAbovePackage pins that a comment block separated
// from the package line by a blank line stays detached from it.
func TestFormatDetachedCommentAbovePackage(t *testing.T) {
	canonical := `// file prologue, not the package doc

package demo

type User {
	id string
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatMethodGroupingPreserved pins that adjacent methods stay adjacent
// and blank-separated methods keep one blank line.
func TestFormatMethodGroupingPreserved(t *testing.T) {
	canonical := `package demo

service Pings {
	get a /a {}
	get b /b {}

	get c /c {}
}
`
	formatExact(t, canonical, canonical)
}
