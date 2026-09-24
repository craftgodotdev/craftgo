package format

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A canonical file with an event and a service, comments included, formats to itself.
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

// A one-line event declaration expands to canonical form.
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

// An event's array payload keeps its `[]` suffix.
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

// A documented @contract event beside a service formats to itself.
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

// Every declaration kind in [ast.AllDeclKinds] prints something.
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

// Every declaration kind but ScalarDecl, which prints its decorators inline,
// has a decorator comment chain.
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
