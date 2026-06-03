# Changelog

All notable changes to craftgo are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and craftgo follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) — from 1.0.0 on, a
breaking change to the DSL or the generated layout bumps the major version.

## [Unreleased]

### Fixed

- A **bare scalar or enum request type** (`request Token` where `Token` is a
  scalar/enum) is now rejected — a fieldless type has nothing to bind or decode
  as a body, so the payload was silently dropped (and a constraint-free scalar
  produced non-compiling Go). Wrap the value in a `type`.
- An **integral-float numeric bound** (`@gte(300.0)`) is now capacity-checked
  like its integer form, and `@default` on a `file` field is rejected — both
  previously produced non-compiling Go.
- An **auto-bound path field with a non-bindable type** (struct/map/array/
  generic) is rejected, matching the explicit `@path` form (it was silently
  dropped and emitted an invalid non-scalar path parameter).
- Repeated `@errors` decorators are no longer false-rejected as a duplicate —
  `@errors` aggregates like `@tags`/`@security`/`@middlewares`, so the
  extend-service inheritance idiom works. The decl-collision check now uses the
  same smart `Err`/`Error` suffix codegen emits, so an error named `…Err`/
  `…Error` is no longer falsely flagged against a coincidentally-named type.
- `@example(<enum-member>)` now resolves to the member's wire value in the spec
  (it was silently dropped, unlike `@default`); a required `any[]` field no
  longer gets a spurious nil presence check; and `@status(205)` on a
  body-returning method is rejected (joining 204/304/1xx).
- **Scalar-declaration numeric bounds** are now validated like field bounds: a
  bound that overflows the scalar's primitive (`scalar X uint8 @lte(300)`) or
  an always-false unsigned bound (`scalar X uint @lt(0)`) is rejected at gen
  time instead of generating non-compiling / reject-everything Go. A negative
  single-arg `@length(-1)` is likewise rejected.
- **Array-shortcut decorator forms** (`@errors([A, B])`, `@tags([A, B])`,
  `@middlewares([A, B])`) are now honoured by codegen — they were accepted by
  the analyzer but silently contributed nothing, dropping error responses,
  tags, and the entire middleware chain. (`@security([...])` already worked.)
- **OpenAPI exclusive bounds intersect** instead of overwriting: stacking
  `@gt(5) @positive` (or `@lt(-5) @negative`) now advertises the tightest bound
  (`exclusiveMinimum: 5`) the order-invariant validator enforces, rather than
  the looser last-writer value.
- A request field diverted to `@query`/`@header`/`@cookie`/`@body` no longer
  satisfies the path-coverage check for a same-named `{segment}` — the segment
  is reported missing instead of producing an OpenAPI spec with no `in: path`
  parameter and a handler that never reads the value.
- Explicit `@path @default` is rejected (a matched route always supplies the
  segment), matching the auto-`@path` form, and a no-content success status
  (`@status(204)`/`304`/`1xx`) on a body-returning method is rejected.
- The multipart handler no longer emits an unused (or duplicate) `types`
  import when the request type lives in another package — it now carries the
  same `NeedsTypes` guard the JSON transport and service templates use.
- A generic type parameter in a **map value** position (`map<K, T>`) is now
  validated (its values walked via the reflective fallback) instead of being
  silently dropped from `Validate()` while OpenAPI advertised the constraints.
- A **field-level numeric/string constraint on an enum field** (`p Priority
  @lte(5)`) is now enforced at runtime (the enum is cast to its underlying
  int/string before the check) instead of being advertised in OpenAPI but
  dropped from the validator.
- The `openapi.securitySchemes` manifest block is now honoured: a declared
  `apiKey` / `oauth2` / `openIdConnect` scheme is emitted with its configured
  type/scheme/in/name instead of every `@security` scheme being hardcoded as
  `http` bearer-JWT.
- A bodyless or header-only **error response** now advertises the
  `{code, message}` envelope the runtime actually writes, instead of an empty
  `object` schema.
- Duplicate `@security` requirements (a method repeating its service's scheme)
  are deduplicated, and the phantom empty `default` response is no longer
  emitted on every operation.

- A **scalar over `bytes`** (`scalar Blob bytes`) marked optional (`?`) or
  `@nullable` now renders as the bare named slice (`Blob`) rather than a
  redundant pointer (`*Blob`) — the named slice already holds nil, exactly like
  a raw `bytes` field. The pointer decision is resolved once through the scalar
  table so the struct field, the validator nil-guards, and the field-level
  checks agree; a null / absent value still skips the scalar's own `Validate()`.
  A scalar over a nilable primitive can no longer be a cross-field-group member
  (its presence is emptiness, not a clean `!= nil`), matching raw `bytes` / `any`.
- A field-level `@doc` / `@example` on a field whose type is a **named ref**
  (a component `$ref`) is now carried onto an `allOf` / `anyOf` wrapper instead
  of being silently dropped — a bare `$ref` cannot hold sibling keywords
  portably, so the metadata rode nowhere before.
- A method's `@errors(...)` reference now **follows the OpenAPI merge's rename**
  when two packages declare an error of the same name. The merge renames the
  colliding schemas (`Dup` → `ADup` / `BDup`); previously the per-operation
  response lookup used the bare decorator name, missed the renamed schema, and
  silently dropped the error from the spec's responses.
- **Cross-package mixin field promotion** is now resolved consistently across
  the transport stage, fixing a cluster of silent / non-compiling failures when
  a request embeds a mixin from another package. A field promoted across the
  package boundary now has its type re-qualified to its home package, and the
  body-decode decision and the handler-import collector thread the project
  resolver. Concretely:
  - a request whose only body fields come from a cross-package mixin now emits
    the JSON body decode (previously skipped — the body was never read and
    required fields failed validation against zero values);
  - a cross-package scalar / enum bound to `@query` / `@path` / `@header` /
    `@cookie` through a mixin now binds (previously aborted gen with
    "type X cannot bind", though the message listed scalars as allowed);
  - the foreign package's import is kept when a promoted field's cast
    references it (previously dropped → `undefined: pkg`, non-compiling);
  - a cross-package scalar / enum `@default` promoted through a mixin now casts
    the pre-fill literal to the qualified type (`shared.Size(20)`,
    `shared.ColorGreen`) instead of emitting an uncast `*int` / dropping the
    default.
- A generic type embedded as a **mixin** in a generic host (`Box<T>` embedding
  `Tree<T>`, instantiated as `Box<Leaf>`) now substitutes the host's type
  parameters into the mixin's generic arguments, so OpenAPI registers
  `TreeOfLeaf` rather than a phantom `TreeOfT` whose element `$ref` dangled. The
  project-merge path likewise rewrites a cross-package mixin's generic args, so
  `lib.Page<lib.Owner>` no longer emits a dangling `$ref: lib.Owner`.
- A cross-package type referenced through a codegen path whose
  **import-collection walk had drifted from its emit walk** generated
  non-compiling Go (`undefined: <pkg>`) when that path was the *only* reference
  to the foreign package. Three instances are fixed: a type appearing solely in
  a mixin's generic argument (`type R { Box<mod.Owner> }` → `Box[mod.Owner]`),
  a cross-package scalar / enum bound with `@form`, and a cross-package element
  under `@uniqueItems` (the dedupe `map[shared.Name]struct{}`). Each import is
  now collected by walking the same structure the emit path renders.
- An array `@query` / `@header` parameter with a `@default` both dropped and
  corrupted the default: a string array was overwritten with `nil` when the key
  was absent (destroying the default), and a parsed array appended the request
  onto the prefilled default (`[7,8]` + `?ids=4&ids=5` → `[7,8,4,5]`). Array
  wire-binding now preserves the default when the key is absent and replaces
  (not appends) when present — `server.BindValues` resets before binding and the
  string paths are presence-guarded.
- An **enum-array** `@query` / `@header` parameter with a `@default` corrupted
  the default: the prefill resolved the member array (`@default([Red, Blue])` →
  `[]Color{ColorRed, ColorBlue}`) but the binder's has-default test could not
  (it routed the array literal through a converter with no enum-member case and
  returned "no default"), so the field used the bare appending shape — `?colors=
  Green` yielded `[Red Green Blue]` instead of `[Green]`, every appended value
  passing validation, so the corruption was silent. The binder now consults the
  same default-resolution oracle the prefill emits from, so the two agree and the
  present-param path clears the slice before appending.
- A **multi-dimensional array** (`int[][]`, `shared.Tag[][]`, …) on a wire-string
  source is now rejected at design time. A wire source encodes an array as
  repeated single values (`?x=1&x=2`), which has no nested form, so codegen
  emitted a one-dimensional binder against an N-D field that did not compile
  (`server.BindValues(… &req.Grid, server.ParseSigned[int])` with `req.Grid` of
  type `[][]int`), while OpenAPI rendered the correct nested `items`. The depth
  guard lives in the shared `isWireBindingType` predicate (and its cross-package
  twin), so every path agrees: the explicit `int[][] @query` / `@header` /
  `@form` form **and** the implicit auto-`@query` promotion of an undecorated
  field on a body-less verb (`get`/`delete`) — the latter previously slipped past
  the depth check and shipped non-compiling Go. The check is structural
  (independent of the element type), so cross-package element types are caught
  too. Single-level arrays and multi-dim arrays in the JSON body are unaffected.
- A cross-field group (`@requiresOneOf` / `@mutuallyExclusive`) referencing a
  field promoted by a **cross-package mixin** is no longer falsely rejected as
  "not a field of this type" — the per-package pass defers (it can't expand the
  foreign mixin) and the field set is resolved project-wide. The deferral is now
  backed by a **project-level re-check**: a member that no field provides —
  including a typo sitting alongside a legitimately-promoted one — is rejected at
  design time instead of slipping through to codegen, which substituted a literal
  `false` and emitted a validator that silently never fired (the whole group,
  De-Morgan'd, went dead). The re-check resolves nested cross-package mixins too.
  As a backstop, codegen now emits an undefined identifier (a loud `go build`
  failure naming the member) rather than `false` for any group member it still
  can't resolve, so a future resolver gap can't ship as a no-op validator. The
  re-check also re-applies the per-field quality rules to a cross-package-promoted
  member (must be optional / `@nullable`, not `@sensitive`, not wire-bound, not
  `@default`) — extracted into one shared helper both passes call — so a plain or
  otherwise-ineligible promoted member is rejected exactly as a local one is,
  rather than only its name being checked.
- A numeric **`@default` outside the field primitive's capacity** — a negative on
  an unsigned type (`uint @default(-5)`) or an out-of-range magnitude on a narrow
  int (`int8 @default(200)`) — is now rejected at design time. Codegen otherwise
  emitted a pre-fill cast (`uint(-5)` / `int8(200)`) that failed `go build` with
  `constant overflows`, and OpenAPI advertised the out-of-range default. The
  capacity check reuses the same range logic the numeric-bound guard uses.
- A **`@default` on a `bytes` field** is now rejected. A bytes value has no
  unambiguous literal form — the Go side needs `[]byte(...)` while OpenAPI's
  `format: byte` default is base64, and the only literal kind the gate accepted
  (string) compiled straight into the `[]byte` slot as a bare quoted string,
  which never built. `bytes[]` is rejected identically.
- A **multi-dimensional array `@default`** (`int[][]? @default([[1, 2], [3, 4]])`,
  `Color[][]? @default(...)`) is now rejected at design time. `@default` targets a
  primitive, scalar, enum, or a single-level array of those — a nested-array
  default has no real use and an exotic nested-literal form. The check is
  structural (array depth), so it fires for cross-package element types too.
  Single-level array defaults are unaffected.
- A required **`any @sensitive`** field made its endpoint reject every request
  with `400 … required`. A `@sensitive` field is `json:"-"` (dropped before
  decode), yet it still received the runtime presence check a required field
  gets — an unsatisfiable gate, since the client can never send the value. The
  presence check now excludes `@sensitive` fields, matching their exclusion from
  the wire body.
- A `@query` / `@header` / `@cookie` / `@form` / `@path` **binding rejection over
  a `map` field** rendered the offending type as a bare `?` (`got ?`); the
  diagnostic now renders the map type (`got map<string, int>`). The rejection
  itself was already correct.
- An **empty `@path("")` wire-name argument** no longer false-rejects the
  path-param check with a nonsensical `field ""` message — it falls back to the
  field name, mirroring the explicit-name fallback every other binding decorator
  already applies.
- A **cross-package qualified request type** (`request shared.Holder`) silently
  dropped every field of its bare nested mixins from the transport binder: a
  `@query` member never bound and a body member never decoded, while the
  validator (and the semantic path-param check, which derived the package
  correctly) still enforced them — so a conformant request failed validation
  against zero values. The request-field resolver now derives the flatten prefix
  from the qualified request name, so bare mixins resolve in the request type's
  home package, matching the semantic side.
- `@uniqueItems` over a **cross-package element that is only transitively
  non-comparable** — reached through a bare member of the foreign struct that
  itself holds a slice / map — was accepted, then emitted a non-compiling
  `map[pkg.T]struct{}` dedup. The comparability walk now threads the foreign
  struct's home package into its recursion, so a bare nested member resolves in
  that package instead of being conservatively accepted as "unknown".
- A whole-number **`@default` on an optional `float64?`** (`@default(1.0)`)
  rendered as `1`, which Go infers as `int`, so the pointer pre-fill `__d := 1`
  was a `*int` that wouldn't assign to the field's `*float64`. A float literal
  now always renders with its decimal point (`1.0`); a fractional default
  (`2.5`) is unchanged and still needs no cast.
- A **`@default` on a field promoted from a nested mixin of a qualified request
  type** (`request shared.Holder`) was silently dropped from the handler
  pre-fill: the binder bound the field and OpenAPI advertised the default, but a
  client omitting it got the zero value instead. The default-collection pass now
  threads the qualified request's home-package prefix (matching the binder), so
  the bare nested mixin's defaulted fields resolve and seed.
- A **qualified generic request type with a local type-arg**
  (`request shared.WrapBag<Item>`, `Item` local) generated a handler that
  referenced `types.Item` but dropped the canonical `types` import → non-compiling
  `undefined: types`. The transport codegen now keeps the import when the rendered
  request type still carries a `types.` reference, mirroring the scaffold-service
  guard.
- `@uniqueItems` over a **cross-package generic instance** whose type-arg makes
  it non-comparable (`shared.Box<shared.User>[]`, `User` holding a slice) was
  accepted, then emitted a non-compiling `map[shared.Box[...]]struct{}`. The
  cross-package comparability walk now substitutes the type-args into the generic
  decl's fields — mirroring the same-package twin — so a `T` field is judged
  against its concrete argument; comparable instances (`Box<string>`) still pass.
- A required **cross-package enum body field** got no field-named presence check
  (only the enum's own value-set rejection), so an omitted field reported
  `"Sev: invalid Sev value"` instead of `"field: required"`, and the check ran in
  a different order than a local enum's. The required-check now resolves a
  qualified enum through the project resolver, matching the local-enum path. (The
  accept/reject decision was already correct — this is the diagnostic + ordering.)
- The project binding-type pass **double-visited every request body** (request
  types are already in the package's type set), emitting byte-identical duplicate
  diagnostics — N+1× for a type reused across N methods. The redundant second
  pass is removed; each binding error now reports once.
- The per-request / per-response codegen passes (field resolver, default
  pre-fill, **import collector**, **response header/cookie writers**) each
  resolved the method's type via the bare-keyed local `pkg.Types` and bailed on
  the qualified cross-package form, so a qualified type was silently dropped by
  one stage while a sibling emitted it. They now share one `lookupMethodType`
  helper (local then project resolver, with the home-package flatten prefix),
  fixing two more leaks:
  - a **qualified cross-package response** (`response shared.Resp`) now writes
    its `@header` / `@cookie` fields — previously the writers were dropped (the
    fields are `json:"-"`, so the values went to neither header/cookie nor body)
    while OpenAPI still advertised them;
  - a **qualified request whose field reaches a third package** (`request
    b.Holder`, `b.Holder.cid` typed `c.CID`) now imports that third package for
    the cast / `@default` pre-fill — previously the import was dropped →
    `undefined: c`, non-compiling.
- A **non-marshalable map key nested inside a generic type-argument**
  (`Box<map<StructKey, V>>`, `lib.Box<map<lib.FloatKey, V>>`) was accepted, then
  emitted either non-compiling Go (`invalid map key type` for a struct/slice
  key) or a runtime `json.Marshal` panic (bool/float/bytes key). The map-key
  comparability checks (per-package and project) now descend into generic
  type-arguments, mirroring the `@uniqueItems` walk; valid keys still pass.
- A **cross-package generic request whose type-arg lives in a DSL package
  literally named `types`** (`request types.Wrap<types.Thing>`) emitted the
  canonical local-types import alongside the cross-package one → `types
  redeclared`. The canonical import is now dropped when the request package's
  own alias is `types`.
- `@uniqueItems` over a generic instance whose non-comparability arrives
  through a **generic mixin of the type-parameter** (`Box<bytes>[]`, where
  `Box<T>` embeds `Inner<T>` and `Inner` holds a `T`) was accepted, then emitted
  a non-compiling `map[Box[[]byte]]struct{}`. The comparability walk now
  substitutes the outer type-args into a mixin ref before descending — the
  mixin branch was the one spot the Field branch's substitution didn't mirror.
- A **cross-package mixin embedded in an error body** dropped its package
  import from the generated `errors.go` → `undefined: <pkg>`. The error
  emitter's import walk skipped mixin members that the type emitter's walk
  already covered; both now share one `collectBodyImports` helper that walks
  fields and mixins, so the two can't drift again.
- `@uniqueItems` over a struct holding **two different instantiations of one
  generic** (`Holder { s Wrap<string>; b Wrap<bytes> }`) accepted a
  non-comparable element when the comparable instantiation was checked first:
  the comparability cycle-guard was keyed by the bare decl name, so the first
  `Wrap<…>` poisoned the guard for the second and a non-compiling
  `map[Holder]struct{}` leaked. The guard is now keyed by the instantiated
  identity (name + type-args), so each instantiation is judged independently
  while a true cycle still breaks.
- A **generic mixin whose type-argument is a stdlib-backed builtin**
  (`Box<file>` → embedded `Box[*multipart.FileHeader]`) dropped its
  `mime/multipart` import from the generated `types.go` / `errors.go` →
  `undefined: multipart`. The shared body import walk now routes mixin args
  through the same `collectFieldImports` the field branch uses.
- A **cross-package scalar / enum field carrying `@nullable`** that auto-binds
  to `@query` on a body-less verb (GET/DELETE) generated a non-pointer
  assignment into a `*pkg.T` slot → non-compiling. The local equivalent was
  already rejected; the rejection is structural, so it now runs before the
  qualified-ref deferral and fires for cross-package types too.
- A cluster of per-field semantic guards resolved a field's primitive /
  category through the LOCAL symbol table and so silently no-op'd on a
  **qualified cross-package ref**; they now run at the project level against the
  resolved type. This catches, for an imported scalar / enum / type in a field:
  a decorator on the wrong category (`@minLength` on an `int` scalar, `@gt` on a
  `string` scalar), `@multipleOf` on a float scalar (Go's modulus is
  integer-only), `@uniqueItems` over a non-comparable element (a `bytes` scalar
  or a struct containing a slice — previously a non-compiling `map[T]struct{}`),
  and a `map` whose key is a bool / float / struct / bytes scalar (not a usable
  JSON object key). Each fires only on the qualified form, matching the
  bare/local behaviour without double-reporting.
- A **redundant self-qualification** (`design.Email` inside the `design`
  package) is now rejected with the bare-name fix, instead of emitting a
  self-import the package can't satisfy (`undefined: design`) and dropping the
  field's validator.
- Two mixins that **lower to the same Go embedded-field name** — a local `Leaf`
  and an imported `shared.Leaf`, or `shared.Leaf` and `other.Leaf` — are now
  rejected together with the exact-duplicate case; all would redeclare the field
  `Leaf` in the generated struct. (The duplicate-embed check now keys on the
  unqualified leaf name, not the dotted reference.)
- A **mixin embedded more than once** in one type body is now rejected at design
  time — the generated Go struct would declare the embedded type twice and fail
  to compile (`X redeclared`).
- A contradictory numeric bound on a **cross-package scalar** is now caught at
  design time: `@negative` / `@lt(0)` on a `shared.Count` over a `uint*`
  (every value rejected), an out-of-capacity literal like `@lte(-1)` over a
  `uint32` (`-1 overflows uint32`, non-compiling), and a fractional bound over a
  cross-package integer scalar. The per-package pass resolved the primitive
  through its local scalar table and missed the imported scalar; the project
  resolver now re-checks qualified-scalar bounds.
- **`@lt(0)` on an unsigned field** is now rejected at design time (the
  desugared spelling of the already-rejected `@negative`): no `uint*` value can
  be `< 0`, so every request would be rejected. The capacity guard missed it
  because `0` is itself an in-range value.
- A field reached through a mixin **nested inside a cross-package mixin**
  (`Req { shared.Outer }`, where `shared.Outer` embeds a sibling-package
  `shared.Inner`) was silently dropped from the generated handler — it
  never bound, defaulted, or appeared in the wire binder, while OpenAPI
  (built from a flattened merged package) still advertised it, so a client
  sent a value the server ignored. The codegen flattener (and the
  project-level `@path` check) now resolve a bare mixin nested in a foreign
  mixin against that foreign package (`shared.Inner`), not the current one.
- A `@default` on an optional field of a narrow numeric width (`int8` /
  `int16` / `int32` / `int64` / `uint*` / `float32`) generated
  **non-compiling** Go. The pointer pre-fill emitted `__d := 1`, which
  infers Go `int` (or `float64` for a float literal), so `&__d` was a
  `*int` that wouldn't assign to the field's `*int32`. The literal is now
  cast to the field's primitive (`__d := int32(1)`), matching what a
  scalar default already did. `int` / `float64` defaults are unchanged
  (the literal already matches); plain `int` and `string` / `bool` were
  never affected. Covers both the body pre-fill and the `@query` /
  `@header` / `@cookie` default path.
- A `@path` parameter supplied by a mixin embedded from another package
  (`type Req { shared.IdHolder }`, where `shared.IdHolder` declares the
  `@path` field) is no longer falsely reported as `path/param-missing`.
  The per-package analyser can't expand a sibling-package mixin, so the
  segment-to-field check now runs at the project level with cross-package
  mixin resolution — the same flattening the codegen binder already does,
  so the design-time check and the generated handler agree. A genuinely
  missing segment or an orphaned `@path` field is still reported, now
  across the package boundary.

## [1.2.0] - 2026-06-02

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

- **`@length` / `@minLength` / `@maxLength` on a `string` now count Unicode
  characters, not bytes.** The generated validator uses
  `utf8.RuneCountInString` instead of `len()`, so a multi-byte value like
  `"日本語"` (3 characters / 9 bytes) passes `@maxLength(3)`. This aligns the
  runtime check with the OpenAPI `minLength`/`maxLength` keyword (JSON Schema
  counts characters) and a Postgres `varchar(n)` — previously the validator
  rejected a value the spec advertised as valid. A `bytes` field still counts
  bytes (binary length, not advertised in OpenAPI); use `@maxBodySize` to cap
  raw network size.
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
  - map allocation instead of N (≈5× fewer allocations on a 5-field handler).
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
  _following_ declaration (it read decorators across the newline), so a
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
