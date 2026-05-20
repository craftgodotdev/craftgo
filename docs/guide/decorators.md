# Decorators

Decorators attach metadata to declarations and fields. Every decorator starts with `@` and is registered in a closed set; `@unknown` fires a `decorator/unknown` diagnostic at semantic analysis time.

## At a glance

~50 decorators, grouped by where they apply. Each decorator declares one or more **sites** (file, type, field, service, method, ...) and an **argument shape** (none, string, number, ident, list, ...). Using a decorator at the wrong site or with the wrong arguments fires a diagnostic with the line and column.

```craftgo
@version("1.0.0")              // file

type User {                    // type
    id    string     // field
    name  string  @length(1, 80)
    email string  @format(email)
}

@prefix("/v1")                 // service
@middlewares(Auth)
service UserService {
    @doc("Get user.")          // method
    @timeout(5s)
    get GetUser /users/{id} {
        request  GetUserReq
        response User
    }
}
```

The rest of this page lists every decorator with its sites, arguments, and effect.

## Sites

| Site name      | Where the decorator sits                                  |
| -------------- | ---------------------------------------------------------- |
| `file`         | Above the `package` line at the top of a `.craftgo` file   |
| `type`         | Above a `type` declaration                                 |
| `field`        | After a field in a `type` body                             |
| `enum`         | Above an `enum` declaration                                |
| `enumValue`    | After an enum value (`Active @doc("...")`)                 |
| `error`        | Above an `error` declaration                               |
| `errorField`   | After a field in an `error` body                           |
| `scalar`       | After a `scalar` declaration                               |
| `service`      | Above a `service` declaration                              |
| `method`       | Above an HTTP method inside a service body                 |
| `middleware`   | After a `middleware` declaration                           |

## Documentation and lifecycle

### `@doc(text)`

Free-form documentation surfaced in OpenAPI descriptions and IDE hover. `text` is a string.

| Sites    | file, type, field, service, method, enum, enumValue, error, errorField, scalar, middleware |
| -------- | -------- |
| Args     | `(string)` |

```craftgo
@doc("The user entity. Email is the canonical login id.")
type User { ... }
```

Doc-comments above a declaration produce the same effect:

```craftgo
// The user entity. Email is the canonical login id.
type User { ... }
```

Use `@doc` when the doc must contain characters not legal in a `//` comment line.

### `@deprecated` / `@deprecated("reason")`

Marks the construct as deprecated. OpenAPI emits the `deprecated: true` flag; Go output gains a `// Deprecated: ...` comment that `go vet` and `staticcheck` recognize.

| Sites | file, type, field, service, method, enumValue, errorField, middleware |
| -------- | -------- |
| Args  | `()` or `(string)` |

```craftgo
@deprecated("Use email instead.")
type LegacyUserReq { ... }

type User {
    legacyId string @deprecated
    email    string
}
```

### `@example(value)` / `@examples(...)`

OpenAPI examples block.

| Sites | type, field, method, error, errorField |
| -------- | -------- |
| Args  | `@example`: a literal or `{key: value}` object. `@examples`: named map of example objects. |

```craftgo
type User {
    name string @example("alice")
}

@examples(
    happy: {summary: "Typical create", value: {name: "alice"}},
    edge:  {summary: "Long name",      value: {name: "alice-the-great"}},
)
type CreateUserReq { name string }
```

### `@externalDocs(url)` / `@externalDocs(url: ..., description: ...)`

OpenAPI external documentation pointer.

| Sites | type, service, method |
| -------- | -------- |
| Args  | a string URL OR `{url: "...", description: "..."}` object |

```craftgo
@externalDocs("https://docs.example.com/api/users")
service UserService { ... }
```

## OpenAPI file-header

### `@version(text)`

Override the OpenAPI document version per file. Without it, the value comes from `craftgo.design.yaml`'s `openapi.version`. The document title is set exclusively via the manifest's `openapi.title`.

| Sites | file |
| -------- | -------- |
| Args  | `(string)` |

```craftgo
@version("2.0.0")
package design
```

## Cross-field type rules

### `@requiresOneOf(field1, field2, ...)`

At least one of the listed fields must be present.

| Sites | type |
| -------- | -------- |
| Args  | one or more idents (or one array literal) |

```craftgo
@requiresOneOf(email, phone)
type Contact {
    email string?
    phone string?
}
```

### `@mutuallyExclusive(field1, field2, ...)`

At most one of the listed fields may be present.

| Sites | type |
| -------- | -------- |
| Args  | one or more idents (or one array literal) |

```craftgo
@mutuallyExclusive(personal, business)
type Account {
    personal bool
    business bool
}
```

## Field validators

> **Required-by-default**: every field is required unless its type carries the `?` suffix. There is no `@required` decorator — to mark a field optional, write `name string?`. To allow `null` while keeping the field mandatory, add `@nullable`. To pre-fill an absent value, add `@default(...)` (which also auto-marks the field optional on save).

### Strings

Run on `string` and `bytes` fields, and on scalars whose primitive is one of those.

| Decorator                   | Args                  | Effect                              |
| --------------------------- | --------------------- | ----------------------------------- |
| `@length(min, max)`         | `(int, int)`          | Length in `[min, max]` inclusive    |
| `@minLength(n)`             | `(int)`               | Length `>= n`                       |
| `@maxLength(n)`             | `(int)`               | Length `<= n`                       |
| `@pattern("regex")`         | `(string)`            | RE2-flavored regex match            |
| `@format(name)`             | bare ident or string  | Named format check (see below)      |

**Available formats** (`@format(...)`): `email`, `url`, `uri`, `uuid`, `datetime` (RFC 3339), `date`, `time`, `phone`, `hostname`, `ipv4`, `ipv6`, `cidr`, `mac`, `creditcard`, `base64`, `base64url`, `hexcolor`, `json`. RFC-compliant validators (email, ipv4/ipv6, cidr, mac, datetime/date/time, base64, json) delegate to Go stdlib (`net`, `net/mail`, `net/url`, `time`, `encoding/*`); the remainder use regex.

```craftgo
type User {
    email   string @format(email) @maxLength(254)
    website string @format(uri)
    avatar  string @pattern("^https://.*\\.(png|jpg)$")
}
```

### Numbers

Run on int / uint / float fields and scalars wrapping them.

| Decorator               | Args               | Effect                          |
| ----------------------- | ------------------ | ------------------------------- |
| `@min(n)`               | `(number)`         | Value `>= n`                    |
| `@max(n)`               | `(number)`         | Value `<= n`                    |
| `@range(min, max)`      | `(number, number)` | Both bounds inclusive           |
| `@positive`             | `()`               | Value `> 0`                     |
| `@negative`             | `()`               | Value `< 0`                     |
| `@multipleOf(n)`        | `(number)`         | Value divisible by `n`          |

```craftgo
type Order {
    quantity int   @positive @max(1000)
    price    int   @min(0) @multipleOf(2)
    rating   float64 @range(0.0, 5.0)
}
```

### Arrays and maps

Run on array / map fields.

| Decorator         | Args     | Effect                               |
| ----------------- | -------- | ------------------------------------ |
| `@minItems(n)`    | `(int)`  | At least `n` elements                |
| `@maxItems(n)`    | `(int)`  | At most `n` elements                 |
| `@uniqueItems`    | `()`     | All elements distinct (arrays only)  |

```craftgo
type Post {
    tags string[] @minItems(1) @maxItems(10) @uniqueItems
}
```

### File uploads

Run on `file` fields used with `@form`.

| Decorator           | Args                | Effect                                |
| ------------------- | ------------------- | ------------------------------------- |
| `@maxSize(bytes)`   | `(size)`            | Cap upload size. Accepts `2MB`, `8KB`, etc. |
| `@mimeTypes([...])` | string array        | Allowed Content-Type list             |

```craftgo
type AvatarReq {
    userId string @path
    file   file   @form @maxSize(2MB) @mimeTypes(["image/png", "image/jpeg"])
}
```

## Field metadata

### `@nullable`

Marks the field as accepting an explicit JSON `null`. Generated Go: pointer wrap if the base type is not already nilable.

| Sites | field, errorField |
| -------- | -------- |
| Args  | `()` |

```craftgo
type Patch {
    name string? @nullable
    // Wire: "name": null is a legal value
}
```

### `@default(value)`

Pre-fill the field before JSON decode. If the client omits the field, the default survives; if present, decode overwrites.

| Sites | field, errorField |
| -------- | -------- |
| Args  | `(any)` - a literal matching the field's primitive, an enum value's bare ident, or an array literal |

Works on:

- Plain primitives (`string`, `int`, `bool`, `float`)
- Optional primitives (`T?`)
- Scalars wrapping primitives
- Enums (use the bare value name: `@default(Active)`)
- Arrays of any of the above (`@default([])`, `@default(["a", "b"])`, `@default([Active, Pending])`)

Conflicts: cannot combine with any binding, or be applied to map / struct / generic fields. The formatter auto-adds `?` to the field type on save so OpenAPI marks the field as not-required (consistent with `@default` firing when absent).

```craftgo
type ListReq {
    page     int     @default(1)
    pageSize int     @default(20) @min(1) @max(100)
    status   Status  @default(Pending)
    tags     string[] @default([])
}
```

### `@sensitive`

Server-only field: tagged `json:"-"` so neither the request decoder nor the response encoder touches it. Skipped from OpenAPI entirely.

| Sites | field, errorField |
| -------- | -------- |
| Args  | `()` |

Conflicts: cannot combine with any wire-shaping decorator (validators, bindings, `@nullable`, `@default`).

```craftgo
type Order {
    id          string
    internalRef string @sensitive   // populated by service code, never on the wire
}
```

## Field bindings

Tell the handler where to read each field from. Mutually exclusive (a field has one binding).

| Decorator     | Sites          | Reads from                                                 |
| ------------- | -------------- | ---------------------------------------------------------- |
| `@body`       | field          | Request body (JSON or form)                                |
| `@path`       | field          | URL path parameter `{name}`                                |
| `@query`      | field          | URL query string                                           |
| `@header`     | field, errorField | Request header (input) / response header (error fields) |
| `@cookie`     | field, errorField | Request cookie (input) / response cookie (error fields) |
| `@form`       | field          | Multipart form field                                       |

All binding decorators take an optional string for an explicit wire name:

```craftgo
type GetUserReq {
    id      string @path                       // matches {id} segment
    page    int    @query                      // ?page=
    apiKey  string @header("X-API-Key")        // explicit header name
    session string @cookie("sid")              // explicit cookie name
}
```

A field with no binding decorator falls back to:

- `body` for body verbs (POST / PUT / PATCH)
- `query` for non-body verbs (GET / DELETE / HEAD / OPTIONS)

## Service decorators

### `@prefix(path)`

Path prefix prepended to every method route in the service.

| Sites | service |
| -------- | -------- |
| Args  | `(string)` |

```craftgo
@prefix("/v1")
service UserService {
    get GetUser /users/{id} { ... }   // -> GET /v1/users/{id}
}
```

### `@group(name)`

Logical grouping label used for OpenAPI tags and router buckets.

| Sites | service |
| -------- | -------- |
| Args  | `(string)` |

```craftgo
@group("admin")
service AdminService { ... }
```

`@group` differs from `@tags`: tags are a list (one method can have many), group is a single bucket label. Use group when you want a clean separation in generated docs / nav.

### `@middlewares(name1, name2, ...)`

Apply named middlewares. On a service, the chain runs on every method. On a method, the chain appends to the service-level chain.

| Sites | service, method |
| -------- | -------- |
| Args  | one or more idents (or one array literal) |

```craftgo
@middlewares(RequestID, AuthRequired)
service UserService {
    @middlewares(AdminOnly)
    delete PurgeUser /users/{id} { ... }   // chain: RequestID, AuthRequired, AdminOnly
}
```

The named middleware must be declared somewhere in the same package via `middleware Name`.

### `@tags(name1, name2, ...)`

OpenAPI tags. Method-level overrides service-level (method's list wins).

| Sites | service, method |
| -------- | -------- |
| Args  | strings or idents (or one array literal) |

```craftgo
@tags(users, public)
service UserService {
    @tags(admin)
    delete PurgeUser /users/{id} { ... }   // tags: [users, public, admin] (appended)
    get GetUser /users/{id} { ... }        // tags: [users, public]
}
```

Method-level `@tags(...)` **appends** to the service-level list. Use `@ignoreTags` if a single method must drop the inherited list (see [Service-level decorators and inheritance](#service-level-decorators-and-inheritance)).

### `@security(scheme)` / `@security(scheme, scopes: [...])`

OpenAPI security requirement. The `scheme` is a key from `craftgo.design.yaml` `openapi.securitySchemes`. The semantic check rejects unknown names.

| Sites | service, method |
| -------- | -------- |
| Args  | scheme ident, optional named `scopes: [...]` |

```craftgo
@security(bearer)
service UserService {
    @security(oauth2, scopes: ["users:write"])
    post CreateUser /users { ... }
}
```

`@security` is OpenAPI metadata - it does not enforce anything at runtime. Pair it with an `AuthRequired` middleware to actually check the token.

### Service-level decorators and inheritance

Service-level decorators (`@prefix`, `@group`, `@tags`, `@security`, service-level `@middlewares`, `@externalDocs`) declared on the primary `service { ... }` block apply to every method inside. Method-level decorators of the same kind **append** to the inherited chain:

```craftgo
@prefix("/v1")
@middlewares(AuthRequired)
@tags("users")
service UserService {
    @doc("inherits AuthRequired + users tag")
    get GetUser /{id} { ... }

    @doc("inherits + adds AdminOnly + 'admin' tag")
    @middlewares(AdminOnly)
    @tags("admin")
    delete PurgeUser /{id}/purge { ... }
}
```

`GetUser` runs `[AuthRequired]` with tags `["users"]`. `PurgeUser` runs `[AuthRequired, AdminOnly]` with tags `["users", "admin"]` - the method-level decorators append, not replace.

#### `@ignoreMiddleware` / `@ignoreSecurity` / `@ignoreTags`

The append default fits the 90% case. For an exceptional method that must drop the inherited chain, use the matching `@ignore*` decorator at method level:

```craftgo
@middlewares(AuthRequired, RateLimit)
@security(Bearer)
@tags("internal")
service SecuredService {
    get ListItems / { ... }       // inherits AuthRequired + RateLimit + Bearer + internal tag

    @doc("Liveness probe - public on purpose.")
    @ignoreMiddleware
    @ignoreSecurity
    @ignoreTags
    @tags("monitoring")
    get Healthz /healthz { ... }  // no middleware, no security, tag = ["monitoring"] only
}
```

The combine semantic is **clear-then-append**:

1. `@ignoreX` on the method clears whatever the service contributed to that decorator's chain.
2. Any method-level `@X(...)` decorators then append to the now-empty chain.

So `@ignoreMiddleware` + `@middlewares(Audit)` = method chain is exactly `[Audit]` (no inherited Auth). This is the **reset-and-replace** pattern - useful when one endpoint needs a completely different chain instead of the default.

The `@ignore*` decorators only apply at method level. They take no arguments. Repeating them is a `decorator/duplicate` error.

> [!NOTE]
> Earlier versions of the DSL used `@security(noauth)` as a sentinel for public endpoints. That syntax is removed - use `@ignoreSecurity` instead. The `@ignore*` form is symmetrical across security/middleware/tags and avoids tying a magic name to one specific decorator.

### `extend service` with decorators

The most common reason to split a service into `extend service` blocks is the **50/50 case**: half the methods need one decorator chain (authenticated admin endpoints), half need another (public probes, sign-up, login). Putting the entire service under `@middlewares(Auth)` and using `@ignoreMiddleware` on every public method is verbose. The clean alternative is to declare the primary service with the public endpoints (no service-level decorators) and use an `extend service` block for the authenticated half:

```craftgo
service Users {
    // Public endpoints
    get  /healthz => Health()
    post /signup  => Signup()
    post /login   => Login()
}

@middlewares(AuthRequired)
@security(Bearer)
extend service Users {
    get    /users      => List()       // authenticated
    get    /users/{id} => Get()         // authenticated
    post   /users      => Create()       // authenticated
    delete /users/{id} => Del()          // authenticated
}
```

Methods inside an `extend` block inherit the **block's own** decorators in addition to whatever the primary service declared. Multiple `extend` blocks may layer different decorator chains on the same service.

#### Rules for `extend service` decorators

- Only **method-level-applicable** decorators are valid on an `extend service` block - `@middlewares`, `@security`, `@tags`, `@deprecated`, `@externalDocs`, `@doc`. Service-only decorators like `@prefix` / `@group` belong on the primary and produce `service/extend-decorator-not-method` if put on extend.
- The primary service declaration must exist in the same package. A cross-package extend produces `service/extend-orphan` with a Related pointer to where the primary was found (or expected). To extend a service from another package, move the extend file into the primary's folder or rename the extend block to a new service.

#### Combinations cheatsheet

| Setup                                                | Result | Notes                                                 |
| ---------------------------------------------------- | ------ | ----------------------------------------------------- |
| `@middlewares` / `@security` / `@tags` on extend     | ✅      | Method-level-applicable decorators on extend OK       |
| `@prefix` / `@group` on extend                       | ❌      | `service/extend-decorator-not-method` - move to primary |
| Extend in a different folder (different package)     | ❌      | `service/extend-orphan`                                |
| Multiple extend blocks targeting the same service    | ✅      | Each block's decorators apply only to its own methods  |
| `@ignoreMiddleware` on a method inside extend        | ✅      | Clears extend-block + primary middleware chain        |

## Method decorators

### `@ignoreMiddleware` / `@ignoreSecurity` / `@ignoreTags`

Opt the current method out of the inherited service-level chain for the matching decorator. Useful for public endpoints (probes, sign-up, login) inside an otherwise-authenticated service, or for an admin method that needs a completely different middleware chain than the service default.

| Sites | method |
| -------- | -------- |
| Args  | none |

```craftgo
@middlewares(AuthRequired)
@security(Bearer)
@tags("internal")
service Users {
    get  /users => List()             // inherits all three

    @ignoreSecurity                   // clears Bearer only
    get  /users/{id}/avatar => Avatar()

    @ignoreMiddleware                 // clears AuthRequired
    @middlewares(BasicAuth, Audit)   // method-level chain becomes [BasicAuth, Audit]
    post /admin/reset => Reset()
}
```

The combine semantic is **clear-then-append**: `@ignoreX` clears the inherited chain first, then any method-level `@X(...)` decorators append to the now-empty chain. See [Service-level decorators and inheritance](#service-level-decorators-and-inheritance).

### `@summary(text)`

OpenAPI operation summary (one-line).

| Sites | method |
| -------- | -------- |
| Args  | `(string)` |

### `@operationId(name)`

Override the auto-generated `operationId` in OpenAPI.

| Sites | method |
| -------- | -------- |
| Args  | `(string)` |

```craftgo
@operationId("getUserById")
get GetUser /users/{id} { ... }
```

### `@status(code)`

Override the default success status code (200 for methods with a response, 204 for methods without).

| Sites | method |
| -------- | -------- |
| Args  | `(int)` |

```craftgo
@status(201)
post CreateUser /users { ... }
```

### `@errors(E1, E2, ...)`

Declare which error types this method may return. Drives OpenAPI's per-status response entries.

| Sites | method |
| -------- | -------- |
| Args  | error type idents (or one array literal) |

```craftgo
@errors(UserNotFound, EmailTaken)
post CreateUser /users { ... }
```

### Content negotiation — `@consumes` / `@produces` / `@accepts`

Removed in v1. craftgo's transport hardcodes `application/json` for
both request decode and response encode; the prior decorators parsed
but produced no runtime / spec effect, hiding the JSON-only constraint
from authors. Multi-codec support (XML / msgpack / cbor via a
`CodecRegistry` dispatch) is planned; when it lands these decorators
will return with a real wiring path. For now the transport pipeline is
JSON in, JSON out.

### `@passthrough`

Bypass framework parsing. The logic function receives raw `http.ResponseWriter` and `*http.Request` and writes the response directly. No JSON decode, no validate, no encode.

| Sites | method |
| -------- | -------- |
| Args  | `()` |

Useful for streaming, server-sent events, or endpoints that need full control over the wire format.

```craftgo
@passthrough
get StreamLogs /logs/stream { ... }
```

### `@timeout(duration)`

Cap the handler's execution time. Returns 503 + cancels context when the deadline elapses. Tighter than the global `server.handlerTimeout` config.

| Sites | method |
| -------- | -------- |
| Args  | `(duration)` like `5s`, `100ms`, `2m` |

```craftgo
@timeout(5s)
post ProcessImage /images/process { ... }
```

### `@maxBodySize(size)`

Cap the request body size in bytes. Reads past the cap surface as a normal Read error which the JSON decoder maps to 400.

| Sites | method |
| -------- | -------- |
| Args  | `(size)` like `1MB`, `8KB`, `100` |

```craftgo
@maxBodySize(1MB)
post UploadAvatar /users/{id}/avatar { ... }
```

## Conflict matrix

Some decorator combinations are rejected by the semantic analyzer:

| Decorator      | Conflicts with                                                                              | Why                                                       |
| -------------- | ------------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `@sensitive`   | All validators, all bindings, `@nullable`, `@default`                                       | Field never crosses the wire - constraints meaningless    |

Wrong-site placement (`@prefix` on a field, `@length` on a number) fires `decorator/placement` or `decorator/typemismatch`.

## Reference

- [Validators](/guide/validators) for runtime semantics
- [Middleware](/guide/middleware) for `@middlewares` and middleware declaration
- [Errors](/guide/errors) for `@errors` and the category-to-status mapping
- [AI Reference](/llms) for a single-page consolidated dump
