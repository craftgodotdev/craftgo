package format

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A file mixing a service with contracts must survive the format round
// trip unchanged, with doc comments, decorators, body comments, trailing
// notes and blank-line grouping all preserved.
func TestFormatServiceWithEventsIsStable(t *testing.T) {
	src := `package orders

type OrderPlacedPayload {
	orderId string
	total   int64
}

// Emitted once an order is accepted.
event OrderPlaced {
	// the shape a listener receives
	payload OrderPlacedPayload // versioned with the contract
}  // end of contract

@prefix("/v1")
service OrderService {
	post PlaceOrder /orders {
		request  PlaceOrderReq
		response Order
	}
}
`
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if out != src {
		t.Errorf("canonical source reformatted.\n--- want ---\n%s\n--- got ---\n%s", src, out)
	}
	again, diags := Format("t.craftgo", out)
	if len(diags) > 0 {
		t.Fatalf("reformat diagnostics: %v", diags)
	}
	if again != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}
}

// A compact one-line declaration is a documented feature; the formatter
// expands it to canonical form exactly as it does for a method.
func TestFormatExpandsCompactEvent(t *testing.T) {
	src := `package p
event E { payload P }
`
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if want := "event E {\n\tpayload P\n}"; !strings.Contains(out, want) {
		t.Errorf("formatted output missing %q:\n%s", want, out)
	}
}

// An array payload keeps its `[]`: the suffix lives on the clause, not on
// the type reference the printer walks, so a missing case silently
// rewrites the contract into one carrying a single object.
func TestFormatRoundTripsAnArrayPayload(t *testing.T) {
	src := `package orders

type OrderPlacedPayload {
	orderId string
}

event BatchPlaced {
	payload OrderPlacedPayload[]
}
`
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if out != src {
		t.Errorf("not round-tripped.\n--- want ---\n%s\n--- got ---\n%s", src, out)
	}
	again, _ := Format("t.craftgo", out)
	if again != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}
}

// A contract declared outside a service is a top-level declaration; the
// printer must round-trip it. A missing case in the decl switch drops the
// declaration silently, which is the failure mode the formatter exists to
// prevent.
func TestFormatRoundTripsFileLevelEvent(t *testing.T) {
	src := `package upstream

type P {
	id string
}

// Published elsewhere.
@contract("payments.settled.v1")
event PaymentSettled {
	payload P
}

service LedgerService {
	post Record /records {
		request  P
		response P
	}
}
`
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if out != src {
		t.Errorf("not round-tripped.\n--- want ---\n%s\n--- got ---\n%s", src, out)
	}
	again, _ := Format("t.craftgo", out)
	if again != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}
}

// Every declaration kind must reach a printer case. A missing case is
// silent data loss - the declaration, or its inter-decorator comments,
// vanish on `craftgo fmt` with no diagnostic. This walks the registry in
// [ast.AllDeclKinds] so a new kind fails here rather than in a user's
// editor.
func TestEveryDeclKindPrints(t *testing.T) {
	for _, d := range ast.AllDeclKinds() {
		var buf bytes.Buffer
		p := &Printer{w: &buf}
		p.Decl(d)
		if got := buf.String(); strings.TrimSpace(got) == "" {
			t.Errorf("%T printed nothing - the printer's Decl switch is missing a case", d)
		}
	}
}

// The same registry guards the comment-chain walker: a declaration whose
// decorators print as a vertical chain must be spanned, or every comment
// written between them is dropped.
//
// ScalarDecl is the one exception and it is deliberate: it prints its
// decorators inline (`scalar ID string @minLength(1)`), so there is no
// chain to span. A comment interleaved with an inline decorator list has
// nowhere to land in the canonical form and is lost - a separate,
// pre-existing gap, not one this walker can close.
func TestEveryDecoratedDeclKindHasACommentChain(t *testing.T) {
	inlineDecorators := map[string]bool{"*ast.ScalarDecl": true}
	dec := []*ast.Decorator{{Name: "doc", Pos: ast.Pos{Line: 1}}}
	for _, d := range ast.AllDeclKinds() {
		withDecorators(d, dec)
		kind := fmt.Sprintf("%T", d)
		spans := chainSpans(&ast.File{Decls: []ast.Decl{d}})
		if len(spans) == 0 && !inlineDecorators[kind] {
			t.Errorf("%s contributes no comment chain - comments between its decorators are dropped", kind)
		}
		if len(spans) > 0 && inlineDecorators[kind] {
			t.Errorf("%s now spans a chain; drop it from the inline exception", kind)
		}
	}
}

// withDecorators attaches decs to whichever declaration kind d is.
func withDecorators(d ast.Decl, decs []*ast.Decorator) {
	switch v := d.(type) {
	case *ast.TypeDecl:
		v.Decorators = decs
	case *ast.EnumDecl:
		v.Decorators = decs
	case *ast.ErrorDecl:
		v.Decorators = decs
	case *ast.ScalarDecl:
		v.Decorators = decs
	case *ast.MiddlewareDecl:
		v.Decorators = decs
	case *ast.ServiceDecl:
		v.Decorators = decs
	case *ast.EventDecl:
		v.Decorators = decs
	}
}
