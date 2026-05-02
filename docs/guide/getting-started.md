# Getting Started

This page walks through creating a craftgo project, defining one endpoint, and running it. About 5 minutes.

## Prerequisites

- Go 1.24 or later
- A terminal

## Install the CLI

```bash
go install github.com/dropship-dev/craftgo/cmd/craftgo@latest
```

Verify:

```bash
craftgo --help
```

## Create a project

```bash
mkdir hello && cd hello
go mod init example.com/hello
go get github.com/dropship-dev/craftgo
```

## Scaffold the design folder

```bash
craftgo init design
```

This creates `design/craftgo.design.yaml` with default settings. Now write a `.craftgo` file:

`design/users/service.craftgo`:

```craftgo
package design

type CreateUserReq {
    name  string @required @length(1, 80)
    email string @required @format(email)
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

Folder layout so far:

```
hello/
├── design/
│   ├── craftgo.design.yaml
│   └── users/
│       └── service.craftgo
├── go.mod
└── go.sum
```

## Generate

```bash
craftgo gen design
```

The CLI walks up from the given path looking for `craftgo.design.yaml`, reads `go.mod` for the Go module path, then writes:

```
hello/
├── design/...                                  (your DSL, unchanged)
├── internal/
│   ├── types/design/                           generated structs + Validate()
│   │   ├── types.go
│   │   ├── validate.go
│   │   └── errors.go
│   ├── transport/user-service/                 generated HTTP handlers
│   │   ├── create-user.go
│   │   └── errors.go
│   ├── service/user-service/                   gen-once business logic stubs
│   │   └── create-user.go
│   ├── routes/                                 generated routing
│   │   ├── routes.go
│   │   └── user-service/routes.go
│   └── middleware/                             gen-once middleware stubs
├── svccontext/svccontext.go                    gen-once dependency container
├── config/                                     gen-once runtime config
│   ├── config.go
│   ├── config.yaml
│   └── example.config.yaml
├── docs/openapi.yaml                           generated OpenAPI 3.1 spec
├── main.go                                     gen-once entry point
└── go.mod
```

## Implement business logic

Open `internal/service/user-service/create-user.go`:

```go
func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
    return &types.User{
        ID:    "u1",
        Name:  req.Name,
        Email: req.Email,
    }, nil
}
```

This is the only file you edit. Everything in `internal/types/`, `internal/transport/`, `internal/routes/` is regenerated.

## Run

```bash
go run .
```

```
listening on :8080 (api)
```

In another terminal:

```bash
curl -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"alice","email":"alice@example.com"}'
```

```json
{"id":"u1","name":"alice","email":"alice@example.com"}
```

(`/api` comes from `openapi.basePath: /api` in `craftgo.design.yaml`; `/v1` from `@prefix("/v1")` in the DSL.)

Try a bad payload:

```bash
curl -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"","email":"not-an-email"}'
```

```
name: must be at least 1 character; email: does not match pattern
```

The validators ran without you writing any code.

## What just happened

1. The DSL described one endpoint, two types, three validators.
2. `craftgo gen` produced typed Go structs, an HTTP handler, a logic stub, route registration, and an OpenAPI spec.
3. You filled the logic stub.
4. The handler decoded JSON, ran `req.Validate()`, called your function, encoded the response.

No reflection. No struct tags. No middleware boilerplate. The handler is a plain `http.HandlerFunc` registered on `*http.ServeMux`.

## Designing with an LLM?

If you draft `.craftgo` files with Claude, ChatGPT, Cursor, or any other LLM, paste this URL into your prompt:

```
https://craftgo.dev/llms
```

It is a single-page consolidated reference (every keyword, decorator, CLI flag, and generated layout) designed for AI ingestion. Your assistant will know the full spec and stop inventing non-existent decorators.

## Next steps

- Read [DSL Basics](/guide/dsl-basics) to learn the syntax in depth.
- Browse [Decorators](/guide/decorators) to see every decorator with arguments and sites.
- Set up the [LSP](/guide/lsp) for completion, hover, and diagnostics in your editor.
- See [Configuration](/guide/configuration) to relocate generated files or add custom config.
