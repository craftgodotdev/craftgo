# Changelog

All notable changes to craftgo are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and craftgo follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) — from 1.0.0 on, a
breaking change to the DSL or the generated layout bumps the major version.

## [Unreleased]

### Added

- Reserved words (`type`, `error`, `map`, `delete`, `request`, ...) are
  accepted as field names and enum value names. A type body holds only fields
  and mixins and an enum body only value names, so a leading keyword reads as
  the identifier — `type string @pattern(...)` and `enum Kind { type ... }`
  parse, and lower to exported Go fields (`Type`) with the keyword as the JSON
  tag.
- Optional non-string primitives bind to `@query` / `@header` / `@cookie`
  (`page int? @query`, `active bool? @header`, ...). The binder writes a
  pointer on presence and leaves it nil when the param is absent or empty
  (`?page=`); a present-but-unparseable value is a 400. Previously only
  optional strings were allowed on those sources.

### Changed

- Wire-bind parse failures (`?page=abc`) and JSON body-decode failures now go
  through `server.WriteValidationError` — the same swappable hook as
  `req.Validate()` failures — so all request-input errors share one response
  envelope. The default hook still writes a plain 400.
- Generated handlers bind parsed primitives through the generic `server.Bind*`
  / `server.Parse*` helpers (one call per field) instead of an inline
  strconv block, so the handlers no longer import `strconv` / `errors` and
  shrink by ~a third. No reflection — the helpers are compile-time
  monomorphized and preserve per-type overflow checks.
- A handler parses `r.URL.Query()` once into a local instead of per query
  field. For a request with N query parameters this is one query-string parse
  + map allocation instead of N (≈5× fewer allocations on a 5-field handler).

### Fixed

- A field-level numeric / string constraint stacked on a scalar-ref field
  (`unitCents Cents @lte(1000000)`) now reaches the OpenAPI schema as
  `allOf: [{$ref}, {maximum: …}]` (or as a sibling of the nullable `anyOf` for
  an optional field). The runtime validator already enforced it; the bare
  `$ref` dropped it from the spec, so a generated client could build a request
  the server then rejects.

## [1.1.0] 2026-05-31

### Changed

- **Scalars now emit as defined Go types** (`type Email string`) instead of
  aliases, each carrying its own `Validate()` method. This lets a generic
  instance over a constrained scalar or enum (`Page<Email>`, `Page<Status>`)
  validate its elements, and deduplicates the generated validator code. Code
  that assigns a bare string/number to a scalar-typed field now needs an
  explicit conversion (`Email("…")`); the generated transport already casts.

### Fixed

- Generic instances over a constrained scalar/enum validate their elements.
  Previously the element constraints were advertised in the OpenAPI spec but
  never enforced at runtime.
- A field-level decorator stacked on a scalar-typed field (`unitCents Cents
@lte(1000000)`) is enforced instead of silently dropped.
- `@mimeTypes("a", "b")` (variadic form) generates the Content-Type allowlist
  check; only the bracketed array form was handled before.
- The served `multipart/form-data` request schema carries each text field's
  constraints (`@maxLength`, nullability) instead of a bare `string`.
- OpenAPI 3.1 nullability is emitted on refs and on map / array element types;
  numeric bounds use the 3.1 numeric `exclusiveMinimum` / `exclusiveMaximum`.
- CORS responses set `Vary: Origin` when the allowed origin is not `*`; the
  OTLP exporters accept a full endpoint URL.
- Parser accepts nested array literals; the lexer strips a trailing carriage
  return in comments; semantic analysis rejects `@uniqueItems` on
  non-comparable element types, negative bounds on unsigned fields, and
  unresolved project-mode `@errors` references.

## [1.0.0] - 2026-05-26

First stable release. craftgo turns a small `.craftgo` DSL into typed Go,
request validation, `net/http` handlers, route wiring, and an OpenAPI 3.1
spec — all from one source.

### DSL

- `type`, `scalar` (with validators that inherit to every field of that type),
  `enum` (string- and int-backed), and typed `error` categories.
- Generics (`Page<User>`), cross-package references (`import` + `pkg.Name`),
  and mixins (cross-package field composition).
- `service` blocks with `extend service` for splitting a service across files;
  per-block decorator inheritance with `@ignoreMiddleware` / `@ignoreSecurity`
  / `@ignoreTags` opt-outs.

### Validation

- Declarative validators compiled to plain Go `if` statements — no reflection,
  no runtime struct tags. `Validate()` is fail-fast.
- String: `@length`, `@minLength`, `@maxLength`, `@pattern`, `@format`.
- Numeric: `@gt`, `@gte`, `@lt`, `@lte`, `@range`, `@positive`, `@negative`,
  `@multipleOf`.
- Array: `@minItems`, `@maxItems`, `@uniqueItems`.
- File upload: `@maxSize`, `@mimeTypes`.
- Cross-field: `@requiresOneOf`, `@mutuallyExclusive`.

### Wire binding

- `@path`, `@query`, `@header`, `@cookie`, `@body`, `@form` — including
  cross-package scalar/enum casts and `@default` pre-fill.

### Codegen

- Typed structs + `Validate()`, per-method `net/http` handlers, route
  registration, gen-once service logic stubs, a `ServiceContext` container,
  and a wired `main.go`.
- OpenAPI 3.1 emitted from the same source, including `propertyNames` for map
  keys and `oneOf`/`anyOf` for cross-field constraints.
- Deterministic, gofmt-clean output; committed generated code is guarded by a
  regeneration drift check.

### Runtime (`pkg/`)

- `pkg/server`: a thin `net/http` wrapper — `*http.ServeMux`, a middleware
  `Chain`, a swappable JSON codec, health checks, CORS, and per-method timeout
  / body-size limits.
- `pkg/log`, `pkg/metrics`, `pkg/otel` for logging, metrics, and tracing.

### Tooling

- CLI: `craftgo init`, `craftgo gen`, `craftgo fmt`.
- Language server (`craftgo-lsp`) and a VS Code extension: completion, hover,
  go-to-definition, rename, live diagnostics, and formatting.

[1.0.0]: https://github.com/craftgodotdev/craftgo/releases/tag/v1.0.0
