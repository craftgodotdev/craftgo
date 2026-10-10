---
title: What changed
description: What upgrading from craftgo 1.10 asks of an existing project and what it will see, then the same for upgrading to 1.10, then what changed from 1.7.1 to 1.9.
---

# What changed

Grouped by where it lands, from the
[changelog](https://github.com/craftgodotdev/craftgo/blob/main/CHANGELOG.md).

## Upgrading from 1.10

This release removes the DSL `import` statement and `FillEmpty`, and changes what some designs mean and what the server answers. A regenerated project picks up everything below. The files craftgo writes once - `main.go`, `config/`, `svccontext.go`, the logic stubs and the middleware scaffolds - keep what they were written with, so the edits they need are listed here.

### Decorators on their own line

A decorator on its own line now decorates the member below it, as it reads, a field or an enum value included; under 1.10 one between two fields or enum values decorated the member above it. Same-line decorators keep their member.

```craftgo
type User {
	id string
	@format(email)
	email string   // 1.10 checked id as an email; now email is checked
}
```

`craftgo fmt` of 1.10 moved each own-line decorator onto the line of the member it decorated, so a design it formatted keeps its meaning. In a design it did not format, each such decorator moves to the member below: before upgrading, `craftgo fmt -l` of 1.10 lists the files that hold one, and running it on a copy shows which member each decorated until now.

### Designs `craftgo gen` refuses

Fix the design and run `craftgo gen` again.

- An `import "<folder>"` line: delete it. A declaration of another package is referenced as `pkg.Type`, which has needed no import since 1.3.0.
- A decorator on its own line with no member below it, at the end of a body.
- `//` right after a route segment, which started a comment that cut the route short: write one `/`.
- NUL, invalid UTF-8, a byte order mark past the start of a file, a control character other than tab, line feed and carriage return, or a noncharacter, anywhere in a file, comments and strings included, and a `\u{…}` escape naming one.
- A float literal past float64, a duration past 2562047h, a duration with two units (`1h30m`: write `90m`).
- A binding decorator on a field of a nested type, which bound nothing: `binding/nested`. Bind a field of the request type itself.
- An `@json` key encoding/json would ignore, and a path, query, header or cookie name holding a backtick.
- A `@cookie` attribute no browser keeps: a path not starting with `/`, a domain that is no host name, a `maxAge` of no whole second, `sameSite: none` without `secure`.
- A copy of a well-known type (`google/protobuf/*`) in the design folder: delete it. A copy of a proto the runtime links in (`google/rpc/*`, `google/api/httpbody.proto`, `grpc/health/*`, `grpc/reflection/*`, `grpc/binlog/*`, `opentelemetry/proto/*`, `io/prometheus/client/*`), which panicked at startup: move it under `proto.includes`.
- Proto directories whose Go packages import one another, which did not compile: the error names the packages and the imports that close the ring.
- RPC files or service directories whose names differ only in case.
- A dangling manifest link.

A design proto whose `go_package` names another module's package is a warning: its path is still ignored, and the binary panics at startup if that module is linked in too.

### What the server answers

- A JSON body that leaves out a member declared without `?`, or sends it as `null` without `@nullable`, answers 400 `<json path>: required`. A client that relied on a missing member reading as `""`, `0` or `false` now sends it, or the member takes `?` or a `@default`. `server.requirePresence: false` in `config.yaml` switches the check off while clients catch up.
- A JSON route answers 415 to a body whose `Content-Type` is not `application/json` or an `application/*+json` type, or that has none: a client posting JSON as `text/plain`, or untyped, sets `Content-Type: application/json`.
- A nil list, map or `bytes` the logic returns goes out as `null`, where 1.10 wrote `[]`, `{}` or `""`: return empty ones where the document requires the member. An optional one the logic sets empty goes out as `[]` or `{}`, where 1.10 left it out.
- `@format` checks follow their RFCs. `email` refuses a display name, a comment or angle brackets (`Ann <ann@example.com>`), `time` needs its offset (`15:04:05Z`, not `15:04:05`), `ipv4` refuses `::ffff:1.2.3.4`, and `url` needs `http` or `https` and a host. A client sending the forms that passed before gets 400, and a bound that only fits the old form, as `@length(8, 8)` on a `time`, needs widening.
- A body past 10 MiB answers 413 on a route without `@maxBodySize` (a new `config.yaml`; see below).
- Cookies a `@cookie` field sets carry `Path=/`, `HttpOnly`, `Secure` and `SameSite=Lax` unless the `@cookie` names others, as `@cookie("theme", httpOnly: false)` for one scripts read. A cookie whose attributes depend on the request, as a lifetime the user picks or a deletion at logout, is a field typed `cookie`, which the logic sets per response from `New<Type><Field>Cookie(value)`.
- The root route answers `/` only, where it answered every path no other route took.
- A new logic stub answers 501, or `Unimplemented` over gRPC, until it is written.
- A float parameter of `0x1p4`, `1_000`, `Inf` or `NaN` answers 400, and a parameter's parse error names its type: `limit: "abc" is not a valid int8`.
- A body value its type decodes itself fails under its JSON path: `at: not a valid RFC 3339 datetime`, `data: not a valid base64`. An empty body is `body: required`. Decode messages read the same on every Go toolchain, Go 1.27 included.
- A `datetime` with an offset of a day or more answers 400 `<field>: not a valid RFC 3339 datetime`, where its response answered 500.
- A read timeout answers 408 and another failed body read 400 `bad request`, the cause logged rather than sent.
- A response whose body fails to encode carries none of its headers or cookies.

### `main.go`, `config.go` and the stubs

- A new `config.yaml` sets `readTimeout: 30s`, `maxBodySize: 10485760` and `requirePresence: true`, and a new `main.go` applies them, registers the docs with `srv.RegisterDocs` and stops at boot naming a value it cannot use. An existing project keeps its files: its `maxBodySize: 0` still sets no cap, so set `10485760` to take the new default; the required-member check applies through its default.
- A new `main.go` binds each listener before it logs the address, stops HTTP and gRPC side by side, and calls `server.SetStrictJSON`. An existing one keeps working; switch its deprecated `srv.SetStrictJSON` to `server.SetStrictJSON` when you next edit it.
- A new logic stub returns `server.ErrNotImplemented` or `status.Error(codes.Unimplemented, …)`. Stubs already written keep what they hold.

### Generated code

- `fill.go` and the `FillEmpty` method are gone, and code calling `FillEmpty` stops compiling.
- A transport package gains `bodies.gen.go`, the structs a JSON body decodes into before its required members are checked.
- Optional list, map and `bytes` members are tagged `omitzero` instead of `omitempty`. A codec installed with `server.SetGlobalJSONCodec` must honour it: `encoding/json`, sonic and goccy/go-json from v0.11.1 do; jsoniter and segmentio/encoding do not, and write a nil member as `null`.
- `validate.go` carries each format check it calls as a function of its own (`_isEmail`, `_isDateTime`, …).
- The constant of an `@contract` holding `\` or `"` holds the contract as written. One holding `\` had a Go escape read into it, so its wire identity changes when you regenerate: drain the old subject, or keep consumers on both, before switching.
- Generated pb code reads `protoc v0.0.0-craftgo` in its header. Delete by hand, once, a pb directory an older craftgo wrote for a proto you have since moved; the sweep takes only what carries the new mark.

### OpenAPI document

- `bytes` is `type: string, contentEncoding: base64`, its length in bytes in the description; `format: byte`, and the `minLength`/`maxLength` that counted base64 characters, are gone.
- `int8`, `int16`, `int32`, the unsigned types and `float32` carry their range, and integer map keys a `propertyNames` pattern.
- A request type no operation takes a body from gets no component, nor do the mixins and generic instances only it used: a client generated from the document loses those types.
- An error whose component name another package also declares takes the package prefix, as `AMissingErr`.
- The top-level `tags` lists every tag in the order of first use, a service's doc describes its tag, and documented enum values reach `x-enum-descriptions`.
- A parameter's description, deprecation and example sit on the parameter, a response header's on the header, and a header every response of its status sends is `required`.
- A string a YAML 1.1 reader would take for another type is quoted, and `servers[0].url` is the basePath as the routes normalise it.

### Manifest, CLI and editor

- `craftgo gen` prints `craftgo: design <dir>, project root <dir>` first and the design's warnings on stderr: a script reading its output matches lines rather than the first one.
- The search for the design stops at the first directory holding `go.mod`, `.git`, `.hg` or `.svn`, and skips hidden directories, `vendor`, `node_modules` and files starting with `.` or `_`. Name a design kept in such a place with `-f`.
- `events.targets: []` generates no event library; leave the key out for the default.
- `craftgo fmt -w=false` prints the formatted text instead of writing it, and `craftgo fmt` refuses a named Go, `.proto` or YAML file.

### Runtime API

- New: `server.Serve`, `ErrNotImplemented`, `SetRequirePresence`, `RequirePresence`, `MissingMember`, `Nullable`, `RequireJSONBody`, `ResponseCookieSetter`, `LimitParts`, `MultipartMemory`, `DefaultMaxBodySize`, `CompressOptions.Validate`, `Server.RegisterDocs`; `rpc.ValidateClientStream`, `rpc.ValidateBidiStream`; `events.EnvelopeError`, `nats.ErrNotCarried`.
- Deprecated, still working: `Server.SetJSONCodec`, `Server.SetStrictJSON` and `Server.Codec`. The codec and the strict switch were always the process's; call `server.SetGlobalJSONCodec`, `server.SetStrictJSON` and `server.JSON`.
- `server.New` caps bodies at 10 MiB until `SetDefaultMaxBodySize` says otherwise. `SetCORS` panics on credentials allowed to `*`, and `Compress` on a level outside -2..9. `Stop` before `Start` makes a later `Start` or `Serve` return nil at once, on both servers. The default logger no longer samples.

### Events

- A shutdown hands back what it did not handle. Cancel the context given to `Start`, then close the transport: JetStream naks a message whose handler the shutdown cut short, a Kafka share group releases it, a classic group commits only what was handled. A record handled and not yet committed when Kafka's `Close` runs comes back, so keep handlers idempotent, as at-least-once delivery always asked.
- Kafka classic subscriptions no longer autocommit: drop `kgo.GreedyAutoCommit` or `kgo.AutoCommitMarks` from `WithClientOptions`, which now fail the subscription.
- The NATS adapters refuse with `nats.ErrNotCarried` a message their headers would alter, where it arrived trimmed or rewritten.
- `PublishAll` refuses a batch with an `*events.EnvelopeError` naming the envelope; code matching the old error text reaches the cause with `errors.As` or `errors.Is`.
- An `ItemErrors` a typed batch `fn` returns fails only the items it names.
- The memory transport runs a handler on its subscription's context and gives each group its own copy of a message.

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
- A field named `fillEmpty`, which the generated `FillEmpty` method takes: `field/invalid-go-name`. Rename it.
- Two middlewares whose scaffolds write one file, as `APIKey` and `ApiKey`: `middleware/collision`. Rename one.
- A middleware named `Config` or `Middlewares`, whose routes did not compile: `decl/go-name-collision`. Rename it.
- A fractional bound on a field of an int enum, as `@gt(1.5)` or `@multipleOf(2.5)`, which 1.9 documented and never checked: `decorator/typemismatch`. Use a whole number.
- `@minItems`, `@maxItems`, `@uniqueItems`, `@maxSize` or `@mimeTypes` on a field typed by a type parameter, as in `type Box<T> { v T @maxSize(10) }`, which 1.9 documented and never checked: `decorator/typemismatch`. Constrain a concrete field, or the collection `T[]`.
- A method whose file the go command builds only for tests or one system under the file case, as `RunTest` (`run_test.go`) or `ListWindows` (`list_windows.go`): `service/method-file-name`. Rename it, or set `output.fileCase` to `kebab` or `camel`.
- Methods of one service directory writing one file, as `GetURL` beside `GetUrl`, or one Go name, as `Order` beside `NewOrder`, or a method named `Logger`, which generated code that did not compile: `service/method-name-clash`. Rename the method, and move its logic stub to the new file name.
- An event payload reaching a field bound to `@path`, `@query`, `@header`, `@cookie` or `@form`, which never reached a consumer: `event/payload-binding`. Drop the binding, or give the event a type without it.

A decorator as another decorator's argument, `@a(@b)`, is out of the grammar: one parse error at the inner `@`.

### What the server answers

- Every error the framework writes is JSON `{"message": "..."}` with `Content-Type: application/json; charset=utf-8` and `X-Content-Type-Options: nosniff`: the 404, the 405 (with `Allow`), a 413, a panic's 500 and the default validation 400 were `text/plain`. None of them is in the OpenAPI document.
- A request missing a required `file` or `any` that a type parameter types, as in a `FilePart<file>` mixin, answers 400 `<field>: required`; 1.9 passed the service nil.
- A required list, map or `bytes` the logic leaves nil goes out as `[]`, `{}` or `""`, wherever it sits in a response or an error body, where 1.9 wrote `null`, which the document does not allow. An optional one is still left out and a `@nullable` one is still `null`. The value the logic returns is filled in place the first time: a response object, or an error's lists and values below its body's top level, shared between requests is written to once, a map never, a copy taking its place, and an already filled value is only read.
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
- `SetLogger` installs `log.Default` and `Logger()` returns it, on both `server.Server` and `rpc.Server`. The `AccessLog(srv.Logger())` of a 1.9 `main.go` writes to the logger of startup, as it did; `AccessLog(log.Follow())`, which a new `main.go` installs, follows a later `SetLogger`.
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
