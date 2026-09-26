# Decorator Registry

Every decorator craftgo understands, where it may appear, and what arguments it takes. This is the complete, closed set - there is no plugin mechanism, and an unknown decorator is a compile error (`decorator/unknown`). The CLI and LSP validate against exactly this table.

A decorator's **level** is where it may be written. Applying one at the wrong level raises `decorator/placement`.

| Level | Site |
|---|---|
| file | top of a `.craftgo` file |
| type | `type` declaration |
| field | a field inside a `type` body |
| service | `service` declaration |
| method | a method inside a `service` |
| enum / enum-value | `enum` declaration / one value |
| error / error-field | `error` declaration / a field in its body |
| scalar | `scalar` declaration |
| middleware | `middleware` declaration |
| event | an `event` declaration (file level) |

## Documentation & lifecycle

| Decorator | Levels | Args | Effect |
|---|---|---|---|
| `@doc("...")` | everywhere | `(string)` | Free-form docs: the Go doc comment of the generated declaration, and the OpenAPI `description` of a file, type, field, error field, scalar, enum or method. The editor hover shows a declaration's `//` comment, not `@doc`. |
| `@deprecated` / `@deprecated("why")` | file, type, field, service, method, enum-value, middleware, event, error-field | `(string?)` | Marks the construct deprecated: OpenAPI's `deprecated` flag on a type, field or operation (a service's marks each of its operations), and a Go `// Deprecated:` paragraph on a type or field. No generated effect on a file, enum value, middleware or event. |
| `@example(v)` | field, error-field | `(literal \| [literals])` | Example value rendered in the field's OpenAPI schema; an object literal is refused. |
| `@version("1.2.3")` | file | `(string)` | OpenAPI document version (overrides `openapi.version` in the manifest); the first file in path order that carries one wins. |

## Field validation - string

All apply at field, scalar, and error-field level. They target `string`-typed values; the three length decorators also bound the length of a `bytes` value.

| Decorator | Args | Effect |
|---|---|---|
| `@length(n)` / `@length(min, max)` | `(int)` or `(int, int)` | Exact length, or inclusive `[min, max]`. |
| `@minLength(n)` | `(int)` | Length `>= n`. |
| `@maxLength(n)` | `(int)` | Length `<= n`. |
| `@pattern("re")` | `(string)` | RE2 regex the value must match. |
| `@format(name)` | `(ident \| string)` | Named format - `email`, `uuid`, `url`, `datetime`, … On a `bytes` field only, `raw`: the bytes already ARE the value in the message's own encoding, so the codec embeds them untouched (Go `wire.Raw`). `raw` checks nothing and is refused on every other type; no other validator applies to a field carrying it. |

## Field validation - number

Field, scalar, error-field level. Target numeric (`int*`, `uint*`, `float*`) values.

| Decorator | Args | Effect |
|---|---|---|
| `@gt(n)` | `(number)` | `x > n` (strictly greater). |
| `@gte(n)` | `(number)` | `x >= n` (inclusive). |
| `@lt(n)` | `(number)` | `x < n` (strictly less). |
| `@lte(n)` | `(number)` | `x <= n` (inclusive). |
| `@range(min, max)` | `(number, number)` | Inclusive `[min, max]`. |
| `@positive` | - | `x > 0` (flag form, sugar for `@gt(0)`). |
| `@negative` | - | `x < 0` (flag form, sugar for `@lt(0)`). |
| `@multipleOf(n)` | `(number)` | `x % n == 0`, on integers only; `n` is a whole number. |

::: tip Coming from JSON Schema, Zod, or class-validator?
craftgo spells numeric bounds `@gte` / `@lte` (inclusive) and `@gt` / `@lt` (strict) - there is no `@min` / `@max`. The split mirrors the strict-vs-inclusive distinction and reads consistently with `@range(min, max)`.
:::

## Field validation - array / map

Field and error-field level. Target arrays (and, for the `Items` pair, map length).

| Decorator | Args | Effect |
|---|---|---|
| `@minItems(n)` | `(int)` | At least `n` elements. |
| `@maxItems(n)` | `(int)` | At most `n` elements. |
| `@uniqueItems` | - | All elements must be distinct (flag form). |

## Field validation - file (multipart)

Field level only. Target `file`-typed fields on a multipart request.

| Decorator | Args | Effect |
|---|---|---|
| `@maxSize(n)` | `(size)` | Upload size cap - `2MB`, `500KB`, `1GB`, or bare bytes. |
| `@mimeTypes("a", "b")` | variadic strings / array | Media types or `type/*` ranges the upload's `Content-Type` must match, its parameters and case aside. |

## Cross-field - type level

Written on the `type` declaration; reference its field names.

| Decorator | Args | Effect |
|---|---|---|
| `@requiresOneOf(a, b, c)` | variadic idents/strings or one array | At least one of the listed fields must be present. Emits `anyOf` in OpenAPI. |
| `@mutuallyExclusive(a, b)` | variadic idents/strings or one array | At most one may be present. Emits `not: { required: [a, b] }` in OpenAPI, and for more fields a `not` of an `anyOf` over every pair. |

## Field shaping & binding

Field level (a few also apply at error-field level for response writing).

| Decorator | Args | Effect |
|---|---|---|
| `@default(v)` | `(literal \| enum value \| array)` | Value the handler pre-fills before binding, kept when the field is absent on the wire. On a field without `?` it still pre-fills, with a `decorator/default-needs-optional` warning (`craftgo fmt` adds the `?`). Refused beside `@path`. |
| `@nullable` | - | The field accepts an explicit JSON `null` (flag form). |
| `@json("key")` | `(string)` | The JSON key of a body field when it is not the field name - a contract another system owns, or a key such as `OrderItem` that the parser would read as a mixin. Used by the Go tag, the documents and validation messages. Not combinable with an off-body binding. |
| `@sensitive` | - | Server-only field - tagged `json:"-"`, skipped from OpenAPI. Cannot combine with any validator, binding, `@json`, `@default`, or `@nullable`. |
| `@path` / `@path("name")` | `(string?)` | Bind from a URL path parameter. |
| `@query` / `@query("name")` | `(string?)` | Bind from the URL query string. |
| `@header` / `@header("Name")` | `(string?)` | Bind from a request header (request fields) or write a response header (response and error fields). |
| `@cookie` / `@cookie("name")` | `(string?)` | Bind from a cookie (request) or set one (response and error fields). |
| `@body` | - | Bind from the request body (the default for body verbs). A name argument is accepted and has no effect; `@json` sets the key. |
| `@form` / `@form("name")` | `(string?)` | Bind from a multipart form field; the request needs a `file`. |

See [Types & Scalars](/guide/types-and-scalars) for how binding interacts with field types.

## Service level

| Decorator | Args | Effect |
|---|---|---|
| `@prefix("/v1")` | `(string)` | Path prefix prepended to every method route. |
| `@group("admin/ops")` | `(string)` | **Replaces** the service-name segment on disk, so handlers, service stubs and `routes.go` land under `<output>/<group>/` instead of `<output>/<service>/`, and adds its value as an OpenAPI tag. Does not affect the route or OpenAPI path. Services may share a group: they merge into one folder with a single `routes.go`. Contributors from different DSL packages raise `group/package-straddle`; two contributors declaring the same method name raise `group/method-collision`. On an `extend service` block it groups only that block's methods. |
| `@middlewares(A, B)` | variadic idents / array | Apply named middlewares (also valid at method level - see below). |
| `@tags(a, b)` | variadic idents/strings / array | OpenAPI tags (also method level). |
| `@security(scheme)` | variadic idents / array | Security-scheme requirements (also method level). Within one decorator schemes AND-combine; multiple `@security(...)` OR-combine. |

## Method level

Method-level `@middlewares` / `@tags` / `@security` **append** to the service-level chain. The `@ignore*` decorators below clear the inherited chain so the method starts fresh.

| Decorator | Args | Effect |
|---|---|---|
| `@summary("...")` | `(string)` | One-line OpenAPI operation summary. |
| `@operationId("...")` | `(string)` | Override the OpenAPI `operationId`. |
| `@errors(UserNotFound, EmailTaken)` | variadic error idents / array | Declared error responses, by error name (drives OpenAPI `responses`). |
| `@status(201)` | `(int)` | Override the default success status code. |
| `@timeout(3s)` | `(duration)` | Cap handler execution; overrides the global `server.handlerTimeout` (used as-is). Cancels the request context on the deadline; nothing is written then, and a handler that returns the context's error (or one wrapping it) gets 504 `{"message":"gateway timeout"}`. |
| `@maxBodySize(1MB)` | `(size)` | Cap the request body (replaces the global `server.maxBodySize`): 413 `{"message":"request entity too large"}` on a declared Content-Length over the cap and on a read past it, a multipart body cut mid-part included. |
| `@rawResponse` | - | Logic writes the response to `http.ResponseWriter`; the request is still bound + validated. A `response` block is a docs-only contract. Stub: `(w, r, req *types.Req) error` (flag form). |
| `@rawRequest` | - | Logic reads the raw `*http.Request`; the response is still JSON-encoded. A `request` block is a docs-only contract. Stub: `(r *http.Request) (*types.Resp, error)` (flag form). |
| `@passthrough` | - | Both sides raw - exactly `@rawRequest @rawResponse`. Stub: `(w, r) error`. Optional blocks document the contract (flag form). |
| `@ignoreMiddleware` | - | Clear the inherited `@middlewares` chain on this method - the method's own decorator then starts from empty instead of appending to the service-level chain. On an `extend service` block, each of its methods drops the primary's chain. |
| `@ignoreSecurity` | - | Clear the inherited `@security` chain (e.g. a public endpoint in an authed service); on an `extend service` block, for each of its methods. |
| `@ignoreTags` | - | Clear the inherited `@tags` list; on an `extend service` block, for each of its methods. |

## Event level

See the [Events guide](/guide/events) for the full picture.

| Decorator | Args | Effect |
|---|---|---|
| `@contract("order.placed.v2")` | `(string)` | Override the event's wire identity. Defaults to `<package>.<Event>`; set it to interoperate with a contract another system already publishes. Two events resolving to one name raise `event/contract-collision`. |

`@doc` and `@deprecated` also apply at event level; nothing else does.

`@key`, `@consumerGroup` and `@consumeMiddlewares` are not decorators: the deployable decides all three, not the shared design - the ordering key is an argument to the publish call (`orders.Placed.Publish(ctx, bus, payload, craftevents.WithKey(id))`), the group an argument to `orders.Placed.Subscribe(bus, group, fn)`, and the chain `bus.Use(...)` where the bus is built. Writing one draws `decorator/removed` with that note, also on LSP hover. See [Groups](/guide/events#groups) and [Middleware](/guide/events#middleware).

## Not supported

`@consumes`, `@produces`, `@accepts` are intentionally **absent**. craftgo's transport hardcodes `application/json` for request decode and response encode (plus `multipart/form-data` when a `file` field is present), so a content-negotiation decorator would parse but have no effect. To emit or accept another format, hand that side to logic with `@rawResponse` / `@rawRequest`; the block on that side stays the documented contract.

## Argument forms

- **Flag** (`@positive`, `@uniqueItems`, `@nullable`, `@sensitive`, `@passthrough`, `@rawRequest`, `@rawResponse`, `@ignore*`) take no parentheses. Writing empty `()` raises `decorator/flag-empty-parens`.
- **Variadic** decorators (`@middlewares`, `@tags`, `@security`, `@errors`, `@mimeTypes`, `@requiresOneOf`, `@mutuallyExclusive`) accept either a comma list `(A, B, C)` or a single array literal. The array's elements follow each decorator's kind: `@middlewares`, `@security` and `@errors` take identifiers (`([A, B, C])`), `@tags`, `@requiresOneOf` and `@mutuallyExclusive` identifiers or strings, and `@mimeTypes` strings.
- **Durations** (`@timeout`) take one number with one unit - `ns`, `us`/`µs`, `ms`, `s`, `m` or `h`, a fraction allowed (`1.5h`) - or a bare integer read as seconds. `1h30m` is a parse error; write `90m`.
- **Sizes** (`@maxSize`, `@maxBodySize`) take `B` / `KB` / `MB` / `GB` suffixes, a fraction allowed (`1.5MB`), or bare bytes.
