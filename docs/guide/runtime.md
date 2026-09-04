# Runtime

The craftgo runtime is a thin wrapper around `net/http`. There is no custom router, no custom middleware shape, no service container.

## At a glance

```go
srv := server.New(svcCtx)              // wraps *http.ServeMux
srv.Use(loggingMiddleware)             // standard func(http.Handler) http.Handler
routes.RegisterAll(srv, svcCtx)        // generated registration
srv.Start(":8080")                     // ListenAndServe
```

Three things matter:

1. `Server` wraps the standard library mux and accepts the standard middleware shape
2. Generated routes register through `srv.Handle("VERB /path", handlerFn, mws...)` using Go 1.22+ pattern syntax
3. Logic, validation, and JSON live in plain Go - no framework runtime in the hot path

If you can name a `net/http` concept, the craftgo equivalent uses it directly.

## The Server

```go
import "github.com/craftgodotdev/craftgo/pkg/server"

srv := server.New(svcCtx)
srv.Use(loggingMiddleware)
srv.Handle("GET /healthz", healthHandler)
srv.Start(":8080")
```

`Server` wraps `*http.ServeMux`. Routes register through `Handle` and `HandleFunc` using Go 1.22+ pattern syntax (`GET /users/{id}`). Middleware is plain `func(http.Handler) http.Handler`.

`Handle` is variadic - `Handle(pattern, h, mws...)` - so a route can carry per-route middleware that wraps the handler outermost-first (the first middleware argument is the outermost frame, hit first on the way in). For composing a reusable stack, `server.Chain` folds a middleware list in the same order:

```go
chain := server.NewChain(server.RequestID(), server.AccessLog(logger))
srv.Handle("GET /healthz", chain.Then(healthHandler))
```

`NewChain(...).Append(...)` returns a new chain (value semantics, safe to share a base), and `.Then(h)` / `.ThenFunc(fn)` produce the wrapped handler. Nil entries are skipped, so an optional middleware can drop into the slice without a guard.

## Built-in middleware

Out of the box:

- `Recovery(logger)` - converts panics to 500 responses with structured logging
- `RequestID()` - extracts or generates `X-Request-Id`
- `AccessLog(logger)` - one structured log line per request
- `BodyLimit(maxBytes)` - caps request bodies
- `Timeout(d)` - hard deadline on handler execution
- `CORSPermissive()` / `CORSStrict(origin)` - build a `CORSOptions` preset, then attach with `srv.SetCORS(opts)` - preflight + headers
- `Compress(opts)` - gzip / deflate response compression

You wire them in `main.go`:

```go
srv := server.New(svcCtx)
srv.Use(server.RequestID())
srv.Use(server.AccessLog(logger))
srv.Use(server.BodyLimit(1 << 20))
```

## Standard middleware works

Because middleware is `func(http.Handler) http.Handler`, anything from the wider Go ecosystem plugs in:

```go
import (
    chiMW "github.com/go-chi/chi/v5/middleware"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

srv.Use(chiMW.Recoverer)
srv.Use(otelhttp.NewMiddleware("api"))
```

No adapter, no shim. craftgo handlers are `http.HandlerFunc`.

## Handlers

Generated handlers look like this:

```go
func CreateUser(svcCtx *svccontext.ServiceContext) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req types.CreateUserReq
        if err := server.JSON().Decode(r.Body, &req); err != nil {
            server.WriteValidationError(w, r, err)
            return
        }
        if err := req.Validate(); err != nil {
            server.WriteValidationError(w, r, err)
            return
        }
        l := service.NewCreateUserService(r.Context(), svcCtx)
        resp, err := l.CreateUser(&req)
        if err != nil {
            server.WriteError(w, r, err)
            return
        }
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        _ = server.JSON().Encode(w, resp)
    }
}
```

This is exactly what you would write by hand: one `http.HandlerFunc`, stdlib status codes, stdlib responses. Two details to note:

- **`server.JSON()`** is the swappable codec accessor - it defaults to `encoding/json` but lets you drop in `sonic`/`jsoniter` process-wide (see [Runtime API](/reference/runtime-api#json-codec)). The decode/encode shape is otherwise standard.
- **The logic call is `l.CreateUser(&req)`** - the request context is captured when the per-method service is constructed (`service.NewCreateUserService(r.Context(), svcCtx)`), so it isn't threaded through the method call.

No framework runtime in the hot path.

## Health endpoints

`Server` mounts `/healthz` (liveness) and `/readyz` (readiness) by default. Add custom checks:

```go
srv.RegisterHealthCheck("db", 2*time.Second, func(ctx context.Context) error {
    return db.PingContext(ctx)
})
```

The second argument is a per-check timeout - the check fails if it runs longer.

Disable with `server.WithoutDefaultHealth()` if you do not want them.

## API reference docs

A freshly generated project serves its OpenAPI document and a rendered docs page
out of the box, configured under `docs` in `config.yaml`:

```yaml
docs:
  enabled: true            # off → no docs/spec routes
  ui: redoc                # redoc | swagger | scalar (assets load from a CDN)
  path: /docs              # HTML docs page
  specPath: /openapi.yaml  # raw OpenAPI document
```

`main.go` embeds the generated `openapi.yaml` and wires it via
`server.ServeDocs(...)`. To add it to a hand-written server (or an existing
project whose gen-once `main.go` predates the feature):

```go
//go:embed docs/openapi.yaml
var openapiSpec []byte

srv.ServeDocs(server.DocsOptions{Spec: openapiSpec, UI: "redoc"})
```

This registers `GET /openapi.yaml` (the spec) and `GET /docs` (the UI page). It
is a no-op when `Spec` is empty.

## Graceful shutdown

```go
go srv.Start(":8080")

stop := make(chan os.Signal, 1)
signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
<-stop

ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
srv.Stop(ctx)
```

`Stop` closes the listener, waits for in-flight requests, then returns.

## Logging

`pkg/log` provides a small structured logger. Generated logic carries a logger pre-bound to the request context:

```go
func (l *GetUserLogic) GetUser(req *pb.GetUserReq) (*pb.User, error) {
    l.Info("fetching user", log.String("id", req.Id))
    // ...
}
```

`trace_id`, `span_id`, and `request_id` flow into every log line automatically when OTel is enabled.

### Log level

The level is process-wide, not per-logger. `srv.SetLogger` mirrors one logger into both the server and `log.Default()`, and `New`/`NewConsole` build that logger over a shared `zap.AtomicLevel`, so a single call retunes the server and the generated logic layer together:

```go
log.SetLevel(log.LevelDebug)   // LevelDebug / LevelInfo / LevelWarn / LevelError
current := log.GetLevel()
```

The default is `LevelInfo`. The swap is atomic and takes effect on the next log call - no logger replacement needed, so it is safe to wire to a `/debug/loglevel` endpoint or a config reload. Loggers you build yourself and pass to `log.NewZap` keep their own level and ignore `SetLevel`.

## Tracing and metrics

Both signals are set up by one call, and the middleware that emits them is a method on what it returns:

```go
tel, err := telemetry.Init(ctx, cfg.Config)
defer tel.Shutdown(ctx)
srv.Use(tel.HTTPMiddleware())
```

`tel` owns everything: both providers, the Prometheus registry, and the scrape listener. `Shutdown` closes the listener and flushes any pending OTLP push batch, so `main.go` has one teardown line rather than three that must stay in step.

The middleware records spans for every request and stamps trace IDs onto the context. Metrics ride the same wrapper, which is why `otel.enabled: false` stops the spans but not the `http.server.*` series - those follow `metrics.enabled`.

### What gets emitted

craftgo follows OTel semantic conventions, so the instruments are the semconv ones - not the names other Go frameworks use. Three instruments, plus whatever your own code records:

| OTel instrument | Prometheus family | Unit |
| --- | --- | --- |
| `http.server.request.duration` | `http_server_request_duration_seconds` | seconds |
| `http.server.request.body.size` | `http_server_request_body_size_bytes` | bytes |
| `http.server.response.body.size` | `http_server_response_body_size_bytes` | bytes |

Each carries `http_request_method`, `http_response_status_code`, `http_route`, `network_protocol_name`, `network_protocol_version`, `server_address`, `url_scheme`, plus the `otel_scope_*` identity labels. `service.name` rides on `target_info`, not on the series.

`http_route` is the **route pattern**, not the request path - `/api/todos/{id}`, never `/api/todos/42` - so grouping by it cannot blow up cardinality. It comes from the pattern Go's `ServeMux` records on the matched request, which is exactly what generated routes register.

A p95-by-route panel therefore reads:

```promql
histogram_quantile(0.95,
  sum by (http_route, le) (rate(http_server_request_duration_seconds_bucket[5m])))
```

Migrating a dashboard from a framework that used the older `http.server.duration` convention (milliseconds, a `path` label) means three edits per panel: the metric name, `path` → `http_route`, and the panel unit from ms to seconds.

`service.name` comes from the top-level `serviceName` in `config.yaml` and rides on `target_info`, so both signals report one identity without configuring it twice.

See [Configuration](/guide/configuration) for the YAML knobs.

## ServiceContext

`ServiceContext` is the dependency container. Generated by craftgo as a struct with one field per declared middleware plus whatever you add:

```go
type ServiceContext struct {
    Config *config.Config
    Middlewares           // generated, embedded

    DB    *sql.DB         // your fields
    Cache *redis.Client
}
```

Pass it once to `server.New(svc)`. Every handler and logic layer receives it.

### Concurrency

`ServiceContext` is shared across every concurrent request - craftgo does NOT auto-lock its fields. Long-lived dependencies (DB pools, Redis clients, gRPC channels, …) handle their own locking internally and are safe to keep as bare fields. Mutable in-process state (maps, slices, counters) is your responsibility: either guard it with `sync.Mutex` / `sync.RWMutex` / `sync.Map`, use atomic types, or make the state per-request and pass it through `context.Context`. The example app embeds a `sync.Mutex` on `ServiceContext` and exposes `Lock()` / `Unlock()` helpers so handlers can wrap a multi-step map mutation in one critical section.

## What is not in craftgo

- No DI container with reflection
- No struct tag based binding for body fields
- No custom HTTP method dispatcher
- No interceptor chain that hides the request lifecycle
- No global state

If you can name a `net/http` concept, the craftgo equivalent uses it directly.
