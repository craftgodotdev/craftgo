# Decorator Registry

Every decorator craftgo understands, where it may appear, and what arguments it takes. This is the complete, closed set — there is no plugin mechanism, and an unknown decorator is a compile error (`decorator/unknown`). The CLI and LSP validate against exactly this table.

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

## Documentation & lifecycle

| Decorator | Levels | Args | Effect |
|---|---|---|---|
| `@doc("...")` | everywhere | `(string)` | Free-form docs; surfaces in OpenAPI `description` and IDE hover. |
| `@deprecated` / `@deprecated("why")` | file, type, field, service, method, enum-value, middleware, error-field | `(string?)` | Marks the construct deprecated; OpenAPI emits the `deprecated` flag. |
| `@example(v)` | field | `(literal \| {k: v})` | Example value rendered in the field's OpenAPI schema. |
| `@version("1.2.3")` | file | `(string)` | OpenAPI document version (overrides `openapi.version` in the manifest). |

## Field validation — string

All apply at field, scalar, and error-field level. They target `string`-typed values.

| Decorator | Args | Effect |
|---|---|---|
| `@length(n)` / `@length(min, max)` | `(int)` or `(int, int)` | Exact length, or inclusive `[min, max]`. |
| `@minLength(n)` | `(int)` | Length `>= n`. |
| `@maxLength(n)` | `(int)` | Length `<= n`. |
| `@pattern("re")` | `(string)` | RE2 regex the value must match. |
| `@format(name)` | `(ident \| string)` | Named format — `email`, `uuid`, `url`, `datetime`, … |

## Field validation — number

Field, scalar, error-field level. Target numeric (`int*`, `uint*`, `float*`) values.

| Decorator | Args | Effect |
|---|---|---|
| `@gt(n)` | `(number)` | `x > n` (strictly greater). |
| `@gte(n)` | `(number)` | `x >= n` (inclusive). |
| `@lt(n)` | `(number)` | `x < n` (strictly less). |
| `@lte(n)` | `(number)` | `x <= n` (inclusive). |
| `@range(min, max)` | `(number, number)` | Inclusive `[min, max]`. |
| `@positive` | — | `x > 0` (flag form, sugar for `@gt(0)`). |
| `@negative` | — | `x < 0` (flag form, sugar for `@lt(0)`). |
| `@multipleOf(n)` | `(number)` | `x % n == 0`. |

::: tip Coming from JSON Schema, Zod, or class-validator?
craftgo spells numeric bounds `@gte` / `@lte` (inclusive) and `@gt` / `@lt` (strict) — there is no `@min` / `@max`. The split mirrors the strict-vs-inclusive distinction and reads consistently with `@range(min, max)`.
:::

## Field validation — array / map

Field and error-field level. Target arrays (and, for the `Items` pair, map length).

| Decorator | Args | Effect |
|---|---|---|
| `@minItems(n)` | `(int)` | At least `n` elements. |
| `@maxItems(n)` | `(int)` | At most `n` elements. |
| `@uniqueItems` | — | All elements must be distinct (flag form). |

## Field validation — file (multipart)

Field level only. Target `file`-typed fields on a multipart request.

| Decorator | Args | Effect |
|---|---|---|
| `@maxSize(n)` | `(size)` | Upload size cap — `2MB`, `500KB`, `1GB`, or bare bytes. |
| `@mimeTypes("a", "b")` | variadic strings / array | Allowed `Content-Type` list for the upload. |

## Cross-field — type level

Written on the `type` declaration; reference its field names.

| Decorator | Args | Effect |
|---|---|---|
| `@requiresOneOf(a, b, c)` | variadic idents/strings or one array | At least one of the listed fields must be present. Emits `anyOf` in OpenAPI. |
| `@mutuallyExclusive(a, b)` | variadic idents/strings or one array | At most one may be present. Emits `not: { required: [...] }` in OpenAPI. |

## Field shaping & binding

Field level (a few also apply at error-field level for response writing).

| Decorator | Args | Effect |
|---|---|---|
| `@default(v)` | `(literal)` | Value applied when the field is absent on the wire. Field must be optional (`?`). |
| `@nullable` | — | The field accepts an explicit JSON `null` (flag form). |
| `@sensitive` | — | Server-only field — tagged `json:"-"`, skipped from OpenAPI. Cannot combine with any validator, binding, `@default`, or `@nullable`. |
| `@path` / `@path("name")` | `(string?)` | Bind from a URL path parameter. |
| `@query` / `@query("name")` | `(string?)` | Bind from the URL query string. |
| `@header` / `@header("Name")` | `(string?)` | Bind from a request header (request fields) or write a response header (error fields). |
| `@cookie` / `@cookie("name")` | `(string?)` | Bind from a cookie (request) or set one (error fields). |
| `@body` / `@body("name")` | `(string?)` | Bind from the request body (the default for body verbs). |
| `@form` / `@form("name")` | `(string?)` | Bind from a multipart form field. |

See [Types & Scalars](/guide/types-and-scalars) for how binding interacts with field types.

## Service level

| Decorator | Args | Effect |
|---|---|---|
| `@prefix("/v1")` | `(string)` | Path prefix prepended to every method route. |
| `@group("name")` | `(string)` | Logical grouping label — OpenAPI tag + router bucket. |
| `@middlewares(A, B)` | variadic idents / array | Apply named middlewares (also valid at method level — see below). |
| `@tags(a, b)` | variadic idents/strings / array | OpenAPI tags (also method level). |
| `@security(scheme)` | variadic idents / array | Security-scheme requirements (also method level). Within one decorator schemes AND-combine; multiple `@security(...)` OR-combine. |

## Method level

Method-level `@middlewares` / `@tags` / `@security` **append** to the service-level chain. The `@ignore*` decorators below clear the inherited chain so the method starts fresh.

| Decorator | Args | Effect |
|---|---|---|
| `@summary("...")` | `(string)` | One-line OpenAPI operation summary. |
| `@operationId("...")` | `(string)` | Override the OpenAPI `operationId`. |
| `@errors(NotFound, Conflict)` | variadic error idents / array | Declared error responses (drives OpenAPI `responses`). |
| `@status(201)` | `(int)` | Override the default success status code. |
| `@timeout(3s)` | `(duration)` | Cap handler execution; returns 503 + cancels the context on deadline. |
| `@maxBodySize(1MB)` | `(size)` | Cap request body — 413 on Content-Length pre-check, 400 on overflow read. |
| `@passthrough` | — | Bypass framework parsing; logic gets the raw `http.ResponseWriter` + `*http.Request` (flag form). |
| `@ignoreMiddleware` | — | Clear the inherited `@middlewares` chain on this method. |
| `@ignoreSecurity` | — | Clear the inherited `@security` chain (e.g. a public endpoint in an authed service). |
| `@ignoreTags` | — | Clear the inherited `@tags` list. |

## Not supported

`@consumes`, `@produces`, `@accepts` are intentionally **absent**. craftgo's transport hardcodes `application/json` for request decode and response encode (plus `multipart/form-data` when a `file` field is present), so a content-negotiation decorator would parse but have no effect. They return when a real multi-codec dispatch path lands.

## Argument forms

- **Flag** (`@positive`, `@uniqueItems`, `@nullable`, `@sensitive`, `@passthrough`, `@ignore*`) take no parentheses. Writing empty `()` raises `decorator/flag-empty-parens`.
- **Variadic** decorators (`@middlewares`, `@tags`, `@security`, `@errors`, `@mimeTypes`, `@requiresOneOf`, `@mutuallyExclusive`) accept either a comma list `(A, B, C)` or a single array literal `(["A", "B", "C"])`.
- **Durations** (`@timeout`) take Go duration syntax: `3s`, `500ms`, `1h30m`.
- **Sizes** (`@maxSize`, `@maxBodySize`) take `KB` / `MB` / `GB` suffixes or bare bytes.
