# craftgo

[![Go Report Card](https://goreportcard.com/badge/github.com/craftgodotdev/craftgo)](https://goreportcard.com/report/github.com/craftgodotdev/craftgo)

**Write the spec. Generate everything.**

craftgo is a design-first framework for Go HTTP services. You describe your API once in a small DSL — types, validators, endpoints, errors — and `craftgo gen` produces typed structs, request validation, HTTP handlers, route wiring, and an OpenAPI 3.1 spec. The generated code is plain `net/http`: no custom router, no reflection, no runtime struct tags. It reads like code you would have written by hand.

📖 **[Documentation](https://craftgodotdev.github.io/craftgo)** · 🤖 **[AI-ready reference (llms.md)](https://craftgodotdev.github.io/craftgo/llms)**

---

## Quickstart

Requires Go 1.24+.

```bash
# 1. Install the CLI
go install github.com/craftgodotdev/craftgo/cmd/craftgo@latest

# 2. New project
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

Generate, then fill the one logic stub:

```bash
craftgo gen design
```

```go
// internal/service/user-service/create-user.go  (the only file you edit)
func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
    return &types.User{ID: "u1", Name: req.Name, Email: req.Email}, nil
}
```

```bash
go run .
# listening on :8080 (api)
```

```bash
curl -X POST localhost:8080/api/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"","email":"nope"}'
# name: length out of range [1, 80]
```

Validation ran with zero hand-written code. The handler decoded JSON, called `req.Validate()`, dispatched to your function, and encoded the response.

## What you get

- **One source of truth** — types, validators, handlers, routes, and the OpenAPI spec all come from the same `.craftgo` files. Change the DSL, regenerate, done.
- **Plain `net/http`** — generated handlers are `http.HandlerFunc` registered on `*http.ServeMux`. Middleware is `func(http.Handler) http.Handler`. Nothing to learn beyond the standard library.
- **Declarative validation** — `@length`, `@format(email)`, `@gte`, `@pattern`, `@requiresOneOf`, … compile to plain Go `if` statements. No reflection, no runtime tags.
- **OpenAPI 3.1** — emitted from the same source, renders in Swagger UI, feeds `openapi-generator` for clients in any language.
- **Rich type system** — scalars with inherited validators, enums, generics (`Page<User>`), cross-package composition, mixins, typed error categories.
- **First-class tooling** — an LSP server (completion, hover, go-to-definition, live diagnostics, formatting) and a VS Code extension.
- **Regenerate-safe** — your business logic lives in gen-once stubs the CLI never overwrites; everything else regenerates on every `craftgo gen`.

## How it fits together

```
design/*.craftgo  ──craftgo gen──▶  internal/types/      typed structs + Validate()
                                    internal/transport/  HTTP handlers
                                    internal/routes/      route registration
                                    internal/service/     logic stubs (you edit these)
                                    docs/openapi.yaml      OpenAPI 3.1 spec
                                    main.go                wired entry point
```

## Documentation

|                                                                                            |                                                            |
| ------------------------------------------------------------------------------------------ | ---------------------------------------------------------- |
| [Getting Started](https://craftgodotdev.github.io/craftgo/guide/getting-started)           | Build and run your first endpoint in 5 minutes             |
| [DSL Basics](https://craftgodotdev.github.io/craftgo/guide/dsl-basics)                     | The full syntax: types, services, decorators               |
| [Decorator Registry](https://craftgodotdev.github.io/craftgo/reference/decorator-registry) | Every decorator, its arguments, and where it applies       |
| [Runtime API](https://craftgodotdev.github.io/craftgo/reference/runtime-api)               | `pkg/server` — the `net/http` wrapper your code runs on    |
| [Codegen Output](https://craftgodotdev.github.io/craftgo/reference/codegen-output)         | Exactly what `craftgo gen` produces, file by file          |
| [llms.md](https://craftgodotdev.github.io/craftgo/llms)                                    | Single-page reference built for pasting into an LLM prompt |

## License

See [LICENSE](./LICENSE).
