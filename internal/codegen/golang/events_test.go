package golang

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/codegen/docs"
	"github.com/craftgodotdev/craftgo/internal/config"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// goEventsOut is the Go target's destination in [eventsConfig].
const goEventsOut = "./internal/events"

// eventsConfig is a manifest with both the Go target and the AsyncAPI
// projection enabled.
func eventsConfig() *config.Config {
	cfg := &config.Config{
		Package: "example.com/app",
		Output: config.Output{
			Types:      "./internal/types",
			Transport:  "./internal/transport",
			Routes:     "./internal/routes",
			Service:    "./internal/service",
			Svccontext: "./svccontext/svccontext.go",
			Wiring:     "./internal/wiring",
			Middleware: "./internal/middleware",
			Config:     "./config",
			OpenAPI:    "./docs/openapi.yaml",
			Main:       "-",
			FileCase:   config.FileCaseSnake,
		},
		OpenAPI: config.OpenAPI{Title: "Events", Version: "1.0.0"},
		Events: config.Events{
			Targets:  []config.EventTarget{{Lang: config.LangGo, Out: goEventsOut}},
			AsyncAPI: "./docs/asyncapi.yaml",
		},
	}
	return cfg
}

// analyzeProject parses each source, analyses them as one project, and
// fails on any error-severity diagnostic.
func analyzeProject(t *testing.T, sources ...string) *semantic.Project {
	t.Helper()
	files := make([]*ast.File, 0, len(sources))
	for i, src := range sources {
		p := craftparser.New("test.craftgo", src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse errors in source %d: %v", i, d)
		}
		files = append(files, f)
	}
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{})
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("semantic errors: %v", diags)
		}
	}
	return proj
}

// genEvents runs the Go event target plus the AsyncAPI projection into a
// temp dir and returns the dir.
func genEvents(t *testing.T, proj *semantic.Project, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	if err := GenerateEventTarget(proj, cfg, dir, goEventsOut); err != nil {
		t.Fatalf("generate events: %v", err)
	}
	// The svccontext Events container is written for every project, so it
	// comes from the main pipeline rather than this target.
	if err := generateSvccontextEvents(proj, cfg, dir); err != nil {
		t.Fatalf("generate svccontext events: %v", err)
	}
	// The AsyncAPI assertions below describe the same contracts this
	// target publishes, so the fixture renders both.
	if err := docs.GenerateAsyncAPI(proj, cfg, dir); err != nil {
		t.Fatalf("generate asyncapi: %v", err)
	}
	return dir
}

func readGen(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

const ordersSrc = `package orders
scalar OrderID string @minLength(1)
enum Channel { Web  Mobile }
type OrderPlacedPayload {
	orderId OrderID
	total   int64
	channel Channel
}
service OrderService {
	event OrderPlaced { payload OrderPlacedPayload }
}`

const notifySrc = `package notify
service NotificationService {
	consume SendReceipt { event orders.OrderPlaced }
}`

func TestGeneratePublisherDerivesFromTheContract(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	dir := genEvents(t, proj, eventsConfig())
	got := readGen(t, dir, "internal/events/order_service/publisher.go")
	mustParseGo(t, got)
	mustContainAll(t, got,
		`OrderPlacedContract = "orders.OrderPlaced"`,
		"func NewPublisher(bus *craftevents.Bus, opts ...craftevents.PublishOption) *Publisher",
		"func (p *Publisher) PublishOrderPlaced(ctx context.Context, payload *types.OrderPlacedPayload, opts ...craftevents.PublishOption) error",
		"p.bus.Publish(ctx, OrderPlacedContract, payload, craftevents.JoinOptions(p.opts, opts)...)",
	)
	// The publisher must not reach for the service context; svccontext
	// holds the publishers, so the dependency runs one way only.
	mustContainNone(t, got, "svccontext")
}

func TestGenerateConsumerHandlerAndStub(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, notifySrc)
	dir := genEvents(t, proj, eventsConfig())

	handler := readGen(t, dir, "internal/transport/notification_service_consumers.go")
	mustParseGo(t, handler)
	mustContainAll(t, handler,
		"type NotificationServiceConsumers struct",
		"func NewNotificationServiceConsumers(svcCtx *svccontext.ServiceContext) *NotificationServiceConsumers",
		"func (h *NotificationServiceConsumers) SendReceipt(ctx context.Context, payload *orders.OrderPlacedPayload) error",
		"NewSendReceiptConsumer(ctx, h.svcCtx).SendReceipt(payload)",
		`orders "example.com/app/internal/types/orders"`,
	)
	// Decoding and validating is the contract's job, spelled once in
	// consumers.go; the application half only dispatches.
	mustContainNone(t, handler, "bus.Decode", "payload.Validate()", "craftevents.Subscription")

	contract := readGen(t, dir, "internal/events/notification_service/consumers.go")
	mustParseGo(t, contract)
	mustContainAll(t, contract,
		"SendReceipt(ctx context.Context, payload *orders.OrderPlacedPayload) error",
		"bus.Decode(msg, &payload)",
		"payload.Validate()",
		"h.SendReceipt(ctx, &payload)",
	)

	stub := readGen(t, dir, "internal/service/notification_service/send_receipt.go")
	mustParseGo(t, stub)
	mustContainAll(t, stub,
		"Scaffold generated by craftgo",
		"func (l *SendReceiptConsumer) SendReceipt(payload *orders.OrderPlacedPayload) error",
	)
}

// The subscription carries the broker identity the semantic layer
// resolved; the target never derives one.
func TestSubscriptionCarriesTheResolvedGroup(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, notifySrc)
	dir := genEvents(t, proj, eventsConfig())
	mustContainAll(t, readGen(t, dir, "internal/events/notification_service/consumers.go"),
		`Consumer: "SendReceipt"`,
		`Group:    "notify-NotificationService-SendReceipt"`,
	)
}

// One group over two contracts inside one service: the feature, and the
// shape a target has to render without deriving anything of its own.
func TestAuthoredGroupSpansContracts(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, `package orders
service Extra { event OrderShipped { payload OrderPlacedPayload } }`, `package notify
@consumerGroup("order-worker")
service NotificationService {
	consume SendReceipt { event orders.OrderPlaced }
	@consumerGroup("shipping-worker")
	consume TrackShipment { event orders.OrderShipped }
}`)
	dir := genEvents(t, proj, eventsConfig())
	mustContainAll(t, readGen(t, dir, "internal/events/notification_service/consumers.go"),
		`Group:    "order-worker"`,
		`Group:    "shipping-worker"`,
	)
}

// Two service names may fold to one handler file name under the file case.
// When only one of them consumes, the design is legal and the sweep for
// handler sets of non-consuming services must not delete the other's.
func TestHandlerSweepKeepsAFoldedSiblingsFile(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, `package notify
type Ping { id string }
service UserAPI {
	consume SendReceipt { event orders.OrderPlaced }
}
service UserApi {
	get Ping /ping { response Ping }
}`)
	dir := genEvents(t, proj, eventsConfig())

	handler := readGen(t, dir, "internal/transport/user_api_consumers.go")
	mustParseGo(t, handler)
	mustContainAll(t, handler, "type UserAPIConsumers struct")

	umbrella := readGen(t, dir, "internal/transport/events.go")
	mustContainAll(t, umbrella, "NewUserAPIConsumers(svcCtx)")
}

// A service that loses its last consumer loses its handler set with it.
func TestHandlerSweepRemovesAServiceThatStopsConsuming(t *testing.T) {
	cfg := eventsConfig()
	dir := t.TempDir()
	if err := GenerateEventTarget(analyzeProject(t, ordersSrc, notifySrc), cfg, dir, goEventsOut); err != nil {
		t.Fatalf("first gen: %v", err)
	}
	handler := filepath.Join(dir, "internal", "transport", "notification_service_consumers.go")
	if _, err := os.Stat(handler); err != nil {
		t.Fatalf("handler not written: %v", err)
	}
	quiet := `package notify
type Ping { id string }
service NotificationService {
	get Ping /ping { response Ping }
}`
	if err := GenerateEventTarget(analyzeProject(t, ordersSrc, quiet), cfg, dir, goEventsOut); err != nil {
		t.Fatalf("second gen: %v", err)
	}
	if _, err := os.Stat(handler); !os.IsNotExist(err) {
		t.Errorf("handler survived the service dropping its consumers (err=%v)", err)
	}
}

// SubscribeAll calls Subscriptions on every consuming service's event
// package, so that package must have been written first. The dependency
// is an ordering constraint inside GenerateEventTarget, asserted here
// rather than left to a comment.
func TestSubscribeAllTargetsTheGeneratedContract(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, notifySrc)
	dir := genEvents(t, proj, eventsConfig())

	contract := readGen(t, dir, "internal/events/notification_service/consumers.go")
	mustContainAll(t, contract,
		"func Subscriptions(bus *craftevents.Bus, h Consumers) []craftevents.Subscription",
	)
	umbrella := readGen(t, dir, "internal/transport/events.go")
	mustContainAll(t, umbrella,
		"notificationserviceevents.Subscriptions(bus, NewNotificationServiceConsumers(svcCtx))",
	)
}

// The consumer stub carries business logic, so a second gen run must
// leave it alone while the subscription regenerates.
func TestConsumerStubIsGenOnce(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, notifySrc)
	cfg := eventsConfig()
	dir := t.TempDir()
	if err := GenerateEventTarget(proj, cfg, dir, goEventsOut); err != nil {
		t.Fatalf("first gen: %v", err)
	}
	stub := filepath.Join(dir, "internal", "service", "notification_service", "send_receipt.go")
	edited := "package notify\n\n// hand-written\n"
	if err := os.WriteFile(stub, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateEventTarget(proj, cfg, dir, goEventsOut); err != nil {
		t.Fatalf("second gen: %v", err)
	}
	b, err := os.ReadFile(stub)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != edited {
		t.Errorf("consumer stub was overwritten:\n%s", b)
	}
}

func TestGenerateEventsUmbrellaAndServiceContext(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, notifySrc)
	dir := genEvents(t, proj, eventsConfig())

	umbrella := readGen(t, dir, "internal/transport/events.go")
	mustParseGo(t, umbrella)
	mustContainAll(t, umbrella,
		"func SubscribeAll(ctx context.Context, bus *craftevents.Bus, svcCtx *svccontext.ServiceContext) error",
		"notificationserviceevents.Subscriptions(bus, NewNotificationServiceConsumers(svcCtx))",
	)

	fields := readGen(t, dir, "svccontext/events.go")
	mustParseGo(t, fields)
	mustContainAll(t, fields,
		"type Events struct",
		"OrderService *orderserviceevents.Publisher",
		"func NewEvents(bus *craftevents.Bus, opts ...craftevents.PublishOption) Events",
	)
}

// Nothing about a message beside its contract and its payload is
// decided here: the publisher hands the caller's options straight to the
// bus and bakes in no key of its own.
func TestPublisherCarriesOptionsRatherThanABakedInKey(t *testing.T) {
	proj := analyzeProject(t, `package p
type P { id string }
service S {
	event E { payload P }
}`)
	dir := genEvents(t, proj, eventsConfig())
	got := readGen(t, dir, "internal/events/s/publisher.go")
	mustParseGo(t, got)
	mustContainAll(t, got,
		"func (p *Publisher) PublishE(ctx context.Context, payload *types.P, opts ...craftevents.PublishOption) error",
		"p.bus.Publish(ctx, EContract, payload, craftevents.JoinOptions(p.opts, opts)...)",
		"func (b Batch) E(payload *types.P, opts ...craftevents.PublishOption) Batch",
		"env.Apply(craftevents.JoinOptions(b.opts, opts)...)",
	)
	mustContainNone(t, got, "strconv", "payload.ID")
}

func TestAsyncAPIProjectsBothSidesOfAContract(t *testing.T) {
	proj := analyzeProject(t, ordersSrc, notifySrc)
	dir := genEvents(t, proj, eventsConfig())
	got := readGen(t, dir, "docs/asyncapi.yaml")
	mustContainAll(t, got,
		"asyncapi: 3.0.0",
		"orders.OrderPlaced:",
		"address: orders.OrderPlaced",
		"action: send",
		"action: receive",
		"$ref: '#/components/schemas/OrderPlacedPayload'",
	)
	// The group rides on the receive operation: it is what the evolution
	// check compares to catch a rename that costs a consumer its position.
	mustContainAll(t, got, "x-craftgo-group: notify-NotificationService-SendReceipt")
}

// The document describes the event model, so it carries the payload
// closure and nothing else - an HTTP request type no contract carries
// would only be noise.
func TestAsyncAPICarriesOnlyThePayloadClosure(t *testing.T) {
	proj := analyzeProject(t, `package p
type Nested { v int64 }
type Carried { id string  nested Nested }
type Unrelated { x string }
service S {
	get Read /r { response Unrelated }
	event E { payload Carried }
}`)
	dir := genEvents(t, proj, eventsConfig())
	got := readGen(t, dir, "docs/asyncapi.yaml")
	mustContainAll(t, got, "Carried:", "Nested:")
	mustContainNone(t, got, "Unrelated")
}

// One service has one publisher, whatever @group its blocks carry.
// Splitting it per group produced two fields of the same name on the
// Events container - valid syntax, so `go/format` let it through.
func TestOneServiceHasOnePublisherAcrossGroups(t *testing.T) {
	proj := analyzeProject(t, `package p
type Thing { id string }
@group("alpha")
service Orders {
	event Placed { payload Thing }
}
@group("beta")
extend service Orders {
	event Shipped { payload Thing }
}`)
	dir := genEvents(t, proj, eventsConfig())
	fields := readGen(t, dir, "svccontext/events.go")
	mustParseGo(t, fields)
	if strings.Count(unaligned(fields), "Orders *") != 1 {
		t.Errorf("Events must carry one field per service:\n%s", fields)
	}
	// A publisher is one file per service, so it lands in the service's
	// own directory rather than either block's @group.
	pub := readGen(t, dir, "internal/events/orders/publisher.go")
	mustParseGo(t, pub)
	mustContainAll(t, pub, "PublishPlaced", "PublishShipped")
}

// Two services sharing one @group both publish. @group merges per-member
// handler files; a publisher is one file for a whole service, so the two
// must not land in one directory.
func TestGroupedServicesKeepSeparatePublishers(t *testing.T) {
	proj := analyzeProject(t, `package shop
type A { id string }
type B { id string }
@group("ops")
service Orders { event Placed { payload A } }
@group("ops")
service Billing { event Invoiced { payload B } }`)
	dir := genEvents(t, proj, eventsConfig())

	orders := readGen(t, dir, "internal/events/orders/publisher.go")
	billing := readGen(t, dir, "internal/events/billing/publisher.go")
	mustParseGo(t, orders)
	mustParseGo(t, billing)
	mustContainAll(t, orders, "PublishPlaced")
	mustContainAll(t, billing, "PublishInvoiced")

	fields := readGen(t, dir, "svccontext/events.go")
	mustParseGo(t, fields)
	mustContainAll(t, fields,
		"ordersevents.Publisher",
		"billingevents.Publisher",
		"Orders:  ordersevents.NewPublisher(bus, opts...)",
		"Billing: billingevents.NewPublisher(bus, opts...)",
	)
}

// A payload package named after an identifier the handler template
// binds must be imported under a different alias, or the generated file
// shadows the name.
func TestPayloadPackageCannotShadowTemplateIdentifiers(t *testing.T) {
	for _, name := range []string{"bus", "ctx", "msg", "svcCtx", "h", "subs"} {
		t.Run(name, func(t *testing.T) {
			payloadPkg := "package " + name + "\ntype Cargo { id string }"
			svcPkg := "package svc\nservice Shipper { event Cargo { payload " + name + ".Cargo } }\n" +
				"service Sink { consume OnCargo { event Cargo } }"
			proj := analyzeProject(t, payloadPkg, svcPkg)
			dir := genEvents(t, proj, eventsConfig())
			got := readGen(t, dir, "internal/transport/sink_consumers.go")
			mustParseGo(t, got)
			if strings.Contains(got, "\t"+name+" \"") {
				t.Errorf("payload package %q shadows a template identifier:\n%s", name, got)
			}
		})
	}
}

// main.go is generated once and never rewritten, so what it names must
// not depend on what the design happens to declare today: it attaches
// everything through one wiring call whose signature is the same for a
// design with routes, with consumers, with both, or with neither. Naming
// the routes or transport package here would freeze a decision the design
// can still reverse.
func TestMainScaffoldDoesNotVaryWithRoutesOrConsumers(t *testing.T) {
	const httpSrc = `package web
type Thing { id string }
service WebService {
	get GetThing /things/{id} { request GetReq  response Thing }
}
type GetReq { id string @path }`
	cases := []struct {
		name    string
		sources []string
	}{
		{"routes only", []string{httpSrc}},
		{"events only", []string{ordersSrc, notifySrc}},
		{"both", []string{httpSrc, ordersSrc, notifySrc}},
		{"publisher, no consumer", []string{ordersSrc}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj := analyzeProject(t, c.sources...)
			cfg := eventsConfig()
			cfg.Output.Main = "./main.go"
			out, err := renderGo(tmpl("main.tmpl"), buildProjectMainData(proj, cfg))
			if err != nil {
				t.Fatalf("render main.go: %v", err)
			}
			got := string(out)
			mustParseGo(t, got)
			mustContainAll(t, got, "shutdownWiring, err := wiring.Register(ctx, srv, svc)", "_ = shutdownWiring(shutdownCtx)")
			mustContainNone(t, got,
				"routes.RegisterAll",
				"transport.SubscribeAll",
				"internal/routes",
				"internal/transport",
			)
		})
	}
}

// main.go is written once, so a project scaffolded before the wiring
// package existed calls routes.RegisterAll directly and nothing else. Its
// HTTP side keeps working and neither the build nor vet says anything,
// while the consumers this run generated are never subscribed - so gen
// has to say it.
func TestWiringNoteWhenMainNeverRegisters(t *testing.T) {
	legacyMain := `package main

import "example.com/app/internal/routes"

func main() {
	svc.Events = svccontext.NewEvents(bus)
	routes.RegisterAll(srv, svc)
}
`
	wiredMain := `package main

func main() {
	svc.Events = svccontext.NewEvents(bus)
	shutdownWiring, err := wiring.Register(ctx, srv, svc)
	_ = shutdownWiring
	_ = err
}
`
	const noConsumerSrc = `package p
type P { id string }
service S {
	event E { payload P }
}`
	cases := []struct {
		name    string
		sources []string
		main    string
		want    bool
	}{
		{"legacy main, consumers declared", []string{ordersSrc, notifySrc}, legacyMain, true},
		{"wired main, consumers declared", []string{ordersSrc, notifySrc}, wiredMain, false},
		{"legacy main, no consumer", []string{noConsumerSrc}, legacyMain, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj := analyzeProject(t, c.sources...)
			cfg := eventsConfig()
			cfg.Output.Main = "./main.go"
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(c.main), 0o644); err != nil {
				t.Fatal(err)
			}
			var got bool
			for _, note := range EventWiringNotes(proj, cfg, dir) {
				if strings.Contains(note, "wiring.Register(") {
					got = true
				}
			}
			if got != c.want {
				t.Errorf("wiring note emitted = %v, want %v (notes: %v)", got, c.want, EventWiringNotes(proj, cfg, dir))
			}
		})
	}
}

// The svccontext scaffold is written once, so the Events field it declares
// has to exist whatever the design says later. The container is emitted for
// every project - with the Go event target off, with no publisher, with no
// event at all - and carries the bus even when it carries no publisher.
func TestServiceContextEventsExistWithoutAGoTarget(t *testing.T) {
	cases := []struct {
		name        string
		sources     []string
		target      []config.EventTarget
		wantRuntime bool
	}{
		// The design declares events but the Go target is off, so no Go
		// publisher or subscription is written and nothing on this side
		// has a bus to hold.
		{"go target off", []string{ordersSrc}, []config.EventTarget{{Lang: config.LangGo, Out: "-"}}, false},
		// The Go target is on and the design publishes, so the container
		// carries the bus the publishers bind to.
		{"go target on", []string{ordersSrc}, nil, true},
		{"no event at all", []string{`package p
type T { id string }
service S { get Read /r { response T } }`}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj := analyzeProject(t, c.sources...)
			cfg := eventsConfig()
			cfg.Output.Main = "./main.go"
			if c.target != nil {
				cfg.Events.Targets = c.target
			}
			dir := t.TempDir()
			if err := generateSvccontext(proj, cfg, dir); err != nil {
				t.Fatalf("svccontext: %v", err)
			}
			if err := generateSvccontextEvents(proj, cfg, dir); err != nil {
				t.Fatalf("svccontext events: %v", err)
			}
			mustContainAll(t, readGen(t, dir, "svccontext/svccontext.go"), "Events Events")
			events := readGen(t, dir, "svccontext/events.go")
			mustParseGo(t, events)
			if c.wantRuntime {
				// gofmt aligns struct fields, so the padding between the
				// name and the type varies with the longest sibling.
				mustContainAll(t, events,
					"*craftevents.Bus",
					"func NewEvents(bus *craftevents.Bus, opts ...craftevents.PublishOption) Events",
					"Bus:",
				)
			} else {
				// A project that declares no event takes on no dependency
				// for one: the container exists, the runtime is not named.
				mustContainAll(t, events, "type Events struct{}", "func NewEvents(bus any, opts ...any) Events")
				mustContainNone(t, events, "craftevents", "pkg/events")
			}
			if !c.wantRuntime {
				mustContainNone(t, events, "*orderserviceevents.Publisher")
			}
		})
	}
}

// Contract names are project-unique; DSL event names are not. Keying the
// AsyncAPI components by name let one package's message overwrite
// another's, so a channel documented the wrong payload.
func TestAsyncAPIKeysComponentsByContract(t *testing.T) {
	proj := analyzeProject(t, `package a
type Created { a string }
service A { event Created { payload Created } }`, `package b
type Created { b string }
service B { event Created { payload Created } }`)
	dir := genEvents(t, proj, eventsConfig())
	got := readGen(t, dir, "docs/asyncapi.yaml")
	mustContainAll(t, got,
		"a_Created:",
		"b_Created:",
		"$ref: '#/components/schemas/ACreated'",
		"$ref: '#/components/schemas/BCreated'",
	)
}

// A contract declared outside any service is one this design describes but
// does not publish: no publisher, and nothing on the ServiceContext.
func TestFileLevelContractHasNoPublisher(t *testing.T) {
	proj := analyzeProject(t, `package upstream
type P { id string }
@contract("payments.settled.v1")
event PaymentSettled { payload P }
service LedgerService {
	consume RecordSettlement { event upstream.PaymentSettled }
}`)
	dir := t.TempDir()
	cfg := eventsConfig()
	if err := GenerateEventTarget(proj, cfg, dir, goEventsOut); err != nil {
		t.Fatalf("generate: %v", err)
	}
	events := filepath.Join(dir, "internal", "events")
	if _, err := os.Stat(filepath.Join(events, "ledger_service", "publisher.go")); !os.IsNotExist(err) {
		t.Errorf("a contract with no producing service got a publisher (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(events, "ledger_service", "consumers.go")); err != nil {
		t.Errorf("the consumer API is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "svccontext", "events.go")); !os.IsNotExist(err) {
		t.Errorf("a project that publishes nothing got an Events container (%v)", err)
	}
}

// A publisher's import alias is the service name plus `events`, so a
// service named `Craft` asks for `craftevents` - the alias the event
// templates bind to the runtime import. The escape renames the publisher's
// alias, never the runtime's, and leaves every other service alone.
func TestEventAliasesEscapeReservedIdentifiers(t *testing.T) {
	for _, c := range []struct {
		svc  string
		want string
	}{
		{"Craft", "craftevents2"},
		{"OrderService", "orderserviceevents"},
	} {
		if got := eventsAlias(c.svc); got != c.want {
			t.Errorf("eventsAlias(%q) = %q, want %q", c.svc, got, c.want)
		}
	}
	proj := analyzeProject(t, `package p
type Thing { id string }
service Craft {
	event Forged { payload Thing }
}`)
	dir := genEvents(t, proj, eventsConfig())
	src := readGen(t, dir, "svccontext/events.go")
	mustParseGo(t, src)
	mustContainAll(t, unaligned(src),
		`craftevents "github.com/craftgodotdev/craftgo/pkg/events"`,
		`craftevents2 "example.com/app/internal/events/craft"`,
		"Craft *craftevents2.Publisher",
	)
}

// A design with no event names no event type, so the empty container must
// stay import-free: one field referring to the runtime would pull pkg/events
// into the go.mod of a project that has no events at all.
func TestEmptyEventsContainerImportsNothing(t *testing.T) {
	proj := analyzeProject(t, `package p
type T { id string }
service S { get Read /r { response T } }`)
	dir := t.TempDir()
	if err := generateSvccontextEvents(proj, eventsConfig(), dir); err != nil {
		t.Fatalf("svccontext events: %v", err)
	}
	src := readGen(t, dir, "svccontext/events.go")
	mustParseGo(t, src)
	file, err := parser.ParseFile(token.NewFileSet(), "events.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(file.Imports) != 0 {
		t.Errorf("the empty container imports %d packages, want none: %s", len(file.Imports), src)
	}
	mustContainNone(t, src, "craftevents", "pkg/events")
}
