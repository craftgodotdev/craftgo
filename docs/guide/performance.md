# Performance

craftgo's design goal is "no overhead": generated code does the same work as what you would write by hand against `net/http` - routing, parameter binding and validation are generated Go, and bodies go through `encoding/json` as a hand-written handler's would.

## What "no overhead" means

A craftgo handler at runtime does:

1. Read `http.Request.Body`
2. Decode JSON via `encoding/json`
3. Run validators (plain if statements)
4. Call your business logic
5. Encode response via `encoding/json`
6. Write headers and body via `http.ResponseWriter`

Path, query, header, cookie and form values bind through generated code, with no reflection and no struct tags read; the JSON body is `encoding/json`'s, as in a hand-written handler. There is no custom router with regex compilation, no hidden interceptor chain, and no DI container.

## Why generated code wins

Generated code emits direct field assignments and explicit type conversions. A reflection-based binder walks struct fields, parses tags, dispatches on rule names, and calls reflect-based setters. The reflection work happens on every request; the generated work is paid once at compile time.

Side benefits:

- Stack traces show your endpoint names, not framework internals
- pprof attributes time to specific handlers, not to a generic dispatcher
- A debugger steps through the actual emitted code line by line

## What craftgo does not make faster

Things craftgo touches but does not optimize:

- **JSON parsing** uses stdlib `encoding/json`. Swap to `goccy/go-json` or `bytedance/sonic` via `srv.SetJSONCodec(...)` if you need faster JSON: the codec implements `server.JSONCodec`, and `server.StrictDecoder` too while `server.strictJSON` is on, as the generated `config.yaml` sets it - see [Runtime API](/reference/runtime-api#json-codec).
- **Database calls, external HTTP, business logic** are your code.
- **OTel tracing** when enabled adds the cost of `otelhttp.NewHandler`. This cost is not specific to craftgo.

## End-to-end benchmarks

For end-to-end numbers, point `wrk`, `bombardier`, or `oha` at your service and read the actual throughput and latency for your workload.

## The eject test

If you regenerate the example and copy `internal/types/`, `internal/transport/`, `internal/service/` and `svccontext/` into a project on plain `net/http`, the code compiles once you supply the `pkg/server` helpers the handlers call (`JSON`, `WriteValidationError`, `WriteError`, the `Bind*` / `Parse*` binders) and register the handlers on an `http.ServeMux`.

The generated code is plain Go. craftgo's runtime additions are convenient, not architectural.
