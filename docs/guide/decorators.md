# Decorators

Decorators attach metadata to declarations and fields. Every decorator starts with `@` and is registered in a closed set; `@unknown` fires a `decorator/unknown` diagnostic at semantic analysis time.

## At a glance

50 decorators, grouped by where they apply. Each decorator declares one or more **sites** (file, type, field, service, method, ...) and an **argument shape** (none, string, number, ident, list, ...). Using a decorator at the wrong site or with the wrong arguments fires a diagnostic with the line and column.

```craftgo
@title("My API")               // file
@version("1.0.0")              // file

type User {                    // type
    id    string  @required    // field
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
    email    string @required
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

### `@title(text)` and `@version(text)`

Override the OpenAPI document title and version per file. Without these, the values come from `craftgo.design.yaml`'s `openapi.title` / `openapi.version`.

| Sites | file |
| -------- | -------- |
| Args  | `(string)` |

```craftgo
@title("My API")
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

### `@required`

Field must be present in the request payload. For strings, also non-empty. For pointers, non-nil.

| Sites | field |
| -------- | -------- |
| Applies to | any primitive |
| Args | `()` |

### Strings

Run on `string` and `bytes` fields, and on scalars whose primitive is one of those.

| Decorator                   | Args                  | Effect                              |
| --------------------------- | --------------------- | ----------------------------------- |
| `@length(min, max)`         | `(int, int)`          | Length in `[min, max]` inclusive    |
| `@minLength(n)`             | `(int)`               | Length `>= n`                       |
| `@maxLength(n)`             | `(int)`               | Length `<= n`                       |
| `@pattern("regex")`         | `(string)`            | RE2-flavored regex match            |
| `@format(name)`             | bare ident or string  | Named format check (see below)      |

**Available formats** (`@format(...)`): `email`, `url`, `uri`, `uuid`, `datetime` (RFC 3339), `date`, `time`, `phone`, `hostname`, `ipv4`, `ipv6`, `cidr`, `mac`, `creditcard`, `base64`, `hexcolor`, `json`.

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
    file   file   @form @required @maxSize(2MB) @mimeTypes(["image/png", "image/jpeg"])
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

Conflicts: cannot combine with `@required`, any binding, or be applied to map / struct / generic fields.

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
    delete PurgeUser /users/{id} { ... }   // shows under "admin" tag
    get GetUser /users/{id} { ... }        // shows under "users" and "public" tags
}
```

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

### Service-level decorators and `extend service`

Service-level decorators (`@prefix`, `@group`, `@tags`, `@security`, service-level `@middlewares`) live **only on the primary `service` declaration**. The DSL hard-rejects two combinations to keep the model unambiguous.

#### Rule 1: decorators are not allowed on `extend service`

```craftgo
package design

service UserService {
    get GetUser /{id} { ... }
}

@group("admin")                     // ❌ rejected
@prefix("/internal")                // ❌ rejected
extend service UserService {
    delete PurgeUser /{id}/purge { ... }
}
```

Diagnostic: `service/extend-decorators`.

```
extend service "UserService" must not have service-level decorators
```

The whole-service settings have one canonical home (the primary block). Method-level `@middlewares(...)` is still permitted inside an `extend` block - those append to the primary's chain on a per-method basis.

#### Rule 2: extend must live in the primary's package

```craftgo
// design/users/service.craftgo  →  package design.users
package design

@group("public-users")
service UserService {
    get GetUser /{id} { ... }
}
```

```craftgo
// design/admin/extra.craftgo  →  package design.admin (different folder!)
package admin

extend service UserService { ... }   // ❌ rejected
```

Diagnostic: `service/extend-orphan`.

```
extend service "UserService": primary lives in package "design" - extend
declarations are per-package, move this block into that package or rename
the service
```

The diagnostic carries a Related pointer back to the primary's location. Codegen writes per-service scaffolds keyed on a single package, so a cross-package extend would split the artifacts; the framework refuses upfront.

**Workarounds:**
- Move the extend file into the primary's folder (most common).
- Rename the extend block to a different service - it becomes a separate service.
- Use `import` to share types across packages while keeping the service definition in one place.

#### Combinations cheatsheet

| Setup                                                | Result | Diagnostic                  |
| ---------------------------------------------------- | ------ | --------------------------- |
| `@group` on primary, extend in same folder           | ✅      | -                           |
| extend in a different folder (different package)     | ❌      | `service/extend-orphan`     |
| `@group` (or any svc-level decorator) on `extend`    | ❌      | `service/extend-decorators` |
| Method-level `@middlewares` inside `extend`          | ✅      | (chain appends to primary)  |

The extended methods inherit every service-level decorator from the primary - `@prefix`, `@group`, `@tags`, `@security`, service-level `@middlewares`. They show up under the same OpenAPI bucket and run the same chain.

## Method decorators

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

### `@consumes("type", ...)` and `@produces("type", ...)`

Restrict request and response content types in OpenAPI.

| Sites | method |
| -------- | -------- |
| Args  | strings (or one array literal) |

```craftgo
@consumes("application/json")
@produces("application/json")
post CreateUser /users { ... }
```

### `@accepts("encoding", ...)`

Restrict accepted request encodings (gzip, deflate, identity).

| Sites | method |
| -------- | -------- |
| Args  | strings |

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
| `@default`     | `@required`                                                                                 | Required fields fail validation before the default applies |

Wrong-site placement (`@prefix` on a field, `@length` on a number) fires `decorator/placement` or `decorator/typemismatch`.

## Reference

- [Validators](/guide/validators) for runtime semantics
- [Middleware](/guide/middleware) for `@middlewares` and middleware declaration
- [Errors](/guide/errors) for `@errors` and the category-to-status mapping
- [AI Reference](/llms) for a single-page consolidated dump
