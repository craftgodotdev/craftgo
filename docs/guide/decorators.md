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

This page is the **example-driven walkthrough** — what each decorator means, with snippets and the inheritance / opt-out mechanics. For a scannable lookup table (every decorator's levels, args, and effect in one grid), see the [Decorator Registry](/reference/decorator-registry).

### Names from other tools

The decorator set is closed — an unknown decorator fires `decorator/unknown`. If you're coming from JSON Schema, Zod, OpenAPI, or class-validator, a few reflexes map to different spellings:

| You might reach for | craftgo uses | Why |
| ------------------- | ------------ | --- |
| `@min(n)` / `@max(n)` | `@gte(n)` / `@lte(n)` (inclusive), `@gt` / `@lt` (strict) | One spelling for inclusive vs strict, consistent with `@range(min, max)`. |
| `@required` | nothing — fields are **required by default** | A field is required unless its type carries `?`. Use `?` to opt out. |
| `title` / `externalDocs` on an operation | `craftgo.design.yaml` `openapi.title`; `@doc("… https://…")` | Document-level metadata lives in the manifest, not per-decorator. |

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

### `@example(value)`

Example value rendered in the OpenAPI schema for this field.

| Sites | field |
| -------- | -------- |
| Args  | a literal (string / int / float / bool) or `{key: value}` object |

```craftgo
type User {
    name  string @example("alice")
    meta  object @example({tier: "gold", trial: false})
}
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
    personal bool?
    business bool?
}
```

::: tip Listed fields must be pointer-backed
Each referenced field must be optional (`?`) or `@nullable` so it has an
unambiguous present/absent state. A plain field (checked by zero-value
emptiness), a `@query` / `@header` / `@cookie` / `@form` parameter, a
`@default` field, a `@sensitive` field, and a collection (`T[]` / `map<…>` /
`bytes` / `any`) are all rejected at design time — their runtime presence
can't match the `present-and-non-null` the OpenAPI fragment advertises.
:::

## Field validators

> **Required-by-default**: every field is required unless its type carries the `?` suffix. There is no `@required` decorator — to mark a field optional, write `name string?`. To allow `null` while keeping the field mandatory, add `@nullable`. To pre-fill an absent value, add `@default(...)` (which also auto-marks the field optional on save).

> **Error-body validators are spec-only.** Validators (`@minLength`, `@pattern`, `@range`, ...) on `error` body fields surface in the generated OpenAPI schema constraints but produce **no runtime check** — errors are emitted server-side from your handler, so the framework cannot validate something it just constructed. Treat the constraints on error fields as documentation contracts that consumer SDKs read; the handler is responsible for shaping the values correctly before calling `NewFooErr(...)`.

### Strings

Run on `string` and `bytes` fields, and on scalars whose primitive is one of those.

| Decorator                   | Args                  | Effect                              |
| --------------------------- | --------------------- | ----------------------------------- |
| `@length(min, max)`         | `(int, int)`          | Length in `[min, max]` inclusive    |
| `@minLength(n)`             | `(int)`               | Length `>= n`                       |
| `@maxLength(n)`             | `(int)`               | Length `<= n`                       |
| `@pattern("regex")`         | `(string)`            | RE2-flavored regex match            |
| `@format(name)`             | bare ident or string  | Named format check (see below)      |

On a **`string`** field, length validators count **Unicode characters**
(runes), not bytes — the generated Go validator uses
`utf8.RuneCountInString(s)`. This matches the OpenAPI `minLength` / `maxLength`
keyword (JSON Schema counts characters) and a Postgres `varchar(n)` (also
characters), so the runtime, the spec, and the database agree. `"日本語"` (3
characters / 9 bytes) passes `@maxLength(3)`. To cap the raw network size
instead, use `@maxBodySize` (which polices bytes).

On a **`bytes`** field, length validators count **bytes** (`len(b)`) — the
binary length — and are not advertised in the OpenAPI schema (an OpenAPI
`maxLength` on a `bytes` field would count base64 characters, a different
number).

**Available formats** (`@format(...)`): `email`, `url`, `uri`, `uuid`, `datetime` (RFC 3339), `date`, `time`, `phone`, `ipv4`, `ipv6`, `cidr`, `mac`, `creditcard`, `base64`, `base64url`, `hexcolor`, `json`. RFC-compliant validators (email, ipv4/ipv6, cidr, mac, datetime/date/time, base64, json) delegate to Go stdlib (`net`, `net/mail`, `net/url`, `time`, `encoding/*`); the remainder use regex.

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
| `@gte(n)`               | `(number)`         | Value `>= n`                    |
| `@gt(n)`                | `(number)`         | Value `> n`                     |
| `@lte(n)`               | `(number)`         | Value `<= n`                    |
| `@lt(n)`                | `(number)`         | Value `< n`                     |
| `@range(min, max)`      | `(number, number)` | Both bounds inclusive (`@gte` + `@lte`) |
| `@positive`             | `()`               | Value `> 0`                     |
| `@negative`             | `()`               | Value `< 0`                     |
| `@multipleOf(n)`        | `(number)`         | Value divisible by `n`          |

```craftgo
type Order {
    quantity int   @positive @lte(1000)
    price    int   @gte(0) @multipleOf(2)
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
    pageSize int     @default(20) @gte(1) @lte(100)
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

**Response-side bindings on response and error types.** `@header` / `@cookie` on a response struct or error body field write the value onto `w.Header()` / `http.SetCookie(...)` instead of the JSON body — the JSON tag is automatically `json:"-"` so the same field doesn't double up. Non-string values (`int`, `bool`, `float`, scalars and enums of those) are formatted to their wire string via `strconv`, just like the request-side binder parses them; an optional (`T?`) header is written only when non-nil, and a `string[]` header emits one value per element. The explicit-name argument applies here too:

```craftgo
type PaginatedResp {
    items   shared.ID[]
    count   int      @header("X-Total-Count")   // strconv.Itoa → X-Total-Count: 42
    nextURL string?  @header("X-Next")           // only written when present
    active  bool     @cookie("active")           // Set-Cookie: active=true
    session string   @cookie("session_id")       // Set-Cookie: session_id=...
}

error TooManyRequests RateLimitedErr {
    retryAfter int  @header("Retry-After")       // error responses can ship custom headers
}
```

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

### `@group(path)`

Does two things: (1) sets where the service's generated **files** land on disk — the value **replaces** the service-name segment, so the files go to `<output>/<group>/` instead of `<output>/<service-name>/` — and (2) adds its value as an **OpenAPI tag**. It does **not** change the HTTP route or the OpenAPI *path* — use `@prefix` for that.

| Sites | service, extend service |
| -------- | -------- |
| Args  | `(string)` |

```craftgo
@prefix("/v1/admin")
@group("admin/ops")
service AdminService {
    get DashboardStats /dashboard { ... }   // -> GET /v1/admin/dashboard
}
```

With the above, the handler and service stub for `DashboardStats` are written to `internal/transport/admin/ops/dashboard-stats.go` and `internal/service/admin/ops/dashboard-stats.go` (the `admin-service` segment is gone — the group took its place), while the route stays `/v1/admin/dashboard`. The value is a relative path: a single segment (`admin`) or nested (`admin/ops`). Routes and types are unaffected — only transport handlers and service stubs move.

> **The group is a global namespace.** Because it replaces the service name, two services that pick the *same* `@group` land in the same directory (and Go package), where their method names can collide. Keep groups unique per service — embed the service name in the group (`@group("admin/ops")`) when in doubt.

The group value also rides along as an OpenAPI **tag**, appended to any explicit `@tags` and deduped. So `@group("admin/ops") @tags(users)` tags every operation `[users, admin/ops]`; `@group("admin") @tags(admin)` collapses to a single `admin`. `@ignoreTags` on a method drops the group tag along with the rest of the inherited service tags.

Because the move changes where the service stub is generated, set `@group` before you start filling in business logic: adding it later leaves your existing stub at the old path and scaffolds a fresh empty one under the group.

**Per-block grouping.** Unlike `@prefix` (primary-only), `@group` is also accepted on an `extend service` block, where it groups **only that block's** methods. This splits one service's code across version/feature folders while a single routes file still registers every method. Each group folder is a self-contained package with its own `writeError` helper:

```craftgo
@prefix("/checkout")
service Checkout {
    post Pay /pay { request PayReq  response Receipt }      // -> transport/checkout/pay.go
}

@group("checkout/v2")
extend service Checkout {
    post PayV2 /v2/pay { request PayReqV2  response Receipt } // -> transport/checkout/v2/pay-v2.go
}
```

The shared `writeError` helper stays at the service's transport root and every group package reaches it through the exported `WriteError`, so the groups do not duplicate it.

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

OpenAPI tags. Method-level **appends** to the service-level list; use `@ignoreTags` to drop the inherited list when a single method needs to opt out.

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

### `@security(A, B, ...)`

OpenAPI security requirements. Each ident is a key from `craftgo.design.yaml` `openapi.securitySchemes`; the semantic check rejects unknown names.

| Sites | service, method |
| -------- | -------- |
| Args  | variadic ident list (one or more scheme names) |

Within a single decorator the idents are AND-combined (the operation requires every listed scheme). Multiple `@security(...)` decorators on the same site OR-combine: any matching set unlocks the operation.

```craftgo
@security(bearer)
service UserService {
    @security(oauth2)
    post CreateUser /users { ... }
}
```

> [!IMPORTANT]
> **`@security` is OpenAPI metadata only — it does NOT enforce anything at runtime.** The decorator drives the `security` block in the generated OpenAPI spec so SDKs and Swagger UI know what the operation expects; no middleware is auto-attached, no header is auto-checked. Pair `@security(Bearer)` with an `AuthRequired` (or equivalent) middleware to actually verify the credential.

`@ignoreSecurity` on a method clears the inherited service-level `@security` chain — useful for a single public endpoint (liveness probe, etc.) inside an otherwise-protected service.

### Service-level decorators and inheritance

Service-level decorators (`@prefix`, `@group`, `@tags`, `@security`, service-level `@middlewares`) declared on the primary `service { ... }` block apply to every method inside. Method-level decorators of the same kind **append** to the inherited chain:

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

When the service is split across an `extend service` block, `@ignore*` clears the **combined** inherited chain — both decorators on the primary `service { ... }` declaration AND decorators on the `extend service` block. A method that opts out walks back to an empty chain regardless of which side of the split introduced the inheritance.

> [!NOTE]
> Earlier versions of the DSL used `@security(noauth)` as a sentinel for public endpoints. That syntax is removed - use `@ignoreSecurity` instead. The `@ignore*` form is symmetrical across security/middleware/tags and avoids tying a magic name to one specific decorator.

### `extend service` with decorators

The most common reason to split a service into `extend service` blocks is the **50/50 case**: half the methods need one decorator chain (authenticated admin endpoints), half need another (public probes, sign-up, login). Putting the entire service under `@middlewares(Auth)` and using `@ignoreMiddleware` on every public method is verbose. The clean alternative is to declare the primary service with the public endpoints (no service-level decorators) and use an `extend service` block for the authenticated half:

```craftgo
service Users {
    // Public endpoints
    get  Healthz /healthz { response HealthResp }
    post Signup  /signup  { request SignupReq response User }
    post Login   /login   { request LoginReq  response Session }
}

@middlewares(AuthRequired)
@security(Bearer)
extend service Users {
    get    List   /users      { response UserList }                 // authenticated
    get    Get    /users/{id} { request GetUserReq  response User } // authenticated
    post   Create /users      { request CreateUserReq response User } // authenticated
    delete Del    /users/{id} { request GetUserReq  response OkResp } // authenticated
}
```

Methods inside an `extend` block inherit the **block's own** decorators in addition to whatever the primary service declared. Multiple `extend` blocks may layer different decorator chains on the same service.

#### Rules for `extend service` decorators

- Only **method-level-applicable** decorators are valid on an `extend service` block - `@middlewares`, `@security`, `@tags`, `@deprecated`, `@doc` - plus `@group`, which groups that block's own methods on disk. `@prefix` is primary-only and produces `service/extend-decorator-not-method` if put on extend.
- The primary service declaration must exist in the same package. A cross-package extend produces `service/extend-orphan` with a Related pointer to where the primary was found (or expected). To extend a service from another package, move the extend file into the primary's folder or rename the extend block to a new service.

#### Combinations cheatsheet

| Setup                                                | Result | Notes                                                 |
| ---------------------------------------------------- | ------ | ----------------------------------------------------- |
| `@middlewares` / `@security` / `@tags` on extend     | ✅      | Method-level-applicable decorators on extend OK       |
| `@prefix` on extend                                  | ❌      | `service/extend-decorator-not-method` - move to primary |
| `@group` on extend                                   | ✅      | groups that block's methods under their own folder |
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
    get  List /users { response UserList }                  // inherits all three

    @ignoreSecurity                                         // clears Bearer only
    get  Avatar /users/{id}/avatar { request GetUserReq response Avatar }

    @ignoreMiddleware                                       // clears AuthRequired
    @middlewares(BasicAuth, Audit)                          // method-level chain becomes [BasicAuth, Audit]
    post Reset /admin/reset { request ResetReq response OkResp }
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

Override the success status code. By default it is chosen from the verb and response:

| Method | Default success status |
| -------- | -------- |
| `POST` returning a body | `201 Created` |
| `GET` / `PUT` / `PATCH` / `DELETE` returning a body | `200 OK` |
| any method with no response body | `204 No Content` |

`@status(code)` overrides that default — for example a `POST` that updates rather than creates:

| Sites | method |
| -------- | -------- |
| Args  | `(int)` |

```craftgo
@status(200)
post SearchUsers /users/search { ... }
```

The generated handler writes the code with `w.WriteHeader` (only when it differs from the implicit `200`), and the OpenAPI spec lists the same code as the success response — the two never drift.

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

Not supported. craftgo's transport hardcodes `application/json` for
both request decode and response encode, so a content-negotiation
decorator would parse but produce no runtime or spec effect — which
hides the JSON-only constraint from authors. The decorator surface
stays small and honest: the transport pipeline is JSON in, JSON out,
and the codec is swappable wholesale via `server.SetGlobalJSONCodec`
when a project wants sonic / jsoniter in place of `encoding/json`.

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

Cap the request body size in bytes. Two enforcement points fire:

1. **Pre-check on `Content-Length`** — when the client declares a length bigger than the cap, the middleware returns 413 immediately without touching the body. Catches oversized requests even when downstream validation would short-circuit before reading.
2. **`http.MaxBytesReader` wraps `r.Body`** — JSON decoders that read past the cap get a normal Read error, which the handler maps to 400.

For multipart uploads, `@maxBodySize` also lifts the in-memory parser budget above the stdlib's 32 MiB floor so files up to the declared cap stay in memory without spilling to a temp file.

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

## See also

- [Decorator Registry](/reference/decorator-registry) — the full lookup table (levels, args, effect)
- [Validators](/guide/validators) for runtime semantics
- [Middleware](/guide/middleware) for `@middlewares` and middleware declaration
- [Errors](/guide/errors) for `@errors` and the category-to-status mapping
- [llms.md](/llms) for a single-page consolidated dump
