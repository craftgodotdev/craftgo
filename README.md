# craftgo

[![Go Report Card](https://goreportcard.com/badge/github.com/craftgodotdev/craftgo)](https://goreportcard.com/report/github.com/craftgodotdev/craftgo)

Design-first framework for Go HTTP services and event contracts.

You describe the API once in a small DSL. `craftgo gen` writes the typed structs, the validation, the HTTP handlers, the route wiring and an OpenAPI 3.1 document. The output is plain `net/http`: no custom router, no reflection, no runtime struct tags. You write the business logic and nothing else.

[Documentation](https://craftgodotdev.github.io/craftgo) · [Single-page reference for LLMs](https://craftgodotdev.github.io/craftgo/llms)

## Quickstart

Requires Go 1.26 or newer.

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

Generate:

```bash
craftgo gen design
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
# name: length out of range [1, 80]
```

The handler decoded the body, ran `req.Validate()`, called your function and encoded the reply. The `/api` comes from the manifest's `openapi.basePath`, the `/v1` from the service's `@prefix`.

## What you get

- **One source of truth.** Types, validation, handlers, routes and the OpenAPI document come from the same files. Change the DSL, regenerate.
- **Plain net/http.** Handlers are `http.HandlerFunc` on an `*http.ServeMux`. Middleware is `func(http.Handler) http.Handler`.
- **Validation as code.** `@length`, `@format(email)`, `@gte`, `@pattern` and the rest compile to ordinary `if` statements.
- **A real type system.** Scalars that inherit their validators, enums, generics such as `Page<User>`, cross-package composition, mixins, typed error categories.
- **Event contracts.** `event` declares a contract and craftgo generates one typed descriptor for it. Which deployable listens, on which group, behind which middleware, is Go you write where the bus is built, so nothing generated names a broker.
- **Editor support.** A language server with completion, hover, go-to-definition, diagnostics and formatting, plus a VS Code extension.
- **Safe to regenerate.** Your logic lives in stubs the CLI writes once and never overwrites.

## What gets generated

```
design/*.craftgo  --craftgo gen-->  internal/types/<package>/   structs and Validate()
                                    internal/transport/<service>/  HTTP handlers
                                    internal/routes/               route registration
                                    internal/service/<service>/    your logic (written once)
                                    internal/wiring/               one call that attaches the design
                                    config/                        config struct and example file
                                    docs/openapi.yaml              the OpenAPI document
                                    main.go                        entry point
```

Every path is a manifest key, so any of them can move.

## Documentation

[Getting started](https://craftgodotdev.github.io/craftgo/guide/getting-started) walks through a first endpoint.
[DSL basics](https://craftgodotdev.github.io/craftgo/guide/dsl-basics) is the syntax, and the
[decorator registry](https://craftgodotdev.github.io/craftgo/reference/decorator-registry) lists every decorator.
[Events](https://craftgodotdev.github.io/craftgo/guide/events) covers contracts, subscriptions and codecs.
[llms.md](https://craftgodotdev.github.io/craftgo/llms) is the whole reference on one page, for pasting into a prompt.

## License

See [LICENSE](./LICENSE).
