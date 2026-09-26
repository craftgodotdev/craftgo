# craftgo

[![Go Report Card](https://goreportcard.com/badge/github.com/craftgodotdev/craftgo)](https://goreportcard.com/report/github.com/craftgodotdev/craftgo)

Design-first framework for Go HTTP services and event contracts.

You describe the API once in a small DSL. `craftgo gen` writes the typed structs, the validation, the HTTP handlers, the route wiring and an OpenAPI 3.1 document. The output is plain `net/http`: no custom router, and binding and validation are generated Go rather than reflection over struct tags (bodies go through `encoding/json`, swappable). You write the business logic and nothing else.

[Documentation](https://craftgodotdev.github.io/craftgo) · [Single-page reference for LLMs](https://craftgodotdev.github.io/craftgo/llms)

## Quickstart

Requires Go 1.26.6 or newer.

```bash
go install github.com/craftgodotdev/craftgo/cmd/craftgo@latest

mkdir hello && cd hello
go mod init example.com/hello
go get github.com/craftgodotdev/craftgo
craftgo init design
```

Write `design/users/service.craftgo`:

```craftgo
package design

type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
    age   int?   @gte(0) @lte(150)
}

type User {
    id    string
    name  string
    email string
}

@prefix("/v1")
service UserService {
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

Generate, then let Go fetch the modules the generated code imports:

```bash
craftgo gen design
go mod tidy
```

Fill the one stub it leaves for you:

```go
// internal/service/user_service/create_user.go
func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
    return &types.User{ID: "u1", Name: req.Name, Email: req.Email}, nil
}
```

Run it:

```bash
go run .
```

```bash
curl -X POST localhost:8080/api/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"","email":"nope"}'
# {"message":"name: length out of range [1, 80]"}
```

The handler decodes the body and runs `req.Validate()`: a body that fails is answered 400 before your code runs; one that passes reaches your function, and its reply is encoded. The `/api` comes from the manifest's `openapi.basePath`, the `/v1` from the service's `@prefix`.

## Events

The same design declares event contracts. Their Go code lands under `./internal/events` unless the manifest's
`events.targets` names another place. Write `design/orders/events.craftgo`:

```craftgo
package orders

type OrderPlaced {
    id     string
    total  float64 @gte(0)
    placed datetime
}

@contract("orders.placed.v1")
event Placed {
    payload OrderPlaced
}
```

`craftgo gen design` writes the payload struct and one typed descriptor:

```go
// internal/events/orders/events.go

// PlacedContract is the wire identity of Placed.
const PlacedContract = "orders.placed.v1"

// Placed is the orders.placed.v1 event contract.
var Placed = craftevents.NewEvent[types.OrderPlaced](PlacedContract, (*types.OrderPlaced).Validate)
```

That descriptor is the whole contract. Publishing is `orders.Placed.Publish(ctx, bus, &payload)` and listening is
`orders.Placed.Subscribe(bus, group, handler)`, both validated for you. Nothing generated names a broker: which
deployable listens, on which group and behind which middleware is Go you write where the bus is built. NATS and
Kafka transports ship in `pkg/events/nats` and `pkg/events/kafka`.

## What you get

- **One source of truth.** Types, validation, handlers, routes and the OpenAPI document come from the same files. Change the DSL, regenerate.
- **Plain net/http.** Handlers are `http.HandlerFunc` on an `*http.ServeMux`. Middleware is `func(http.Handler) http.Handler`.
- **Validation as code.** `@length`, `@format(email)`, `@gte`, `@pattern` and the rest compile to ordinary `if` statements.
- **A real type system.** Scalars that inherit their validators, enums, generics such as `Page<User>`, cross-package composition, mixins, typed error categories.
- **Editor support.** A language server with completion, hover, go-to-definition, diagnostics and formatting, plus a VS Code extension.
- **gRPC from protobuf.** A `.proto` in the design folder is a gRPC design: craftgo runs the protoc plugins for the pb code and generates the same structure around it - server layer, logic stubs, wiring, config, main - on the same runtime.
- **Safe to regenerate.** Your logic lives in stubs the CLI writes once and never overwrites.

## What gets generated

```
design/*.craftgo  --craftgo gen-->  internal/types/<package>/      structs and Validate()
                                    internal/events/<package>/     event descriptors
                                    internal/transport/<service>/  HTTP handlers
                                    internal/routes/               route registration
                                    internal/service/<service>/    your logic (written once)
                                    internal/middleware/           one scaffold per middleware (written once)
                                    internal/wiring/               one call that attaches the design
                                    svccontext/                    the dependency container (written once)
                                    config/                        config struct and example file
                                    docs/openapi.yaml              the OpenAPI document
                                    main.go                        entry point
design/*.proto    --craftgo gen-->  internal/pb/<dir>/             protoc-gen-go + protoc-gen-go-grpc output
                                    internal/grpc/<service>/       server layer, one file per RPC
                                    internal/service/<service>/    your logic (written once)
                                    internal/wiring/grpc.go        RegisterGRPC
```

Every path is a manifest key, so any of them can move. `output.kind: contracts` generates the contract half alone -
the payload types and the event descriptors - which is what a shared events package is.

## Documentation

[Getting started](https://craftgodotdev.github.io/craftgo/guide/getting-started) walks through a first endpoint.
[DSL basics](https://craftgodotdev.github.io/craftgo/guide/dsl-basics) is the syntax, and the
[decorator registry](https://craftgodotdev.github.io/craftgo/reference/decorator-registry) lists every decorator.
[Events](https://craftgodotdev.github.io/craftgo/guide/events) covers contracts, subscriptions and codecs.
[llms.md](https://craftgodotdev.github.io/craftgo/llms) is the whole reference on one page, for pasting into a prompt.

## License

See [LICENSE](./LICENSE).
