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
	events := proj.events()
	if len(events) != 1 {
		t.Fatalf("project events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Contract != "orders.OrderPlaced" {
		t.Errorf("contract = %q, want orders.OrderPlaced", ev.Contract)
	}
	if ev.PayloadPkg != "orders" || ev.Payload == nil || ev.Payload.Name != "OrderPlacedPayload" {
		t.Errorf("payload = %s (%v)", ev.PayloadPkg, ev.Payload)
	}
}

// An event payload declared in another package resolves to that package.
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
	if ev.PayloadPkg != "shared" || ev.Payload == nil || ev.Payload.Name != "Envelope" {
		t.Errorf("payload = %s (%v)", ev.PayloadPkg, ev.Payload)
	}
}

func TestContractDecoratorOverridesTheDerivedName(t *testing.T) {
	src := `package orders
type P { id string }
@contract("order.placed.v2")
event OrderPlaced { payload P }`
	expectClean(t, src)
	proj, _ := AnalyzeProject(parseFiles(t, src), Options{})
	if got := proj.events()[0].Contract; got != "order.placed.v2" {
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

// An event payload may be an array of a declared type.
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
	if ev.PayloadPkg != "orders" || ev.Payload == nil || ev.Payload.Name != "OrderPlacedPayload" {
		t.Errorf("element = %s (%v), want the declared type", ev.PayloadPkg, ev.Payload)
	}
}

// The element of an array payload resolves across packages.
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
	if !ev.PayloadArray || ev.PayloadPkg != "shared" || ev.Payload == nil || ev.Payload.Name != "Envelope" {
		t.Errorf("payload = []%s (%v, array=%v)", ev.PayloadPkg, ev.Payload, ev.PayloadArray)
	}
}

// An event and a type may share a name.
func TestEventAndTypeShareANamespaceFreely(t *testing.T) {
	expectClean(t, `package p
type OrderPlaced { id string }
event OrderPlaced { payload OrderPlaced }`)
}

// Two events in different packages with one @contract name collide.
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

// An event decorator on a method, and a method decorator on an event, are misplaced.
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

func TestEventNameCaseError(t *testing.T) {
	d := expectError(t, `package p
type P { id string }
event lowered { payload P }`, CodeDeclNameCase)
	if !strings.Contains(d.Msg, "event name") {
		t.Errorf("msg = %q, want it to name the event site", d.Msg)
	}
}

// Decl lookups return events under EventDecls, never under TypeRefDecls.
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
	for _, d := range pkg.Decls(TypeRefDecls) {
		if d.DeclName() == "PaymentSettled" {
			t.Error("an event is offered in a type position")
		}
	}
	if d := pkg.Decl("PaymentSettled", EventDecls); d == nil {
		t.Error("Decl cannot find a file-level event")
	}
}

// @consumerGroup and @consumeMiddlewares are rejected with their replacements.
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

// @key is rejected with its replacement, WithKey on the publish call.
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

// A misspelt decorator is still reported as unknown.
func TestAnUnrelatedUnknownDecoratorIsStillUnknown(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
@keyy(id)
event E { payload P }`, CodeDecoratorUnknown)
	if !strings.Contains(d.Msg, "unknown decorator @keyy") {
		t.Errorf("msg = %q", d.Msg)
	}
}

// An event payload's generic arguments are checked like a field type's.
func TestEventPayloadGenericArgsChecked(t *testing.T) {
	const decls = `package app
type Page<T> { items T[] }
type Item { id string }
`
	d := expectError(t, decls+`event Listed { payload Page<string, int> }`, CodeGenericArity)
	expectMessage(t, d, "Page expects 1")
	expectError(t, decls+`event Listed { payload Item<string> }`, CodeGenericNonGeneric)
	expectError(t, decls+`event Listed { payload Page<Item?> }`, CodeGenericOptionalArg)
}

// A payload naming an error gets the reference diagnostic alone, bare or
// qualified.
func TestEventPayloadErrorReportedOnce(t *testing.T) {
	diags := analyzeOneFile(t, "package p\nerror NotFound Gone\nevent E { payload Gone }")
	if got := codes(diags); !slices.Equal(got, []string{CodeRefUnknownSymbol}) {
		t.Errorf("bare: want one %s, got %v", CodeRefUnknownSymbol, diags)
	}
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": "package shared\nerror NotFound Gone",
		"app/a.craftgo":    "package app\nevent E { payload shared.Gone }",
	})
	_, diags = AnalyzeProject(files, Options{DesignRoot: root})
	if got := codes(diags); !slices.Equal(got, []string{CodeRefUnknownSymbol}) {
		t.Errorf("qualified: want one %s, got %v", CodeRefUnknownSymbol, diags)
	}
}
