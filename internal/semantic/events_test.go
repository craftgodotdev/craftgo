package semantic

import (
	"slices"
	"strings"
	"testing"
)

const ordersDesign = `package orders
type OrderPlacedPayload {
	orderId string
	total   int64
	tags    string[]
	nested  Nested
}
type Nested { a string }
event OrderPlaced { payload OrderPlacedPayload }`

func TestEventResolvesContractAndPayload(t *testing.T) {
	pkg := expectClean(t, ordersDesign)
	if len(pkg.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(pkg.Events))
	}
	proj, _ := AnalyzeProject(parseFiles(t, ordersDesign), Options{})
	events := proj.Events()
	if len(events) != 1 {
		t.Fatalf("project events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Contract != "orders.OrderPlaced" {
		t.Errorf("contract = %q, want orders.OrderPlaced", ev.Contract)
	}
	if ev.Package != "orders" {
		t.Errorf("home = %s", ev.Package)
	}
	if ev.PayloadPkg != "orders" || ev.PayloadName != "OrderPlacedPayload" || ev.Payload == nil {
		t.Errorf("payload = %s.%s (%v)", ev.PayloadPkg, ev.PayloadName, ev.Payload)
	}
}

// A payload declared in another package resolves to that package, which
// is what a target needs to import the type from the right place.
func TestEventPayloadResolvesAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
type Envelope { id string }`,
		"orders/orders.craftgo": `package orders
event OrderPlaced { payload shared.Envelope }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	expectNoDiags(t, diags)
	ev, ok := proj.LookupEvent("orders", "OrderPlaced")
	if !ok {
		t.Fatal("event did not resolve")
	}
	if ev.PayloadPkg != "shared" || ev.PayloadName != "Envelope" || ev.Payload == nil {
		t.Errorf("payload = %s.%s (%v)", ev.PayloadPkg, ev.PayloadName, ev.Payload)
	}
}

func TestContractDecoratorOverridesTheDerivedName(t *testing.T) {
	src := `package orders
type P { id string }
@contract("order.placed.v2")
event OrderPlaced { payload P }`
	expectClean(t, src)
	proj, _ := AnalyzeProject(parseFiles(t, src), Options{})
	if got := proj.Events()[0].Contract; got != "order.placed.v2" {
		t.Errorf("contract = %q", got)
	}
}

func TestEventRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		msg  string
	}{
		{
			name: "payload missing",
			src:  "package p\nevent E {}",
			code: CodeEventPayloadMissing,
			msg:  "has no payload",
		},
		{
			name: "payload is not a struct",
			src:  "package p\nenum E1 { A }\nevent E { payload E1 }",
			code: CodeEventPayloadKind,
			msg:  "not a struct type",
		},
		{
			// An array payload resolves its element exactly as a scalar
			// one does, so an array of an enum is refused for the same
			// reason the enum itself is.
			name: "array payload of a non-struct",
			src:  "package p\nenum E1 { A }\nevent E { payload E1[] }",
			code: CodeEventPayloadKind,
			msg:  "not a struct type",
		},
		{
			name: "payload is a primitive",
			src:  "package p\nevent E { payload string }",
			code: CodeEventPayloadKind,
			msg:  "not a struct type",
		},
		{
			name: "array payload of a primitive",
			src:  "package p\nevent E { payload string[] }",
			code: CodeEventPayloadKind,
			msg:  "not a struct type",
		},
		{
			name: "empty contract name",
			src:  `package p` + "\n" + `type P { id string }` + "\n" + `@contract("")` + "\n" + `event E { payload P }`,
			code: CodeEventContractFormat,
			msg:  "without whitespace",
		},
		{
			name: "duplicate event name in a package",
			src: `package p
type P { id string }
event E { payload P }
event E { payload P }`,
			code: CodeEventDuplicate,
			msg:  "a listener names an event by this identifier",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := expectDiag(t, c.src, c.code)
			if !strings.Contains(d.Msg, c.msg) {
				t.Errorf("msg = %q, want it to contain %q", d.Msg, c.msg)
			}
		})
	}
}

// A contract may carry an array of a declared type: the body on the wire
// is a JSON array, and everything else about the payload - which package
// the type lives in, which declaration it is - resolves exactly as a
// single one does.
func TestEventPayloadMayBeAnArrayOfAType(t *testing.T) {
	src := `package orders
type OrderPlacedPayload { orderId string }
event BatchPlaced { payload OrderPlacedPayload[] }`
	expectClean(t, src)
	proj, _ := AnalyzeProject(parseFiles(t, src), Options{})
	ev, ok := proj.LookupEvent("orders", "BatchPlaced")
	if !ok {
		t.Fatal("event did not resolve")
	}
	if !ev.PayloadArray {
		t.Error("PayloadArray is false - the contract reads as a single payload")
	}
	if ev.PayloadPkg != "orders" || ev.PayloadName != "OrderPlacedPayload" || ev.Payload == nil {
		t.Errorf("element = %s.%s (%v), want the declared type", ev.PayloadPkg, ev.PayloadName, ev.Payload)
	}
}

// The element of an array payload resolves across packages too - the
// array suffix says nothing about where the type lives.
func TestArrayPayloadResolvesAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
type Envelope { id string }`,
		"orders/orders.craftgo": `package orders
event Batch { payload shared.Envelope[] }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	expectNoDiags(t, diags)
	ev, ok := proj.LookupEvent("orders", "Batch")
	if !ok {
		t.Fatal("event did not resolve")
	}
	if !ev.PayloadArray || ev.PayloadPkg != "shared" || ev.PayloadName != "Envelope" || ev.Payload == nil {
		t.Errorf("payload = []%s.%s (%v, array=%v)", ev.PayloadPkg, ev.PayloadName, ev.Payload, ev.PayloadArray)
	}
}

// An event and its payload may share a name: they live in separate
// namespaces, and naming the contract after the shape it carries is the
// common case.
func TestEventAndTypeShareANamespaceFreely(t *testing.T) {
	expectClean(t, `package p
type OrderPlaced { id string }
event OrderPlaced { payload OrderPlaced }`)
}

// Two events in different packages may resolve to one contract name only
// through `@contract`; a listener could not tell them apart on the wire,
// so it is rejected.
func TestContractCollisionAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/a.craftgo": `package a
type P { id string }
@contract("shared.Thing")
event One { payload P }`,
		"b/b.craftgo": `package b
type Q { id string }
@contract("shared.Thing")
event Two { payload Q }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeEventContractCollision) == nil {
		t.Fatalf("want %s, got %v", CodeEventContractCollision, codes(diags))
	}
}

// Decorator placement is registry-driven: a method decorator on an event
// (or the reverse) is rejected by the same pass that guards every other
// site.
func TestEventDecoratorPlacement(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service S {
	@contract("x.v1")
	get Read /r { response P }
}`, CodeDecoratorPlacement)
	if !strings.Contains(d.Msg, "@contract") || !strings.Contains(d.Msg, "method") {
		t.Errorf("msg = %q", d.Msg)
	}
	d = expectDiag(t, `package p
type P { id string }
@timeout(5s)
event E { payload P }`, CodeDecoratorPlacement)
	if !strings.Contains(d.Msg, "@timeout") || !strings.Contains(d.Msg, "event") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestEventNameCaseWarning(t *testing.T) {
	d := expectWarning(t, `package p
type P { id string }
event lowered { payload P }`, CodeDeclNameCase)
	if !strings.Contains(d.Msg, "event name") {
		t.Errorf("msg = %q, want it to name the event site", d.Msg)
	}
}

// An event is a declaration kind the lookup can yield - otherwise
// completion and go-to-definition cannot reach a contract.
func TestEventsAreALookupKind(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"upstream/upstream.craftgo": `package upstream
type P { id string }
@contract("x.v1")
event PaymentSettled { payload P }
event Shipped { payload P }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	expectNoDiags(t, diags)
	pkg := proj.Packages["upstream"]
	if pkg == nil {
		t.Fatal("package missing")
	}
	var names []string
	for _, d := range pkg.Decls(EventDecls) {
		names = append(names, d.DeclName())
	}
	for _, want := range []string{"PaymentSettled", "Shipped"} {
		if !slices.Contains(names, want) {
			t.Errorf("Decls(EventDecls) missing %q: %v", want, names)
		}
	}
	// An event is a contract, never a type shape.
	for _, d := range pkg.Decls(TypeShapeDecls) {
		if d.DeclName() == "PaymentSettled" {
			t.Error("an event is offered in a type-shape position")
		}
	}
	if d := pkg.Decl("PaymentSettled", EventDecls); d == nil {
		t.Error("Decl cannot find a file-level event")
	}
}

// The decorators that named a broker group and a consume chain are gone.
// A design still carrying one is told what replaced it rather than that
// the name was never a decorator.
func TestRemovedEventDecoratorsAreRejectedWithTheirMigration(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "consumerGroup",
			src: `package p
type P { id string }
@consumerGroup("shared")
event Placed { payload P }`,
			want: "Subscribe(bus",
		},
		{
			name: "consumeMiddlewares",
			src: `package p
type P { id string }
@consumeMiddlewares(Retry)
event Placed { payload P }`,
			want: "bus.Use",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := expectDiag(t, c.src, CodeDecoratorRemoved)
			expectMessage(t, d, c.want)
		})
	}
}

// A design still carrying `@key` is told what replaced it. "Unknown
// decorator" would be true and useless: the author has to learn that the
// key moved to the publish call, and the diagnostic is where they look.
func TestAKeyDecoratorIsRejectedWithItsMigration(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
@key(id)
event E { payload P }`, CodeDecoratorRemoved)
	for _, want := range []string{"@key", "no longer", "WithKey"} {
		if !strings.Contains(d.Msg, want) {
			t.Errorf("migration message does not mention %q: %s", want, d.Msg)
		}
	}
}

// A decorator that never existed keeps the message it had: the removed
// set is not a catch-all for typos.
func TestAnUnrelatedUnknownDecoratorIsStillUnknown(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
@keyy(id)
event E { payload P }`, CodeDecoratorUnknown)
	if !strings.Contains(d.Msg, "unknown decorator @keyy") {
		t.Errorf("msg = %q", d.Msg)
	}
}
