---
layout: home
description: "Design-first Go framework on net/http: write your API once in a small DSL and generate typed structs, validators, net/http handlers, and an OpenAPI 3.1 spec - no reflection, no framework in the hot path."

hero:
  name: craftgo
  text: Write the spec. Generate everything.
  tagline: A design-first Go framework on net/http. Zero overhead.
  actions:
    - theme: brand
      text: Get Started
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/craftgodotdev/craftgo

features:
  - icon: 🎯
    title: Design-first
    details: Write your API once in a small DSL. Get types, validators, handlers, and an OpenAPI spec from the same source.

  - icon: 🔌
    title: net/http compatible
    details: Built on the standard library. Middleware is plain func(http.Handler) http.Handler. No custom router. No magic.

  - icon: 📜
    title: OpenAPI 3.1
    details: Emitted from the same source. Renders in Swagger UI and ReDoc, feeds openapi-generator for clients in any language.

  - icon: ✅
    title: Built-in validation
    details: Declarative validators in the DSL. Generated as plain Go if statements. No reflection, no struct tags at runtime.

  - icon: ⚡
    title: No overhead
    details: Generated code looks like what you would write by hand. Stdlib mux, stdlib JSON, stdlib middleware shape.

  - icon: 🛠
    title: First-class tooling
    details: LSP server with completion, diagnostics, hover, and go-to-definition. VS Code extension included.
---

## What you write

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
    age   int?
}

@prefix("/v1")
service UserService {
    @doc("Create a new user.")
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

## What you get

```bash
$ craftgo gen design
craftgo: generated 1 package(s) under .
```

Four files generated, one folder structure to fill in:

- `internal/types/design/types.go` - Go structs with `Validate()` methods
- `internal/transport/user-service/create-user.go` - HTTP handler wired to your logic
- `internal/service/user-service/create-user.go` - business logic stub for you to fill
- `docs/openapi.yaml` - OpenAPI 3.1 spec

You write the business logic. Everything else is generated.

## Quick example

```go
// internal/service/user-service/create-user.go
func (s *Service) CreateUser(ctx context.Context, req *types.CreateUserReq) (*types.User, error) {
    user := types.User{
        ID:    uuid.NewString(),
        Name:  req.Name,
        Email: req.Email,
        Age:   req.Age,
    }
    if err := s.svcCtx.UserStore.Save(ctx, &user); err != nil {
        return nil, err
    }
    return &user, nil
}
```

That is it. The handler decodes JSON, runs the validators, calls your code, encodes the response. Re-run `craftgo gen` after any DSL change. Logic files are scaffold-once and stay yours.

## Designing with an LLM?

If you use Claude, ChatGPT, Cursor, GitHub Copilot, or another LLM to draft your `.craftgo` files, paste this URL into your prompt:

```
https://craftgodotdev.github.io/craftgo/llms
```

The page is a single-file consolidated reference: every keyword, every decorator, every CLI command, the generated layout, and common patterns. Designed for AI ingestion, not human reading. Your assistant will know the spec end-to-end and stop hallucinating non-existent decorators.

Example prompt:

```
Read https://craftgodotdev.github.io/craftgo/llms then design a craftgo DSL file for a
Tasks service with create / list / get / update / delete endpoints.
Use a Status enum (Pending / InProgress / Done) and validate the
title with @length(1, 200).
```
