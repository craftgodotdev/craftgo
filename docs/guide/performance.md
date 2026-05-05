# Performance

craftgo's design goal is "no overhead": generated code does the same work as what you would write by hand against `net/http`, with no reflection in the hot path.

## What "no overhead" means

A craftgo handler at runtime does:

1. Read `http.Request.Body`
2. Decode JSON via `encoding/json`
3. Run validators (plain if statements)
4. Call your business logic
5. Encode response via `encoding/json`
6. Write headers and body via `http.ResponseWriter`

There is no reflection-based field binding, no struct tag parsing at request time, no custom router with regex compilation, no hidden interceptor chain, and no DI container.

## Bind benchmarks

The benchmarks live in the craftgo repo at `internal/bench/`. They compare the generated bind path against a reflection-based equivalent; both populate the same struct from the same pre-extracted inputs (path map, query map, JSON body). HTTP machinery (request parsing, header maps, cookie iteration) is excluded so the numbers reflect only the parse and write-back work.

Reproduce the run:

```bash
git clone https://github.com/craftgodotdev/craftgo
cd craftgo
go test -run=^$ -bench=BenchmarkParse -benchmem -benchtime=2s -count=1 ./internal/bench/
```

Output on Apple M1, Go 1.24:

```
BenchmarkParseSimpleCodegen-8       55,751,988    37.74 ns/op    24 B/op     1 allocs/op
BenchmarkParseSimpleReflect-8       10,511,245   231.1  ns/op    48 B/op     2 allocs/op
BenchmarkParseComplexCodegen-8       1,657,273  1450    ns/op   744 B/op    16 allocs/op
BenchmarkParseComplexReflect-8         875,757  2701    ns/op   960 B/op    26 allocs/op
BenchmarkParseComplexBodyOnly-8      1,818,680  1322    ns/op   720 B/op    14 allocs/op
```

What the rows mean:

- **SimpleCodegen / SimpleReflect**: a 2-field request (path id + query int). Codegen is ~6x faster and uses half the allocations. The simple shape is where reflection's overhead dominates.
- **ComplexCodegen / ComplexReflect**: a 9-field request (path + 4 query + header + cookie + JSON body). Codegen is ~1.9x faster; the gap shrinks because JSON decoding (shared by both paths) takes a larger share of the time.
- **ComplexBodyOnly**: only the JSON body decode, no path/query/header/cookie binding. This is the floor for the complex shape. Codegen adds ~128 ns over the floor; reflection adds ~1379 ns.

These numbers are real and reproducible. The exact figures depend on hardware, Go version, and request shape, so re-run on your target machine to ground decisions in your environment.

## Where allocations come from

In the handler hot path, allocations are exactly:

- The `*Req` struct
- Any string or slice fields populated from the body
- The `*Resp` struct your logic returns
- `json.Decoder` and `json.Encoder` internal buffers

No framework allocations beyond stdlib.

## Why generated code wins

Generated code emits direct field assignments and explicit type conversions. The reflection path walks struct fields, parses tags, dispatches on rule names, and calls reflect-based setters. The reflection work happens on every request; the generated work is paid once at compile time.

Side benefits:

- Stack traces show your endpoint names, not framework internals
- pprof attributes time to specific handlers, not to a generic dispatcher
- A debugger steps through the actual emitted code line by line

## What craftgo does not make faster

Things craftgo touches but does not optimize:

- **JSON parsing** uses stdlib `encoding/json`. Swap to `goccy/go-json` or `bytedance/sonic` via `srv.SetCodec(...)` if you need faster JSON.
- **Database calls, external HTTP, business logic** are your code.
- **OTel tracing** when enabled adds the cost of `otelhttp.NewHandler`. This cost is not specific to craftgo.

## End-to-end benchmarks

`internal/bench` covers the bind path. There are no end-to-end HTTP benchmarks shipped because the wrapper around `net/http` adds nothing measurable beyond the bind path; the result would match `net/http` directly.

If you want end-to-end numbers, point `wrk`, `bombardier`, or `oha` at your service and read the actual throughput and latency for your workload.

## The eject test

If you regenerate the example and copy `internal/transport/`, `internal/types/`, and `internal/routes/` into a fresh project that uses `net/http` directly, the code still compiles and runs once you replace the `pkg/server` calls with `http.NewServeMux` and the `pkg/log` calls with your logger of choice.

The generated code is plain Go. craftgo's runtime additions are convenient, not architectural.
