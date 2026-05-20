# DSL Basics

The craftgo DSL is a small file format that describes your API. From it, craftgo generates Go types, validators, HTTP handlers, route registration, and an OpenAPI 3.1 spec.

## At a glance

A `.craftgo` file has three things:

1. A `package` line (mandatory)
2. Optional `import` lines for cross-package references
3. Declarations: `type`, `enum`, `scalar`, `error`, `service`, `middleware`

Every declaration produces specific generated code. The DSL is the single source of truth: change a field once, every generated artifact updates.

```craftgo
package design

type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
}

@prefix("/v1")
service UserService {
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

This page covers the syntax. For per-decorator detail see [Decorators](/guide/decorators). For runtime behavior see [Runtime](/guide/runtime).

## File layout

A craftgo project keeps DSL files under a `design/` folder. Each subfolder is one logical package.

```
design/
├── users/
│   ├── service.craftgo
│   └── errors.craftgo
└── orders/
    └── service.craftgo
```

Files in the same subfolder share one package and see each other's declarations directly. Across subfolders, use `import`.

## Six declaration kinds

Every craftgo file declares from this set:

| Keyword     | Purpose                                       |
| ----------- | --------------------------------------------- |
| `type`      | Request / response struct                     |
| `enum`      | Closed value set                              |
| `scalar`    | Named primitive with bundled validators       |
| `error`     | Typed error with HTTP status                  |
| `service`   | Group of HTTP methods                         |
| `middleware`| Named middleware slot                         |

```craftgo
package design

import "shared"

type User       { id string  name string }
enum Status     { Active  Inactive }
scalar Email    string @format(email)
error NotFound  UserNotFound
service UserService { ... }
middleware Auth
```

## Types

Describe the shape of request and response bodies.

```craftgo
type CreateUserReq {
    name  string
    email string
    age   int?
    tags  string[]
    meta  map<string, string>
}
```

Field syntax: `name TypeRef [@decorator(...) ...]`. Type references are primitives, arrays (`T[]`), maps (`map<K, V>`), or other declared types. Append `?` to mark optional.

| DSL form           | Go output                |
| ------------------ | ------------------------ |
| `string`           | `string`                 |
| `int` / `int64`    | matching Go integers     |
| `float64`          | `float64`                |
| `bool`             | `bool`                   |
| `bytes`            | `[]byte`                 |
| `T?`               | `*T`                     |
| `T[]`              | `[]T`                    |
| `map<K, V>`        | `map[K]V`                |
| `Custom`           | `Custom` (your type)     |

See [Types and Scalars](/guide/types-and-scalars) for advanced shapes (generics, mixins).

## Decorators

Decorators attach metadata. They start with `@` and may take arguments.

```craftgo
type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
    age   int?   @gte(0) @lte(150)
}
```

50 decorators total, grouped by purpose: validators, bindings, metadata, service-level. Full reference at [Decorators](/guide/decorators).

## Services

A `service` is a group of HTTP methods sharing a path prefix and middleware chain.

```craftgo
@prefix("/v1")
@tags(users)
service UserService {
    @doc("Fetch a user.")
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }

    @doc("Create a user.")
    @status(201)
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

Method form: `<verb> <Name> <path> { request <Type>  response <Type> }`.

Verbs: `get`, `post`, `put`, `patch`, `delete`, `head`, `options`.

Path parameters use `{name}` and bind to fields with `@path`:

```craftgo
type GetUserReq {
    id string @path
}
```

### Extending a service across files

Real services grow. To split methods across files (or add admin endpoints in a separate file from the public ones), use `extend service`:

```craftgo
// design/users/service.craftgo - the primary block
package design

@prefix("/users")
@middlewares(AuthRequired)
service UserService {
    get GetUser /{id} {
        request  GetUserReq
        response User
    }
}
```

```craftgo
// design/users/admin.craftgo - additional methods, same service
package design

extend service UserService {
    @middlewares(AdminOnly)
    delete PurgeUser /{id}/purge {
        request  GetUserReq
        response shared.OkResp
    }
}
```

After codegen, both methods live under the same service, sharing the `/users` prefix and the `AuthRequired` middleware. `PurgeUser` additionally runs `AdminOnly`.

#### `extend service` carries its own decorators

The `extend` block itself can declare **method-level-applicable decorators** that propagate to every method in the block. The canonical use case is the **50/50 split**: half the methods need auth, half don't.

```craftgo
service Users {
    // Public endpoints - no service-level decorators
    get  /healthz => Health()
    post /signup  => Signup()
    post /login   => Login()
}

@middlewares(AuthRequired)
@security(Bearer)
extend service Users {
    get    /users      => List()       // inherits AuthRequired + Bearer
    get    /users/{id} => Get()         // inherits
    post   /users      => Create()       // inherits
}
```

The extend block's `@middlewares` / `@security` decorators apply to every method inside as if they were written directly on the method. Each method can still add its own decorators on top - those append.

**Rules** (enforced at gen time with a diagnostic, not silently):

- The primary `service` block declares whole-service decorators (`@prefix`, `@group` belong here).
- `extend service` blocks may carry **method-level-applicable** decorators only (`@middlewares`, `@security`, `@tags`, `@deprecated`, `@externalDocs`, `@doc`). Service-only decorators like `@prefix` on an extend raise `service/extend-decorator-not-method`.
- The extended service must already be declared somewhere in the **same package** (same design subfolder); a cross-package extend raises `service/extend-orphan`.
- Multiple `extend` blocks for the same service are allowed (one per file is the typical pattern). Each block contributes its own decorators only to its own methods.

The extended methods inherit every service-level decorator from the primary AND every decorator on the extend block. Method-level decorators of the same kind (`@middlewares`, `@security`, `@tags`) append; use `@ignoreMiddleware` / `@ignoreSecurity` / `@ignoreTags` to drop the inherited chain for one specific method.

See [Decorators - Service-level decorators and inheritance](/guide/decorators#service-level-decorators-and-inheritance) for the full combine semantics and combinations cheatsheet.

**When to use it**:

- Split a large service across files for navigability (admin vs public, read vs write)
- Group methods by feature area (`profile.craftgo`, `billing.craftgo`, `notifications.craftgo`)
- Separate frequently changed endpoints from stable ones

If your service has 5 methods, keep them in one file. `extend` shines around 10+ methods or when methods cluster by audience.

## Enums

Closed value sets:

```craftgo
enum Status {
    Active
    Inactive
    Pending
}
```

Three forms: bare identifiers (Go string constants), `= 1` (integer), `= "active"` (custom string). See [Enums](/guide/enums).

## Scalars

Named primitives with built-in validators:

```craftgo
scalar Email string @format(email) @maxLength(254)
scalar Cents int @gte(0) @multipleOf(2)

type Order {
    email Email
    total Cents
}
```

Every field of type `Email` automatically runs `@format(email)` and `@maxLength(254)`. See [Types and Scalars](/guide/types-and-scalars).

## Errors

Typed errors with HTTP status mapping:

```craftgo
error NotFound UserNotFound
error Conflict EmailTaken {
    email string
}
```

See [Errors](/guide/errors).

## Middleware

Declared at file level, attached to services or methods via `@middlewares`:

```craftgo
middleware AuthRequired
middleware RateLimit

@middlewares(AuthRequired, RateLimit)
service UserService { ... }
```

See [Middleware](/guide/middleware).

## Imports

Files reference declarations across folders with `import`:

```craftgo
package design

import "shared"

type User {
    contact shared.Contact
}
```

The codegen wires the matching Go imports automatically.

## Comments

`//` line comments. Comments above a declaration become its doc string and surface in OpenAPI:

```craftgo
// User is the public user entity.
// Email is the canonical login id.
type User { ... }
```

`//` only - no `/* */`.

## Next

- [Decorators](/guide/decorators) - the full decorator catalog
- [Validators](/guide/validators) - validation runtime semantics
- [Types and Scalars](/guide/types-and-scalars) - generics, mixins, advanced types
- [AI Reference](/llms) - one-page consolidated reference (paste this into LLM prompts)
