---
title: What changed
description: What upgrading to craftgo 1.10 asks of an existing project and what it will see, then what changed from 1.7.1 to 1.9.
---

# What changed

Grouped by where it lands, from the
[changelog](https://github.com/craftgodotdev/craftgo/blob/main/CHANGELOG.md).

## Upgrading to 1.10

1.10 removes no exported API. A regenerated project picks up everything below. The files craftgo writes once - `main.go`, `config/`, `svccontext.go`, the logic stubs and the middleware scaffolds - keep what they were written with, so the edits they need are listed here.

### Designs `craftgo gen` refuses

Each of these generated a project that built and ran under 1.9. Fix the design and run `craftgo gen` again.

- A file that imports or declares anything without a `package` clause: `package/missing`. Add `package <name>` at its top; a comment-only file needs none.
- A lower-case `type`, `enum`, `scalar`, `error`, `middleware`, `event` or method name, or type parameter: `decl/name-case`. Capitalise it; a lower-case `service` name only warns.
- A package named `init`, or like a predeclared Go name such as `int`, `string` or `len`: `package/name`. Rename the package.
- `scalar When datetime`: `scalar/bad-primitive`. Use `datetime` directly.
- A decorator after a declaration on its line, as in `middleware M @doc("m")`: a parse error. Put it on the line above what it decorates.
- `type T` with no body: a parse error. Write `type T {}`.
- An enum value outside the int64 range, or a `\u{…}` escape that names no character: a parse error.
- A `@default` that breaks a validator of its field or scalar: `decorator/conflict`. Fix the default or the validator.
- Bounds no value meets - `@positive @negative`, `@gt(5) @lt(5)`, a field's bound against its scalar's: `decorator/empty-range` or `decorator/range`.
- `@uniqueItems` over elements with an optional or `@nullable` member, or holding a `datetime`: `decorator/typemismatch`.
- A mixin field and a field of the type sharing a JSON key: `field/name-collision`. Rename one, or set its `@json`.
- An error mixin field named `errCode`, `error`, `httpStatus` or `writeResponseHeaders`, or an error body field named `marshalJSON`: `field/invalid-go-name`. Rename it.
- A `file` anywhere but a request's top level - a response, an error body, an event payload, a `map<string, file>`, another package's type, a generic argument such as `Box<file>`: `binding/file-position`. Send the content as `bytes`, or move the `file` to the request.
- A `@sensitive` field that a path variable names: `path/param-missing`. Drop `@sensitive` or the variable.
- An unknown decorator on an `extend service` block: `decorator/unknown`. `@operationId` there: `service/extend-decorator-not-method`; put it on the method.
- A generic argument on a built-in, as in `s string<int>`: `generic/non-generic`. Drop it.
- `payload Page<string?>`: `generic/optional-arg`, as on a field. Put the `?` on a field inside the generic.
- A bound past the field type's range, as `@multipleOf(18446744073709551616.0)` on a `uint64`, or a `@default` past `float32` on a `float32` field: `decorator/bound-overflow`.
- A security scheme an operation names that lacks a field its type needs - an `oauth2` flow without the URL its grant needs, `http` without `scheme`, `apiKey` without `in` or `name`, `openIdConnect` without its URL: a run that writes the OpenAPI document stops, naming the scheme and the field. Add it.
- A constraint decorator on a struct, generic-instance or `any` field, such as `a Addr @gt(3)` or `p Page<Item> @maxItems(3)`, or one an enum's backing type does not take, such as `@multipleOf` on a string enum, which 1.9 ignored: `decorator/typemismatch`. Drop it, or constrain the fields inside.
- `@json("-")`: `decorator/argvalue`. Use `@sensitive` to keep a field off the wire.
- An optional array or map as a map value, as in `map<string, int[]?>`: `type/map-value`. Drop the `?`; an absent entry reads as empty.
- `@path("rest...")` for a `{rest...}` variable: an error. The variable is `rest`.
- `@form` on a field of a request with no `file`: `binding/form-without-file`. Drop `@form`; the field rides the JSON body, as it did.
- `@minItems`, `@maxItems`, `@uniqueItems`, `@maxSize` or `@mimeTypes` on a field typed by a type parameter, as in `type Box<T> { v T @maxSize(10) }`, which 1.9 documented and never checked: `decorator/typemismatch`. Constrain a concrete field, or the collection `T[]`.
- Methods of one service directory writing one file, as `GetURL` beside `GetUrl`, or one Go name, as `Order` beside `NewOrder`, or a method named `Logger`, which generated code that did not compile: `service/method-name-clash`. Rename the method, and move its logic stub to the new file name.
- An event payload reaching a field bound to `@path`, `@query`, `@header`, `@cookie` or `@form`, which never reached a consumer: `event/payload-binding`. Drop the binding, or give the event a type without it.

A decorator as another decorator's argument, `@a(@b)`, is out of the grammar: one parse error at the inner `@`.

### What the server answers

- Every error the framework writes is JSON `{"message": "..."}` with `Content-Type: application/json; charset=utf-8` and `X-Content-Type-Options: nosniff`: the 404, the 405 (with `Allow`), a 413, a panic's 500 and the default validation 400 were `text/plain`. None of them is in the OpenAPI document.
- A body read past its cap answers 413 `{"message":"request entity too large"}`, where 1.9 answered 400.
- A multipart body the parser refuses answers 400 through `SetDefaultValidationFailed`, where 1.9 answered 413 (a regenerated handler).
- A handler that returns a deadline's error answers 504 `{"message":"gateway timeout"}`, and a context error after the client has gone writes nothing, the access log recording 499; 1.9 answered 500 and logged `unhandled service error`. `SetHandleUnknownError` no longer receives these errors.
- A float query, header, cookie or form value of `NaN` or `±Inf` answers 400.
- An array `@header` binds a comma-separated list (`X-Ids: 1,2`), as the OpenAPI document describes it; a multipart text part no longer takes a query parameter of the same name (regenerated handlers).
- `SetCORS` runs ahead of the `srv.Use` middlewares: a preflight is answered before an auth middleware can refuse it, and the responses those middlewares write carry the CORS headers.
- A success response that cannot be encoded, such as one holding a `NaN` float, answers 500 `{"message":"internal server error"}` and logs `unhandled service error`, where 1.9 sent the success status with an empty body (a regenerated handler).
- Validation texts: a cross-field group lists its members by wire name with no type prefix (`requiresOneOf [primary_email backup_email] - at least one must be set`), an enum value outside its set reads `status: must be one of [open in_progress done]` (was `status: invalid TodoStatus value`), a nested failure carries its path (`home: rooms: furniture: name: length less than 1`), and a body value of the wrong JSON type names its JSON path (`c: expected string, got number`). A client that matches the old text needs the new.
- An error whose body fields are all optional and unset is written as `{}`, the body its OpenAPI response declares, where 1.9 wrote the `{"code","message"}` envelope.

### `main.go`, `config.go` and the stubs

- A panic line carries the request's trace ids when the server is built with `server.New(svc, server.WithTelemetry(tel.HTTPMiddleware()))`, as a new `main.go` does. In an existing `main.go`, replace `srv.Use(tel.HTTPMiddleware())` with that option: keeping both records every span and metric twice.
- A new project's `config.go` fills only `server.addr`, `grpc.addr` and `serviceName`; the runtime defaults the rest, and an empty `metrics.adminAddr` starts no scrape listener (the generated `config.yaml` sets `":9090"`). An existing `config.go` keeps its own defaults.
- `config.Path()` names the `config.yaml` under `output.config`. A `config.go` written before, with `output.config` moved, needs its `Path()` edited by hand.
- A logic stub for a method returning `Page<map<string, Item>[]>` keeps the signature it was written with; edit it to `*types.Page[[]map[string]types.Item]`.

### Generated code

- Regenerated files change shape, not behaviour: imports in three groups, one-line generated comments, the design's `@doc` as the Go doc, sizes as shifts (`12 << 20`), a bare-integer `@timeout` as a duration literal, and no bound check the Go type already enforces.
- A generated error holds no `code` or `message` field: `Error()` and `ErrCode()` return constants, and a generated `MarshalJSON` writes its JSON.
- An optional map value whose type holds nil, as in `map<string, Blob?>` over `scalar Blob bytes`, is a `Blob`, not a `*Blob`.

### OpenAPI document

- Components renamed: `[]` or `?` on a map or a generic instance, or on an array of one, leads the name - `Page<map<string, Item>[]>` is `PageOfArrayOfMapOfStringAndItem` (was `PageOfMapOfStringAndItem`), `Page<Box<Item>[]>` is `PageOfArrayOfBoxOfItem` (was `PageOfBoxOfItemArray`). A client generated from the document gets the new type names.
- A generic instance named only by a `@sensitive` field or a header-inlined response, and the `<Method>ReqBody` of a request with nothing on its body, get no component.
- A basePath variable is a server variable, where 1.9 listed it as a query parameter or a body property.
- Bodies use the `@json` key, every `@errors` response is kept, and services of one name from every package are listed.

### Manifest, CLI and editor

- An unknown manifest key is a warning on stderr and in the editor; a removed key still stops the run.
- `craftgo gen` sweeps less. Under `output.pb` it takes only the pb code of the design's own protos, from the directories they write into, and nothing when the design has no proto; next to the OpenAPI document it takes only the document. The pb code of a proto moved to another directory or of a project's last proto, and the old document of a renamed `output.openapi`, stay until you delete them.
- `craftgo fmt` sets trailing comments off by one space and keeps every literal as written: run it once to settle the design files (`craftgo fmt -l` lists them).

### Runtime API

- New: `server.WithTelemetry`, `log.Follow`, `kafka.ErrClosed`.
- `Server.Logger()` returns a `log.Follow()` logger, and `SetLogger` installs `log.Default`, on both `server.Server` and `rpc.Server`.
- Deprecated, still working: `Server.RegisterMiddleware`, `Server.With`, `server.Timeout`, the `server.Logger` alias, `server.DocsUI` and its constants.
- `pkg/events/nats` needs Go 1.25, where it needed 1.26.

## From 1.7.1 to 1.9

### DSL

- `event Name { payload T }` declares a contract at file level; `@contract("subject")` sets its wire identity, which defaults to `<package>.<Event>`.
- A payload may be an array of a declared type, `payload T[]`: the descriptor is typed on the slice and every element is validated in turn.
- A `service` holds HTTP methods only, and the design names no listener - which events a deployable listens to, on which group, behind which middleware, is Go code in that deployable.
- Two reserved words, `event` and `payload`, are still usable as identifiers where unambiguous, as is every other reserved word.
- `datetime` is a `time.Time` in Go and an RFC 3339 string in JSON (`format: date-time`); body fields only, no validators, no `@default`.
- `@json("key")` on a field sets the JSON key when it is not the field name; the Go tag, the OpenAPI document and validation messages follow it.

### Generated output

- One file per DSL package that declares an event, `events.go` under `events.targets[].out`: a `<Name>Contract` constant and an `events.Event[T]` descriptor per event, its `@doc` as the Go comment.
- `output.kind: contracts` generates only payload types and this library, for a design several deployables import.
- `main.go` attaches the design through one generated call, `wiring.Register`, in a `wiring` package (`output.wiring`) whose surface does not change with the design; the scaffold no longer lists routes.
- A design package that declares no type gets no types package, and `validate.go` is written only where a type has something to validate.
- Stale output is pruned: inside the output directories the manifest names, every file carrying a generated header that the run did not write is deleted, and emptied directories with it.

### Runtime

- `pkg/events` is a new module: `Bus` (`Use`, `Register`, `Start`, `Plan`), typed `Group`, `Event[T]` descriptors that decode and validate before a handler runs, publish options, batch publishing, dispositions, a JSON codec, an in-process transport and access-log middleware.
- `pkg/events/nats` adds core NATS and JetStream, with one durable per group carrying the group's filter subjects, per-group settings, redelivery backoff, a delivery cap and NAK hand-back during a rolling deploy.
- `pkg/events/kafka` (franz-go, Go 1.25) adds classic and share groups, TLS and SASL options, `WithClientOptions` and `RecordFrom`.
- `log.Slog()` returns a `*slog.Logger` writing through craftgo's own logger.

### Manifest

- Output keys are checked as a set: `output.transport` may be named anything, two keys may not name one directory, and `-` is rejected on a key that has no disabled mode.
- The routes umbrella is removed with the last route.
- `events.targets[]` names the languages event artefacts are generated for; omit the block and a design that declares events gets one Go target at `./internal/events`, or `./gen/events` under `output.kind: contracts`.

### Removed, or never shipped

![The design owns the event declaration, the payload types and one folder per version; the group, the middleware and the folder layout belong to the application.](/diagrams/responsibilities.svg)

The strip under the diagram lists what the design no longer carries; none of it ever reached a release.

- `pkg/otel` and `pkg/metrics` are gone - alias-only shims over `pkg/telemetry`; import `pkg/telemetry` directly.
- The listener declaration, the decorator that named its group, the decorator that named its ordering key and the one that named its middleware chain never shipped: the group is an argument to `Subscribe`, the key an argument to `Publish`, the chain `bus.Use(...)` where the bus is built.
- Projections, the AsyncAPI document and the `.craftgo-gen/` bookkeeping folder never shipped either.

### Fixed

- `craftgo-lsp` exits on `exit`, with status 1 when no `shutdown` preceded it, instead of waiting for the client to close its stdin.
- The editor reports what `craftgo gen` reports.
- A type whose fields all delegate to another package compiles.
- A `@group` whose name ends in `time`, a service named `Craft`, a package whose name ends in `types`: none breaks its generated files.
- `@security` scheme names are listed in a stable order.

### Upgrading

**Drop the manifest keys that are gone.** `design`, `output.services`, `output.consumeMiddleware` and `events.asyncapi` are rejected while the manifest is read, each naming what replaced it (`<key> is no longer a manifest key - <note>; drop it`).

**Delete every `.craftgo-gen/` folder.** Nothing reads them. The `// Code generated by craftgo. DO NOT EDIT.` header is the whole record now: at the end of a run craftgo deletes every headed file in its output directories that the run did not write.

**`wiring.Register` is optional for an existing `main.go`.** It is generated for every project, but `main.go` is gen-once and is never rewritten, so a project that already registers its routes keeps working unchanged. Switch the registration line to `wiring.Register(ctx, srv, svcCtx)` to pick up the startup check that fails for every middleware the design applies and `svcCtx` leaves nil.

**`datetime` and `@json("key")` are available.** Both are body-field features; see [Types and Scalars](/guide/types-and-scalars).

**Nothing else needs doing for events.** A design that declares no `event` generates nothing new. When you add one, read [Events](/guide/events) - the contract goes in the design, and the group, the middleware and the transport go in the deployable.
