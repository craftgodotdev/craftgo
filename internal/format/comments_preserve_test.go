// Round-trip tests for comment placement and blank-line grouping: canonical
// sources must format to themselves byte-for-byte, and messy blank runs must
// collapse to the canonical single blank without moving any comment.
package format

import (
	"strings"
	"testing"
)

// formatExact runs Format and requires the output to equal want exactly,
// then re-formats the output to prove idempotency.
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

// TestFormatTypeBodyCommentPlacement pins the full comment vocabulary of a
// type body in canonical form: attached docs, section dividers (attached and
// blank-isolated), closing notes, and the blank-line grouping around them.
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

// TestFormatServiceBodyCommentPlacement pins comments inside service and
// method bodies: section blocks between methods, comments above the
// request/response lines, trailing notes on those lines, comments above a
// method's closing brace, and a closing note attached to the service brace.
// Before the parser-side FreeComment harvest these were dropped or moved to
// the end of the file.
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

// TestFormatBlankRunsCollapse pins that runs of two or more blank lines
// collapse to a single blank while zero-blank groupings stay tight.
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

// TestFormatNoBlankInsertedAfterBrace pins that a section comment sitting
// directly under the opening brace stays there - the formatter must not
// insert a blank line between `{` and the first member.
func TestFormatNoBlankInsertedAfterBrace(t *testing.T) {
	canonical := `package demo

type User {
	// Section: Identity

	id string
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatEmptyMethodTrailingNote pins the `// note` after an empty
// method body literal, which the printer used to drop.
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

// TestFormatTopLevelBlocksKeepBlankSeparation pins that two file-scope
// comment blocks separated by a blank line stay two blocks - the retired
// loose-map merge used to fuse them with a bogus `//` line.
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

// TestFormatDetachedCommentAbovePackage pins a comment block separated from
// the package line by a blank: it stays detached instead of being glued to
// (and later absorbed as) the package doc. The old loose-map never flushed
// blocks anchored to the package line, silently dropping them.
func TestFormatDetachedCommentAbovePackage(t *testing.T) {
	canonical := `// file prologue, not the package doc

package demo

type User {
	id string
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatMethodGroupingPreserved pins that methods written back-to-back
// stay tight while blank-separated methods keep one blank - the formatter
// preserves the author's grouping instead of imposing one.
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
