# Changelog

All notable changes to craftgo are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and craftgo follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) — from 1.0.0 on, a
breaking change to the DSL or the generated layout bumps the major version.

## [Unreleased]

### Added

- A duplicate OpenAPI `operationId` is now reported at design time (in the
  editor), not only as a codegen error. Auto-prefixing resolves every
  same-method-name collision, so a survivor comes from an explicit
  `@operationId("...")` two methods share (or one that equals another
  method's auto id); the analyser flags each colliding method so the IDE
  points at the names to fix.
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
- `@path` accepts any wire-bindable type — `int*` / `uint*` / `float*` /
  `bool`, or a scalar / enum over one — not just strings. `/users/{id}`
  with `id int` parses the segment through the same `server.Parse*`
  helper a numeric `@query` field uses; the OpenAPI path parameter is
  typed (`type: integer`, or a `$ref` to the scalar/enum) and the client
  follows. Optional and array `@path` fields stay rejected — a matched
  route always supplies exactly one value per segment.
- A required (non-optional, no-`@default`) single-value `@query` / `@header`
  parameter now returns 400 when its key is absent, matching the
  `required: true` the OpenAPI spec already advertised — previously the
  handler silently accepted the zero value. A present-but-empty value
  (`?q=`) still passes; optional and defaulted parameters are unaffected.
- A file-header `@version("1.2.3")` decorator now sets the OpenAPI
  `info.version`, overriding `craftgo.design.yaml`'s `openapi.version` as
  its documentation always promised; it was previously parsed but ignored.

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
- `@nullable` on a `@query` / `@header` / `@cookie` / `@form` / `@path`
  parameter is now rejected at design time. A wire value is a string with
  no JSON-null form (and the pairing previously generated a non-compiling
  pointer binder); use `?` to make a parameter optional.
- `@uniqueItems` on a generic type-parameter array field (`items T[]
  @uniqueItems` in a generic decl) is rejected at design time. The
  parametric validator can't build a `map[T]` dedupe over an
  `any`-constrained element, so the combination previously emitted
  non-compiling Go while the spec advertised `uniqueItems`.
- `@minLength` / `@maxLength` / `@length` on a `bytes` field no longer emit
  OpenAPI `minLength` / `maxLength`: those count base64-encoded characters
  on a `format: byte` string, contradicting the raw-byte count the runtime
  validator enforces. The bound is left to the runtime rather than
  advertised incorrectly.
- An un-decorated request field that auto-binds to `@query` on a non-body
  verb but can't ride a query string (a struct / map / `bytes` / generic)
  is now rejected by the semantic analyser — the same combination the
  codegen already refused, but reported in the editor with a source
  position so the LSP and `craftgo gen` agree.

### Fixed

- A field-level numeric / string constraint stacked on a scalar-ref field
  (`unitCents Cents @lte(1000000)`) now reaches the OpenAPI schema as
  `allOf: [{$ref}, {maximum: …}]` (or as a sibling of the nullable `anyOf` for
  an optional field). The runtime validator already enforced it; the bare
  `$ref` dropped it from the spec, so a generated client could build a request
  the server then rejects.

A combination audit (synthetic cases generated across every decorator ×
type-shape pairing, cross-checked through validate → OpenAPI → client)
surfaced a cluster of cases where the stages disagreed. All are fixed and
pinned by a new `tests/e2e/cornercase/design/regression` fixture:

- A `@nullable` scalar field carrying a field-level constraint
  (`x Plain @nullable @lte(50)`) generated **non-compiling** `validate.go`
  — the dereferenced primitive local was treated as a pointer
  (`_sv != nil` / `*_sv`), breaking the whole types package build. It now
  compiles and enforces the bound.
- `@nullable` on a named-type field (scalar / enum / struct / generic)
  without `?` dropped the OpenAPI null union, so a generated client typed
  the field as required and non-null while the server serialised `null`.
  It now emits `anyOf: [{$ref}, {type: null}]` and stays in `required`.
- `@default` and `@deprecated` on a non-optional ref-typed field were
  dropped from OpenAPI; they now ride a wrapper schema.
- Single-argument `@length(N)` (the documented exact-length form) was
  dropped at runtime and emitted only `minLength` in OpenAPI (so the spec
  said “≥ N” while the contract was “exactly N”). It now lowers to
  `min == max == N` in both the validator and the spec.
- `@minLength` / `@maxLength` on a `bytes` scalar or field were advertised
  in OpenAPI but never enforced; `validate.go` now checks
  `len([]byte(…))`.
- The request-body schema (`<Method>ReqBody`) dropped embedded mixin
  fields, dangled a `$ref` to a bare generic type parameter (breaking
  client generation), and dropped type-level `@requiresOneOf` /
  `@mutuallyExclusive`. A pure-body request now reuses the full type
  schema — mixins flattened via `allOf`, a generic instance `$ref`-ing its
  monomorphised component (`PageOfEmail`), and the cross-field fragments
  carried.
- Map-key enum `propertyNames` listed the DSL value names (`Red`, `Low`)
  instead of the wire values; it now emits what the server marshals as the
  key (`"red"`, and `"1"` for an int-backed enum, since JSON object keys
  are strings).
- `@default` on a string-backed `@query` / `@header` / `@cookie` field was
  clobbered to the empty string on an absent request; the bind is now
  presence-guarded (`if _v := _q.Get("sort"); _v != "" { … }`) so the
  pre-filled default survives, matching the parsed-primitive path.
- Error-response schemas dropped field-level constraints, `@default`,
  `@deprecated`, and nullability; error fields now carry the same metadata
  as request / response entity fields.

A second, deeper audit re-ran after the fixes above and found a further
cluster, now fixed and pinned by the `regression` fixture:

- An array of maps whose value carries a validator (`map<string, Tag>[]`)
  generated **non-compiling** `validate.go` — the map walk ran on the
  slice and called `Validate()` on a whole map. The array dimensions are
  now peeled before the map is ranged.
- A `@nullable` string carrying `@format` (`a string @nullable
  @format(email)`) dereferenced the pointer without a nil-guard, panicking
  the validator on `{"a": null}`. The guard now keys on the field's
  pointer-ness, not just the `?` suffix.
- Integer bounds beyond 2^53 (`@gte(9007199254740993)`,
  `@gte(math.MaxInt64)`) lost precision in OpenAPI — the float64 the spec
  carried rounded, so the advertised bound disagreed with the exact int64
  the validator enforced (at the extreme an unsatisfiable spec). Large
  integer bounds — and `@multipleOf` divisors — now emit as exact
  `json.Number`s.
- Fields a request **inherits through a mixin** were resolved for the body
  schema and the validator but skipped by the wire-binding, OpenAPI
  parameter, default-prefill, and body-decode passes. A mixin's `@header` /
  `@query` field is now bound and documented; a mixed request carries the
  mixin's body fields in its `<Method>ReqBody`; and a request whose body
  comes only from a mixin decodes that body instead of 400-ing every
  request.

A third audit pass closed the remaining cross-stage gaps:

- A map whose value is itself a map (`map<K, map<K2, V>>`, or a
  map-of-array-of-map) skipped the inner values' `Validate()` while
  OpenAPI advertised their constraints; the validator now walks every
  nested level.
- `@multipleOf` written with a whole-valued float literal
  (`@multipleOf(5.0)`) on an integer field was advertised in OpenAPI but
  dropped by the validator; it is now enforced.
- An int-enum that defines `0` as a real member (`Inactive = 0`) had its
  required-field check reject that valid member (the check used `0` as the
  "absent" sentinel); the presence check is skipped for such enums.
- An enum `@default` referenced by member name (`@default(active)`)
  emitted the DSL spelling as the OpenAPI `default` instead of the
  member's wire value (`= "ACTIVE"` / `= 1`); it now resolves to the wire
  value the runtime and client use.
- A response type embedding a mixin alongside a `@header` / `@cookie` field
  dropped the mixin's body fields from the per-operation response schema;
  they are now included.
- A type-level `@requiresOneOf` / `@mutuallyExclusive` may now reference a
  mixin-promoted field — the validator resolves it through field promotion
  instead of emitting a no-op check.
- Embedding an instantiated generic as a mixin (`type Host { Page<Item> }`)
  generated a struct embedding the bare, un-instantiable `Page` (a Go
  compile error) and a dangling OpenAPI `$ref`. The embed now monomorphises
  to `Page[Item]` — which Go accepts and promotes the fields of — and the
  schema `allOf`-refs the `PageOfItem` component.
- A `@sensitive` field with no explicit binding auto-promoted to `@query`
  on a non-body verb — reading a server-only value from the URL and adding
  a required parameter the OpenAPI never documents. The binder now skips
  `@sensitive` fields entirely, matching the `json:"-"` / schema exclusion.
- `@multipleOf` on a float scalar, `@pattern` / `@format` on a `bytes`
  field or scalar, and any value constraint (`@gte`, `@maxLength`, …) on a
  generic type-parameter field are rejected at design time: each was
  advertised in OpenAPI but unenforceable by the generated validator.
- A field whose Go field-name equals an embedded mixin's type name
  (`type Host { Pagination  pagination int }` — the embed and the field
  both become the Go identifier `Pagination`) generated a struct that
  declared the same identifier twice and failed to compile. The
  collision is now reported at design time, alongside the existing
  same-name field-vs-mixin conflict.

A fourth all-flows audit (every decorator × type-shape × binding
combination, re-checked through validate → OpenAPI → transport, each
finding adversarially verified) surfaced 20 more cross-stage gaps. All
are fixed and pinned by the `tests/e2e/cornercase/design/regression`
fixture (Rg5-prefixed).

- A `@nullable` field with no explicit binding on a body-less verb
  (GET / DELETE) auto-bound to `@query` and emitted a non-compiling
  binder (a wire string written into the pointer `@nullable` lowers to).
  The implicit auto-`@query` path is now rejected at design time, like
  the explicit `@nullable @query` pairing already was.
- `@uniqueItems` on an array of a generic instance whose argument makes
  it non-comparable (`Pair<bytes>[]`) was accepted and then emitted a
  `map[Pair[[]byte]]` dedupe that does not compile. The comparability
  check now substitutes the generic argument before judging the element.
- A `scalar` declaration consumed the leading decorators of the
  *following* declaration (it read decorators across the newline), so a
  `@requiresOneOf` / `@deprecated` on a type declared right after a
  scalar was mis-attributed to the scalar and rejected. A scalar now
  takes only the decorators on its own line.
- An invalid `@pattern` regex (`@pattern("(unclosed")`) reached
  `regexp.MustCompile` in the generated validator and panicked at package
  init. The regex is now compiled at design time and a bad one is
  rejected with a position, the same guard `@format` already had.
- `@requiresOneOf` / `@mutuallyExclusive` referencing a non-optional,
  non-`@nullable` field is now rejected. OpenAPI expresses the group with
  key-presence (`required` / `not.required`) while the runtime used
  zero-value emptiness, so the two disagreed on an empty-but-present
  value; requiring pointer-backed fields makes "present" mean the same on
  both sides.
- An error-body field carrying `@nullable` was rendered as a non-pointer
  Go field while the validator nil-guarded it — non-compiling Go — and
  the OpenAPI advertised `type: [T, "null"]` the server could never
  marshal. Error bodies now honour `@nullable` (pointer field + nil
  guard), matching entity types.
- A `@header` / `@cookie` field an error inherits through a mixin was
  dropped: no `WriteResponseHeaders`, absent from the body, and missing
  from the OpenAPI response headers. The error header/cookie walk now
  expands mixins, so the promoted field is written and documented.
- A wire-bound field (`@query` / `@path` / `@form`, including one
  promoted through a mixin) leaked into a component / response body
  schema as a required property while it is `json:"-"`. The body schema
  now excludes the full non-body-bound set, not just `@header` / `@cookie`.
- An array-of-enum `@default([Card, Bank])` was dropped from OpenAPI
  (the default resolver had no enum-member case for array elements) while
  the transport still pre-filled it. The default now lands its wire
  values in the spec.
- A non-string scalar map key carrying a constraint (`map<UserID, …>`,
  `UserID int @gte(1)`) was enforced by the server (`key.Validate()`) but
  advertised no `propertyNames`. The key constraint now rides
  `propertyNames`, mirroring the scalar's own schema.
- A wire parameter with `@default` was advertised `required: true` while
  the server treats it as optional (the default fills absence). A
  defaulted field — wire param or body — is no longer marked required, so
  the spec stops contradicting the `default` it carries.
- A required `@cookie` was advertised `required: true` but the transport
  never enforced presence. A required cookie now returns 400 when absent,
  matching `@query` / `@header` (a present-but-empty value still passes).
- A user-declared `code` / `message` error field — which is marshalled on
  the wire and validated — was excluded from the OpenAPI error schema
  along with its constraints. It is now emitted like any other property.
- A constraint declared on the element of a composite generic argument
  (`Page<map<string, Item>>`, `Page<Item[]>`) was advertised in OpenAPI
  but never enforced: the parametric `any(x).(Validate)` probe can't reach
  a map / slice element. A generic type-parameter validator now falls
  back to a reflection walk (`validateValue`) that validates each leaf —
  the only reflection in generated code, scoped to generic type-params
  and only reached when the direct probe finds no `Validate()`.
- On a nilable Go type (`bytes` → `[]byte`, slices, maps), `@nullable` /
  `?` added no pointer, so a `@minLength` / `@minItems` check ran on the
  nil value and rejected an explicit `null` the OpenAPI null-union
  advertises as valid. These constraints are now nil-guarded for
  optional / nullable nilable fields, matching the pointer-typed case.

A fifth full-syntax verification pass (every stage incl. client-spec
consumability, LSP↔build parity, and `craftgo fmt`) closed a further
cluster, pinned by the `regression` fixture (Rg6-prefixed):

- A map keyed by a non-comparable type — a generic type-parameter
  (`map<K, V>` → `map[K any]`) or a struct / generic containing a slice /
  map / `bytes` — was accepted by gen but emitted Go that does not compile
  (`invalid map key type`). The key's comparability is now checked at
  design time, like `@uniqueItems` element comparability.
- A route template repeating a path variable (`/items/{id}/x/{id}`) and
  two fields binding to the same wire name on one source (`a @query("x")
  b @query("x")`) are rejected: the first panics net/http's ServeMux at
  registration, the second emits a duplicate OpenAPI parameter.
- `@requiresOneOf` / `@mutuallyExclusive` over a wire-bound (`@query` /
  `@header` / `@cookie`) or `@default` member is rejected — the wire field
  isn't in the JSON body so a body-level group can't reference it, and a
  defaulted member is always present so the group is a no-op the spec
  contradicts. The OpenAPI fragment now also requires each member be
  present **and non-null** (`required` + `properties: {x: {not: {type:
  null}}}`), matching the runtime's `!= nil` check on an explicit JSON
  `null` body.
- Stacking same-family bound decorators (`@gte(10) @lte(90) @range(0,
  100)`, `@length(5) @minLength(3) @maxLength(10)`) advertised the last
  writer's bound in OpenAPI while the validator enforces the tightest. The
  spec now intersects them (tightest wins), matching the runtime.
- A `@header` / `@cookie` field promoted into a **response** through a
  mixin was documented in OpenAPI but never written by the handler (the
  write pass walked the body directly instead of the flattened field list,
  unlike the doc pass). It is now written. The same flattening gap left a
  `file @form` / text `@form` field inherited via a mixin uncollected; it
  now binds and rides the multipart schema.
- A required array `@query` / `@header` parameter was advertised
  `required: true` but never presence-checked; it now returns 400 on an
  absent key, matching the single-value params.
- A numeric scalar map key (`map<UserID, …>`, `UserID int @gte(1)`) no
  longer emits `propertyNames: {type: integer}` — a conformant 3.1
  validator rejects it because JSON object keys are strings. A numeric key
  bound has no consumable spec form, so it is left to the runtime; a
  string scalar key still carries its length / pattern / format.
- `craftgo fmt` no longer silently drops or corrupts comments: a trailing
  `//` on a non-last decorator in a multi-line chain (`@minLength(1) //
  note` above `@maxLength(5)`) folded the following decorators into the
  comment — **deleting a real constraint** — and now lands the comment at
  the end of the collapsed line with every decorator intact; an
  end-of-file comment block (after the last declaration) and a
  blank-line-isolated separator comment inside a type body were both
  dropped, and are now preserved. All three round-trip idempotently.

A sixth pass, run after consolidating the per-stage field metadata behind
a single resolver, swept the cross-field, map-key, `@multipleOf`,
error-body, and generic-instantiation paths once more and closed the
remaining divergences:

- A generic type **argument** can no longer be optional (`Page<Item?>`).
  The trailing `?` has no single, well-defined position once the argument
  is substituted into the decl's body: substituted into `items T[]` the Go
  side lowers it to a nullable element (`[]*Item`) while the OpenAPI array
  items stay a non-null `$ref` — the two stages disagree, and the AST's
  single optionality flag can't distinguish "array of nullable element"
  from "nullable array". Declare the nullability on a concrete field of the
  generic instead (`type Box<T> { item T? }`, used as `Box<Item>`), where
  it lowers to a clean pointer on both sides.

- An error body that embeds a mixin now validates the mixin's promoted
  fields. The body struct embeds the mixin and the OpenAPI `allOf`
  advertises its constraints, but `<Error>Body.Validate()` skipped them —
  it walked the error's direct fields only. It now dispatches to the
  mixin's `Validate()`, the same as any other type that embeds a mixin.
- `@multipleOf` with a fractional divisor (`@multipleOf(2.5)`) on an
  integer field or scalar is rejected. Go's modulus is integer-only, so
  the validator can't enforce a fractional divisor while the OpenAPI
  advertises it — the same rule already applied to a whole-valued float
  literal is now extended to a genuinely fractional one.
- A `@requiresOneOf` / `@mutuallyExclusive` member that is `@sensitive`
  (server-only, `json:"-"`, excluded from the schema) or whose Go type is
  nilable-but-not-a-pointer (a slice / map, or a raw `bytes` / `any`) is
  rejected. A `@sensitive` member names a property the public schema never
  carries; the nilable-non-pointer members have no clean `!= nil` presence
  check — a slice / map is checked by emptiness (`len(...) > 0`, so an empty
  `[]` / `{}` reads as absent) and a `bytes` / `any` member is always treated
  as present — so both diverge from the group's OpenAPI present-and-non-null.
  (A pointer-backed field — string, number, bool, struct, enum, or a scalar —
  is the unambiguous case the group needs.)
- A map keyed by a type that compiles but `encoding/json` can't marshal
  as an object key — `bool`, `float*`, or a scalar over them — is now
  rejected at design time alongside the non-comparable keys. `json.Marshal`
  fails at runtime on such a key (`unsupported type`) even though the Go
  map itself is valid, so only string / integer-kind keys (and scalars /
  enums over them) are accepted.

A full-syntax matrix recheck (every construct × stage, generated and
diffed across validate / OpenAPI / transport) found three latent edges,
now fixed:

- A required `string`-enum field whose enum defines `""` as a real member
  (`enum Status { Unknown = "" ... }`) no longer emits a `== ""` presence
  check that rejected the legal `Unknown` member (`""` is the Go zero
  value, so the check fired before the value-set switch). The check is
  dropped for such an enum, mirroring the existing guard for an int-enum
  with a `0`-valued member.
- An integer literal beyond the signed 64-bit range (e.g. a `uint64`
  `@lte(18446744073709551615)`) is now rejected at parse time instead of
  being silently clamped to `9223372036854775807` in both the validator
  and the spec — the bound was corrupted identically on both sides, so it
  passed a naive cross-stage diff while diverging from the design. (Full
  `uint64` bounds above the int64 max remain a future addition.)
- Two pathless methods of the same verb in one service
  (`get Ping {}` + `get Health {}`) are no longer flagged as a duplicate
  route. The same-service collision check now keys on the resolved route
  (with the kebab method-name fallback applied, `/ping` vs `/health`),
  matching the cross-service check, instead of the empty path both
  pathless methods shared.

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
