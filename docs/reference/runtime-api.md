# Runtime API

The generated code runs on `github.com/craftgodotdev/craftgo/pkg/server` - a thin wrapper over `net/http`. This page is the API reference for that package. You rarely call most of it directly: the generated `main.go` wires `server.New`, `wiring.Register` (which calls each service's `RegisterRoutes`), and `Start`. You reach for this when adding middleware, swapping the JSON codec, customizing health checks, or shaping error responses.

Everything here is plain standard-library shape - `http.Handler`, `http.HandlerFunc`, `func(http.Handler) http.Handler`. There is no custom router: routes go on `http.ServeMux`, request binding is generated code, and JSON goes through `encoding/json` unless you install another codec.

## Server

```go
srv := server.New(svcCtx, opts...)
```

`New` takes your `ServiceContext` (accepted for the documented constructor shape; the runtime does not introspect it) and any number of `Option` values.

| Option | Effect |
|---|---|
| `WithHealthPaths(HealthPaths{Liveness, Readiness})` | Override the default `/healthz` + `/readyz` paths. |
| `WithoutDefaultHealth()` | Disable the auto-registered health endpoints entirely. |
| `WithTelemetry(mw Middleware)` | Install `mw` - `tel.HTTPMiddleware()` - outside `Recovery` and every `Use` middleware, so their log lines carry the span it opens. The health probes bypass it, a panic in `mw` itself is not recovered, and `WithTelemetry(nil)` installs nothing and keeps an earlier one. |

### Lifecycle

| Method | Description |
|---|---|
| `Use(mw Middleware) *Server` | Append a global middleware. Outermost-added wraps first. |
| `Handle(pattern, h http.Handler, mws ...Middleware) *Server` | Register a route. Optional per-route middlewares wrap the handler **outermost-first** (first arg = outermost frame). |
| `HandleFunc(pattern, fn http.HandlerFunc) *Server` | Register a route from a bare function. |
| `Handler() http.Handler` | Build the fully-wrapped handler (the `WithTelemetry` middleware → Recovery → global chain → CORS → mux; the health probes are answered ahead of it, wrapped in Recovery only). Use it with `httptest.NewServer(srv.Handler())` to exercise the full stack without binding a port. |
| `Start(addr string) error` | Bind and serve until `Stop`; returns nil after a graceful `Stop`. Request headers must arrive within 10s, and idle connections close after 120s. |
| `Stop(ctx context.Context) error` | Graceful shutdown; no-op if `Start` never ran. |
| `Mux() *http.ServeMux` | The underlying mux, if you need raw access. Routes registered on it directly skip the default body cap and handler timeout that `Handle` applies. |

`pattern` is Go 1.22+ syntax: `"GET /users/{id}"`.

### Configuration setters

Each setter returns `*Server` for chaining, except `SetJSONCodec` and `SetStrictJSON`, which return an `error`.

| Method | Description |
|---|---|
| `SetLogger(l log.Logger)` / `Logger() log.Logger` | `SetLogger` installs `l` as `log.Default()` (nil is ignored), the logger the server's `Recovery` and the generated logic write to; the server keeps none of its own. `Logger` returns a `log.Follow()` logger, which writes each line - and each line of the loggers its `With` and `WithContext` return - through the `log.Default()` of that moment, so `AccessLog(srv.Logger())` follows a later `SetLogger`. |
| `SetJSONCodec(c JSONCodec) error` / `Codec() JSONCodec` | Swap or read the process-wide codec handlers and health endpoints use: `SetJSONCodec` delegates to `SetGlobalJSONCodec`, `Codec` returns what `JSON()` returns. `SetJSONCodec` fails, keeping the previous codec, when strict JSON is on and `c` has no `DecodeStrict`. |
| `SetStrictJSON(strict bool) error` | Reject a JSON body with an unknown field (400 `{"message":"<field>: unknown field"}`) or data after the JSON value (400 `{"message":"body: unexpected data after the JSON value"}`); off by default. `server.strictJSON` in `config.yaml` drives it, and the generated `config.yaml` sets it `true`. Fails, keeping the previous setting, when the installed codec has no `DecodeStrict`. |
| `SetCORS(opts CORSOptions)` | Install CORS. Calling twice replaces the previous config. |
| `SetHandleNotFound(h http.Handler)` | Answer the requests the mux would answer 404, which otherwise get 404 `{"message":"not found"}`; nil restores that default. A method mismatch keeps its 405 `{"message":"method not allowed"}` with `Allow`. |
| `SetDefaultReadTimeout(d)` / `SetDefaultWriteTimeout(d)` | The `*http.Server`'s deadlines for reading a whole request, 30s by default, and for writing a whole response, none (0) by default so streaming and long downloads are not cut. |
| `SetDefaultMaxBodySize(bytes)` | Body cap for each route `Handle` registers afterwards, unless its `WithLimits` sets one (a method's `@maxBodySize`). 0, the default, sets none. |
| `SetDefaultHandlerTimeout(d)` | Request-context deadline for each route `Handle` registers afterwards, unless its `WithLimits` sets one (a method's `@timeout`). 0, the default, sets none. |
| `SetDefaultMaxHeaderSize(kb)` | The `*http.Server`'s cap on request headers, in kilobytes; 32 by default. |

### Health checks

```go
srv.RegisterHealthCheck("db", 2*time.Second, func(ctx context.Context) error {
    return db.PingContext(ctx)
})
```

`RegisterHealthCheck(name, timeout, fn)` adds, or replaces, the probe `name` on `/readyz`. Each run calls `fn` under `context.WithTimeout`: a check that returns the context's error on expiry fails (`context deadline exceeded` under `checks`), while one that ignores its context is waited for and judged by what it returns. A check that panics fails too, reported as `panic: <value>` under `checks`, and the panic is logged with its stack. A failure answers 503 `{"checks":{…},"status":"not_ready"}`. `/healthz` (liveness) always returns 200 once the process is up.

Both probes are answered ahead of the middleware chain (only `Recovery` wraps them): they are never access-logged, traced, counted in the HTTP metrics or CORS-processed, and no `srv.Use` middleware runs for them. `WithoutDefaultHealth()` removes them; register your own route for observed probes.

## Middleware

`Middleware` is a defined type over the standard shape, so a `func(http.Handler) http.Handler` value is assignable to it as is:

```go
type Middleware func(http.Handler) http.Handler
```

### Built-in middleware

| Constructor | Purpose |
|---|---|
| `Recovery(logger)` | Converts a panic into 500 `{"message":"internal server error"}` and logs it (`panic recovered`) with its stack. Once the response has started it logs the panic and aborts the connection instead, so the client sees the response cut off. A panic with `http.ErrAbortHandler` goes on to `net/http`, which aborts the connection without logging it. `Handler()` installs it itself, logging to `log.Default()`: outside every `Use` middleware and inside the `WithTelemetry` middleware, so the panic line carries the request's trace ids. |
| `AccessLog(logger, opts...)` | One `http access` line at Info per request: `method`, `path`, `status` (499 for a client that left before anything was written), `latency`, plus the `trace_id` / `span_id` an outer tracing middleware (`WithTelemetry`) put on the context. A request whose handler panics gets no line; `Recovery` logs the panic instead. `AccessLogSkipPaths(paths...)` keeps chosen routes out; `AccessLogFields(fn)` appends fields `fn` derives from the request (client address, user agent, the matched `r.Pattern`). |
| `BodyLimit(maxBytes)` | Caps request bodies at `maxBytes`: a declared `Content-Length` above it is answered 413 `{"message":"request entity too large"}` before the handler runs, and a read past it fails with an `*http.MaxBytesError`, which `WriteValidationError` answers 413 the same way without calling the validation hook. |
| `Timeout(d)` | Deprecated: use `srv.SetDefaultHandlerTimeout(d)`, or `WithLimits` for one route. Runs the handler under `http.TimeoutHandler`: 503 on deadline, and a buffered response that cannot flush. |

`WithLimits(h, Limits{...})` applies timeout + body limits to a single handler - this is what `@timeout` / `@maxBodySize` compile to.

### Chain

`Chain` composes middlewares outermost-first without nesting calls:

```go
type Chain []Middleware

base := server.NewChain(server.BodyLimit(1 << 20), server.AccessLog(logger))
authed := base.Append(authMiddleware)          // returns a NEW chain (value semantics)

srv.Handle("GET /me", authed.Then(meHandler))  // Then folds the chain over the handler
srv.Handle("GET /ping", base.ThenFunc(pingFn)) // ThenFunc for bare functions
```

`NewChain(A, B, C).Then(h)` yields `A(B(C(h)))` - a request flows A → B → C → h, the response leaves in reverse. Nil entries are skipped, so an optional middleware can sit in the slice without an `if != nil` guard.

### DSL-driven middleware

`@middlewares(Name)` in the DSL resolves at compile time, not by name at runtime. Codegen adds a `Name` field of type `server.Middleware` to the `Middlewares` struct embedded in `ServiceContext` and a gen-once constructor in `internal/middleware/<name>_middleware.go`; `main.go` assigns `svc.Name = middleware.NewNameMiddleware()`, and the generated `routes.go` passes the fields to `srv.Handle` as per-route middlewares.

`RegisterMiddleware(name, mw)` and `With(names, h)`, a registry keyed by name, are deprecated: nothing generated calls them. Pass the middleware to `Handle` or `Use`, or build a `Chain`.

## JSON codec

Swap the JSON implementation process-wide (e.g. for `sonic` or `jsoniter`):

```go
type JSONCodec interface {
    Encode(w io.Writer, v any) error
    Decode(r io.Reader, v any) error
}

// Optional: needed only while server.strictJSON is on.
type StrictDecoder interface {
    DecodeStrict(r io.Reader, v any) error // rejects unknown fields and data after the value
}

if err := server.SetGlobalJSONCodec(myCodec{}); err != nil { /* strict JSON is on and myCodec has no DecodeStrict */ }
server.JSON().Encode(w, payload) // every generated handler reads through this accessor
```

`Server.SetJSONCodec(c)` delegates to `SetGlobalJSONCodec`. Both fail, keeping the previous codec, when strict JSON is on and `c` has no `DecodeStrict`; `SetStrictJSON(true)` fails the same way when the installed codec has none, so `config.yaml` can never claim a strictness the server does not enforce. The swap is atomic, so it is safe while requests are served.

The built-in codec names a body value of the wrong JSON type by its JSON path: `name: expected string, got number`, `home.name: expected string, got number`, `age: expected integer, got number 1.5`, `age: 300 is out of range`, `m: "x" is not an integer` (a map key), and `body: expected object, got array` at the root. Each answers 400 `{"message": …}` through the validation hook. A codec installed with `SetGlobalJSONCodec` reports its own errors.

A wrapper for another JSON library adds `DecodeStrict` with that library's own unknown-field switch and finishes with `server.TrailingData`, the shared check every codec uses to report leftover data the same way. With sonic:

```go
type Sonic struct{}

func (Sonic) Encode(w io.Writer, v any) error { return sonic.ConfigDefault.NewEncoder(w).Encode(v) }
func (Sonic) Decode(r io.Reader, v any) error { return sonic.ConfigDefault.NewDecoder(r).Decode(v) }

var strictAPI = sonic.Config{DisallowUnknownFields: true}.Froze()

func (Sonic) DecodeStrict(r io.Reader, v any) error {
    dec := strictAPI.NewDecoder(r)
    if err := dec.Decode(v); err != nil {
        return err
    }
    return server.TrailingData(dec.Buffered(), r)
}
```

jsoniter (`Config{DisallowUnknownFields: true}.Froze()`) and goccy/go-json (`NewDecoder(r).DisallowUnknownFields()`, the stdlib API) wrap the same way.

## Validation error hook

Generated handlers route `req.Validate()` failures through one global, swappable hook:

```go
type ValidationFailedHandler func(w http.ResponseWriter, r *http.Request, err error)

server.SetDefaultValidationFailed(func(w http.ResponseWriter, r *http.Request, err error) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusBadRequest)
    json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
})
```

The handler calls `server.WriteValidationError(w, r, err)` on a decode, binding or validation failure; it dispatches to your installed hook, or to the default: 400 `{"message":"<err text>"}`. A body read past its cap is answered 413 `{"message":"request entity too large"}` without the hook. The default is post-commit safe - if the response already started, it logs the dropped validation rather than smearing a 400 into a half-sent body; a hook you install is called even after the response started.

## Raw responses

Helpers for `@rawResponse` / `@passthrough` handlers that already hold the bytes they want on the wire:

| Function | Description |
|---|---|
| `WriteBytes(w, status, contentType, body) error` | Sets `Content-Type` (when non-empty) and `Content-Length`, writes the status, writes `body`. |
| `WritePrecompressed(w, r, status, contentType, coding, body, decode) error` | Serves a body stored already compressed (`"gzip"`, `"zstd"`, `"br"`, ...). When the client accepts `coding` the bytes go out verbatim with `Content-Encoding`; otherwise `decode` produces the identity form first. Always adds `Vary: Accept-Encoding`. A nil `decode` with a client that does not accept the coding returns `ErrNoDecoder` before anything is written. |
| `AcceptsEncoding(r, coding) bool` | Whether the request's `Accept-Encoding` lists `coding` with a non-zero quality (`gzip;q=0` is a refusal). Shares its parser with the `Compress` middleware. |

```go
func (l *SnapshotService) Snapshot(w http.ResponseWriter, r *http.Request, req *types.SnapshotReq) error {
	region := "global"
	if req.Region != nil {
		region = *req.Region
	}
	blob, ok := snapshotCache[region] // gzip bytes, exactly as stored
	if !ok {
		http.NotFound(w, r)
		return nil
	}
	return server.WritePrecompressed(w, r, http.StatusOK, "application/json; charset=utf-8", "gzip", blob, gunzip)
}
```

The framework pulls in no compression library: you supply `decode`, using the library that filled the cache. `Compress` leaves a response that already carries `Content-Encoding` untouched, so a verbatim body is never re-encoded.

`server.WriteError`, and `server.WriteValidationError` with the default hook, are post-commit safe: when a raw handler has already written a status or body, or flushed, and then returns an error, the error is logged with the request's trace context and the wire is left alone rather than splicing an envelope into the body. A context error is dropped without a line, except a dependency's deadline on a live request, logged at Warn.

## CORS

```go
srv.SetCORS(server.CORSPermissive())          // dev: any origin (Access-Control-Allow-Origin: *)
srv.SetCORS(server.CORSStrict("https://app")) // prod: one allowed origin
```

Or build a `CORSOptions` value directly for fine control over methods, headers, credentials, and max-age.

## Event runtime

Generated event code runs on `github.com/craftgodotdev/craftgo/pkg/events`, the
transport- and codec-neutral counterpart of `pkg/server`. It knows nothing about
any broker and nothing about any serialisation format.

```go
type Message struct {
	Event          string            // the contract name the design declared
	Key            string            // the WithKey value, "" for a keyless message
	DedupID        string            // the WithDedupID value; a transport without the notion ignores it
	Payload        []byte            // already encoded
	Metadata       map[string]string // side-band values a publisher sets; MetaCodec always present
	AdapterOptions map[string]map[string]any // what WithAdapterOption filled in, per adapter
}

func (m *Message) AdapterOption(adapter, key string) (any, bool)

type Codec interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type Publisher interface {
	Publish(ctx context.Context, msg *Message) error
}

type Subscriber interface {
	Subscribe(ctx context.Context, subs []Subscription) error
}

type Handler func(ctx context.Context, msg *Message) error

type Group string // the broker identity: Kafka group, NATS queue group, JetStream durable

type Subscription struct {
	Event    string // the contract, matching Message.Event
	Consumer string // names the handler in diagnostics and Plan; Event[T].Subscription sets it to the contract
	Group    Group  // the broker identity; Register refuses an empty one
	Chain    Chain  // this subscription's own middleware, applied inside the bus chain
	Handle   Handler
}
```

`Subscribe` takes the whole batch, and there is no one-at-a-time path beside it.
A broker that binds one identity to several contracts - a JetStream durable
filtering every subject its group consumes - cannot register a group one contract
at a time, so every adapter is handed the set. It registers and returns; it must
not block. A push transport hands the handler its callback, a pull transport
starts its own loop; delivery runs until `ctx` is cancelled. A handler error
does not decide what becomes of the message: the chain does, through the
[disposition](#dispositions) it asks for. Once the handler returns, the
transport answers the delivery with that disposition, and settles it when
nothing was asked or the transport cannot honour what was.

`Group` is a named type so an application declares its groups once and passes
them around as values rather than as loose strings. It has no fallback: a
subscription without one is refused at registration, because the name is where a
consumer resumes on a broker that keeps a position, and that is the deployable's
to choose rather than something to default into.

`events.New(opts...)` builds a `*Bus` binding one transport to one codec;
`WithPublisher` / `WithSubscriber` / `WithTransport` install the transport,
`WithCodec` the codec, `WithCodecFor(contract, c)` overrides it for a single
contract, `WithMiddleware(mws...)` installs the [consumer
chain](#consumer-middleware), `WithPublishDefaults(opts...)` sets the [publish
options](#publishing) every message through this bus starts from, and
`WithDispositionRequired(d)` refuses a transport that cannot honour a
[disposition](#dispositions). There is no default codec - a bus built without one
fails rather than picking an encoding.

### Register and start

```go
func (b *Bus) Register(sub Subscription) error
func (b *Bus) Start(ctx context.Context) error

type RegisterError struct {
	Event    string
	Consumer string
	Group    Group
	Err      error // one of the sentinels; errors.Is reaches it
}
```

`Register` records one subscription, to be handed over by `Start`. It makes the
checks the bus can make on its own: the bus has not started, `Handle` is
non-nil, `Group` is non-empty, a codec resolves for the contract, the transport
can honour every disposition `WithDispositionRequired` named, and no earlier
subscription holds the same contract and group. A refusal is a `*RegisterError`
carrying the offending subscription, so `errors.Is(err, events.ErrNoGroup)`
reaches the reason while the message still names which consumer. Whether the
*broker* accepts the set is the transport's answer, and it comes from `Start`.

A module states its whole consumption as `errors.Join` over one
[`Subscribe`](#event-descriptors) line per contract. Every line is offered to the
bus that way - a refusal in the middle stops neither the registrations after it nor
the refusals beside it - and the joined error carries each `*RegisterError`, so a
deployable wired wrongly in two places hears about both at once. Nothing has started
either way, so a caller returning the error abandons the bus.

`Start` hands every registered subscription to the transport in **one** call,
each handler already wrapped: a recover outermost, then the bus-wide chain, then
the subscription's own `Chain` innermost. The batch is sorted by group, contract
then consumer. Group leads because it is the identity a transport can refuse a
second claim on, so the order decides which of two colliding subscriptions
registers first; contract and consumer complete it, since two services may name a
consumer alike and an order that is not total would move the refusal between
runs.

A second `Start`, or a `Register` after one, is `ErrStarted` - including after a
`Start` that failed, since the transport may have taken part of the batch before
it did. A bus with nothing registered starts successfully and hands the transport
nothing.

### Event descriptors

A generated contract package declares one descriptor per event, and everything
typed about that event goes through it:

```go
type Event[T any] struct{ ... }

func NewEvent[T any](contract string, validate func(*T) error) Event[T]

func (e Event[T]) Contract() string
func (e Event[T]) Publish(ctx context.Context, bus *Bus, payload *T, opts ...PublishOption) error
func (e Event[T]) Handler(bus *Bus, fn func(ctx context.Context, payload *T) error) Handler
func (e Event[T]) Subscribe(bus *Bus, group Group,
	fn func(ctx context.Context, payload *T) error) error
func (e Event[T]) Subscription(bus *Bus, group Group,
	fn func(ctx context.Context, payload *T) error) Subscription
```

`validate` may be nil, for a payload type that carries none. `Publish` validates
first and sends nothing when that fails - the contract is refused where it is
broken rather than at every consumer. `Handler` adapts a typed function to the
untyped one a transport delivers to, decoding and validating before it runs.

`Subscribe` is one line of an application's consumption: it builds the subscription
and registers it, so a module is `errors.Join` over its lines. It takes neither a
consumer nor a chain - the middleware every handler runs behind belongs on the bus
through [`Use`](#consumer-middleware), and `Consumer` defaults to the contract.

`Subscription` returns the value instead of registering it, for the line that needs
one of those two fields set - two subscriptions of one contract to tell apart, one
handler to wrap alone. Set the field, then hand the value to `Register`.

The bus is a parameter at every call and never a field: a descriptor is a value
in a contract package and knows nothing about how any deployable is wired, so one
contract catalogue serves every binary that imports it.

### The plan

```go
func (b *Bus) Plan() Plan

type Plan struct {
	Groups []PlanGroup `json:"groups"`
}

type PlanGroup struct {
	Name      Group          `json:"name"`
	Consumers []PlanConsumer `json:"consumers"`
}

type PlanConsumer struct {
	Event    string `json:"event"`
	Consumer string `json:"consumer"`
}

func (p Plan) MarshalJSON() ([]byte, error)
```

`Plan` reports what is registered, before or after `Start`: groups ordered by
name, consumers within a group by contract then consumer, so two runs of the same
wiring produce the same plan. `MarshalJSON` renders that order rather than the one
the plan was built in, so a golden file compares a plan and not a map iteration.
No generated file states a deployable's consumption - the groups are the
application's - so a project that wants it stated pins this in a test.

### Publishing

```go
type PublishOption func(*Envelope)

func WithKey(key string) PublishOption
func WithDedupID(id string) PublishOption
func WithHeader(key, value string) PublishOption
func WithAdapterOption(adapter, key string, value any) PublishOption

func JoinOptions(defaults, opts []PublishOption) []PublishOption
func (env *Envelope) Apply(opts ...PublishOption)

type Envelope struct {
	Event          string            // the contract
	Key            string            // the WithKey value; "" keeps a default key, else is keyless
	DedupID        string            // the WithDedupID value
	Payload        any               // the value to encode
	Metadata       map[string]string // optional side-band values; nil and empty behave alike
	AdapterOptions map[string]map[string]any
}

func (b *Bus) Publish(ctx context.Context, event string, payload any, opts ...PublishOption) error
func (b *Bus) PublishAll(ctx context.Context, envs []Envelope) error
```

Options apply in order, so the last one setting a given value wins - which is
what makes a defaults list defaults. `WithPublishDefaults` is that list for a
whole bus. `JoinOptions` does the same join for a hand-written publisher carrying
its own - defaults first, the per-call options after, without writing into
either. An empty value clears a default only as a per-call option: `WithKey("")`
on `Publish` clears a default key, while an `Envelope` whose `Key` is empty keeps
it.

`PublishAll` encodes every envelope up front, then hands the batch to the
transport in one call when it implements `BatchPublisher` and one message at a
time otherwise; a failure partway through returns a `*PartialPublishError` whose
`Unsent` holds the indices, ascending, of the envelopes that did not go out, so
retrying exactly those sends nothing twice. It is a **set, not a count**,
because a transport publishing to several partitions at once does not fail in
batch order - the ones that landed need not be the first. `Sent` is the length
of the leading published run, which is `Unsent[0]`.

An adapter builds its report through one of two constructors, whose shapes say
which kind of failure it had - and whose signatures make the wrong one a
compile error rather than a wrong report:

```go
func UnsentFrom(i int, msgs []*Message, err error) *PartialPublishError
func UnsentAt(indices []int, msgs []*Message, err error) *PartialPublishError
```

`UnsentFrom` is for a transport that STOPPED at i, so everything from i onward
is unsent - the shape a one-at-a-time loop produces. `UnsentAt` is for one whose
failures are SCATTERED, which is what publishing to several partitions at once
produces. Both sort the indices and derive `Sent` and `Event` from the first, so
the invariants hold by construction.

`PublishAll` checks the adapter's report against the batch before returning it.
One that names an index outside the batch, or out of order, is replaced with
"none of it was sent" and the error names the adapter: an understated `Unsent`
loses the messages it calls delivered, and nothing downstream can tell.

### Dispositions

```go
type Disposition uint8

const (
	DispositionUnset Disposition = iota // nothing decided; settles
	DispositionSettle
	DispositionRedeliver
	DispositionReject
)

func (m *Message) Settle()
func (m *Message) Redeliver()
func (m *Message) Reject()
func (m *Message) Disposition() Disposition
func (m *Message) Deliveries() int   // the broker's count; 0 where it keeps none
func (m *Message) SetDeliveries(n int) // transport adapters only

type Dispositioner interface {
	CanDisposition(d Disposition) bool
}

func WithDispositionRequired(d Disposition) Option
var ErrDispositionUnsupported = errors.New(...)
```

A middleware asks for something other than "done" through the message. Options
apply in chain order and the last writer wins: the chain returns innermost
first, so the outermost middleware decides last. A frame that panicked did not
finish deciding, so the recover clears what it asked for; [Recovery](#recovery)
says what is asked in its place.

`Dispositioner` is asked per INSTANCE, not per type: one adapter may be built in
a mode that can redeliver and in a mode that cannot. A transport that does not
implement it honours settle alone. `WithDispositionRequired` refuses at
`Register` rather than at the first message, because a chain calling
`Redeliver()` on a transport that settles instead loses every message it meant
to retry with nothing to report it.

### Per-adapter options

```go
type OptionAware interface {
	AdapterName() string   // the namespace WithAdapterOption addresses
	KnownOptions() []string // every option key this adapter reads
}

type UnknownOptionError struct {
	Adapter, Key, Event string
	Known               []string
}
```

A transport implementing `OptionAware` gets both halves of the rule, enforced
by the bus before anything is encoded: an option under **another** adapter's
name is ignored, and one under its **own** name that `KnownOptions` does not
list fails the publish with an `*UnknownOptionError`. A transport that does not
implement it gets neither - nothing can tell an option meant for it from one
meant for somebody else. Every adapter craftgo ships implements it;
`kafka.OptionTimestamp` (a `time.Time`) is the only option any of them reads.

```go
const MetaCodec  = "content-codec" // the codec that encoded the payload
const MetaPrefix = "craftgo-"      // reserved for transport adapters

func IsReservedMeta(key string) bool
```

`IsReservedMeta` names every key the caller does not own, case-insensitively:
`MetaCodec`, and every key under `MetaPrefix`, which holds the adapter headers -
Kafka's `craftgo-event`, `craftgo-key` and `craftgo-dedup-id`, NATS's
`Craftgo-Key`. An `Envelope.Metadata` entry under one of those is dropped
silently and the runtime's or the adapter's own value takes its place. Every
other key is carried untouched, except that the NATS adapters never let one
override `Nats-Msg-Id`, the header that carries the dedup ID. A generated
consumer is handed the decoded payload, so metadata is read in a
[middleware](#consumer-middleware) or a hand-written `Subscription`, both of
which are handed the `Message`.

### Recovery

```go
type PanicError struct {
	Event    string // the contract being delivered
	Consumer string // the consumer whose handler or chain panicked
	Group    Group  // its broker identity
	Value    any    // what was passed to panic
	Stack    []byte // the trace where the panic fired; not part of Error()
}
```

`Bus.Start` wraps every handler it hands over in a recover before the batch
reaches the transport, so a panicking consumer cannot end the
process - the same rule `Recovery` is for the HTTP chain, and every transport
inherits it, including adapters written outside craftgo. With a chain installed
the wrap goes on both sides of it, so the chain observes the panic and a panic in
the chain is caught too. The recovered panic is
returned as a `*PanicError`, which the transport sees as an ordinary handler
error: it reaches the error handler installed on the transport, and delivery
continues with the next message. A panicking handler leaves the disposition
unset, not settled, so the chain above it decides as it does for any error; a
panic in the chain itself, with nothing above it to decide, asks for redelivery
where the transport can honour one.

`*PanicError` is a concrete type, so `errors.As` picks one out of a chain, and
its `Unwrap` reaches the panic value when that value is an error.

```go
type PayloadError struct {
	Event string // the contract the payload arrived on
	Err   error
}

var ErrCodecMismatch = errors.New(...)
```

A payload `Event[T].Handler` could not decode or that failed its
`Validate()` comes back as a `*PayloadError` - the same bytes fail the same way
on every delivery. A message stamped with a codec the consumer is not
configured for fails with `ErrCodecMismatch` instead, a configuration error
rather than a poison payload. Nothing else is classified: what to do with a
failure is a middleware's decision.

### Consumer middleware

```go
type Middleware func(sub Subscription, next Handler) Handler
type Chain []Middleware

func WithMiddleware(mws ...Middleware) Option // install on the bus at construction
func (b *Bus) Use(mws ...Middleware)          // append to the same chain afterwards

func NewChain(mws ...Middleware) Chain
func (c Chain) Append(mws ...Middleware) Chain
func (c Chain) Apply(subs []Subscription) []Subscription
func Recover() Middleware
```

`WithMiddleware` installs the chain every subscription registered through the bus
is wrapped in, outermost first. The bus is the seam that covers all of them
because every subscription passes through `Bus.Register` - a descriptor's
`Subscription`, a hand-built one, and one built against another design's contracts
alike. A subscription's own `Chain` is applied *inside* this one, so a bus-wide
concern - logging, tracing - still sees what a per-consumer chain did. Repeating the
option appends. Nothing is generated for any of it.

`Bus.Use(mws...)` appends to that same chain after construction, for a deployable
whose delivery chain is assembled out of a service context or out of configuration
rather than held back until the `New` call. `Use` after `Start` **panics**: the batch
has gone to the transport with its handlers already wrapped, so a middleware arriving
then would cover nothing at all and say nothing about it - a wiring mistake to fix in
the code, like `net/http`'s `ServeMux` on a duplicate pattern, rather than an
`ErrStarted` for the caller to handle.

Each middleware is handed the `Subscription` it wraps, so one chain can read the
contract, the consumer and the group it is running for.

`Chain` is [`server.Chain`](#chain) for the consumer side and folds the same way:
`NewChain(A, B, C)` makes A outermost, `Append` returns a new chain without
mutating the receiver, and nil entries are skipped. The verb differs - `Apply`
over a slice of subscriptions rather than `Then` over one handler - because each
wrap is handed the subscription it wraps. Most projects never call `Apply`;
it is for decorating a slice the bus will not see, or one slice differently from
the rest.

`Bus.Start` recovers on *both* sides of the chain: the inner recover turns a
panicking handler into a `*PanicError` your middleware observes as an ordinary
error, the outer one catches a panic in the chain itself. Exactly one
`PanicError` is built per panic. A bus with no middleware installs the inner one
alone.

`Recover()` is for a chain folded by `Apply` instead of installed on the bus -
the bus wraps that from outside as one opaque handler, so place `Recover()` at
its innermost end for the same visibility. A bus chain needs it nowhere. See
[Events](/guide/events#middleware).

### Shipped implementations

- `pkg/events/memory` - an in-process transport for tests, local development and
  single-binary deployments. Subscriptions sharing a group for one contract form
  one competing-consumer group. `Drain()` waits for in-flight deliveries, and
  is safe to call while another goroutine publishes - which is what a shutdown
  racing a request still in flight looks like.
- `pkg/events/nats` - core NATS, where a contract is a subject and a group is a
  queue group, plus a separate `JetStream` transport binding one durable per
  group, filtered to every subject that group consumes. See
  [NATS JetStream](/guide/events#nats-jetstream).
- `pkg/events/kafka` - one contract per topic by default, the publish key as the
  record key. A classic consumer group by default; `WithShareGroup` asks for a
  KIP-932 share group, which is what makes `Redeliver` and `Reject` mean anything
  there.
- `pkg/events/codecjson` - a JSON codec.
- `pkg/events/logging` - `AccessLog(l *slog.Logger, opts ...AccessLogOption)`,
  one line per delivery. It is a sub-package so `log/slog` stays out of the
  exported surface of `pkg/events`, which every generated contract package
  imports.

Any other broker - RabbitMQ, SQS, Pub/Sub, Redis Streams - is an outside package
implementing `Publisher` and/or `Subscriber`, and those two interfaces are all it
needs: none of the above is privileged. See [Events](/guide/events).

## gRPC runtime

The generated gRPC layer runs on `github.com/craftgodotdev/craftgo/pkg/rpc` - a thin wrapper over `*grpc.Server` that installs the guards the HTTP server installs. The generated `main.go` wires it; see [gRPC](/guide/grpc).

```go
grpcSrv := rpc.New(svcCtx, opts...)
```

| Option | Effect |
|---|---|
| `WithStatsHandler(h stats.Handler)` | Install a stats handler - `tel.GRPCServerHandler()` - which runs in the transport, ahead of every interceptor. A nil handler is ignored. |
| `WithReflection(on bool)` | Serve the gRPC reflection service. |
| `WithoutDefaultHealth()` | Disable the auto-registered `grpc.health.v1` service. |
| `WithServerOptions(opts ...grpc.ServerOption)` | Pass options straight to `grpc.NewServer` - message size limits, keepalive, credentials. |

| Method | Description |
|---|---|
| `Use(i Interceptor) *Server` | Append an interceptor to the chain, outermost first. Recovery is always ahead of the chain, and health and reflection calls bypass it. A `Use` after the server is built has no effect. |
| `SetLogger(l log.Logger)` / `Logger() log.Logger` | `SetLogger` installs `l` as `log.Default()` (nil is ignored), the logger the server's `Recovery` and `Error` write to; `Logger` returns a `log.Follow()` logger, so `rpc.AccessLog(grpcSrv.Logger())` follows a later `SetLogger`. The server keeps none of its own. |
| `RegisterService(desc *grpc.ServiceDesc, impl any)` | `grpc.ServiceRegistrar`, so `pb.RegisterXServer(srv, impl)` takes the server directly. |
| `GRPCServer() *grpc.Server` | Build the underlying server once and return it. |
| `Start(addr string) error` | Listen and serve until `Stop`; returns nil once stopped. |
| `Serve(lis net.Listener) error` | Serve on a listener you own - a `bufconn` in tests. |
| `Stop(ctx context.Context) error` | Health flips to `NOT_SERVING`, in-flight RPCs finish, and when `ctx` expires first the rest are cut off and `ctx.Err()` is returned. No-op on a server never built (by `GRPCServer`, `Serve` or `Start`). |

`Interceptor` pairs the two shapes gRPC needs, `Unary grpc.UnaryServerInterceptor` and `Stream grpc.StreamServerInterceptor`; `rpc.Unary(f)` and `rpc.Stream(f)` wrap one side.

| Function | Description |
|---|---|
| `Recovery(logger)` | Panic → `Internal` with an opaque message, logged with the stack and the call's trace ids. Installed outermost by the server itself. |
| `AccessLog(logger, opts...)` | One `grpc access` line per call with `method`, `code`, `latency` and the trace ids; a stream logs once when it ends. `AccessLogSkipMethods(...)` and `AccessLogFields(fn)` shape it. |
| `Timeout(d)` | Bound unary calls to `d`; a late response is dropped for `DeadlineExceeded`. Streams are not bounded; `d <= 0` installs nothing. |
| `Error(ctx, err) error` | What the generated server layer returns on failure: status errors pass through, a craftgo typed error (`HTTPStatus() int`) becomes the matching code with an `ErrorInfo{Reason: ErrCode()}` detail, a context error becomes `Canceled` / `DeadlineExceeded`, anything else goes to the unknown-error handler. |
| `SetHandleUnknownError(h)` | Swap the process-wide handler for errors that carry neither a status nor an HTTP status; the default logs and answers `Internal`. |
| `Validate(msg any) error` | Run the message's own `Validate() error` (protoc-gen-validate) and answer `InvalidArgument`. |
| `IsInfrastructureMethod(fullMethod string) bool` | Whether a full method belongs to the health or reflection services. |

`tel.GRPCServerHandler()`, a method of the `*telemetry.Telemetry` that `telemetry.Init` returns, is the stats handler the generated `main.go` installs: one `otelgrpc` handler emitting the span and the `rpc.server.call.duration` histogram against the stack's providers, adopting the caller's W3C trace context from the request metadata, and leaving the health and reflection calls out.

### Calling another service

```go
conn, err := rpc.Dial(addr, rpc.WithClientStatsHandler(tel.GRPCClientHandler()))
```

`Dial` returns a `*grpc.ClientConn` with the guards a craftgo service makes calls under. It connects lazily, so an unreachable target is a failed call rather than a failed startup, and the caller closes it.

| Option | Effect |
|---|---|
| `WithClientStatsHandler(h stats.Handler)` | The client span, the `rpc.client.call.duration` histogram, and - the reason a caller needs one - the W3C trace context written into the request metadata. A nil handler is ignored. |
| `WithClientTimeout(d)` | A default deadline for a unary call whose context carries none; a caller with its own keeps it. Streams are not bounded. |
| `WithClientAccessLog(l log.Logger)` | One `grpc client` line per unary call with `method`, `code`, `latency` and the trace ids. |
| `WithClientTransportCredentials(c)` | Transport security; the default is insecure. |
| `WithDialOptions(opts ...grpc.DialOption)` | Straight to `grpc.NewClient`. |

`tel.GRPCClientHandler()` is the caller-side twin of `GRPCServerHandler()`. Without it a gRPC client sends no `traceparent`, so the service it calls starts a trace of its own.

## Related packages

- `pkg/log` - the structured `Logger` interface and default zap-backed implementation. `log.SetLevel(level)` / `log.GetLevel()` retune the process-wide level (shared by the server and generated logic); `log.SetDefault` / `log.Default` swap or read the package-level logger. `log.Follow()` returns a logger that writes each line - and each line of the loggers its `With` and `WithContext` return - through the `log.Default()` of that moment; `srv.Logger()` hands one out. `log.SetDefault` given a `Follow` logger installs the logger it writes through at that call, its `With` fields and context included.
- `pkg/telemetry` - traces and metrics as one stack. `telemetry.Init(ctx, cfg)` builds the providers the `otel:` / `metrics:` blocks of `config.yaml` select (spans: `none` / `stdout` / `otlp_grpc` / `otlp_http`; metrics: `prometheus` / `otlp_grpc` / `otlp_http` / `none`) and returns a `*Telemetry`, whose `HTTPMiddleware()` instruments every HTTP request, `GRPCServerHandler()` every RPC served and `GRPCClientHandler()` every RPC made, `ScrapeURL()` names the Prometheus listener and `ScrapeHandler()` serves the same scrape on a route of your own, and `Shutdown` flushes both signals. Generated `main.go` wires `Init`, `HTTPMiddleware` through `server.WithTelemetry` (or `GRPCServerHandler` through `rpc.WithStatsHandler`), `ScrapeURL` and `Shutdown`; `GRPCClientHandler` goes to the `rpc.Dial` calls you write, and `ScrapeHandler` to a route of yours.
