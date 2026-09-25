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
} // end of contract

@prefix("/v1")
service OrderService {
	post PlaceOrder /orders {
		request  PlaceOrderReq
		response Order
	}
}
`
	formatExact(t, src, src)
}

// A one-line event declaration expands to canonical form.
func TestFormatExpandsCompactEvent(t *testing.T) {
	src := `package p
event E { payload P }
`
	out := formatStable(t, src)
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
	formatExact(t, src, src)
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
	formatExact(t, src, src)
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

// Every declaration kind keeps the comments inside its decorator chain and
// after its decorators in place.
func TestEveryDeclKindKeepsItsChainComments(t *testing.T) {
	decls := map[string]string{
		"*ast.TypeDecl":       "type T {\n\ta string\n}\n",
		"*ast.EnumDecl":       "enum E {\n\tA\n}\n",
		"*ast.ErrorDecl":      "error NotFound Gone\n",
		"*ast.ScalarDecl":     "scalar S string\n",
		"*ast.MiddlewareDecl": "middleware M\n",
		"*ast.ServiceDecl":    "service S {\n\tget A /a {}\n}\n",
		"*ast.EventDecl":      "event E {\n\tpayload T\n}\n",
	}
	for _, d := range ast.AllDeclKinds() {
		kind := fmt.Sprintf("%T", d)
		decl, ok := decls[kind]
		if !ok {
			t.Errorf("%s has no case", kind)
			continue
		}
		t.Run(kind, func(t *testing.T) {
			src := "package p\n\n// doc\n@a // after a\n// above b\n@b\n// above the keyword\n" + decl
			formatExact(t, src, src)
		})
	}
}
