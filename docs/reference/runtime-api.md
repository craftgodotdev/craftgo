# Runtime API

The generated code runs on `github.com/craftgodotdev/craftgo/pkg/server` - a thin wrapper over `net/http`. This page is the API reference for that package. You rarely call most of it directly: the generated `main.go` wires `server.New`, `RegisterRoutes`, and `Start`. You reach for this when adding middleware, swapping the JSON codec, customizing health checks, or shaping error responses.

Everything here is plain standard-library shape - `http.Handler`, `http.HandlerFunc`, `func(http.Handler) http.Handler`. There is no custom router and no reflection.

## Server

```go
srv := server.New(svcCtx, opts...)
```

`New` takes your `ServiceContext` (accepted for the documented constructor shape; the runtime does not introspect it) and any number of `Option` values.

| Option | Effect |
|---|---|
| `WithHealthPaths(HealthPaths{Liveness, Readiness})` | Override the default `/healthz` + `/readyz` paths. |
| `WithoutDefaultHealth()` | Disable the auto-registered health endpoints entirely. |

### Lifecycle

| Method | Description |
|---|---|
| `Use(mw Middleware) *Server` | Append a global middleware. Outermost-added wraps first. |
| `Handle(pattern, h http.Handler, mws ...Middleware) *Server` | Register a route. Optional per-route middlewares wrap the handler **outermost-first** (first arg = outermost frame). |
| `HandleFunc(pattern, fn http.HandlerFunc) *Server` | Register a route from a bare function. |
| `Handler() http.Handler` | Build the fully-wrapped handler (Recovery → global chain → CORS → mux). Use it with `httptest.NewServer(srv.Handler())` to exercise the full stack without binding a port. |
| `Start(addr string) error` | Bind and serve until `Stop`. |
| `Stop(ctx context.Context) error` | Graceful shutdown; no-op if `Start` never ran. |
| `Mux() *http.ServeMux` | The underlying mux, if you need raw access. |

`pattern` is Go 1.22+ syntax: `"GET /users/{id}"`.

### Configuration setters

Each returns `*Server` for chaining.

| Method | Description |
|---|---|
| `SetLogger(l Logger)` / `Logger() Logger` | Swap or read the logger. Also mirrors to `log.Default()` so generated logic reaches the same instance. |
| `SetJSONCodec(c JSONCodec)` / `Codec() JSONCodec` | Swap the codec used by handlers, the access log, and health endpoints. Delegates to `SetGlobalJSONCodec`. |
| `SetCORS(opts CORSOptions)` | Install CORS. Calling twice replaces the previous config. |
| `SetHandleNotFound(h http.Handler)` | Customize 404 responses - receives every request that matches no route. |
| `SetDefaultReadTimeout(d)` / `SetDefaultWriteTimeout(d)` | Defaults applied to the underlying `*http.Server`. |
| `SetDefaultMaxBodySize(bytes)` / `SetDefaultMaxHeaderSize(kb)` | Defaults for every method that doesn't declare its own `@maxBodySize`. |

### Health checks

```go
srv.RegisterHealthCheck("db", 2*time.Second, func(ctx context.Context) error {
    return db.PingContext(ctx)
})
```

`RegisterHealthCheck(name, timeout, fn)` adds a probe to `/readyz`. The timeout is mandatory - each probe runs under `context.WithTimeout` and counts as a failure on deadline. `/healthz` (liveness) always returns 200 once the process is up.

## Middleware

`Middleware` is an alias for the standard shape:

```go
type Middleware = func(http.Handler) http.Handler
```

### Built-in middleware

| Constructor | Purpose |
|---|---|
| `Recovery(logger)` | Converts a panic into a 500 (or logs + leaves the committed status if the response already started). Always outermost in the generated chain. |
| `RequestID()` | Reads or generates `X-Request-Id`, stashes it on the context (`RequestIDFromContext(ctx)`). |
| `AccessLog(logger)` | One structured log line per request. |
| `BodyLimit(maxBytes)` | Wraps `r.Body` in `http.MaxBytesReader`. |
| `Timeout(d)` | Caps handler execution; cancels the context and returns 503 on deadline. Panics still propagate to `Recovery`. |

`WithLimits(h, Limits{...})` applies timeout + body limits to a single handler - this is what `@timeout` / `@maxBodySize` compile to.

### Chain

`Chain` composes middlewares outermost-first without nesting calls:

```go
type Chain []Middleware

base := server.NewChain(server.RequestID(), server.AccessLog(logger))
authed := base.Append(authMiddleware)          // returns a NEW chain (value semantics)

srv.Handle("GET /me", authed.Then(meHandler))  // Then folds the chain over the handler
srv.Handle("GET /ping", base.ThenFunc(pingFn)) // ThenFunc for bare functions
```

`NewChain(A, B, C).Then(h)` yields `A(B(C(h)))` - a request flows A → B → C → h, the response leaves in reverse. Nil entries are skipped, so an optional middleware can sit in the slice without an `if != nil` guard.

### DSL-driven middleware

The generated middleware stubs register their impls so `@middlewares(Name)` in the DSL resolves at runtime:

| Method | Description |
|---|---|
| `RegisterMiddleware(name, mw) *Server` | Map a DSL identifier to a concrete middleware. Called from the gen-once `internal/middleware/<name>-middleware.go`. |
| `With(names []string, h http.HandlerFunc) http.HandlerFunc` | Look each name up and fold them through a `Chain`, outermost-first (first name = outermost). Unknown names are skipped silently. The generated `routes.go` calls this. |

## JSON codec

Swap the JSON implementation process-wide (e.g. for `sonic` or `jsoniter`):

```go
type JSONCodec interface {
    Encode(w io.Writer, v any) error
    Decode(r io.Reader, v any) error
}

server.SetGlobalJSONCodec(myCodec{})  // swap once at init
server.JSON().Encode(w, payload)      // every generated handler reads through this accessor
```

`Server.SetJSONCodec(c)` is a convenience that delegates to `SetGlobalJSONCodec`. Reads during dispatch are safe via an `atomic.Value` swap.

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

The handler calls `server.WriteValidationError(w, r, err)` on a validation failure; it dispatches to your installed hook (or a sensible default). The hook is post-commit safe - if the response already started, it logs the dropped validation rather than smearing a 400 into a half-sent body.

## CORS

```go
srv.SetCORS(server.CORSPermissive())          // dev: reflect any origin
srv.SetCORS(server.CORSStrict("https://app")) // prod: one allowed origin
```

Or build a `CORSOptions` value directly for fine control over methods, headers, credentials, and max-age.

## Related packages

- `pkg/log` - the structured `Logger` interface and default zap-backed implementation. `log.SetLevel(level)` / `log.GetLevel()` retune the process-wide level (shared by the server and generated logic); `log.SetDefault` / `log.Default` swap or read the package-level logger.
- `pkg/metrics` - Prometheus-style counters/histograms the access log can feed.
- `pkg/otel` - OpenTelemetry tracing helpers. Generated `main.go` wires these when enabled.
