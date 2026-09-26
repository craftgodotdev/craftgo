---
title: gRPC
description: Design a gRPC service in protobuf, let craftgo run the protoc plugins and generate the same project structure the HTTP side gets - server layer, logic stubs, wiring, config, main - on the same runtime.
---

# gRPC

A gRPC service is designed in **protobuf**, not in the craftgo DSL. A `.proto` under the design folder is the design; `craftgo gen` compiles it, runs `protoc-gen-go` and `protoc-gen-go-grpc` for the pb code, and writes the craftgo structure around their output - the same split the HTTP side has: a regenerated server layer, gen-once logic stubs, one wiring call, and a `main.go` that boots the listener on the runtime the HTTP server uses (OTel traces and metrics, access log, recovery, deadlines, error mapping, health, graceful shutdown, one `ServiceContext`).

## The model

```
design/greet/greet.proto              yours - the design
        │
        ▼ craftgo gen
internal/pb/greet/*.pb.go             protoc-gen-go + protoc-gen-go-grpc, run by craftgo
internal/grpc/greeter/server.go       REGEN     implements pb.GreeterServer
internal/grpc/greeter/<rpc>.go        REGEN     one file per RPC, delegates to logic
internal/service/greeter/<rpc>.go     GEN-ONCE  the logic, yours
internal/wiring/grpc.go               REGEN     RegisterGRPC - the one call main.go makes
config/, svccontext/, main.go         GEN-ONCE  written once, with a `grpc:` block and a gRPC listener
```

Nothing here parses protobuf by hand: the `.proto` files are compiled in-process (the same compiler `buf` uses), the Go names come from `protogen` - the library `protoc-gen-go` itself is built on - and the pb code is written by the standard plugins. What craftgo adds is the structure and the runtime around them.

## Declaring a service

Put the `.proto` files under the design folder, beside any `.craftgo` files. The design folder is the **import root**: a proto imports a sibling by its path relative to that folder, and the well-known types (`google/protobuf/*.proto`) are always available.

```
design/
├── craftgo.design.yaml
├── greet/
│   └── greet.proto            package greet
└── common/
    └── money.proto            package common   ← imported as "common/money.proto"
```

```protobuf
syntax = "proto3";

package greet;

// Greeter is the service the generated server layer implements.
service Greeter {
  // SayHello answers one greeting.
  rpc SayHello(HelloRequest) returns (HelloReply);
  rpc ListHellos(HelloRequest) returns (stream HelloReply);
  rpc RecordHellos(stream HelloRequest) returns (HelloReply);
  rpc Chat(stream HelloRequest) returns (stream HelloReply);
}

message HelloRequest {
  string name = 1;
  int32 count = 2;
}

message HelloReply {
  string message = 1;
}
```

`option go_package` is not needed. craftgo places every design proto's Go package under `output.pb` (default `./internal/pb`), mirroring the proto's directory: `design/greet/greet.proto` becomes `internal/pb/greet`, import path `<module>/internal/pb/greet`, Go package `greet`. A `go_package` you do write keeps its `;name` suffix for the package name; its path is ignored. Two protos in one directory must declare one package, as Go would demand of the directory, and a proto directly in the design root lands in `output.pb` itself as package `pb` - give it a directory. An RPC may not be named `Server` (the server struct's file), `Logger` (the `log.Logger` its logic type embeds) or so that its file is one the go command builds only for tests or one system (`RunTest` → `run_test.go`), and two RPCs may not be named `X` and `NewX` (the logic type and its constructor); each is refused before anything is written.

A design with protos alone - no `.craftgo` at all - is a plain gRPC service; a design with both boots both listeners from one `main.go`. A gRPC-only project needs no manifest key of its own: its config carries no `server:` or `docs:` block, and no OpenAPI document is written, because the document describes the `.craftgo` half and there is none.

### The plugins

`protoc-gen-go` and `protoc-gen-go-grpc` run as **Go tools pinned in `go.mod`**, so the plugin version is the protobuf version the project builds with, on every machine, and no `protoc` install is needed. Pin them once:

```sh
go get -tool google.golang.org/protobuf/cmd/protoc-gen-go@latest google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

`craftgo gen` runs `go tool protoc-gen-go` and `go tool protoc-gen-go-grpc` from the project root. A project without the tool directives gets an error naming that command; nothing on `PATH` is picked up unasked. To run a particular binary instead, name it in the manifest:

```yaml
proto:
  plugins:
    go:     protoc-gen-go          # a bare name is looked up on PATH
    goGrpc: /opt/bin/protoc-gen-go-grpc   # a path is run as given
```

A team that generates pb code through its own `buf` or `protoc` pipeline sets `output.pb: "-"`: craftgo then runs no plugin, and every design proto must carry `option go_package` so the generated server layer knows what to import.

## What is generated

Per service, one package under `output.grpc` (default `./internal/grpc`), named by the service in the manifest's file case, with the proto file's Go package name as its package clause - the same rule the HTTP layers follow (directory by service, package by design package):

```go
// Code generated by craftgo. DO NOT EDIT.

package greet

import (
	pb "github.com/craftgodotdev/craftgo/example/grpc/internal/pb/greet"
	"github.com/craftgodotdev/craftgo/example/grpc/svccontext"
)

// Server implements pb.GreeterServer (greet.Greeter).
type Server struct {
	pb.UnimplementedGreeterServer
	svcCtx *svccontext.ServiceContext
}

// NewServer returns the implementation wiring.RegisterGRPC registers.
func NewServer(svcCtx *svccontext.ServiceContext) *Server {
	return &Server{svcCtx: svcCtx}
}
```

The methods live one file per RPC beside it, and the embedded `UnimplementedGreeterServer` answers `Unimplemented` for an RPC the proto gains before the next gen. Each file does what the HTTP handler does: validate, hand the call context to the logic, map its error onto a status:

```go
// SayHello answers one greeting.
//
// SayHello serves the unary RPC /greet.Greeter/SayHello.
func (s *Server) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	if err := rpc.Validate(req); err != nil {
		return nil, err
	}
	l := service.NewSayHelloService(ctx, s.svcCtx)
	resp, err := l.SayHello(req)
	if err != nil {
		return nil, rpc.Error(ctx, err)
	}
	return resp, nil
}
```

`rpc.Validate` runs the request's own `Validate() error` when it has one - the method `protoc-gen-validate` generates - and answers `InvalidArgument`. A message without that method passes, so the call is inert until you add such a plugin; a validator with no method of its own, protovalidate among them, goes on the server as an interceptor instead.

The logic stub lands under `output.service`, from the same template the HTTP stubs use, and is yours once written:

```go
// SayHelloService runs Greeter.SayHello for one request.
type SayHelloService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewSayHelloService binds SayHelloService to ctx; its Logger carries ctx's trace ids.
func NewSayHelloService(ctx context.Context, svcCtx *svccontext.ServiceContext) *SayHelloService

// SayHello answers one greeting.
//
// SayHello implements Greeter.SayHello.
func (l *SayHelloService) SayHello(req *pb.HelloRequest) (*pb.HelloReply, error) {
	// TODO: implement
	return nil, nil
}
```

The four streaming shapes give the logic these signatures - the generic stream types `protoc-gen-go-grpc` declares:

| RPC | Logic signature |
|---|---|
| `rpc SayHello(HelloRequest) returns (HelloReply)` | `SayHello(req *pb.HelloRequest) (*pb.HelloReply, error)` |
| `rpc ListHellos(HelloRequest) returns (stream HelloReply)` | `ListHellos(req *pb.HelloRequest, stream grpc.ServerStreamingServer[pb.HelloReply]) error` |
| `rpc RecordHellos(stream HelloRequest) returns (HelloReply)` | `RecordHellos(stream grpc.ClientStreamingServer[pb.HelloRequest, pb.HelloReply]) error` |
| `rpc Chat(stream HelloRequest) returns (stream HelloReply)` | `Chat(stream grpc.BidiStreamingServer[pb.HelloRequest, pb.HelloReply]) error` |

A request or response from another proto package - a sibling in the design, or a well-known type - is imported under that package's name (`emptypb.Empty`, `common.Money`).

`internal/wiring/grpc.go` holds the one call that attaches every service; its signature never changes with the design, so `main.go` is written once:

```go
func RegisterGRPC(ctx context.Context, srv *rpc.Server, svcCtx *svccontext.ServiceContext) (func(context.Context) error, error) {
	greetpb.RegisterGreeterServer(srv, greetergrpc.NewServer(svcCtx))
	return func(context.Context) error { return nil }, nil
}
```

It exists only while a proto declares a service, so an HTTP-only project's wiring package never imports the gRPC runtime.

## Runtime

`main.go` boots the gRPC listener with the guards the HTTP chain has, in the same order:

```go
grpcSrv := rpc.New(svc,
	rpc.WithStatsHandler(tel.GRPCServerHandler()),
	rpc.WithReflection(cfg.GRPC.Reflection),
)
grpcSrv.Use(rpc.AccessLog(log.Follow()))
// The unary deadline; a shorter client deadline wins, and streams are not bounded.
grpcSrv.Use(rpc.Timeout(cfg.GRPC.HandlerTimeout))

shutdownGRPC, err := wiring.RegisterGRPC(ctx, grpcSrv, svc)
```

- **Telemetry.** `tel.GRPCServerHandler()` is the gRPC twin of `tel.HTTPMiddleware()`: one `otelgrpc` stats handler emits the span and the `rpc.server.call.duration` histogram (`rpc_server_call_duration_seconds` on the Prometheus scrape), against the same providers and the same `serviceName`. It runs in the transport, ahead of every interceptor, so the access log and the logic's `log.Logger` carry `trace_id` / `span_id`. A traced stack adopts the caller's W3C trace context from the request metadata.
- **Recovery** is always outermost: a panic answers `Internal` with an opaque message and is logged with its stack and the call's trace ids.
- **Access log**: one `grpc access` line per call with `method`, `code` and `latency`; a stream logs once when it ends.
- **Timeout** bounds unary calls to `grpc.handlerTimeout`; a handler that returns after the deadline has its late response dropped for `DeadlineExceeded`. Streams are not bounded.
- **Health and reflection.** `grpc.health.v1` is registered and reports `SERVING` for the server and each registered service, then `NOT_SERVING` once `Stop` begins. Reflection follows `grpc.reflection` in `config.yaml`. Neither reaches the interceptors or the traces, as the HTTP probes never reach the middleware chain.
- **Shutdown.** `grpcSrv.Stop(ctx)` drains in-flight RPCs within the budget and cuts off the rest when it runs out, beside the HTTP drain and the telemetry flush.

### Errors

`rpc.Error(ctx, err)` is what the generated server layer returns when logic fails, the counterpart of `server.WriteError`:

- a gRPC status error, or an error wrapping one, passes through as it is;
- a craftgo typed error - any `error` with `HTTPStatus() int`, which every `error` the DSL declares has - becomes the status code its HTTP status maps to, with its message, and an `ErrorInfo` detail carrying its `ErrCode()` as the reason and the service as the domain. It is an expected outcome and is not logged;
- a context cancellation or deadline becomes `Canceled` or `DeadlineExceeded`;
- anything else is logged with the call's trace ids and answers `Internal` with an opaque message. `rpc.SetHandleUnknownError` swaps that handler process-wide.

| HTTP status | gRPC code |
|---|---|
| 400, 406, 411, 415, 422 | `InvalidArgument` |
| 401 | `Unauthenticated` |
| 402, 412, 423 | `FailedPrecondition` |
| 403 | `PermissionDenied` |
| 404, 410 | `NotFound` |
| 405, 501 | `Unimplemented` |
| 408, 504 | `DeadlineExceeded` |
| 409 | `AlreadyExists` |
| 413, 429 | `ResourceExhausted` |
| 500 | `Internal` |
| 502, 503 | `Unavailable` |
| any other | `Unknown` |

### Config

`config.yaml` gains a `grpc:` block beside `server:`:

```yaml
grpc:
  addr: ":9000"
  handlerTimeout: 0s
  reflection: true
```

A project with routes and RPCs runs both listeners in one process, with one `ServiceContext`, one config and one telemetry stack; a project with RPCs alone boots the gRPC listener only.

## Calling a gRPC service

The client is generated too: `protoc-gen-go-grpc` writes `GreeterClient` and `NewGreeterClient(cc)` into `greet_grpc.pb.go`, so nothing has to be written by hand. What the caller has to get right is the connection, and a bare `grpc.NewClient` gets one thing wrong: **a gRPC client sends no trace context of its own**. Without a stats handler there is no `traceparent` in the request metadata, the service you call opens a trace of its own, and the two halves of one request never meet in the backend.

`rpc.Dial` is where that is decided, beside the deadline and the access log:

```go
conn, err := rpc.Dial(addr,
	rpc.WithClientStatsHandler(tel.GRPCClientHandler()),
	rpc.WithClientAccessLog(log.Default()),
	rpc.WithClientTimeout(2*time.Second),
)
if err != nil {
	return nil, err
}
svc.Greeter = greetpb.NewGreeterClient(conn)
```

A connection is long-lived and dialed once, so it belongs on the `ServiceContext` beside your database handles - add the field there and the address to `config.go`, both of which are yours. `Dial` connects lazily, so a service may be dialed before the one it calls is up; an unreachable target is a failed call, not a failed startup. Close it when the process drains.

| Option | Effect |
|---|---|
| `WithClientStatsHandler(h)` | The client span, `rpc.client.call.duration`, and the trace context on the wire. `tel.GRPCClientHandler()` is the stack's own. |
| `WithClientTimeout(d)` | A default deadline for a unary call that carries none; a caller with its own keeps it. Streams are not bounded. |
| `WithClientAccessLog(l)` | One `grpc client` line per unary call with `method`, `code`, `latency` and the trace ids. |
| `WithClientTransportCredentials(c)` | Transport security. The default is insecure, which is what a call inside a cluster or a mesh uses. |
| `WithDialOptions(...)` | Straight to `grpc.NewClient`: a resolver, a load-balancing policy, keepalive, interceptors of your own. |

With the handler installed the caller's trace id reaches the callee's logs and spans; without it the callee starts a new trace. That is the whole difference, and it is one option.

### A client other services import

`internal/pb` is `internal`, so only the project that generated it can import the client. A service other teams call ships its contract instead: a project with `output.kind: contracts` whose design holds the `.proto` writes the pb code - client included - to `./gen/pb`, which any module can `go get`. The callers then dial it with `rpc.Dial` exactly as above.

## Adding gRPC to an existing project

`main.go` and `config/config.go` are written once. A project that had them before its first proto keeps them, and `craftgo gen` says so:

```
craftgo: ./main.go predates the gRPC services and never calls wiring.RegisterGRPC - it is generated once, so add the gRPC listener block by hand (docs/guide/grpc.md shows it)
craftgo: ./config/config.go predates the gRPC services and has no GRPCConfig - it is generated once, so add the `grpc:` block by hand (docs/guide/grpc.md shows it)
```

Add the `GRPCConfig` struct and its default to `config.go`, the `grpc:` block to `config.yaml`, and the listener block above to `main.go`; the wiring, the server layer and the stubs are already there. The reverse holds for a gRPC project that gains its first route, and for one that drops its last proto: `wiring/grpc.go` is swept, and `craftgo gen` names the `main.go` that still calls `RegisterGRPC`. The pb code of that last proto stays under `internal/pb/`, which a design with no proto leaves alone; delete it by hand.

The protos are compiled on every run, `--target docs` included: a proto that does not compile fails the run before anything is written, the way a design error does. Only the plugins are skipped when the Go target is not selected.

## Who owns what

| Layer | Owner |
|---|---|
| `design/**/*.proto` | you - the design |
| `internal/pb/` | the protoc plugins, run by `craftgo gen`; a proto's code is swept when the proto goes and its directory keeps another design proto |
| `internal/grpc/<svc>/` | craftgo, regenerated on every run |
| `internal/service/<svc>/<rpc>.go` | you, from the first `craftgo gen` on - a renamed or dropped service leaves its stubs where they are and `craftgo gen` names their directory, so move or delete them by hand |
| `internal/wiring/grpc.go` | craftgo, regenerated; present while a proto declares a service |
| `config/`, `svccontext/`, `main.go` | you, seeded once by `craftgo gen` |

The gRPC runtime is `pkg/rpc`; its API is on the [Runtime API](/reference/runtime-api) page.
