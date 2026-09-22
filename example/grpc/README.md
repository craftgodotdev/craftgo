# grpc

A gRPC service designed in protobuf. `design/greet/greet.proto` is the
design; `craftgo gen` runs `protoc-gen-go` and `protoc-gen-go-grpc` (pinned
as tools in `go.mod`) and writes the craftgo structure around their output:

```
internal/pb/greet/            greet.pb.go, greet_grpc.pb.go   the plugins' code
internal/grpc/greeter/        server.go + one file per RPC     regenerated: implements pb.GreeterServer
internal/service/greeter/     one file per RPC                 written once: the logic, yours
internal/wiring/grpc.go       RegisterGRPC                     regenerated
config/, svccontext/, main.go                                  written once
```

`main.go` boots the gRPC listener with the same guards the HTTP server
gets: the OTel stats handler (spans + `rpc.server.call.duration`), the
access log, the default deadline, recovery, the `grpc.health.v1` service
and, when `config.yaml` says so, reflection.

```sh
go run .
grpcurl -plaintext -d '{"name":"craftgo"}' localhost:9000 greet.Greeter/SayHello
grpcurl -plaintext -d '{"name":"craftgo","count":3}' localhost:9000 greet.Greeter/ListHellos
```

The pinned plugins come from `go.mod`; a fresh project pins them once with

```sh
go get -tool google.golang.org/protobuf/cmd/protoc-gen-go@latest google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```
