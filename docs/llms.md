# AI Reference

A single-page consolidated reference for AI agents and search indexes. Covers the entire DSL syntax, every decorator, every CLI command, the configuration files, and the generated layout. Treat this as the source of truth when generating craftgo code.

This page lives at `/llms` so AI tooling can fetch one URL and ingest the full surface.

## Quick mental model

1. Write `.craftgo` files describing your API (types, services, methods, validators). The shorter `.cg` extension is also accepted, and a project may mix both.
2. Run `craftgo gen <design-dir>` to generate Go types, validators, HTTP handlers, an OpenAPI 3.1 spec, and stubs for business logic + middleware.
3. Fill in business logic at `internal/service/<service>/<method>.go` (gen-once - your edits stick). Generated file/dir names are snake_case by default (e.g. `internal/service/user_service/get_user.go`); see `output.fileCase`.
4. Run with `go run .`. The framework wraps `net/http` directly.

DSL is the contract. Generated code is plain Go. No reflection at runtime.

## File grammar

```
package <ident>

[<decl>]*

<decl> is one of:
  [@decorator]* type Name { fields... }
  [@decorator]* type Name<T> { fields... }              // generic; also Name<T, U, ...>
  [@decorator]* enum Name { values... }
  [@decorator]* error Category Name [{ fields... }]
  [@decorator]* scalar Name <Primitive> [@validators...]
  [@decorator]* service Name { methods... }
  [@decorator]* extend service Name { methods... }
  [@decorator]* middleware Name
```

Files in the same directory share `package` and see each other's declarations. Cross-directory references qualify with the target package's name (`shared.Type`); no import statement is needed (an `import "<sibling-dir>"` line is still accepted but deprecated).

## Keywords (15)

`package`, `import`, `type`, `enum`, `error`, `scalar`, `service`, `extend`, `middleware`, `request`, `response`, `map`, `true`, `false`, `null`. Plus HTTP verbs (`get`, `post`, `put`, `patch`, `delete`, `head`, `options`).

## Types

Field syntax: `name TypeRef [@decorator(...) ...]`.

| DSL form         | Go output               | Notes                                      |
| ---------------- | ----------------------- | ------------------------------------------ |
| `string`         | `string`                |                                            |
| `bytes`          | `[]byte`                | base64-decoded from JSON                   |
| `int`            | `int`                   | platform-sized                             |
| `int8/16/32/64`  | matching Go             |                                            |
| `uint`           | `uint`                  |                                            |
| `uint8/16/32/64` | matching Go             |                                            |
| `float32/64`     | matching Go             |                                            |
| `bool`           | `bool`                  |                                            |
| `any`            | `any`                   | arbitrary JSON value (`object` is rejected as a field type) |
| `file`           | `*multipart.FileHeader` | only with `@form`                          |
| `T?`             | `*T` or nilable as-is   | optional                                   |
| `T[]`            | `[]T`                   | array                                      |
| `map<K, V>`      | `map[K]V`               | K must be string / int* / uint* (or a scalar/enum over one); no `?`, bool, float, struct, slice keys |
| `Custom`         | `Custom`                | references a declared type / scalar / enum |

### Mixins

A bare PascalCase type name on its own embeds that type's fields into the enclosing type. No special prefix - the parser disambiguates by context.

```craftgo
type Auditable { createdAt string  updatedAt string }
type Identified { id string }

type User {
    Auditable
    Identified
    name string
}
```

Compact form (multiple members on one line) is also valid:

```craftgo
type User { Auditable  Identified  name string }
```

Generic mixins:

```craftgo
type Page<T> { items T[]  total int }

type UserList { Page<User>  requestId string }
```

Cross-package mixins use the qualified form:

```craftgo
type User { shared.Auditable  name string }
```

Disambiguation rules (parser, in priority order):

1. Next token is `.` or `<` -> mixin (qualified or generic name)
2. Next token is a builtin (`string`, `int`, ...) on the same line -> field
3. First identifier starts with lowercase -> field
4. Otherwise -> mixin (PascalCase ident alone, or followed by another non-builtin ident)

Mixin targets must be `type` declarations. Referencing an `enum`, `error`, `scalar`, or `middleware` as a mixin fires `mixin/non-type`; unknown names fire `mixin/unresolved`; embedding a type parameter of the enclosing generic (`type Box<T> { T }`) is also rejected. Becomes Go struct embedding.

### Generics

```craftgo
type Page<T> {
    items T[]
    total int
}
```

Type parameters are bare idents (no constraints or variance). Go output uses standard Go 1.18+ generics with implicit `any`. A generic argument cannot itself be optional (`Page<User?>` is rejected - put the `?` on a field inside the generic). OpenAPI emits each concrete instantiation as a flat component with FastAPI-style naming: `<Type>Of<Arg>` for one arg (`PageOfUser`), `<Type>Of<A>And<B>` for several args, an `Array` suffix for an array arg (`Page<User[]>` -> `PageOfUserArray`), and an `OrNull` suffix for a nullable arg. (`extend` only applies to `service`.)

## Enums

Three forms - all values share one form per enum.

```craftgo
enum Status {
    Active                          // bare: wire = "Active"
    Inactive
}

enum Priority {
    Low      = 1                    // integer (negative values allowed)
    High     = 2
    Deferred = -1
}

enum Color {
    Red   = "red"                   // string with custom payload
    Green = "green"
}
```

Generated Go: `type <Enum><base>` plus one constant per value named `<Enum><Value>` (e.g. `StatusActive`), and a `Validate() error` method that rejects any value outside the declared set.

## Scalars

```craftgo
scalar Email     string  @format(email) @maxLength(254)
scalar OrderID   string  @length(8, 64) @pattern("^ord_[A-Z0-9]+$")
scalar Cents     int     @gte(0) @multipleOf(2)
```

Wraps a primitive. Validators inherit to every field of the scalar's type. Generated as a Go **defined type** (`type Email string`, not an alias) so it can carry a `Validate()` method; callers convert raw primitives (`Email("a@b.com")`).

## Errors

```craftgo
error NotFound UserNotFound                       // empty body, 404

error Conflict EmailTaken {                       // body fields, 409
    email      string
    existingId string?
}
```

Categories (drives HTTP status):

| Category             | Status | Category              | Status |
| -------------------- | ------ | --------------------- | ------ |
| `BadRequest`         | 400    | `PayloadTooLarge`     | 413    |
| `Unauthorized`       | 401    | `UnprocessableEntity` | 422    |
| `PaymentRequired`    | 402    | `Locked`              | 423    |
| `Forbidden`          | 403    | `TooManyRequests`     | 429    |
| `NotFound`           | 404    | `Internal`            | 500    |
| `MethodNotAllowed`   | 405    | `NotImplemented`      | 501    |
| `NotAcceptable`      | 406    | `BadGateway`          | 502    |
| `Conflict`           | 409    | `ServiceUnavailable`  | 503    |
| `Gone`               | 410    | `GatewayTimeout`      | 504    |
| `LengthRequired`     | 411    | `UnsupportedMediaType` | 415    |
| `PreconditionFailed` | 412    |                       |        |

Constructed via `New<TypeName>()` (no body) or `New<TypeName>(<TypeName>Body{...})`, where `<TypeName>` is the DSL name with `Err` appended unless it already ends in `Err`/`Error` (DSL `EmailTaken` -> `NewEmailTakenErr()`; DSL `RateLimitedErr` -> `NewRateLimitedErr()`). Implements `Error() string`, `HTTPStatus() int`, and `ErrCode() string` (the machine-readable code used in the no-body wire envelope). Each error type also exports a package-level `const ErrCode<Name>` holding that code string (e.g. `const ErrCodeEmailTaken = "EMAIL_TAKEN"`).

## Services and methods

```craftgo
@prefix("/v1")
@tags(users)
@middlewares(RequestID, AuthRequired)
@security(bearer)
service UserService {
    @doc("Fetch a user.")
    @summary("Get user")
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }

    @doc("Create a user.")
    @status(201)
    @errors(EmailTaken, ValidationFailed)
    post CreateUser /users {
        request  CreateUserReq
        response User
    }
}
```

Method form: `<verb> <Name> <path> { request <Type>  response <Type> }`. `request` and `response` are optional. Verbs: `get`, `post`, `put`, `patch`, `delete`, `head`, `options`. Path syntax: `/segments/{paramName}/more`.

### `extend service`

Add methods to an existing service from a different file. The extend block can carry its own **method-level-applicable** decorators (`@middlewares`, `@security`, `@tags`, `@deprecated`, `@doc`) that propagate to every method inside:

```craftgo
service Users {
    get  Healthz /healthz { response HealthResp }              // public, no decorators
    post Signup  /signup  { request SignupReq response User }
}

@middlewares(AuthRequired)
@security(Bearer)
extend service Users {
    get    List /users      { response UserList }              // inherits AuthRequired + Bearer
    delete Del  /users/{id} { request GetUserReq response OkResp }
}
```

`@prefix` belongs on the **primary** `service` block - putting it on extend raises `service/extend-decorator-not-method`. `@group` is allowed on an extend block, where it nests that block's own methods on disk (per-block grouping) and adds the group value as an OpenAPI tag on those methods. `@group` REPLACES the service-name segment rather than nesting under it, so several services may deliberately share one output directory; their methods merge into that directory's single `routes.go` and the umbrella calls it once. A shared directory must not straddle DSL packages (`group/package-straddle`, since generated files take their Go package from the DSL package) and its contributors must not repeat a method name (`group/method-collision`, since handlers are one file per method). Multiple `extend` blocks for the same service are allowed (one per file is the typical pattern). The extended service's primary must be in the same package or `service/extend-orphan` fires.

### Inheritance and opt-outs

Service-level decorators (and decorators on an `extend service` block) apply to every method inside. Method-level decorators of the same kind **append** to the inherited chain. Use `@ignoreMiddleware` / `@ignoreSecurity` / `@ignoreTags` at method level to drop the inherited chain entirely (then any method-level `@X(...)` decorators start from empty - the "clear-then-append" reset pattern).

## Middleware

```craftgo
middleware AuthRequired
middleware RateLimit
```

Declared at file (package) level. Codegen produces two artefacts:

- `svccontext/middlewares.go` (regenerated every run) - a `Middlewares` struct with one field per declaration (field type `<Name>Middleware`), meant to be embedded in your `ServiceContext`.
- `internal/middleware/<name>_middleware.go` (gen-once - you fill it) - a `New<Name>Middleware() server.Middleware` stub. The filename follows `output.fileCase` (default `snake`; `<name>-middleware.go` with `kebab`).

Wire each field once at startup in `main.go`, then attach via `@middlewares(Name, ...)` on services or methods:

```go
svc.AuthRequired = middleware.NewAuthRequiredMiddleware()
```

## Decorator registry

Argument types: `string`, `int`, `number` (int or float), `bool`, `ident`, `duration` (`5s` / `100ms`), `size` (`1MB` / `8KB`), `array literal`. All arguments are positional - named args are not accepted.

### File-level

| Decorator                            | Args               | Effect                   |
| ------------------------------------ | ------------------ | ------------------------ |
| `@version("...")`                    | `(string)`         | OpenAPI document version |
| `@deprecated` / `@deprecated("...")` | `()` or `(string)` | Mark file deprecated     |
| `@doc("...")`                        | `(string)`         | File description         |

### Type / error / enum / scalar / middleware level

| Decorator                    | Sites                                                        | Args                      |
| ---------------------------- | ------------------------------------------------------------ | ------------------------- |
| `@doc("...")`                | any level (file, type, field, service, method, enum, error, scalar, middleware, enumValue, errorField) | `(string)` |
| `@deprecated`                | file, type, field, service, method, enumValue, middleware, errorField | `()` or `(string)`        |
| `@example(value)`            | field, errorField                                            | literal (string/int/float/bool/null) or array - **not** an object |
| `@requiresOneOf(a, b, ...)`  | type                                                         | idents (or array literal) |
| `@mutuallyExclusive(a, ...)` | type                                                         | idents (or array literal) |

### Field validators

`AppliesTo` column means the field's primitive (after resolving scalars) must be in that category, or the validator is rejected.

> **Required-by-default**: every field is required unless the type carries `?`. There is no `@required` decorator - append `?` to the type to opt out (`name string?`).

| Decorator           | AppliesTo | Args               | Effect                        |
| ------------------- | --------- | ------------------ | ----------------------------- |
| `@length(n)` / `@length(min, max)` | string | `(int)` or `(int, int)` | Exact length (1 arg) or inclusive [min, max] (2 args) |
| `@minLength(n)`     | string    | `(int)`            | Length `>= n`                 |
| `@maxLength(n)`     | string    | `(int)`            | Length `<= n`                 |
| `@pattern("regex")` | string    | `(string)`         | RE2 regex match               |
| `@format(name)`     | string    | ident or string    | Named format (see list below) |
| `@gte(n)`           | number    | `(number)`         | Value `>= n` (inclusive)      |
| `@lte(n)`           | number    | `(number)`         | Value `<= n` (inclusive)      |
| `@gt(n)`            | number    | `(number)`         | Value `> n` (strict)          |
| `@lt(n)`            | number    | `(number)`         | Value `< n` (strict)          |
| `@range(min, max)`  | number    | `(number, number)` | Both bounds, inclusive        |
| `@positive`         | number    | `()`               | Value `> 0` (= `@gt(0)`)      |
| `@negative`         | number    | `()`               | Value `< 0` (= `@lt(0)`)      |
| `@multipleOf(n)`    | number    | `(number)`         | Divisible by `n` (int only)   |
| `@minItems(n)`      | array     | `(int)`            | At least `n` elements         |
| `@maxItems(n)`      | array     | `(int)`            | At most `n` elements          |
| `@uniqueItems`      | array     | `()`               | All elements distinct         |
| `@maxSize(N)`       | file      | `(size)`           | Multipart upload size cap     |
| `@mimeTypes([...])` | file      | string array       | Multipart MIME allow-list     |

**`@format` values**: `email`, `url`, `uri`, `uuid`, `datetime`, `date`, `time`, `phone`, `ipv4`, `ipv6`, `cidr`, `mac`, `creditcard`, `base64`, `base64url`, `hexcolor`, `json`.

Validators on `errorField` are emitted as OpenAPI schema constraints only (no runtime check on server-emitted error bodies). Every string/number validator above may also sit directly on a `scalar` declaration to bake the constraint into the scalar type (`scalar Email string @format(email) @maxLength(254)`).

### Field bindings (mutually exclusive)

| Decorator | Sites             | Args               | Reads from / writes to                     |
| --------- | ----------------- | ------------------ | ------------------------------------------ |
| `@body`   | field             | `()` or `(string)` | Request body                               |
| `@path`   | field             | `()` or `(string)` | URL path parameter `{name}`                |
| `@query`  | field             | `()` or `(string)` | URL query string                           |
| `@header` | field, errorField | `()` or `(string)` | Request header / response header on errors |
| `@cookie` | field, errorField | `()` or `(string)` | Request cookie / response cookie on errors |
| `@form`   | field             | `()` or `(string)` | Multipart form field                       |

The optional string is the explicit wire name. Without it, the wire name is the DSL field name verbatim.

A field with no binding decorator falls back to `body` for body verbs (POST/PUT/PATCH) and `query` for non-body verbs (GET/DELETE/HEAD/OPTIONS).

### Field metadata

| Decorator         | Sites             | Effect                                                                                           |
| ----------------- | ----------------- | ------------------------------------------------------------------------------------------------ |
| `@nullable`       | field, errorField | Accept JSON `null` as a legal value (Go: pointer wrap if base is not already nilable)            |
| `@default(value)` | field, errorField | Pre-fill before JSON decode. Works on primitive, scalar, enum, optional / array of those.        |
| `@sensitive`      | field, errorField | Server-only. `json:"-"`, omitted from OpenAPI. No validators, bindings, `@nullable`, `@default`. |

`@default` requires the field be optional (`?`). The formatter auto-adds `?` on save when missing, and the semantic analyzer warns until you do. For enum fields, the value is the bare ident (`@default(Active)`).

### Service / method

| Decorator                   | Sites           | Args                                   |
| --------------------------- | --------------- | -------------------------------------- |
| `@prefix("/path")`          | service         | `(string)`                             |
| `@group("name")`            | service         | `(string)`                             |
| `@middlewares(A, B, ...)`   | service, method | idents (or array literal)              |
| `@tags(a, b, ...)`          | service, method | idents/strings (or array literal)      |
| `@security(A, B, ...)`      | service, method | variadic scheme idents (AND within one decorator, OR across multiple) |
| `@ignoreMiddleware`         | method          | `()` - clear inherited middleware chain |
| `@ignoreSecurity`           | method          | `()` - clear inherited security chain   |
| `@ignoreTags`               | method          | `()` - clear inherited tags             |
| `@summary("...")`           | method          | `(string)`                             |
| `@operationId("name")`      | method          | `(string)`                             |
| `@status(code)`             | method          | `(int)`                                |
| `@errors(E1, E2, ...)`      | method          | error idents (or array literal)        |
| `@passthrough`              | method          | none (flag) - both sides raw           |
| `@rawRequest`               | method          | none (flag) - logic reads `*http.Request` |
| `@rawResponse`              | method          | none (flag) - logic writes `http.ResponseWriter` |
| `@timeout(d)`               | method          | `(duration)`                           |
| `@maxBodySize(n)`           | method          | `(size)`                               |

Raw sides: `@rawResponse` keeps request bind + validate and hands `w` to logic (stub `(w, r, req) error`); `@rawRequest` hands `r` over unread and JSON-encodes the returned response (stub `(r) (*Resp, error)`); `@passthrough` is both (stub `(w, r) error`). A `request` / `response` block on a raw side is a docs-only contract: OpenAPI and the generated types describe it, the transport never touches it. `@status` and response `@header` / `@cookie` fields on a raw response side are docs-only too. `@timeout` applies to raw routes (context cancel only). Runtime helpers: `server.WriteBytes`, `server.WritePrecompressed` (negotiates `Accept-Encoding` for a body stored compressed), `server.AcceptsEncoding`.

`@timeout(d)` and `@maxBodySize(n)` **override** (not stack on) the server-wide `handlerTimeout` / `maxBodySize` from `config.yaml` for that route - the decorator value is used as-is (it may be larger or smaller than the global default). The global applies only to routes without the decorator.

### Conflicts

- `@sensitive` + any of: validators, bindings (`@body`/`@path`/`@query`/`@header`/`@cookie`/`@form`), `@nullable`, `@default`
- `@passthrough` + `@rawRequest` / `@rawResponse`, or both flags together: `decorator/redundant` (warning, identical output)

Wrong-site placement (`@prefix` on a field, `@length` on a number) fires `decorator/placement` or `decorator/typemismatch`. `@default` on a non-optional field fires `decorator/default-needs-optional` (warning; formatter auto-fixes on save).

## CLI

| Command                          | Description                                                                         |
| -------------------------------- | ----------------------------------------------------------------------------------- |
| `craftgo init [path]`            | Scaffold a design folder with starter `craftgo.design.yaml`. Default path `design`. |
| `craftgo gen [path]`             | Walk up from `path` (or cwd) looking for `craftgo.design.yaml`, then generate.      |
| `craftgo gen -f <design-folder>` | Skip walk-up; use the manifest at that folder.                                      |
| `craftgo gen -c <project-root>`  | Resolve `output.*` paths against this root.                                         |
| `craftgo fmt [path]`             | Canonical-format `.craftgo` files. Defaults to writing in place.                    |
| `craftgo fmt -l`                 | List files that would change (no write).                                            |
| `craftgo fmt -w`                 | Write the formatted result back (default).                                          |
| `craftgo version`                | Print CLI version.                                                                  |
| `craftgo help`                   | Show top-level help.                                                                |

Exit codes: 0 (success), 1 (any error during gen/fmt/init, including semantic errors), 2 (bad usage - missing subcommand or unknown command). The Go module path is read from `go.mod` walking up from the project root - run `go mod init <module>` before `craftgo gen` if `go.mod` is missing.

`craftgo-lsp` is a separate binary. Install with `go install github.com/craftgodotdev/craftgo/cmd/craftgo-lsp@latest`. Officially supported editor integration: VS Code only.

## `craftgo.design.yaml` (codegen config)

Lives **inside** the design folder. The folder is the design root; its parent is the project root.

```yaml
output:
  types: ./internal/types # directory
  transport: ./internal/transport # directory
  routes: ./internal/routes # directory
  service: ./internal/service # directory
  middleware: ./internal/middleware # directory
  svccontext: ./svccontext/svccontext.go # FILE PATH (single file)
  openapi: ./docs/openapi.yaml # FILE PATH (single file)
  config: ./config # directory
  main: ./main.go # FILE PATH (single file)
  fileCase: snake # snake (default) | kebab | camel - generated file/dir names only; URL routes and Go identifiers unaffected

openapi:
  title: My API
  version: 1.0.0
  description: My API description # optional
  basePath: /api
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
      bearerFormat: JWT
```

All `output.*` paths resolve against the **project root** (the directory holding `go.mod`, the parent of the design folder). Override any of them to relocate the corresponding artifact. Set any path to `-` to skip generation. Setting `main: -` also skips `config/` and `svccontext` (middleware scaffolds are still generated).

The Go module path is **not** in this file. craftgo reads it from `go.mod` at gen time.

### `openapi.basePath`

Single string used as the path prefix in the generated spec (lands as `servers[0].url`). Combine with per-service `@prefix` for full paths:

```yaml
openapi:
  basePath: /api
```

```craftgo
@prefix("/v1")
service UserService {
    get GetUser /users/{id} { ... }
    // -> /api/v1/users/{id} on the wire
}
```

### `openapi.securitySchemes`

Each key is the name referenced via `@security(<key>)`. Supported `type` values: `http`, `apiKey`, `oauth2`, `openIdConnect`, `mutualTLS`. Per-type extra fields:

- `http`: `scheme` (`bearer`, `basic`), optional `bearerFormat`
- `apiKey`: `in` (`header` / `query` / `cookie`), `name`
- `oauth2`: a `flows` object (e.g. `authorizationCode` with `authorizationUrl`, `tokenUrl`, and a `scopes` map)
- `openIdConnect`: `openIdConnectUrl`

The semantic analyzer cross-checks every `@security(<key>)` reference against this map - unknown keys fail at gen time.

## `config/config.yaml` (runtime config)

Read by generated `main.go` via `config.Load()`. Default content (shown annotated as in the generated `example.config.yaml`; the working `config.yaml` carries the same values without the inline comments):

```yaml
server:
  addr: ":8080"
  handlerTimeout: 0s
  maxBodySize: 0
  compression:
    enabled: false
    minSize: 0
    level: 0

logging:
  level: info # debug | info | warn | error

otel:
  enabled: true
  serviceName: my-app
  exporter: none # none | stdout | otlp_grpc | otlp_http
  endpoint: ""

metrics:
  enabled: true
  exporter: prometheus # prometheus | otlp_grpc | otlp_http | none
  endpoint: ""
  adminAddr: ":9090"
  path: /metrics

docs:
  enabled: true
  ui: redoc # redoc | swagger | scalar
  path: /docs
  specPath: /openapi.yaml
```

craftgo does not read environment variables. The YAML file is the single source of runtime configuration. Edit `config/config.go` (gen-once) to add custom fields.

## Generated layout

```
project/
├── design/
│   ├── craftgo.design.yaml
│   └── <pkg>/<file>.craftgo                       YOU WRITE
├── internal/
│   ├── types/<pkg>/                              GEN every run
│   │   ├── types.go
│   │   ├── validate.go
│   │   ├── enums.go
│   │   └── errors.go
│   ├── transport/<svc>/                          GEN every run
│   │   └── <method>.go
│   ├── service/<svc>/<method>.go           GEN ONCE
│   ├── routes/routes.go                          GEN every run (umbrella)
│   ├── routes/<svc>/routes.go                    GEN every run
│   └── middleware/<name>_middleware.go           GEN ONCE per declared middleware
├── svccontext/
│   ├── svccontext.go                             GEN ONCE
│   └── middlewares.go                            GEN every run
├── config/
│   ├── config.go                                 GEN ONCE
│   ├── config.yaml                               GEN ONCE
│   └── example.config.yaml                       GEN ONCE
├── docs/openapi.yaml                             GEN every run
├── main.go                                       GEN ONCE
├── go.mod                                        YOU WRITE (`go mod init`)
└── go.sum
```

`GEN every run` files start with `// Code generated by craftgo. DO NOT EDIT.` and are overwritten on every `craftgo gen`. `GEN ONCE` files are written when missing and never touched again.

Default paths come from `applyDefaults()` in `internal/config/config.go`. Override any of them in `craftgo.design.yaml`.

File and directory names derived from DSL identifiers use `output.fileCase` (default `snake`), so service `UserService` method `CreateUser` produces `internal/transport/user_service/create_user.go` and `internal/service/user_service/create_user.go`. `enums.go` is emitted only when the package declares enums; `errors.go` only when it declares errors.

## Generated handler shape

Every method gets a handler that does:

```go
func <Method>(svcCtx *svccontext.ServiceContext) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req types.<Req>
        // 1. pre-fill @default values
        req.Field = defaultValue
        // 2. decode JSON body (body verbs only)
        if err := server.JSON().Decode(r.Body, &req); err != nil {
            server.WriteValidationError(w, r, err)
            return
        }
        // 3. bind @path / @query / @header / @cookie / @form fields
        // ...
        if err := req.Validate(); err != nil {
            server.WriteValidationError(w, r, err)
            return
        }
        l := service.New<Method>Service(r.Context(), svcCtx)
        resp, err := l.<Method>(&req)   // ctx is captured in the service, not passed
        if err != nil { server.WriteError(w, r, err); return }
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        _ = server.JSON().Encode(w, resp)
    }
}
```

Plain Go. No reflection. JSON goes through `server.JSON()` - the swappable codec (defaults to `encoding/json`). Handlers register on `*http.ServeMux` via `srv.Handle("VERB /path", <Method>(svc), mws...)`.

## Generated logic shape

`internal/service/<svc>/<method>.go` (gen-once - you fill):

```go
type <Method>Service struct {
    log.Logger
    ctx    context.Context
    svcCtx *svccontext.ServiceContext
}

func New<Method>Service(ctx context.Context, svcCtx *svccontext.ServiceContext) *<Method>Service {
    return &<Method>Service{
        Logger: log.Default().WithContext(ctx),
        ctx:    ctx,
        svcCtx: svcCtx,
    }
}

func (l *<Method>Service) <Method>(req *types.<Req>) (*types.<Resp>, error) {
    // TODO: implement
    return nil, nil
}
```

The struct embeds `log.Logger` so logic can call `l.Info(...)` directly. Trace IDs flow into log lines automatically when OTel is enabled.

## Runtime entry points

```go
import "github.com/craftgodotdev/craftgo/pkg/server"

srv := server.New(svcCtx)
srv.Use(server.RequestID())
srv.Use(server.AccessLog(logger))
srv.Use(craftotel.HTTPMiddleware(cfg.OTel.ServiceName))
routes.RegisterAll(srv, svcCtx)
srv.Start(":8080")
```

`server.Server` wraps `*http.ServeMux`. `srv.Use` accepts any `func(http.Handler) http.Handler`. Routes register with `srv.Handle("VERB /path", ...)` using Go 1.22+ pattern syntax.

### Built-in runtime middleware

| Constructor                  | Effect                                                   |
| ---------------------------- | -------------------------------------------------------- |
| `server.Recovery(logger)`    | Panic -> 500 + structured log (auto-installed outermost) |
| `server.RequestID()`         | Extract or generate `X-Request-Id`                       |
| `server.AccessLog(logger)`   | One log line per request                                 |
| `server.BodyLimit(maxBytes)` | Cap request body size                                    |
| `server.Timeout(d)`          | Per-handler deadline                                     |
| `srv.SetCORS(opts)`          | CORS headers + genuine-preflight short-circuit (opts via `server.CORSPermissive()` / `server.CORSStrict(origin)`; a Server method, not a `srv.Use` middleware) |
| `server.Compress(opts...)`   | gzip / deflate response compression (`opts` optional)    |

## Error response format

The default `server.WriteError`:

- Typed errors with declared body fields: `json.Marshal(err)` emits the user fields. Status from `HTTPStatus()`.
- Typed errors with no body fields: `{"message":"<text>"}`, plus `"code":"<CODE>"` when the error implements `ErrCode() string`. Status from `HTTPStatus()`.
- Plain (non-`StatusError`) errors: `{"message":"internal server error"}` - the raw `err.Error()` text is logged with trace context but **never** written to the response (it routinely carries DSNs / file paths). Status 500.

`Content-Type` is `application/json; charset=utf-8` for all `WriteError` paths. Panic responses from the `Recovery` middleware use `http.Error`, which writes `text/plain; charset=utf-8`.

To customise the envelope: `server.SetHandleUnknownError(fn)` overrides the 500 for untyped errors, and `server.SetDefaultValidationFailed(fn)` overrides the 4xx validation body. `server.WriteError(w, r, err)` / `server.WriteValidationError(w, r, err)` are the entry points the generated handlers call.

## Common patterns

### CRUD

```craftgo
type CreateUserReq {
    name  string @length(1, 80)
    email string @format(email)
}

type GetUserReq {
    id string @path
}

type User { id string  name string  email string }
type OkResp { ok bool }

@prefix("/v1")
service UserService {
    post   CreateUser /users     { request CreateUserReq  response User }
    get    GetUser    /users/{id} { request GetUserReq    response User }
    delete DeleteUser /users/{id} { request GetUserReq    response OkResp }
}
```

### Pagination with defaults

```craftgo
type ListReq {
    cursor string?
    limit  int? @default(20) @gte(1) @lte(100)
    sort   string? @default("created_at")
}

type ListResp {
    items  User[]
    cursor string?
    total  int?
}
```

### Path + body combination

```craftgo
type UpdateUserReq {
    id    string  @path
    name  string?
    email string? @format(email)
}
```

`id` rides the URL; the rest ride the JSON body (default for POST/PUT/PATCH).

### Multipart upload

```craftgo
type UploadAvatarReq {
    userId string @path
    file   file   @form @maxSize(2MB) @mimeTypes(["image/png", "image/jpeg"])
}

@prefix("/v1")
service UserService {
    // craftgo auto-detects multipart from the request's `file @form`
    // field - no content-type decorator needed.
    post UploadAvatar /users/{userId}/avatar {
        request  UploadAvatarReq
        response OkResp
    }
}
```

### Custom error with body and headers

```craftgo
error TooManyRequests RateLimited {
    code       string @default("RATE_LIMITED")
    message    string @default("Slow down")
    retryAfter int    @header("Retry-After")
}

service UserService {
    @errors(RateLimited)
    post CreateUser /users { request CreateUserReq  response User }
}
```

In service code:

```go
return nil, types.NewRateLimitedErr(types.RateLimitedBody{RetryAfter: 30})
```

### Server-only field

```craftgo
type Order {
    id          string
    customerId  string
    internalRef string @sensitive   // populated by service code, never on wire
}
```

### Extending a service across files

```craftgo
// design/users/service.craftgo
package users

@prefix("/users")
@middlewares(AuthRequired)
service UserService {
    get GetUser /{id} { request GetUserReq  response User }
}
```

```craftgo
// design/users/admin.craftgo
package users

extend service UserService {
    @middlewares(AdminOnly)
    delete PurgeUser /{id}/purge {
        request  GetUserReq
        response OkResp
    }
}
```

Both methods share `/users` prefix and `AuthRequired`. `PurgeUser` additionally runs `AdminOnly`.

## Things craftgo does not do

- Service discovery (etcd, k8s)
- Database model generation
- gRPC code generation (yet)
- Runtime middleware library (auth, ratelimit, breaker) - use any `func(http.Handler) http.Handler`
- Multi-language client gen - emit OpenAPI and use openapi-generator
- Custom routers - uses Go 1.22+ stdlib `*http.ServeMux`
- Environment-variable config - YAML file is the single source of runtime values

## Things craftgo guarantees

- Generated code compiles
- `craftgo gen` is deterministic (same input -> same output)
- Logic stubs (`internal/service/...`) are never touched after first creation
- The generated OpenAPI is structurally valid OAS 3.1 and renders cleanly in Swagger UI, ReDoc, and openapi-generator (Spectral / Redocly may flag nullable-union representations under their default 3.1 rulesets)
- The runtime is `net/http` only - no fork, no patch, no parallel runtime
- The DSL is a closed set: unknown decorators fire `decorator/unknown` at gen time, never silently ignored
