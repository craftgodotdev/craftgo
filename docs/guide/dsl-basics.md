# DSL Basics

The craftgo DSL is a small file format that describes your API. From it, craftgo generates Go types, validators, HTTP handlers, route registration, and an OpenAPI 3.1 spec.

## At a glance

A `.craftgo` file has two parts:

1. A `package` line, first in the file - only comments and the file-level decorators `@doc`, `@deprecated` and `@version` may come before it. A file that imports or declares anything without one is rejected as `package/missing` at its first import or declaration; a file holding only comments needs none. The name is also the generated Go package's, so a Go keyword, a predeclared Go identifier such as `int` or `len`, `main`, `init` and `_` are rejected as `package/name`
2. Declarations: `type`, `enum`, `scalar`, `error`, `service` (and `extend service`), `middleware`, `event`

Declaration names start with an upper-case letter: a lower-case `type`, `enum`, `scalar`, `error`, `middleware`, `event` or method name is rejected as `decl/name-case` (the Go identifier generated from it would be unexported); a lower-case service name only warns. Type parameter names follow the same rule (`type Page<T>`, not `<t>`).

Every declaration produces specific generated code. The DSL is the single source of truth: change a field once, every generated artifact updates.

```craftgo
package design

type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
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

This page covers the syntax. For per-decorator detail see [Decorators](/guide/decorators). For runtime behavior see [Runtime](/guide/runtime).

## File layout

A craftgo project keeps its DSL files under one design folder - the folder holding `craftgo.design.yaml`, `design/` by default. A package is every file that declares the same `package` name, in any subfolder; one subfolder per package is the usual layout.

```
design/
├── craftgo.design.yaml
├── users/
│   ├── service.craftgo
│   └── errors.craftgo
└── orders/
    └── service.craftgo
```

Files that declare the same package see each other's declarations directly, whatever their folder. To use a declaration from another package, qualify it with that package's name (`shared.Type`) - cross-package references resolve automatically, no import needed.

## Seven declaration kinds

Every craftgo file declares from this set:

| Keyword     | Purpose                                       |
| ----------- | --------------------------------------------- |
| `type`      | Request / response struct                     |
| `enum`      | Closed value set                              |
| `scalar`    | Named primitive with bundled validators       |
| `error`     | Typed error with HTTP status                  |
| `service`   | Group of HTTP methods                         |
| `middleware`| Named middleware slot                         |
| `event`     | Event contract this package owns              |

```craftgo
package design

type User       { id string  name string }
enum Status     { Active  Inactive }
scalar Email    string @format(email)
error NotFound  UserNotFound
service UserService {}
middleware Auth
event UserCreated { payload User }
```

A service body holds HTTP methods and nothing else; `event` is a file-level
declaration like the rest. Methods are covered below; see
[Events](/guide/events) for contracts and the listeners that receive them.

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

Field syntax: `name TypeRef [@decorator(...) ...]`. Type references are primitives, arrays (`T[]`), maps (`map<K, V>`), or other declared types. Append `?` to mark optional. A `type` always has a body: `type T` alone is a parse error.

| DSL form           | Go output                |
| ------------------ | ------------------------ |
| `string`           | `string`                 |
| `int` / `int64`    | matching Go integers     |
| `float64`          | `float64`                |
| `bool`             | `bool`                   |
| `bytes`            | `[]byte`                 |
| `T?`               | `*T` (an optional array, map or `bytes` stays `[]T` / `map[K]V` / `[]byte`, nil when absent) |
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

52 decorators, grouped by purpose: validators, bindings, metadata, service-level. Full reference at [Decorators](/guide/decorators).

A field's decorators follow its type and may continue on the lines below: every decorator up to the next field name belongs to the field above it, blank lines included - `@doc(...)` written on its own line between fields `a` and `b` decorates `a` (`craftgo fmt` moves it onto `a`'s line). Only the first field of a body takes decorators from the lines above it. Enum values work the same way, except that a decorator above the first value is an error. A declaration's or method's decorators go before its keyword; one after a declaration or method on the same line is a parse error (`decorator @doc follows a declaration on its line; a decorator goes before what it decorates`) unless the next declaration starts on that line. A decorator cannot be another decorator's argument: `@doc(@deprecated)` is a parse error (`a decorator cannot be an argument of @doc`).

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

Method form: `<verb> <Name> [<path>] { request <Type>  response <Type> }`; both clauses are optional, and without a path the route is the method name in kebab case (`get NoPath { ... }` serves `/no-path`).

Each method writes `<name>.go` holding `<Name>Service` and its constructor `New<Name>Service` into its service's directory, so the methods of one directory may not write one file (`GetURL` beside `GetUrl`) or one Go name (`Order` beside `NewOrder`), and no method is named `Logger`, the `log.Logger` every logic type embeds: `service/method-name-clash`. Nor may the file be one the go command sets apart: under the default `snake` file case `RunTest` writes `run_test.go`, built only for tests, and `ListWindows` writes `list_windows.go`, built only on Windows (`service/method-file-name`).

Verbs: `get`, `post`, `put`, `patch`, `delete`, `head`, `options`. `trace` and `connect` are not supported.

**A `request` names a `type`** (a generic instantiation such as `Page<Order>` included); **a `response` names a `type`, an enum or a scalar.** Bare arrays (`response Order[]`), optional markers (`response User?`) and built-in primitives (`response string`) are rejected in both clauses - wrap the shape in a type (`type Items { items Order[] }`) and reference that instead.

Path parameters use `{name}` and bind to the request field of that name (`@path` makes it explicit, `@path("name")` binds a field named otherwise):

```craftgo
type GetUserReq {
    id string @path
}
```

A path that declares `{name}` segments requires a request struct whose fields cover every segment; otherwise the route would parse the URL but the handler would never see the value, so the semantic phase rejects it with `path/param-missing`. The exception is a raw-request method (`@rawRequest` / `@passthrough`) with no request block: logic receives the raw `*http.Request` and reads the value with `r.PathValue`. A literal segment holds letters, digits, `-`, `.`, `_` and `~`, so `/.well-known/jwks.json`, `/v1.0/users` and `/reports/2024` are routes as written. A trailing slash (`/users/`) is a parse error: the route is built from segments, and a `net/http` pattern ending in `/` would match a whole subtree.

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
    get  Healthz /healthz { response HealthResp }
    post Signup  /signup  { request SignupReq  response User }
    post Login   /login   { request LoginReq   response Session }
}

@middlewares(AuthRequired)
@security(Bearer)
extend service Users {
    get  List   /users      { response UserList }                 // inherits AuthRequired + Bearer
    get  Get    /users/{id} { request GetUserReq  response User } // inherits
    post Create /users      { request CreateUserReq response User } // inherits
}
```

The extend block's `@middlewares` / `@security` decorators apply to every method inside as if they were written directly on the method. Each method can still add its own decorators on top - those append.

**Rules** (enforced at gen time with a diagnostic, not silently):

- The primary `service` block declares `@prefix` (the URL prefix is whole-service).
- `extend service` blocks may carry **method-level-applicable** decorators (any decorator a method takes, such as `@middlewares`, `@security`, `@tags` or `@timeout`, which every method of the block inherits) plus `@group` (which moves that block's own methods into the group's directory). `@prefix` on an extend raises `service/extend-decorator-not-method`, as does `@operationId`, which names a single operation.
- The extended service must be declared in the **same package** - a file declaring the same `package` name, in any folder, before or after the extend block; an extend whose primary lives in another package raises `service/extend-orphan`.
- Multiple `extend` blocks for the same service are allowed (one per file is the typical pattern). Each block contributes its own decorators only to its own methods.

The extended methods inherit every service-level decorator from the primary AND every decorator on the extend block. Method-level decorators of the same kind (`@middlewares`, `@security`, `@tags`) append; use `@ignoreMiddleware` / `@ignoreSecurity` / `@ignoreTags` to drop the inherited chain for one specific method, or on the extend block to drop the primary's chain for every method of the block.

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
service UserService {}
```

See [Middleware](/guide/middleware).

## Cross-package references

Reference a declaration from another package by qualifying it with that package's name - no import line needed (the same package in another folder needs no qualifier):

```craftgo
package design

type User {
    contact shared.Contact
}
```

The codegen wires the matching Go imports automatically.

Each package's types are one Go package, so two packages cannot reference each other's types in a cycle: `design` using `shared.Contact` while `shared` uses a `design` type is rejected as `ref/package-cycle`. An event payload does not count - events are generated outside the types packages.

## Comments

`//` line comments. Comments above a declaration become its doc string: the OpenAPI description, the Go doc of the generated declaration and the editor hover:

```craftgo
// User is the public user entity.
// Email is the canonical login id.
type User {}
```

Below a declaration's decorators, the comment right above its keyword joins the doc too, after the one above the decorators:

```craftgo
// Order is a placed order.
@deprecated
// Use Purchase instead.
type Order {}
```

A blank line between that comment and the keyword leaves it out of the doc. `//` only - no `/* */`. A line ends at `\n`, `\r\n` or a lone `\r`, and a UTF-8 byte-order mark opening a file is skipped.

## Next

- [Events](/guide/events) - event contracts, listeners, and the event runtime
- [Decorators](/guide/decorators) - the full decorator catalog
- [Validators](/guide/validators) - validation runtime semantics
- [Types and Scalars](/guide/types-and-scalars) - generics, mixins, advanced types
- [AI Reference](/llms) - one-page consolidated reference (paste this into LLM prompts)
