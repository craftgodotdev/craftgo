# AI Reference

A single-page consolidated reference for AI agents and search indexes. Covers the entire DSL syntax, every decorator, every CLI command, the configuration files, and the generated layout. Treat this as the source of truth when generating craftgo code.

This page lives at `/llms` so AI tooling can fetch one URL and ingest the full surface.

## Quick mental model

1. Write `.craftgo` files describing your API (types, services, methods, validators).
2. Run `craftgo gen <design-dir>` to generate Go types, validators, HTTP handlers, an OpenAPI 3.1 spec, and stubs for business logic + middleware.
3. Fill in business logic at `internal/service/<service>/<method>.go` (gen-once - your edits stick).
4. Run with `go run .`. The framework wraps `net/http` directly.

DSL is the contract. Generated code is plain Go. No reflection at runtime.

## File grammar

```
package <ident>

[import "<path>"]*

[<decl>]*

<decl> is one of:
  [@decorator]* type Name { fields... }
  [@decorator]* type Name<TypeParam any, ...> { fields... }
  [@decorator]* enum Name { values... }
  [@decorator]* error Category Name [{ fields... }]
  [@decorator]* scalar Name <Primitive> [@validators...]
  [@decorator]* service Name { methods... }
  [@decorator]* extend service Name { methods... }
  [@decorator]* middleware Name
```

Files in the same directory share `package` and see each other's declarations. Cross-directory references use `import "<sibling-dir>"`.

## Keywords (16)

`package`, `import`, `type`, `enum`, `error`, `scalar`, `service`, `extend`, `middleware`, `request`, `response`, `map`, `true`, `false`, `null`. Plus HTTP verbs (`get`, `post`, `put`, `patch`, `delete`, `head`, `options`).

## Types

Field syntax: `name TypeRef [@decorator(...) ...]`.

| DSL form         | Go output                | Notes                                  |
| ---------------- | ------------------------ | -------------------------------------- |
| `string`         | `string`                 |                                        |
| `bytes`          | `[]byte`                 | base64-decoded from JSON               |
| `int`            | `int`                    | platform-sized                         |
| `int8/16/32/64`  | matching Go              |                                        |
| `uint`           | `uint`                   |                                        |
| `uint8/16/32/64` | matching Go              |                                        |
| `float32/64`     | matching Go              |                                        |
| `bool`           | `bool`                   |                                        |
| `file`           | `*multipart.FileHeader`  | only with `@form`                      |
| `T?`             | `*T` or nilable as-is    | optional                               |
| `T[]`            | `[]T`                    | array                                  |
| `map<K, V>`      | `map[K]V`                | K must be primitive                    |
| `Custom`         | `Custom`                 | references a declared type / scalar / enum |

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
type Page<T any> { items T[]  total int }

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

Mixin targets must be `type` declarations. Referencing an `enum`, `error`, `scalar`, or `middleware` as a mixin fires `mixin/non-type`. Unknown names fire `mixin/unresolved`. Becomes Go struct embedding.

### Generics

```craftgo
type Page<T any> {
    items T[]
    total int
}
```

Standard Go 1.18+ generics. OpenAPI emits each concrete instantiation as a flat schema.

## Enums

Three forms - all values share one form per enum.

```craftgo
enum Status {
    Active                          // bare: wire = "Active"
    Inactive
}

enum Priority {
    Low    = 1                      // integer
    High   = 2
}

enum Color {
    Red   = "red"                   // string with custom payload
    Green = "green"
}
```

Generated Go: `type <Enum><base>` plus one constant per value named `<Enum><Value>` (e.g. `StatusActive`).

## Scalars

```craftgo
scalar Email     string  @format(email) @maxLength(254)
scalar OrderID   string  @length(8, 64) @pattern("^ord_[A-Z0-9]+$")
scalar Cents     int     @min(0) @multipleOf(2)
```

Wraps a primitive. Validators inherit to every field of the scalar's type. Generated as Go type alias (`type Email = string`).

## Errors

```craftgo
error NotFound UserNotFound                       // empty body, 404

error Conflict EmailTaken {                       // body fields, 409
    email      string
    existingId string?
}
```

Categories (drives HTTP status):

| Category              | Status | Category              | Status |
| --------------------- | ------ | --------------------- | ------ |
| `BadRequest`          | 400    | `PayloadTooLarge`     | 413    |
| `Unauthorized`        | 401    | `UnprocessableEntity` | 422    |
| `PaymentRequired`     | 402    | `Locked`              | 423    |
| `Forbidden`           | 403    | `TooManyRequests`     | 429    |
| `NotFound`            | 404    | `Internal`            | 500    |
| `MethodNotAllowed`    | 405    | `NotImplemented`      | 501    |
| `NotAcceptable`       | 406    | `BadGateway`          | 502    |
| `Conflict`            | 409    | `ServiceUnavailable`  | 503    |
| `Gone`                | 410    | `GatewayTimeout`      | 504    |
| `LengthRequired`      | 411    |                       |        |
| `PreconditionFailed`  | 412    |                       |        |

Constructed via `New<Name>Err()` (no body) or `New<Name>Err(<Name>Body{...})`. Implements `Error() string` and `HTTPStatus() int`.

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

Add methods to an existing service from a different file:

```craftgo
extend service UserService {
    @middlewares(AdminOnly)
    delete PurgeUser /users/{id}/purge {
        request  GetUserReq
        response shared.OkResp
    }
}
```

Service-level decorators (`@prefix`, `@tags`, `@security`, service-level `@middlewares`) live on the **primary** `service` block. `extend` blocks contain only methods and method-level decorators. Multiple `extend` blocks for the same service are allowed (one per file is the typical pattern).

## Middleware

```craftgo
middleware AuthRequired
middleware RateLimit
```

Declared at file (package) level. Codegen produces a typed slot on `ServiceContext` and a stub at `internal/middleware/<name>-middleware.go` (gen-once - you fill it). Attach via `@middlewares(Name, ...)` on services or methods.

## Decorator registry (50)

Argument types: `string`, `int`, `number` (int or float), `bool`, `ident`, `duration` (`5s` / `100ms`), `size` (`1MB` / `8KB`), `array literal`, named arg (`scopes: [...]`).

### File-level

| Decorator                       | Args              | Effect                                            |
| ------------------------------- | ----------------- | ------------------------------------------------- |
| `@title("...")`                 | `(string)`        | OpenAPI document title                            |
| `@version("...")`               | `(string)`        | OpenAPI document version                          |
| `@deprecated` / `@deprecated("...")` | `()` or `(string)` | Mark file deprecated                          |
| `@doc("...")`                   | `(string)`        | File description                                  |
| `@externalDocs("url")` / object | string or object  | OpenAPI externalDocs                              |

### Type / error / enum / scalar / middleware level

| Decorator                  | Sites                         | Args                          |
| -------------------------- | ----------------------------- | ----------------------------- |
| `@doc("...")`              | type, enum, error, scalar, middleware, enumValue, errorField | `(string)` |
| `@deprecated`              | type, enumValue, errorField, middleware | `()` or `(string)`  |
| `@example(value)`          | type, field, method, error, errorField | literal or object   |
| `@examples(name: ..., ...)`| type, field, method, error, errorField | object              |
| `@externalDocs(...)`       | type, service, method         | string or object              |
| `@requiresOneOf(a, b, ...)`| type                          | idents (or array literal)     |
| `@mutuallyExclusive(a, ...)` | type                        | idents (or array literal)     |

### Field validators

`AppliesTo` column means the field's primitive (after resolving scalars) must be in that category, or the validator is rejected.

| Decorator                  | AppliesTo  | Args                          | Effect                          |
| -------------------------- | ---------- | ----------------------------- | ------------------------------- |
| `@required`                | any        | `()`                          | Field must be present           |
| `@length(min, max)`        | string     | `(int, int)`                  | Length bounds inclusive         |
| `@minLength(n)`            | string     | `(int)`                       | Length `>= n`                   |
| `@maxLength(n)`            | string     | `(int)`                       | Length `<= n`                   |
| `@pattern("regex")`        | string     | `(string)`                    | RE2 regex match                 |
| `@format(name)`            | string     | ident or string               | Named format (see list below)   |
| `@min(n)`                  | number     | `(number)`                    | Value `>= n`                    |
| `@max(n)`                  | number     | `(number)`                    | Value `<= n`                    |
| `@range(min, max)`         | number     | `(number, number)`            | Both bounds                     |
| `@positive`                | number     | `()`                          | Value `> 0`                     |
| `@negative`                | number     | `()`                          | Value `< 0`                     |
| `@multipleOf(n)`           | number     | `(number)`                    | Divisible by `n`                |
| `@minItems(n)`             | array      | `(int)`                       | At least `n` elements           |
| `@maxItems(n)`             | array      | `(int)`                       | At most `n` elements            |
| `@uniqueItems`             | array      | `()`                          | All elements distinct           |
| `@maxSize(N)`              | file       | `(size)`                      | Multipart upload size cap       |
| `@mimeTypes([...])`        | file       | string array                  | Multipart MIME allow-list       |

**`@format` values**: `email`, `url`, `uri`, `uuid`, `datetime`, `date`, `time`, `phone`, `hostname`, `ipv4`, `ipv6`, `cidr`, `mac`, `creditcard`, `base64`, `hexcolor`, `json`.

Validators on `errorField` are emitted as OpenAPI schema constraints only (no runtime check on server-emitted error bodies).

### Field bindings (mutually exclusive)

| Decorator | Sites             | Args         | Reads from / writes to                            |
| --------- | ----------------- | ------------ | ------------------------------------------------- |
| `@body`   | field             | `()` or `(string)` | Request body                                |
| `@path`   | field             | `()` or `(string)` | URL path parameter `{name}`                 |
| `@query`  | field             | `()` or `(string)` | URL query string                            |
| `@header` | field, errorField | `()` or `(string)` | Request header / response header on errors |
| `@cookie` | field, errorField | `()` or `(string)` | Request cookie / response cookie on errors |
| `@form`   | field             | `()` or `(string)` | Multipart form field                        |

The optional string is the explicit wire name. Without it, the wire name is the DSL field name verbatim.

A field with no binding decorator falls back to `body` for body verbs (POST/PUT/PATCH) and `query` for non-body verbs (GET/DELETE/HEAD/OPTIONS).

### Field metadata

| Decorator         | Sites              | Effect                                                                                          |
| ----------------- | ------------------ | ----------------------------------------------------------------------------------------------- |
| `@nullable`       | field, errorField  | Accept JSON `null` as a legal value (Go: pointer wrap if base is not already nilable)           |
| `@default(value)` | field, errorField  | Pre-fill before JSON decode. Works on primitive, scalar, enum, optional / array of those.       |
| `@sensitive`      | field, errorField  | Server-only. `json:"-"`, omitted from OpenAPI. No validators, bindings, `@nullable`, `@default`.|

`@default` cannot combine with `@required` or any binding. For enum fields, the value is the bare ident (`@default(Active)`).

### Service / method

| Decorator                  | Sites           | Args                                  |
| -------------------------- | --------------- | ------------------------------------- |
| `@prefix("/path")`         | service         | `(string)`                            |
| `@group("name")`           | service         | `(string)`                            |
| `@middlewares(A, B, ...)`  | service, method | idents (or array literal)             |
| `@tags(a, b, ...)`         | service, method | idents/strings (or array literal)     |
| `@security(scheme, ...)`   | service, method | scheme ident, optional `scopes: [...]`|
| `@summary("...")`          | method          | `(string)`                            |
| `@operationId("name")`     | method          | `(string)`                            |
| `@status(code)`            | method          | `(int)`                               |
| `@errors(E1, E2, ...)`     | method          | error idents (or array literal)       |
| `@consumes("type", ...)`   | method          | strings (or array literal)            |
| `@produces("type", ...)`   | method          | strings (or array literal)            |
| `@accepts("encoding", ...)`| method          | strings                               |
| `@passthrough`             | method          | `()`                                  |
| `@timeout(d)`              | method          | `(duration)`                          |
| `@maxBodySize(n)`          | method          | `(size)`                              |

`@passthrough` bypasses framework parsing - logic receives raw `http.ResponseWriter` and `*http.Request`.

### Conflicts

- `@sensitive` + any of: validators, bindings (`@body`/`@path`/`@query`/`@header`/`@cookie`/`@form`), `@nullable`, `@default`
- `@default` + `@required`

Wrong-site placement (`@prefix` on a field, `@length` on a number) fires `decorator/placement` or `decorator/typemismatch`.

## CLI

| Command                                 | Description                                                       |
| --------------------------------------- | ----------------------------------------------------------------- |
| `craftgo init [path]`                   | Scaffold a design folder with starter `craftgo.design.yaml`. Default path `design`. |
| `craftgo gen [path]`                    | Walk up from `path` (or cwd) looking for `craftgo.design.yaml`, then generate. |
| `craftgo gen -f <design-folder>`        | Skip walk-up; use the manifest at that folder.                    |
| `craftgo gen -c <project-root>`         | Resolve `output.*` paths against this root.                       |
| `craftgo fmt [path]`                    | Canonical-format `.craftgo` files. Defaults to writing in place.  |
| `craftgo fmt -l`                        | List files that would change (no write).                          |
| `craftgo fmt -w`                        | Write the formatted result back (default).                        |
| `craftgo version`                       | Print CLI version.                                                |
| `craftgo help`                          | Show top-level help.                                              |

Exit codes: 0 (success), 1 (generic failure), 2 (semantic errors). The Go module path is read from `go.mod` walking up from the project root - run `go mod init <module>` before `craftgo gen` if `go.mod` is missing.

`craftgo-lsp` is a separate binary. Install with `go install github.com/dropship-dev/craftgo/cmd/craftgo-lsp@latest`. Officially supported editor integration: VS Code only.

## `craftgo.design.yaml` (codegen config)

Lives **inside** the design folder. The folder is the design root; its parent is the project root.

```yaml
output:
  types:      ./internal/types         # directory
  transport:    ./internal/transport       # directory
  routes:     ./internal/routes        # directory
  service:      ./internal/service         # directory
  middleware: ./internal/middleware    # directory
  svccontext: ./svccontext/svccontext.go   # FILE PATH (single file)
  openapi:    ./docs/openapi.yaml          # FILE PATH (single file)
  config:     ./config                 # directory
  main:       ./main.go                # FILE PATH (single file)

openapi:
  title:    My API
  version:  1.0.0
  basePath: /api
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
      bearerFormat: JWT
```

All `output.*` paths resolve against the **project root** (the directory holding `go.mod`, the parent of the design folder). Override any of them to relocate the corresponding artifact. Set any path to `-` to skip generation. Setting `main: -` also skips `config/`, `svccontext`, and `middleware`.

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
- `oauth2`: scopes are application-defined
- `openIdConnect`: `openIdConnectUrl`

The semantic analyzer cross-checks every `@security(<key>)` reference against this map - unknown keys fail at gen time.

## `config/config.yaml` (runtime config)

Read by generated `main.go` via `config.Load()`. Default content:

```yaml
server:
  addr: ":8080"
  handlerTimeout: 0s
  maxBodySize: 0
  compression:
    enabled: false
    minSize: 0
    level: 0

otel:
  enabled: true
  serviceName: my-app
  exporter: none              # none | stdout | otlp_grpc | otlp_http
  endpoint: ""

metrics:
  enabled: true
  exporter: prometheus        # prometheus | otlp_grpc | otlp_http | none
  endpoint: ""
  adminAddr: ":9090"
  path: /metrics
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
│   ├── handler/<svc>/                            GEN every run
│   │   ├── <method>.go
│   │   └── errors.go
│   ├── logic/<svc>/<method>.go             GEN ONCE
│   ├── routes/routes.go                          GEN every run (umbrella)
│   ├── routes/<svc>/routes.go                    GEN every run
│   └── middleware/<name>-middleware.go           GEN ONCE per declared middleware
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

## Generated handler shape

Every method gets a handler that does:

```go
func <Method>(svcCtx *svccontext.ServiceContext) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req types.<Req>
        // pre-fill from @default decorators
        req.Field = defaultValue
        // bind path/query/header/cookie/form fields
        // ...
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil { /* 400 */ }
        if err := req.Validate(); err != nil {
            server.WriteValidationError(w, r, err)
            return
        }
        l := logic.New<Method>Service(r.Context(), svcCtx)
        resp, err := l.<Method>(&req)
        if err != nil { writeError(w, err); return }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(resp)
    }
}
```

Plain Go. No reflection. Stdlib JSON. Handlers register on `*http.ServeMux` via `srv.Handle("VERB /path", <Method>(svc))`.

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
import "github.com/dropship-dev/craftgo/pkg/server"

srv := server.New(svcCtx)
srv.Use(server.RequestID())
srv.Use(server.AccessLog(logger))
srv.Use(craftotel.HTTPMiddleware(cfg.OTel.ServiceName))
routes.RegisterAll(srv, svcCtx)
srv.Start(":8080")
```

`server.Server` wraps `*http.ServeMux`. `srv.Use` accepts any `func(http.Handler) http.Handler`. Routes register with `srv.Handle("VERB /path", ...)` using Go 1.22+ pattern syntax.

### Built-in runtime middleware

| Constructor                  | Effect                                                  |
| ---------------------------- | ------------------------------------------------------- |
| `server.Recovery(logger)`    | Panic -> 500 + structured log (auto-installed outermost)|
| `server.RequestID()`         | Extract or generate `X-Request-Id`                      |
| `server.AccessLog(logger)`   | One log line per request                                |
| `server.BodyLimit(maxBytes)` | Cap request body size                                   |
| `server.Timeout(d)`          | Per-handler deadline                                    |
| `server.CORS(opts)`          | Preflight + CORS headers                                |
| `server.Compress(opts)`      | gzip / deflate response compression                     |

## Error response format

The default `writeError`:

- Typed errors with declared body fields: `json.Marshal(err)` emits the user fields. Status from `HTTPStatus()`.
- Typed errors with no body fields: `{"code":"<CODE>","message":"<text>"}`. Status from `HTTPStatus()`.
- Plain errors: `{"message":"<err.Error()>"}`. Status 500.

`Content-Type` always `application/json`.

## Common patterns

### CRUD

```craftgo
type CreateUserReq {
    name  string @required @length(1, 80)
    email string @required @format(email)
}

type GetUserReq {
    id string @path @required
}

type User { id string  name string  email string }

@prefix("/v1")
service UserService {
    post   CreateUser /users     { request CreateUserReq  response User }
    get    GetUser    /users/{id} { request GetUserReq    response User }
    delete DeleteUser /users/{id} { request GetUserReq    response shared.OkResp }
}
```

### Pagination with defaults

```craftgo
type ListReq {
    cursor string?
    limit  int @default(20) @min(1) @max(100)
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
    id    string  @path @required
    name  string?
    email string? @format(email)
}
```

`id` rides the URL; the rest ride the JSON body (default for POST/PUT/PATCH).

### Multipart upload

```craftgo
type UploadAvatarReq {
    userId string @path @required
    file   file   @form @required @maxSize(2MB) @mimeTypes(["image/png", "image/jpeg"])
}

@prefix("/v1")
service UserService {
    @consumes("multipart/form-data")
    post UploadAvatar /users/{userId}/avatar {
        request  UploadAvatarReq
        response shared.OkResp
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
package design

@prefix("/users")
@middlewares(AuthRequired)
service UserService {
    get GetUser /{id} { request GetUserReq  response User }
}
```

```craftgo
// design/users/admin.craftgo
package design

extend service UserService {
    @middlewares(AdminOnly)
    delete PurgeUser /{id}/purge {
        request  GetUserReq
        response shared.OkResp
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
- The generated OpenAPI passes Spectral and Redocly linters
- The runtime is `net/http` only - no fork, no patch, no parallel runtime
- The DSL is a closed set: unknown decorators fire `decorator/unknown` at gen time, never silently ignored
