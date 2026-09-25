# Changelog

All notable changes to craftgo are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and craftgo follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) - from 1.0.0 on, a
breaking change to the DSL or the generated layout bumps the major version.

## [Unreleased]

### Changed

- **Both servers log through `log.Default`.** `server.Server` and
  `rpc.Server` keep no logger of their own: `SetLogger` installs
  `log.Default`, `Logger()` returns it, and the panic recovery each server
  installs looks it up when a panic happens, so `log.SetDefault`, or either
  server's `SetLogger`, reaches both, even after the handler is built.

- **Two parser errors read as facts.** An out-of-range integer reports
  `integer literal N is outside the signed 64-bit range (max …)`, and
  `consume` in a service body `a service has no consume member - …`.

- **Formatting sets every trailing comment off by one space**, where a
  closing brace, a decorator, an import or a scalar took two.

- **Kafka publish errors name the adapter and the contract**, as
  `nats.JetStream`'s do: `kafka: publish orders.Placed: <cause>`, `kafka:
  publish batch of N: <cause>`, and `kafka: <cause>` inside a
  `*PartialPublishError`. `errors.Is` still reaches franz-go's error. A
  publish or batch after `Close` opens no producer and returns `kafka: open
  producer: transport closed`.

- **A bare-integer `@timeout` renders like a duration literal.** The routes
  file writes `@timeout(60)` as `1 * time.Minute`, the largest whole unit, as
  it always wrote `@timeout(60s)`; the value is unchanged.

- **A decorator on the wrong kind of value reads the same everywhere.**
  `@pattern` on `bytes`, `@multipleOf` on a float and `@uniqueItems` on a
  map report `@X applies to <kinds> fields, but <field> is <kind>`, as every
  other decorator on the wrong type does; a fractional `@multipleOf` divisor
  on an integer reads like a fractional bound, and a `@format` other than
  `raw` on `bytes` reads `@format(email) applies to string, but …`. The codes
  are unchanged.

- **A `@default` on a type it cannot target is reported once, at the
  decorator.** A default on a `bytes`, `file` or `datetime` array was
  reported once per element, beside a warning asking for `?`.

- **Diagnostics spell a type as the design does, generic arguments
  included**: `got Page<User>` where they said `got Page`, and `Point[]`
  where a generator error said `[]Point`. The editor's hover and
  completion details use the same spelling.

- **`object` is no longer a built-in type.** It was listed as one only to be
  rejected: a declaration may now take the name, while a field typed `object`
  still gets the hint to use `any` or `map<string, V>`.

- **`pkg/events/nats` needs Go 1.25, not 1.26.** Its tests against an
  embedded nats-server live in a module of their own, so the adapter no
  longer requires `nats-server`, whose Go 1.26 floor it inherited.

- **An unknown manifest key is reported instead of dropped in silence.**
  `craftgo gen` names each key of `craftgo.design.yaml` it does not read in
  a warning on stderr (`craftgo: warning: output.typs is not a manifest key
  and is ignored`), then generates. A removed key still stops the run.

- **Every design file declares its `package`.** A file that imports or
  declares anything without a `package` clause is an error,
  `package/missing`, at its first import or declaration, and joins no
  package; it used to merge into the design's only package, or into an
  unnamed one. A file holding only comments needs no clause.

### Fixed

- **A flushed response counts as committed.** A panic, or an error a raw
  handler returns, after a `Flush` is logged and never written into the
  stream: `Recovery` no longer appends a 500 body to it and `WriteError` no
  longer writes a JSON envelope into an event stream. The access log records
  the first final status written, the one the client receives.

- **`Compress` survives a flush before any write.** A handler that flushes
  before writing, as a server-sent-events stream does, gets a 200 head sent
  uncompressed instead of a `WriteHeader(0)` panic and a 500.

- **`Compress` sends an informational status at once.** It held a
  `103 Early Hints` back as the final status, so the client got 200 in
  place of the status written after it, and a `WriteError` after it was
  dropped, leaving an empty 200.

- **A method's own `@timeout` survives the default body cap.** With both
  `server.handlerTimeout` and `server.maxBodySize` set, a method with a
  `@timeout` longer than the default and no `@maxBodySize` was cut to the
  default deadline; its own timeout now applies alone, as documented.

- **A custom not-found handler keeps the 405.** With `SetHandleNotFound`
  set, a request whose path matches a route under another method got the
  custom 404 instead of 405 with `Allow`, and an unclean path got it instead
  of the redirect to the cleaned one; the handler now receives only what the
  mux answers 404.

- **`Server.Codec` reports the codec in use.** It returned the codec last
  passed to `Server.SetJSONCodec`, missing a `SetGlobalJSONCodec` swap and
  strict JSON; it now returns what `JSON()` returns.

- **A failing readiness check is never healthy.** A check whose error text
  was `ok` counted as passing; `/readyz` now answers 503 for any check that
  returns an error.

- **A panicking readiness check fails the probe.** Each check runs on its
  own goroutine, so a panic in one ended the process; `/readyz` now answers
  503 with `panic: <value>` for that check and logs the panic with its
  stack.

- **An aborted handler aborts the connection.** `Recovery` took a
  `panic(http.ErrAbortHandler)` - what `httputil.ReverseProxy` does when
  copying a response fails - for a crash: it logged the panic and answered
  500, or ended a response already under way as if it were complete. The
  panic now goes on to `net/http`, which aborts the connection, so the
  client sees the response cut off.

- **A panic after the response started aborts the connection.** `Recovery`
  logged it and let the handler return, so `net/http` finished the response
  as if it were complete - a chunked stream got its closing chunk - and the
  client could not tell the body was cut short. It still logs the panic,
  then aborts the connection as a `panic(http.ErrAbortHandler)` does.

- **An invalid status is answered 500.** A `WriteHeader` with a code
  outside 100-999, which `net/http` rejects with a panic, counted as
  committing the response, so `Recovery` left an empty 200 in place of its
  500.

- **`server.Server` is safe for concurrent use.** The `SetDefault*`,
  `SetCORS` and `SetLogger` setters wrote without the lock that route
  registration and `Handler` read under, a data race when configuration ran
  on another goroutine.

- **An `otlp_http` endpoint must be a URL.** `telemetry.Init` fails on an
  `otlp_http` endpoint that is not an `http://` or `https://` URL with a
  host. A bare `host:port` was accepted, and the exporter then sent to
  `localhost:4318` or nowhere; `otlp_grpc` still takes `host:port`, and
  fails on a URL with no host.

- **An `otlp_http` URL whose path is `/` sends each signal to its own
  path**, `/v1/traces` and `/v1/metrics`, as a URL with no path does. It
  posted both to `/`.

- **An empty OTLP endpoint means the OpenTelemetry default.** With
  `otlp_grpc` or `otlp_http` and no `endpoint`, the exporter sends to
  `OTEL_EXPORTER_OTLP_ENDPOINT`, or the signal's own variable, else to
  `localhost:4317` or `localhost:4318`. It was built with an empty address
  and sent nothing, whatever the environment said.

- **Formatting leaves a file alone rather than damage it.** `craftgo fmt`
  and the editor's Format Document keep a file unchanged when its formatted
  text would not parse, or would drop, duplicate or add a comment.

- **`craftgo fmt` checks every file for errors, whatever path names it.**
  Given a relative path, or no path at all, it formatted a file with a
  parse or semantic error - reading the fields after an unclosed decorator
  as its arguments - where an absolute path refused the file. It also
  reports what the formatter refuses, and exits 1 for either.

- **`craftgo fmt` finds a file's errors whatever the spelling of its
  path.** A path in another case on a case-insensitive file system, or one
  through a symbolic link, missed the file in its project's analysis, so a
  file with errors was formatted. fmt now finds the file on disk, and checks
  a file its project does not load on its own.

- **A command reports a bad flag or argument once, with its usage.** The
  error was printed twice, around the flag package's own list, and
  `init -h` printed only `Usage of init:`; `craftgo <command> -h` now prints
  the command's part of `craftgo help`. `gen -f design extra` is an error
  rather than dropping `extra`, and `fmt` on a design folder of protos alone
  has nothing to format instead of failing.

- **`craftgo fmt` reads its flags like `gen` and `init`.** A flag after the
  path - `craftgo fmt design -l`, the order the help text showed - was
  ignored, so the files were rewritten instead of listed; it is now an
  error, as is a second path. The help text reads `craftgo fmt [-l] [-w]
  [path]`, and `craftgo fmt -h` exits 0.

- **`craftgo fmt` checks a file outside every design folder on its own.** A
  file beside a design folder was checked against that folder's project,
  which does not hold it, so its semantic errors went unseen.

- **Formatting prints every literal as it is written.** A float of 1e6 or
  more or below 1e-4 (`1234567.5`, `0.00001`) came out in an exponent form
  the DSL does not read, `\u{7}` as Go's `\a`, a character such as U+200B as
  a `\u200b` escape, and a raw string such as `` `^\d+$` `` lost its
  backticks. Strings, floats, enum values and import paths now keep their
  source spelling.

- **`craftgo help` gives the `-c` default `gen` uses: the design folder's
  parent.** It said the working directory whenever `-f` is given, and the
  CLI reference said the directory holding `go.mod`.

- **An enum value outside the int64 range is an error**, as it is in a
  decorator argument. `A = 99999999999999999999` parsed silently as
  9223372036854775807.

- **A token where a name belongs is never read as the name.** `type {` or
  `error {` went on to report `{` as the declaration's name - `unknown error
  category "{"`, `type name "{" should start with an uppercase letter`; the
  missing name is now the only error.

- **A `\u{…}` escape must name a character.** A surrogate, `\u{D800}` to
  `\u{DFFF}`, or a value above `\u{10FFFF}` is an error; it decoded
  silently to U+FFFD.

- **A `type` needs a body.** `type T` with no `{ … }` is a parse error; it
  parsed as an empty type, and formatting wrote `type T {}`.

- **A file may open with a UTF-8 byte-order mark.** gen and fmt rejected
  one as an unexpected character U+FEFF; the mark is skipped, and formatting
  writes the file without it.

- **A lone carriage return ends a line**, as `\n` and `\r\n` do. In a file
  with CR-only line ends, or with a CR among LF ones, a `//` comment ran on
  to the next `\n` and swallowed the declarations after it, which gen then
  left out without an error.

- **Formatting a CRLF file writes LF line ends throughout.** A line with a
  trailing comment kept its `\r`.

- **Formatting keeps a `@format` name that is a reserved word quoted.**
  `@format("null")` was printed as `@format(null)`, which reads as the null
  literal; the same held for `true`, `service` and the other reserved words.

- **Formatting keeps every comment where it was.** A comment after
  `package`, `middleware`, a bodiless `error`, a mixin, an opening brace or
  a decorator line above a field, and one inside a scalar's or a field's
  decorator chain, was dropped, so formatting refused the file; one under
  the decorators of a file without `package` was copied again on every run,
  and one between imports moved. Decorators above a field or a scalar keep
  their own lines when a comment sits among them.

- **A decorator continued on the next line stays with its field.**
  Formatting joins the decorators after a field or an enum value onto its
  line without adding a blank line below it, and a comment among them keeps
  them on their own lines, one level deeper. The comment was moved out below
  the member, and two trailing comments among them made formatting refuse
  the file.

- **A trailing comment stays with its own member.** With a comment block
  inside a member's lines, in a decorator's arguments or in a declaration
  split over lines, formatting put the trailing comment of a later line of
  that member on the next member. Formatting also refuses a file whose
  result would hold a comment in another place: after another member, as a
  doc, or in another block.

- **An argument list with a comment keeps its lines.** A decorator's
  arguments, an array or an object written over several lines stays on its
  lines when a trailing comment sits on one of them: each line of elements
  one level deeper, the closing bracket on a line of its own. The list was
  joined onto one line, and with two comments in it formatting refused the
  file. A member written over several lines no longer gains a blank line
  below it, and a refusal to put two comments on one line now says so
  rather than that a comment would be dropped.

- **A decorator after a declaration on its line is an error.** In
  `middleware M @doc("m")`, `error NotFound E @doc("e")`, `} @doc("t")` and
  a method's `} @deprecated`, the decorator went silently to the next
  declaration or method; it is now reported, like one after a mixin. A
  decorator goes before what it decorates.

- **A comment right above a declaration's keyword is its doc.** In
  `@deprecated` / `// Order is the order.` / `type Order {}`, the comment
  reached neither the Go doc nor the OpenAPI description; it now follows the
  doc above the decorators there, and formatting keeps it above the keyword.
  One set off from the keyword by a blank line, or between two decorators,
  is no doc.

- **A comment in a declaration's header stays in the declaration.** A
  comment block between the keyword of a type, enum, error, service, event
  or method and its `{` moved below the whole declaration; formatting now
  prints it at the top of the body.

- **`kafka.WithTLS(nil)` dials over TLS.** franz-go reads a nil config as
  "no TLS", so the brokers were dialed in plaintext and `WithSASLPlain`
  sent the password in clear. A nil config now dials with an empty one,
  which verifies the brokers against the system roots.

- **A zero `nats.WithPublishAckTimeout` waits until `JetStream.Close` on
  both publish paths.** A batch waited for its verdicts until `Close`, but
  a single `Publish` gave up after the client's own 5s default.

- **The Kafka transport refuses a publish or subscribe after `Close`.** It
  opened a fresh producer or consumer that nothing closed; both now return
  an error wrapping the new `kafka.ErrClosed`, like `nats.ErrClosed`.

- **A Kafka publish that `Close` cuts off returns `kafka.ErrClosed`.** A
  publish or batch still waiting on the broker when `Close` ran surfaced
  only franz-go's `kgo.ErrClientClosed`, which the error still wraps.

- **`nats.JetStream` refuses a subscribe once `Close` has begun**, with
  `nats.ErrClosed`. A late subscribe created its durable and consumed with
  nothing to stop it: neither `Close` nor cancelling its context ended it.

- **The core NATS transport refuses a publish or subscribe after `Close`**,
  with `nats.ErrClosed`, as `nats.JetStream` and the Kafka transport do. A
  subscribe registered a queue subscriber that only the end of its context
  removed, and a publish still went out on the connection, which `Close`
  leaves open.

- **A finished JetStream group can subscribe again.** A group whose
  context had ended, or whose durable `nats.ErrConsumerStopped` reported
  deleted, stayed "already subscribed" on its transport for good. The group
  is now free once its running handler has returned, and before that report
  is made; until the handler returns, a group whose context has ended is
  refused as "still stopping".

- **Concurrent subscribes of one JetStream group let one through.** Two
  `Subscribe` calls naming the same group on one transport could both pass
  the "already subscribed" check and consume side by side; the later one is
  now refused.

- **A core NATS subscription on a context that never ends parks no
  goroutine.** Each one left a goroutine waiting for ever on `ctx.Done()`.

- **A `*PartialPublishError` message names its first unsent index.** It
  called that index the number already sent, which a scattered report
  contradicts: `Unsent` [1 3] of five read "1 already sent" when three
  went out.

- **The editor's rename refuses a reserved word.** Renaming a declaration to
  `service`, `get` or any other keyword is an error instead of an edit that
  breaks every use.

- **Signature help highlights the argument the cursor is in**, on whitespace
  and right after a comma too, and closes outside the parentheses.

- **`@` inside an event or a method body offers no decorator**: no member of
  those bodies takes one.

- **An error declaration shows one symbol kind** in the outline and in the
  workspace symbol search.

- **Editor ranges are exact after an emoji.** Highlights, references, rename
  and completion edits cover the right text on a line holding a character
  outside the Basic Multilingual Plane, and import-path completion narrows
  correctly after a non-ASCII character.

- **Completion on a one-line type body uses the field before the cursor**:
  `@default(|)` after a body's second field offers that field's values.

- **The editor analyses a file outside every design folder on its own**, as
  `craftgo fmt` checks it: the folder's declarations do not resolve in it.

- **Diagnostics come out in a stable order.** `craftgo gen` and the editor
  listed a design's problems in an order that changed from run to run; they
  are now sorted by file and position.

- **A `@sensitive` field no longer fills a path variable.** `get /users/{id}`
  with a request field `id string @sensitive` passed analysis, then
  generated a route that never read `{id}` and an OpenAPI path without the
  parameter. The design is now rejected with `path/param-missing`.

- **A qualified error name is rejected as a field type.** `x shared.Gone`,
  where `Gone` is an `error` of package `shared`, passed analysis and
  generated Go that did not compile (`undefined: shared.Gone`); it now gets
  the diagnostic the bare `x Gone` gets.

- **An event payload's generic arguments are checked.** `payload Page<string,
  int>` against `type Page<T>` passed analysis and generated Go that did not
  compile; a payload now gets the arity and optional-argument checks a field
  type gets.

- **Generic arguments on an enum, a scalar or a built-in are rejected.**
  `c Color<int>` passed analysis and generated Go that did not compile, and
  `s string<int>` silently dropped its argument; both now report
  `generic/non-generic`.

- **A file without a `package` clause cannot qualify its own package.** Such
  a file joins the design's only package, so `x app.A` in it generated Go
  that did not compile; it now gets the `ref/qualified` error the same
  reference gets in a file that declares `package app`.

- **Packages that reference each other's types in a cycle are rejected.**
  `app.Req { b shared.Base }` beside `shared.Base { a app.Audit }` passed
  analysis and generated Go packages that import each other, which does not
  compile; each cycle is now reported once, with its path, as
  `ref/package-cycle`. Event payloads do not count: events are generated
  outside the types packages.

- **A generic mixin's field can bind a path variable.** `type GetReq {
  IdHolder<string> }` with `type IdHolder<T> { id T }` on `get /things/{id}`
  was rejected with `binding/type ... got T`; a promoted field now takes its
  mixin's type arguments, also when the mixin sits inside another package's
  mixin.

- **The editor's `@group` hover describes the layout gen writes.** It said a
  group nests files under `<service>/<group>/`; the group replaces the
  service's directory, as the decorators guide says.

- **Fewer duplicate diagnostics.** A generic mixin with the wrong number of
  arguments, or a mixin naming an error or a middleware, got a second
  diagnostic beside `mixin/arity` or `mixin/non-type`; an event payload
  naming an error got `event/payload-kind` beside `ref/unknown-symbol`; and a
  malformed `openapi.basePath` warned once per package. Each is now reported
  once.

- **`@timeout` rejects bare seconds past a Go duration.** `@timeout(9999999999)`
  generated routes that did not compile (`constant ... overflows int64`); it
  now reports `decorator/range`.

- **An `extend service` block's decorators are checked once, at the block.**
  An unknown decorator there passed analysis silently; it now reports
  `decorator/unknown`. A bad argument or a repeated decorator on the block
  was reported once per method of the block; it is now reported once. The
  editor offers above an extend block every decorator analysis accepts
  there - any decorator a method takes, plus `@group` - where it left out
  `@timeout`, `@errors` and the other method-only ones.

- **Every Go name a declaration generates is checked for clashes.** A type
  named like an error's `ErrCode<Name>` constant or `New<Type>` constructor,
  like an enum value's `<Enum><Value>` constant, or two events where one is
  named like the other's `<Event>Contract` constant passed analysis and
  generated Go that did not compile; each now reports
  `decl/go-name-collision`, naming what each side emits. An error whose body
  holds only a comment no longer counts as emitting a `<Name>Body` struct.

- **`@uniqueItems` refuses elements it cannot compare by value.** An element
  type with an optional or `@nullable` member passed analysis, and the
  validator then compared that member's pointer, so two equal elements
  counted as distinct; one with a `bytes?` member generated Go that did not
  compile; and a cross-package generic instance such as `lib.Box<Item>[]`
  was judged without its argument. Each now reports `decorator/typemismatch`
  naming the member at fault. A map key naming no declared type gets only
  the reference error.

- **A `@multipleOf` divisor past int64 is enforced.** On a `uint64` field,
  `@multipleOf(10000000000000000000.0)` - a whole float, the only way to
  write a divisor that size - passed analysis and reached the OpenAPI
  document, but the generated validator had no check for it; it now checks
  the exact integer. A `@default` is held to the same range rule as a bound:
  one beyond `float32` is rejected too, and an out-of-range one reads
  `@default 200 exceeds int8 range [-128, 127]`.

- **The editor offers a field the decorators its type takes.** On a field
  typed with a scalar declared in another file or package, `@` offered
  every validator - `@gt` on a string scalar; it now resolves the scalar in
  the project. `@pattern` on `bytes`, `@multipleOf` on a float and
  `@uniqueItems` on a map are no longer offered, and an error's fields are
  filtered like a type's.

- **`@negative` or `@lt(0)` on an array of unsigned integers is reported
  once**, as a decorator on the wrong type; it also drew the unsigned-value
  error meant for a single number.

- **A mixin's fields count toward a body's JSON keys.** `type R { Base
  identifier string @json("id") }` with `Base { id string }` passed analysis,
  and the generated struct carried two fields tagged `json:"id"`, one of
  which encoding/json silently drops; it now reports
  `field/name-collision` at R's own field, as two local fields sharing a
  key already did.

- **A repeated enum value name or literal points at its first use.** The
  third `A` of an enum related to the second one as "first declared here".

- **A `file` nested in another package's type is found.** A request field
  `att shared.Attachment`, whose type holds a `file`, passed analysis, and
  the multipart binder never read the file; so did a request type declared
  in another package. Both now report `binding/file-position`, as a local struct
  already did.

- **`@ignoreTags()`, `@ignoreMiddleware()` and `@ignoreSecurity()` warn about
  their empty parentheses** with `decorator/flag-empty-parens`, as every
  other decorator that takes no argument does and as the decorator
  reference says; `craftgo fmt` already removed them.

- **A decorator with the wrong number or kind of arguments gets that error
  alone.** `@pattern("(", "x")` also reported its first argument as a bad
  regular expression, and `@group("..", "x")` its path; a decorator's values
  are now checked only once its arguments fit.

- **A declaration the parser left without a name gets the parse error
  alone.** Two `type {}` lines also reported `duplicate top-level
  declaration ""`, a nameless `enum {}` `enum "" has no values`, and a
  `scalar S` missing its primitive `primitive must be a built-in (got "")`.

- **Diagnostics state the rules they apply.** A scalar over an unknown
  primitive lists every built-in it may wrap; a `@group` segment of `.` or
  `..` says the group's directory takes the place of the service's own,
  where it said the group nests under it; and a `@form` on the wrong type
  says a single-level array binds, `file[]` included, where it said file
  arrays do not.

- **A package named like an import of the generated code compiles.** A DSL
  package named `server`, `log`, `context`, `fmt` or another name a handler,
  logic stub or event file already imports clashed with that import (`server
  redeclared in this block`); the file now imports the package under a
  numbered alias such as `server2`. A `datetime` or `file` type argument, as
  in `payload Page<datetime>`, now brings its `time` or `mime/multipart`
  import too.

- **`config.Path()` names the `config.yaml` under `output.config`.** With
  `output.config: ./internal/config`, gen wrote `config.yaml` there while the
  generated `Path()` returned `config/config.yaml`, so `main.go` silently ran
  on defaults. A `config.go` generated before keeps the old path, since gen
  writes it once; edit its `Path()` by hand.

- **A scalar cannot wrap `datetime`.** `scalar When datetime` generated
  `type When time.Time`, which has none of `time.Time`'s methods: it encoded
  as `{}` and never decoded. It is `scalar/bad-primitive` now, as a scalar
  over `file` or `any` is, and the message lists the primitives a scalar
  wraps. Use `datetime` directly.

- **A generic mixin's arguments bind its own fields only.** With `type
  Meta { t T? }` naming a declared `T`, `type Page<T> { Meta }` instantiated
  as `Page<string>` gave `t` the argument's type: the handler bound the
  query value as a string into the `T` field and did not compile, and a
  `@requiresOneOf` or `@uniqueItems` over such a field was refused for the
  argument's type. A field keeps the type its own declaration gives it; a
  nested generic mixin receives the arguments through its own.

- **An optional type parameter over an array is refused on the wire.** In
  `type Box<T> { a T? }` used as `Box<string[]>`, `a` is a `*[]string` in Go,
  which neither the query binder of a body-less verb nor the multipart form
  binder can fill, so the handler did not compile. Both are `binding/type`
  now; a JSON body still carries the field, and `a T` binds as the slice.

- **`@ignoreMiddleware`, `@ignoreSecurity` and `@ignoreTags` on an `extend
  service` block take effect.** They were accepted and changed nothing: the
  block's methods kept the primary service's middlewares, security and tags.
  They apply to each method of the block as if written on it: the primary's
  chain is dropped, and the block's own `@middlewares`, `@security` or
  `@tags` start it afresh.

- **A generic request type binds with its type arguments.** `get Get
  /things/{id} { request IdHolder<string> }` was refused (`{id}` requires a
  string, `got T`), as was a `filter T?` of `Paged<Status>` on a GET: the
  request's fields were checked without the arguments. The checks, the
  handler's binder and the OpenAPI parameters read the fields substituted,
  and the body schema of a request that also binds path or query fields
  substitutes them once, not over the fields a nested mixin brings.

### Deprecated

- **Vestigial `pkg/server` API.** Behaviour is unchanged:
  - `Server.RegisterMiddleware` and `Server.With`, a middleware registry
    keyed by name that nothing generated calls: pass the middleware to
    `Handle` or `Use`, or build a `Chain`.
  - `server.Timeout`: use `SetDefaultHandlerTimeout`, or `WithLimits` for
    one route; both put the deadline on the request context.
  - The `server.Logger` alias: use `log.Logger`.
  - `server.DocsUI` and its constants: `DocsOptions.UI` takes the name as a
    string.

## [1.9.0] - 2026-09-22 [UTC+7]

### Added

- **gRPC, designed in protobuf.** A `.proto` under the design folder is a
  gRPC design: `craftgo gen` compiles it in-process, runs `protoc-gen-go`
  and `protoc-gen-go-grpc` through `go tool` - pinned in `go.mod`, so the
  plugin version is the project's protobuf version and no `protoc` install
  is needed - and writes the craftgo structure around the pb code: a
  regenerated server package per service under `output.grpc`, gen-once
  logic stubs under `output.service` from the same template the HTTP stubs
  use, `wiring/grpc.go` with `RegisterGRPC`, and a `grpc:` block in the
  config and main.go scaffolds. A design of protos alone boots the gRPC
  listener only; routes and RPCs boot both. The new `pkg/rpc` runtime gives
  the listener the HTTP chain's guards - recovery, access log, a default
  deadline, `rpc.Error` mapping craftgo typed errors onto status codes with
  an `ErrorInfo` detail, `grpc.health.v1`, reflection - and
  `telemetry.GRPCServerHandler()` emits the spans and
  `rpc.server.call.duration` against the same stack as the HTTP wrapper.
  New manifest keys: `output.pb`, `output.grpc`, `proto.includes`,
  `proto.plugins`. See the gRPC guide and `example/grpc`.

- **A dialer that carries the trace to the service it calls.** A gRPC
  client sends no `traceparent` of its own, so an uninstrumented caller
  breaks one request into two unrelated traces. `rpc.Dial(addr, opts...)`
  installs the stats handler that carries it - `telemetry.GRPCClientHandler()`,
  the caller-side twin of `GRPCServerHandler()`, which also emits
  `rpc.client.call.duration` - beside a default deadline for a call that
  carries none and an access log. The pb client itself is already
  generated; a service other teams call ships it with
  `output.kind: contracts`, which puts the pb code outside `internal/`.

### Fixed

- **The version in the docs nav follows the release.** It was a literal in
  `docs/.vitepress/config.ts` that nothing bumped, so the site still read
  `v1.7.1` three releases after 1.7.1. It is now a `const VERSION` that
  `scripts/release.sh` rewrites along with the two Go version vars, so it
  moves with every `make tag`.

- **A release publishes its GitHub Release again.** `make tag` printed one
  `git push` carrying the release commit and all five tags; GitHub raises no
  push event when a push carries more than three tags, so the tags landed on
  origin and the release workflow never ran - 1.8.0 and 1.8.1 have module
  versions but no binaries. The printed push is now two commands, with the
  root tag alone in the second.

## [1.8.2] - 2026-09-19 [UTC+7]

### Fixed

- **A built-in primitive in `request` or `response` is refused instead of
  generating a tree that does not compile.** The request rule only looked
  in the package's scalars and enums, so `string` / `bytes` / `any` and
  every other built-in slipped past both clauses - and the response side
  had no rule at all beyond the parser's bare-array reject. What came out
  named a type nothing declares: `var req types.string` followed by a
  `req.Validate()` no generator emits, a stub returning `(*types.string,
  error)`, an OpenAPI response body `$ref`-ing a
  `#/components/schemas/string` the document never declares, and a
  request body dropped from the contract entirely. Both clauses now
  reject every built-in spelling, pointing at the wrap (`type Resp {
  value string }`). Raw sides are refused too: `@rawRequest` /
  `@rawResponse` / `@passthrough` make the block docs-only for the
  transport, but the document is still emitted from it, so the dangling
  `$ref` outlived the flag. A scalar or an enum in `response` stays
  legal - both generate a real named type whose schema IS emitted. No
  design that spelled a primitive there ever produced a buildable tree,
  so nothing that worked before stops working.

### Changed

- **Completion offers a block's keys the moment its brace opens.** A
  cursor just after `{` used to answer nothing, because the fallback then
  dumped 24 keywords and every declared type. The fallback is now the
  block's own set, so `service S {` offers the seven HTTP verbs, a method
  body `request` / `response`, and an `event` body `payload`. A `type` /
  `error` body and an `enum` body still stay quiet: those open on free
  text, and a type body's one closed alternative is every declared type
  in the project.
- **The `request` / `response` popups list message types only.** They
  used to carry the built-in primitives alongside them, which the
  analyser now rejects; `payload` already listed types alone.

## [1.8.1] - 2026-09-18 [UTC+7]

### Added

- **`@format(raw)` on a `bytes` field.** The bytes already ARE the value,
  in the message's own encoding, and the codec embeds them untouched
  instead of base64-encoding the buffer - so an explicit `null`, an
  integer past 2^53 and a trailing zero such as `1.50` all survive, none
  of which does when the field is declared `any`. Generates `wire.Raw` in
  every shape - `?` only omits an absent value, `@nullable` only keeps the
  key - and an unconstrained OpenAPI schema described as `raw encoded
  value`. Body fields only, no other validator, no `@default`; refused on
  every type but `bytes`.
- **`github.com/craftgodotdev/craftgo/pkg/wire`, a fifth published
  module.** It holds `wire.Raw` alone, imports nothing but the standard
  library, and is released at the shared version like the others - so a
  package consuming a contract that carries a raw value inherits that and
  nothing else, neither the event runtime nor the craftgo toolchain. Its
  `pkg/wire/codectest` suite is how a codec proves it passes such a value
  through in its own encoding.
- **`nats.ErrConsumerStopped`**, the sentinel behind the report a group
  makes when its durable stops delivering - deleted, or its stream was.
  The server answers Consumer Deleted only to a pull request already
  waiting, so a durable deleted between two pulls is caught instead on
  the first missed heartbeat, about 30s, where the adapter asks whether
  it is still there and stops the group when it is not. The transport
  keeps running with that group dead, so an application that wants it
  back matches this error and acts.

### Fixed

- **A panic in a bus middleware is handed back rather than settled.** The
  recover outside the chain reset the message to no disposition at all,
  which a transport reads as "take it as done" - so a panic raised in a
  middleware (not in the handler) was acked and the message lost. It now
  asks for redelivery wherever the transport can honour one
  (`Dispositioner`), and the delivery is retried like any other failure;
  on a transport that only settles, nothing changes. A panicking handler
  is unaffected: the chain above it still decides.

## [1.8.0] - 2026-09-15 [UTC+7]

### Added

- **Events in the DSL.** `event Name { payload T }` declares a contract at
  file level; `@contract("subject")` sets its wire identity (default
  `<package>.<Event>`). A payload may be an array of a declared type,
  `payload T[]`, for a contract whose body is a JSON array: the descriptor
  is typed on the slice and every element is validated in turn. A
  `service` holds HTTP methods only. The design names no listener: which
  events a deployable consumes, on which group, behind which middleware,
  is Go code in that deployable. Two reserved words, `event` and
  `payload`, still usable as identifiers where unambiguous.
- **Event codegen, per DSL package under `events.targets[].out`.** One
  file, `events.go`: a `<Name>Contract` constant and an `events.Event[T]`
  descriptor per event, its `@doc` as the Go comment. `output.kind:
  contracts` generates only payload types and this library, for a design
  several deployables import. Nothing else is generated for events.
- **Event runtime, `pkg/events`** (its own module). `Bus` is the events
  server: `Use` installs bus-wide middleware, `Event[T].Subscribe` is the
  listener's line and `Register` takes a subscription value (local checks,
  `*RegisterError`, joined with `errors.Join` so every line is offered),
  `Start` hands the whole batch to the transport once, handlers wrapped
  with panic recovery, the bus chain and the subscription's own `Chain`;
  `Plan()` with a stable JSON form for golden tests; typed `Group`;
  `Event[T]` descriptors (`Publish`, `Handler`, `Subscribe(bus, group,
  fn)`, and `Subscription` for the value a field is set on) that decode
  and validate before a handler runs (`*PayloadError`,
  `ErrCodecMismatch`); publish options (`WithKey`,
  `WithDedupID`, `WithHeader`, `WithAdapterOption`, `WithPublishDefaults`);
  batch publishing with `PartialPublishError`; dispositions (`Settle`,
  `Redeliver`, `Reject`) a chain decides, `WithDispositionRequired` refusing
  a transport that cannot honour one; JSON codec, in-process transport
  (`pkg/events/memory`), access-log middleware (`pkg/events/logging`).
- **NATS transports, `pkg/events/nats`.** Core NATS, and JetStream with
  one durable per group carrying the group's filter subjects: an existing
  durable is adopted only when its filter equals the plan or is a strict
  subset of it (then widened); a narrower or partly overlapping plan is
  refused unless the group carries `AllowNarrow()`. Per-group
  `WithGroupConfig` (`MaxInFlight`, `AckWait`, `DeliverPolicy`,
  `ConsumerConfig`), redelivery backoff through NAK delay, a delivery cap,
  drain on `Close`, and NAK hand-back for a subject this process does not
  handle during a rolling deploy.
- **Kafka transport, `pkg/events/kafka`** (franz-go, Go 1.25): classic and
  share groups, TLS and SASL options, `WithClientOptions`, `RecordFrom`.
- **`datetime` primitive.** A `time.Time` in Go, an RFC 3339 string in JSON
  (`format: date-time`); body fields only, no validators, no `@default`.
- **`@json("key")` on a field.** Sets the JSON key when it is not the field
  name; the Go tag, the OpenAPI document and validation messages follow it.
- **Stale output is pruned.** Inside the output directories the manifest
  names, every file carrying a generated header that the run did not write
  is deleted, and emptied directories with it; an output directory belongs
  to one design. Gen-once files carry no header and are never touched.
- **`log.Slog()`**, a `*slog.Logger` writing through craftgo's own logger.

### Changed

- **`main.go` attaches the design through one generated call**,
  `wiring.Register`, in a `wiring` package (`output.wiring`) whose surface
  does not change with the design; the scaffold no longer lists routes.
- **A design package that declares no type gets no types package**, and
  `validate.go` is written only where a type has something to validate.
- **Output keys are checked as a set.** `output.transport` may be named
  anything; two keys may not name one directory; `-` is rejected on a key
  that has no disabled mode; the routes umbrella is removed with the last
  route.
- **Reserved words are accepted wherever an identifier is unambiguous.**

### Fixed

- **`craftgo-lsp` exits on `exit`**, with status 1 when no `shutdown`
  preceded it, instead of waiting for the client to close its stdin.
- **The editor reports what `craftgo gen` reports**, and a design file
  without a `package` declaration resolves the same way in both.
- **A type whose fields all delegate to another package compiles.**
- **A `@group` whose name ends in `time`, a service named `Craft`, a
  package whose name ends in `types`**: none breaks its generated files.
- **`@security` scheme names are listed in a stable order.**

### Removed

- **`pkg/otel` and `pkg/metrics`.** Alias-only shims over `pkg/telemetry`;
  import `pkg/telemetry` directly.

## [1.7.1] - 2026-09-08 [UTC+7]

### Changed

- **Formatting touches only files without errors.** `craftgo fmt` and the
  editor's Format Document skip a file that has a parse or a semantic error
  (warnings do not count), report the diagnostics, and `craftgo fmt` exits
  1 when it left a file unformatted. A mistake the parser tolerates reads
  as a different construct, and formatting used to write that reading back.

### Fixed

- **A decorator stranded after a mixin is an error.** `user string S
  @default("")` above `name string` parsed silently as field, mixin `S`,
  and a default on `name`, so formatting moved the decorator to the wrong
  field. The parser now reports it; `craftgo fmt` and the editor leave the
  file alone.
- **An unfinished type-argument list no longer hangs the parser.** Typing
  `Page<` and pausing froze the language server; it is reported and parsing
  moves on. An empty list (`Box<>`) is an error instead of vanishing on
  format.
- **The parser reports what the formatter used to paper over.** A trailing
  slash in a path (`/items/`) is an error rather than being dropped from the
  route; decorators with no
  declaration after them (at the top of a file, or after the last
  declaration) are reported rather than lost; a missing comma
  between decorator arguments, array elements, object fields, type
  parameters or type arguments (`@length(1 80)`) is an error rather than
  being inserted on format.

## [1.7.0] - 2026-09-07 [UTC+7]

### Added

- **Strict JSON bodies.** `server.strictJSON: true` in `config.yaml` (the
  default for new projects) rejects a request body with an unknown field
  (`400 <field>: unknown field`) or data after the JSON value, instead of
  silently ignoring them. `server.SetStrictJSON` toggles it at runtime. A
  custom codec takes part by implementing `server.StrictDecoder`
  (`DecodeStrict`, finishing with the shared `server.TrailingData` check);
  a codec without it is refused while strict JSON is on, so the config can
  never claim a strictness the server does not enforce.
- **Fields of your own on every log line.** `log.SetContextFields(fn)`
  derives fields from the request context (a tenant or user id a middleware
  stored) and `WithContext` appends them next to the trace ids, so the
  framework's lines and the generated logic's carry them alike.
  `server.AccessLogFields(fn)` appends request-derived fields (client
  address, user agent, the matched `r.Pattern`) to the `http access` line.

### Changed

- **`SetGlobalJSONCodec` and `Server.SetJSONCodec` return an error** (the
  previous codec stays) instead of accepting a codec that cannot honour
  strict JSON; `Server.SetJSONCodec` no longer chains.
- **`pkg/telemetry` is the one observability package.** The tracer and
  meter bootstrap that lived in `pkg/otel` and `pkg/metrics` moved into it:
  one exporter switch per signal, one resource rule (`service.name` over
  the SDK defaults - an empty name now keeps the SDK's
  `unknown_service:<binary>` for spans too, instead of `craftgo`; both
  signals now carry the SDK's `telemetry.sdk.*` attributes next to
  `service.name`), and no package-level gates or shared registry.
  `telemetry.Init` still installs the stack it builds as the process-wide
  default, so `otel.Tracer` / `otel.Meter` in application code keep
  reporting through it. New: `Telemetry.ScrapeHandler()` serves the
  Prometheus scrape on a route of your own when `metrics.adminAddr` is
  empty.
- **Route overlaps are analyser diagnostics.** Two routes of one verb that
  net/http would refuse to register together (they overlap and neither is
  more specific) are reported as `path/collision` next to same-shape
  duplicates, so the editor shows them as you type; `craftgo gen` no longer
  runs a separate route-conflict check.
- **Health probes are answered ahead of the middleware chain.** `/healthz`
  and `/readyz` (or the `WithHealthPaths` overrides) go straight to the probe
  handler, wrapped in `Recovery` alone: they are no longer access-logged,
  traced, counted in the `http.server.*` metrics or CORS-processed, and no
  `srv.Use` middleware runs for them. `server.AccessLog` therefore logs
  every request that reaches it; `AccessLogSkipPaths(...)` keeps other
  routes (a `/metrics` scrape on the API port) out.
- **Analyser codes.** `decl/package-mismatch` is gone: files group into
  packages by their `package` declaration wherever they sit under the
  design root, and a file without one joins the project's only named
  package. `mixin/unresolved` is gone: an unknown mixin name is reported by
  the type-reference pass as `ref/unknown-symbol`, or `ref/unknown-package`
  for a `pkg.Type` naming a package that does not exist, like any other
  reference. `ref/qualified` covers a malformed qualifier only: more than
  one package segment, or a redundant self-qualification.
- **Editor.** Go-to-definition, hover and completion resolve names exactly
  as the analyser does: a qualified `pkg.Name` by package name (an `import`
  alias no longer redirects them), a bare name in the buffer's package
  first and then in any sibling package. Untitled buffers get diagnostics,
  workspace symbols include the open document, and completions work
  outside a project.

### Removed

- **`server.AccessLogAll()`** - the access log has no built-in skip set to
  undo any more.
- **`server.RequestID()`**, `server.RequestIDFromContext`, `log.WithRequestID`
  and the `request_id` log field. Tracing is on by default in the generated
  `config.yaml` (`otel.exporter: none` keeps the spans in-process), so every
  request already carries `trace_id` / `span_id` on its log lines and a
  `traceparent` response header; a second correlation id added nothing. A
  project that must honour an upstream `X-Request-Id` wires its own
  middleware.
- **The global-slot bootstrap API of `pkg/otel` and `pkg/metrics`**
  (`otel.Init` / `InitDefault` / `InitFromConfig` / `HTTPMiddleware` /
  `IsEnabled` / `Disable`, `metrics.Init` / `InitDefault` /
  `InitFromConfig` / `StartAdmin` / `ShutdownAdmin` / `SnapshotHandler` /
  `Registerer` and the `With*` options). Hand-wired projects switch to
  `telemetry.Init(ctx, telemetry.Config{...})` and `tel.HTTPMiddleware()`.

### Deprecated

- **`pkg/otel` and `pkg/metrics`** now only alias the config types,
  exporter names and defaults of `pkg/telemetry`, for one release.

### Fixed

- **A typed error wrapped with `%w` keeps its status.** `WriteError` now finds
  the typed error with `errors.As`; a wrapped one used to fall through to the
  unknown-error path and come back as a logged 500.

## [1.6.0] - 2026-09-05 [UTC+7]

### Added

- **Raw transport sides: `@rawRequest` and `@rawResponse`.** `@rawResponse`
  keeps request bind + validate and hands the `http.ResponseWriter` to logic
  (stub `(w, r, req *T) error`); `@rawRequest` hands the `*http.Request` over
  unread and JSON-encodes the returned response (stub `(r) (*T, error)`).
  `@passthrough` is exactly both flags. Spelling a raw side twice warns
  `decorator/redundant` and generates identical code.
- **`request` / `response` blocks on `@passthrough` (and on any raw side) are
  a docs-only contract.** They shape the OpenAPI document and the generated Go
  types exactly like a typed method; the transport never touches them.
  Previously `@passthrough` rejected blocks (`passthrough/has-body`), so a raw
  endpoint could only be documented as `*/*`.
- **`server.WritePrecompressed`, `server.WriteBytes`, `server.AcceptsEncoding`.**
  Serve a body stored already compressed (gzip / zstd / br) verbatim when the
  client accepts the coding and decoded otherwise, with `Vary: Accept-Encoding`;
  the framework pulls in no compression library, the caller supplies `decode`.
  `example/raw` shows every mode, including a cache-backed `Snapshot` endpoint.

### Changed

- **`@timeout` now applies to `@passthrough` routes** (and raw sides). It only
  cancels the request context, so a streaming handler that selects on
  `ctx.Done()` stops cleanly; previously the decorator was silently dropped on
  passthrough methods while the server-wide default still applied.
- **One transport template and one service template.** The passthrough and
  multipart variants folded into `transport.tmpl` / `service.tmpl` with mode
  switches, and the stub signature and the handler call come from a single
  `buildSignature`. Generated output for existing designs is byte-identical.
- The `passthrough/has-body` diagnostic is gone.

### Fixed

- **`server.WriteError` no longer writes into a committed response.** A raw
  handler that streamed part of a body and then returned an error had the JSON
  envelope spliced into the body; the error is now logged with the request's
  trace context and the wire is left alone. The guard sees through the
  access-log and compression writers, and `WriteValidationError` shares it.

## [1.5.4] - 2026-09-04 [UTC+7]

### Added

- **`pkg/telemetry` sets up traces and metrics together.** One `Init`, one
  `HTTPMiddleware()`, one `Shutdown`, and a top-level `serviceName` in
  `config.yaml` covering both signals. `pkg/otel` and `pkg/metrics` are
  unchanged.

### Fixed

- **Metrics carried no `service.name`** - every metric reported
  `unknown_service:<binary>`. Harmless for a Prometheus scrape; under OTLP push
  it merged the whole fleet into one series.
- **`otel.enabled: false` silently dropped every `http.server.*` metric.** One
  `otelhttp` wrapper emits both signals, and the middleware gated on the tracing
  flag alone.
- **`otelhttp.NewHandler` was rebuilt on every request** - hoisted now, ~1.6x
  faster with ~1.8x fewer allocations.
- **An empty `metrics.adminAddr` leaked a goroutine** blocked on a nil error
  channel.

### Changed

- The observability guide lists the instruments craftgo actually emits, with
  their Prometheus names, labels and units; it previously named the pre-1.21
  semconv metrics.

## [1.5.3] - 2026-08-28 [UTC+7]

### Fixed

- **Services sharing a `@group` no longer lose routes.** `@group` replaces the
  service-name segment, so several services can deliberately land in one folder -
  but `routes.go` was emitted once per *service* into a directory that holds only
  one, so the service sorting last silently overwrote the other's routes. The
  umbrella then called the survivor once per service, mounting every pattern
  twice, which `http.ServeMux` rejects with a panic at startup: a green
  `craftgo gen` produced a server that could not boot. Routes are now emitted per
  output *directory*, with every service in a folder feeding its single
  `RegisterRoutes` and the umbrella dispatching once. Each method keeps its own
  service's middleware chain. Two shapes a shared directory cannot express are
  now rejected at analysis time instead: `group/package-straddle` (contributors
  from different DSL packages, which would need two Go `package` declarations in
  one directory) and `group/method-collision` (two contributors declaring the
  same method name, which would claim the same `<method>.go`).
- **A middleware named at two inheritance layers is wrapped once.** The layers
  append, so `@middlewares(Auth)` on both a service and an `extend` block of it
  emitted `svcCtx.Auth, svcCtx.Auth` and ran the middleware twice per request.
  The first occurrence is kept, so the inherited layer holds its outermost
  position - matching the dedup tags and security requirements already got.
- **LSP: ctrl+click on `extend service X` jumps to X's primary declaration.** The
  name in a service header was misread as a type position, so the lookup returned
  the first declaration named `X` in the file - the extend itself in the
  one-extend-per-file layout, leaving the cursor where it started.

### Security

- **Bumped Go to 1.26.6.**

## [1.5.2] - 2026-07-29 [UTC+7]

### Security

- **Bumped Go to 1.26.5 and `google.golang.org/grpc` to 1.82.1** to clear the two
  reachable vulnerabilities `govulncheck` flagged: GO-2026-5856 (Encrypted Client
  Hello privacy leak in the `crypto/tls` standard library, fixed in Go 1.26.5)
  and GO-2026-6061 (gRPC xDS RBAC / HTTP-2, fixed in grpc 1.82.1). `govulncheck`
  now reports zero affected symbols.

### Changed

- **`server.AccessLog` skips the health probes by default** - `/healthz` and
  `/readyz` are no longer access-logged (liveness / readiness pollers hit them
  every few seconds and flooded the log). Opt back in with
  `server.AccessLogAll()`, or choose a different skip set with
  `server.AccessLogSkipPaths(...)`.

## [1.5.1] - 2026-07-05 [UTC+7]

### Added

- **`output.fileCase` selects the case of generated file and directory names.**
  The per-method handler and service files, the per-service directory, and each
  middleware file follow `snake` (the new default - `create_user.go`,
  `user_service/`), `kebab` (`create-user.go`, `user-service/`), or `camel`
  (`createUser.go`, `userService/`), set in `craftgo.design.yaml`'s `output`
  block. It affects on-disk names only: URL routes stay kebab-case, Go package
  names and identifiers are unchanged, and a `@group` directory keeps its
  verbatim segment. `craftgo init` now scaffolds the option as a documented
  comment.

### Changed

- **Generated file and directory names now default to snake_case** instead of
  kebab-case, matching Go's file-naming convention: `create_user.go` and
  `internal/service/user_service/` rather than `create-user.go` /
  `user-service/`. Projects that regenerate pick up the new names; set
  `output.fileCase: kebab` to keep the previous layout. The change is on-disk
  only - URL routes, Go package names, and the OpenAPI document are unaffected.

## [1.5.0] - 2026-07-04 [UTC+7]

### Added

- **Negative integer enum values.** `enum Direction { Left = -1  Right = 1 }` now
  parses, generates the Go const, and emits the OpenAPI enum `[-1, 1]`.
- **`example/taskflow` reference app.** A deploy-ready team-task API that exercises
  nearly the whole DSL surface (multi-package, generics, mixins, scalars, string /
  int enums, typed errors, multipart upload, `@security` / `@middlewares`, the
  OpenAPI controls, `@timeout` / `@maxBodySize`), with an in-memory store
  (`go run .` works with zero dependencies) and a Docker + OpenTelemetry deploy
  scaffold.

### Changed

- **Generated service / transport / routes packages now use the DSL package name.**
  A design `package project` emits its handler, transport, and routes code as
  `package project` (matching the types package), instead of the old
  service-derived `projectservice`. Import aliases stay per-service, so multiple
  services in one DSL package still don't collide. For an existing project,
  `craftgo gen` updates the transport / routes files; the scaffold-once service
  stubs keep their old package name until renamed.
- **`@timeout` and `@maxBodySize` override the global config default** instead of
  being clamped to it. The per-method value is used as-is (it may be larger *or*
  smaller than `server.handlerTimeout` / `server.maxBodySize`); the global is a
  per-route default applied only to routes that don't declare the decorator.
  Per-method `@timeout` cancels the request context (no automatic 503 - the
  blanket `server.Timeout()` middleware still returns one).

### Fixed

- **Wire names are escaped in generated code.** A wire name containing a quote or
  `%` no longer aborts `craftgo gen` at `go/format` (generated validator and
  multipart binder).
- **Cross-package mixin duplicate wire-names are rejected** instead of silently
  double-reading one value into two fields.
- **Numeric-bound edge cases.** `@lt(0.0)` on an unsigned type is rejected like
  `@lt(0)`; an integral float bound past the `int64` range (`@lte(2e19)`) reports a
  capacity error instead of generating non-compiling Go.
- **`BodyLimit` returns 413 up front** when `Content-Length` exceeds the cap, even
  if the handler never reads the body.
- **CORS only short-circuits genuine preflights** (`OPTIONS` + allowed origin +
  `Access-Control-Request-Method`); other `OPTIONS` requests fall through to the
  real route.
- **Multipart requests no longer emit an orphaned `<Method>ReqBody` OpenAPI schema**
  - the multipart body is rendered inline on the operation.
- **Streaming through `Recovery` works** (the response wrapper now forwards
  `Flush`), and generated output ordering (middleware wiring, service-collision
  diagnostics) is deterministic across runs.

### Docs

- Guide and reference synced with the above: the `@timeout` / `@maxBodySize`
  override semantics, the `srv.SetCORS(...)` API, the generated layout (no
  `transport/<svc>/errors.go`, `@default` pre-fill before decode, service stubs
  embed `log.Logger`), the `logging` config section, and enum / map / scalar
  details.

## [1.4.2] - 2026-06-17 [UTC+7]

### Security

- **OpenTelemetry bumped to v1.44 to patch two advisories the runtime reaches.**
  `govulncheck` flagged GO-2026-4985 (oversized OTLP/HTTP response → memory
  exhaustion, in the metric/trace HTTP exporters) and GO-2026-4394 (OTel SDK
  arbitrary code execution via PATH hijacking) as called by `pkg/otel` /
  `pkg/metrics`. Updating the OTel family clears both; `govulncheck` now reports
  zero reachable vulnerabilities.

### Added

- **CI hardened and broadened.** `go vet` + `gofmt` now run alongside a
  `govulncheck` gate; the test suite runs on Linux, macOS, and Windows - the
  three release targets - instead of Linux only, catching path / file /
  line-ending bugs a single-OS run misses (race detector on Linux/macOS).
  Dependabot keeps Go modules, GitHub Actions, and the docs npm deps current,
  and every workflow declares least-privilege `permissions` plus
  run-cancelling `concurrency`.

- **Cross-platform release binaries via GoReleaser.** Pushing a `v*` tag now
  runs `.github/workflows/release.yml`, which cross-compiles the `craftgo` CLI
  and `craftgo-lsp` for linux / macOS / windows × amd64 / arm64, bundles both
  binaries per archive (`.tar.gz`; `.zip` on Windows) alongside the licence and
  changelog, writes `checksums.txt`, and publishes a GitHub Release with
  generated notes. The git tag is injected as the reported version, so
  `craftgo version` / `craftgo-lsp -version` match the release. Build config
  lives in `.goreleaser.yaml`; verify locally with
  `goreleaser release --snapshot --clean`. (`version` / `lsp.Version` became
  `var`s so the tag can be linked in via `-ldflags -X`.)

### Changed

- **Minimum Go is now 1.26.4** (was 1.24.2, which is past end-of-life - Go
  supports only the two latest releases). Required by the OpenTelemetry security
  update; building craftgo from source now needs Go 1.26+.

- **Generated structs name a non-body field's wire location in its tag.** A
  field bound to `@path` / `@query` / `@header` / `@cookie` previously rendered
  as `json:"-"` alone; it now carries a second tag key - `path:"id"`,
  `query:"page"`, `header:"X-Trace-Id"`, `cookie:"session"` - whose value is
  the explicit wire-name override when given, else the field name. The
  `json:"-"` stays (the field is still off the JSON body in both directions),
  so the only change is the added, documentary key, letting a reader see where
  a value rides without opening the handler. craftgo binds these fields by
  generated code, not tag reflection, so Go ignores the key. `@form` and
  `@sensitive` fields are unchanged. The tag is rendered once (`structTag`),
  shared by the type and error-body emitters.

### Fixed

- **The language server now resolves project mode on Windows.** A `file:///C:/…`
  URI was converted to the invalid path `/C:/…` (and the reverse produced the
  malformed `file://C:\…`), so the design-root lookup failed and the editor fell
  back to single-file diagnostics. Both conversions now handle the drive letter,
  restoring cross-file diagnostics, go-to-definition, and completion on Windows.
  (Surfaced by the new Windows CI runner; a `.gitattributes` forces LF so golden
  fixtures compare identically there.)

### Docs

- **Search-engine metadata for the documentation site.** The VitePress docs
  site now generates a `sitemap.xml` and stamps a canonical link plus per-page
  Open Graph / Twitter tags on every page; the home, getting-started, and
  why-design-first pages carry unique meta descriptions (previously every page
  shared the site-level description). The published location sits behind a
  single `SITE_ORIGIN` / `BASE` constant so moving to a custom domain is a
  two-line change, and the Google Search Console verification tag is wired into
  the global `<head>`. Documentation-only - the craftgo tool and its generated
  output are unchanged.

## [1.4.1] - 2026-06-13

### Added

- **Process-wide log level via `config.logging.level`.** Generated `config.yaml`
  now carries a `logging.level` key (`debug` / `info` / `warn` / `error`;
  default `info`) and the scaffolded `main.go` feeds it to the new
  `log.SetLevel` at boot. The server logger and the generated logic layer share
  one `zap.AtomicLevel` behind `log.New` / `log.NewConsole`, so a single call
  retunes both - no per-logger wiring, and the swap is atomic so it can back a
  `/debug/loglevel` endpoint or a config reload. `log.SetLevel`, `log.GetLevel`,
  and `log.ParseLevel` (config-string → `Level`, reporting unknown values rather
  than snapping) are exported for that. Loggers brought in via `log.NewZap` keep
  their own level. An unrecognised level string leaves the `info` default in
  place.

### Changed

- **`log.NewConsole` follows the shared process-wide level instead of
  hard-coding debug.** Both `log.New` and `log.NewConsole` now build over the
  same package-level `zap.AtomicLevel` (default `info`); `NewConsole` still
  differs only in format (human-readable, colour-tagged). Call
  `log.SetLevel(log.LevelDebug)` for verbose local runs.

## [1.4.0] - 2026-06-12

### Security

- **The default 500 handler no longer leaks raw error text to the client.** A
  service error with no HTTP status was serialised as `{"message": err.Error()}`
  - and `err.Error()` routinely carries DSNs, file paths, upstream URLs, and
  other internal detail. The default now logs the full error (with trace
  context) and returns an opaque `{"message": "internal server error"}`;
  `SetHandleUnknownError` still installs a richer envelope when wanted.
- **OTLP metrics exporters honour TLS from the endpoint scheme.** The gRPC and
  HTTP metric readers hard-wired `WithInsecure()`, so an `https://collector`
  endpoint silently sent plaintext. They now use the same scheme-detection as
  the trace exporters (`WithEndpointURL`), so `https://` upgrades to TLS.

### Added

- **Multiple-file uploads via `file[]`.** A `file[]` form field now binds every
  repeated multipart part into a `[]*multipart.FileHeader` (from
  `r.MultipartForm.File["<name>"]`), validated by `@minItems`/`@maxItems`, and
  the OpenAPI multipart schema renders it as `{type: array, items: {type:
  string, format: binary}}` (so generated clients type it as an array of files -
  heyapi: `Array<Blob | File>`). Previously a `file[]` field compiled the type
  (`[]*multipart.FileHeader`) but the binder used the single-file `r.FormFile`,
  producing non-compiling Go. A single `file` is unchanged. A
  multi-dimensional `file[][]` (which has no multipart encoding) is now rejected
  at gen time with a clear diagnostic instead of emitting non-compiling Go. An
  optional file (`cover file?`) is rendered as `{type: string, format: binary}`
  and simply omitted from the multipart `required[]` - a binary part is
  present-or-absent, never JSON `null`, so it no longer carries a
  `type: [string, "null"]` union (which is meaningless for a file and breaks
  Swagger UI's file picker).

### Changed

- **Internal decide-once consolidation (no behavior change; generated output is
  byte-identical).** The route join (basePath + @prefix + method path) is now a
  single exported `semantic.ResolveRoute` that the analyzer, the routes/OpenAPI
  emitters, and the route-conflict detector all call - the codegen copy
  (`methodFullPath`/`servicePrefix`) is gone. `IsBodyVerb` is the one body-verb
  rule (was duplicated per layer; now built on `http.Method*`). The binding-kind
  vocabulary ("path"/"query"/"header"/"cookie"/"form"/"body"/"sensitive") is a
  set of shared `semantic.Binding*` constants used by both layers instead of
  repeated string literals, and the otel/metrics `exporter:` selector values are
  exported constants. `pkg/server` exports `DefaultLivenessPath`/
  `DefaultReadinessPath`; a test pins the analyzer's reserved-route mirror to
  them. Two new leaf packages now hold the cross-layer authorities:
  `internal/wire` (binding-kind vocabulary, wire names, the request
  auto-binding rule, body-verb) and `internal/route` (route assembly, path
  strings, route shape, ServeMux pattern-overlap) - the analyzer, codegen, and
  the LSP all import the same implementation. The biggest source files were
  split by topic for maintainability: `parser.go` (1,115 lines, the whole
  package in one file) into five files, `semantic/combination_checks.go`
  (1,412) into six, `semantic/ranges.go` (1,011) into three,
  `semantic/imports.go` (930) into four, `lsp/lookup.go` into three, and
  `codegen/transport.go` into two. Small duplications folded into one home:
  `idents.LastSegment` replaces the identical path-segment helpers in
  semantic and the LSP, and codegen's `kebabCase` alias is gone (callers use
  `idents.KebabCase` directly). Codegen templates parse once per process
  (were re-parsed on every render). Dead plumbing removed: the always-false request-side `needsStrconv`
  return, two unused parameters (`Lexer.peek` width, LSP completion source),
  and four unused `svcName` parameters on method-level checks.

### Fixed

- **Every generated route is smoke-tested over real HTTP.** The e2e matrix
  gains a spec-driven probe (`TestEveryRouteRegisteredAndHandled`): it boots
  the full umbrella `RegisterAll` server through the production handler chain
  and walks every operation in the committed OpenAPI document - 155 operations
  across all 32 route hubs - asserting each one is mounted and its
  parse→validate chain answers (an app-level 404 counts as alive; only the
  mux's own "404 page not found" fails). Previously only 9 of 29 services were
  ever exercised over HTTP, the exact blind spot that let boot-time route
  panics ship.

- **`craftgo gen` output is fully deterministic.** The umbrella `routes.go`
  sorted its (service, group) registrations by service name only, so a service
  with several `@group`s registered them in map-iteration order - different on
  every run. The sort now tie-breaks on group; `RegisterAll` is byte-stable and
  the CI drift gate (gen output must match the committed files) is re-enabled.
- **Cross-package duplicate `operationId`s are reported at analysis time with a
  source position.** Two methods in different packages pinned to the same
  explicit `@operationId(...)` collide in the single merged OpenAPI document; the
  per-package check never saw both, so this surfaced only as a position-less
  gen-time error. A project-level pass now flags it in the editor and at gen with
  both methods named. (Auto ids that share a method name are service-prefixed in
  the merged document and do not collide.)
- **`Accept-Encoding: gzip;q=0` is honoured as a refusal.** Compression read the
  coding token and ignored the quality value, so a client that explicitly
  refused gzip with `q=0` still received a compressed response (RFC 7231
  §5.3.1). A `q=0` directive now skips that coding.
- **`Hijack` / WebSocket upgrades work through the `AccessLog` and `Compress`
  middleware.** `statusRecorder` and `compressWriter` now implement `Unwrap()`,
  so `http.ResponseController` can reach the underlying `Hijacker` instead of
  failing with "feature not supported".
- **`metrics.exporter: "none"` no longer secretly serves Prometheus.** The
  `none` path installed no reader, so `Init` fell back to its Prometheus default
  and started serving metrics. It now installs a silent manual reader.
- **A default `IdleTimeout` (120s) reaps idle keep-alive connections.** Prevents
  unbounded goroutine/connection accumulation from clients that never reuse a
  connection, without touching in-flight responses. `WriteTimeout` stays 0 by
  default on purpose (a hard value would cut off streaming / large downloads);
  set one with `SetDefaultWriteTimeout` for bounded-JSON servers.
- **Cross-package route duplicates are now rejected at analysis time with
  source positions.** Two services in *different* packages whose methods
  resolve to the same VERB + route shape (`alpha.AlphaService GET /things/items`
  vs `beta.BetaService GET /things/items`, including `{id}` vs `{uid}` renames)
  register the same `net/http` pattern, so the second registration panics at
  boot. The route-collision scan only saw one package at a time; a project-level
  twin (`checkProjectPathCollision`) now reports the cross-package pair with a
  file:line diagnostic naming both methods - in the editor (LSP) and at
  `craftgo gen` - instead of the late, position-less gen-time conflict error.
  Same-package pairs stay with the per-package pass (no double-fire).
- **Generated multipart handlers clean up their temp files on return.** The
  handler now `defer`s `r.MultipartForm.RemoveAll()` right after a successful
  `ParseMultipartForm`, releasing parts the parser spilled to disk as soon as
  the handler finishes. `net/http` sweeps these at end-of-response too; the
  explicit cleanup releases disk before the response flush and still runs on
  panic paths that bypass that sweep.
- **`@nullable` on a form-bound field no longer emits non-compiling Go.** A
  `@nullable` body field in a multipart request (form-bound because a sibling
  `file` field makes the request multipart) is rendered as `*T` by the type
  emitter, but the wire binder decided pointer-vs-direct from `f.Type.Optional`
  alone - blind to `@nullable` - and emitted a bare `req.F = r.FormValue(...)`
  (a `string` assigned to `*string`). The binder now uses the same
  `goFieldIsPointer` predicate as the type emitter, so the two can't disagree on
  pointer-ness (it pointer-wraps with a present-guard: `if _v := r.FormValue(k);
  _v != "" { req.F = &_v }`). (`@nullable` on an explicit `@query`/`@header`/
  `@cookie` was already rejected and stays rejected.)
- **A method path variable that reuses a `@prefix` path variable is now rejected
  at gen time.** `@prefix("/tenant/{tenantID}")` + method path `/{tenantID}/items`
  concatenates to `/tenant/{tenantID}/{tenantID}/items` - a duplicate wildcard
  that `net/http`'s ServeMux panics on at registration. The duplicate-path-var
  check only scanned the method path; it now seeds from the prefix's path
  variables too and fails with a clear diagnostic ("…already bound by the
  service @prefix… Drop {tenantID} from the method path.") instead of generating
  a server that crashes on boot.
- **A field matching a `@prefix` path variable now auto-binds to `@path`
  instead of being silently dropped.** An un-decorated field whose name matches
  a `{var}` in the service `@prefix` (e.g. `tenantID` under
  `@prefix("/tenant/{tenantID}")`) was bound from the **query** (on a body-less
  GET) or the **JSON body** (on a POST) instead of `r.PathValue` - so the value
  in the URL path never reached the field (no compile error, no panic: a request
  to `/tenant/acme/items` left `tenantID` empty, or 400'd demanding `?tenantID=`).
  The auto-binding rule scanned only the method path; both the analyser and
  codegen now read the **full route's** path variables (prefix + method) through
  one shared `MethodRoutePathVars`, so a prefix-variable field binds from the
  path on every verb - exactly like a method-path-variable field. (Explicit
  `@path` already worked; only the auto-bind case was affected.)

## [1.3.10] - 2026-06-08

### Fixed

- **Validation errors on scalar / enum fields now report the field name, not
  the type name.** A field `domain StoreDomain` (scalar) or `status Status`
  (enum) used to fail with `StoreDomain: ...` / `Status: invalid Status value`
  - the type name - because the constraint lives on the type's shared
  `Validate()` method, which the parent returned verbatim. The shared method's
  message is now subject-less (`length greater than 253`, `invalid Status
  value`) and the field's caller wraps it with the field name
  (`domain: length greater than 253`, `status: invalid Status value`),
  including through arrays and maps of scalars/enums. Nested struct fields are
  unchanged (their `Validate()` already names the inner field). The shared
  `Validate()` method (and generic-over-scalar dispatch) is otherwise intact.
- **Validation errors on bound fields now report the wire alias, not the DSL
  field name.** A field bound with an explicit name -
  `src Host @header("x-source-domain")`, `page int @query("p")` - used to fail
  with `src: ...` / `page: ...` (the DSL name), which doesn't match what the
  caller sent. The message now uses the wire name (`x-source-domain: ...`,
  `p: ...`) for every `@path`/`@query`/`@header`/`@cookie`/`@form` field, across
  all validators. Body fields are unchanged (their JSON key is the field name).

## [1.3.9] - 2026-06-07

### Removed

- **`@format(hostname)` is removed.** Its built-in regex approximated RFC 1123
  but did not enforce the 63-character-per-label limit (a 100-char label passed),
  and the total-length cap can't be expressed in Go's RE2 regex anyway - a
  half-correct validator that gave false confidence. `@format(hostname)` now
  fails with an "unknown format" error listing the valid values. To validate a
  hostname, use a scalar with an explicit `@pattern(...)` (and `@maxLength` for
  the total bound) tuned to your needs. craftgo no longer emits the OpenAPI
  `format: hostname` keyword. (Designs that used `@format(hostname)` must switch
  to a `@pattern`.)

## [1.3.8] - 2026-06-07

### Added

- **In-process API reference docs.** Generated servers can now serve the OpenAPI
  document and a rendered docs page directly, configured under a new `docs`
  section in `config.yaml` (and the `config.Config` struct):

  ```yaml
  docs:
    enabled: true            # default in a freshly generated project
    ui: redoc                # redoc | swagger | scalar (assets load from a CDN)
    path: /docs              # HTML docs page
    specPath: /openapi.yaml  # raw OpenAPI document
  ```

  When enabled, `main.go` embeds the generated `openapi.yaml` (`//go:embed`) and
  wires `server.ServeDocs(...)`, which registers `GET <specPath>` (the spec) and
  `GET <path>` (an HTML page loading the chosen UI from a CDN, pointed at the
  spec). The new `server.ServeDocs` / `server.DocsOptions` are also callable
  directly from hand-written servers. `main.go` and `config.go` are gen-once, so
  existing projects keep theirs - add the `docs` block + `srv.ServeDocs(...)` by
  hand, or delete the scaffold to regenerate. Docs wiring is skipped when the
  OpenAPI output is disabled (`output.openapi: "-"`) or lives outside the main
  package's tree (a `go:embed` cannot cross `..`).

### Changed

- **Generated `main.go` boots through craftgo's structured logger
  (`pkg/log`) instead of the standard-library `log`.** The bootstrap lines
  (config / OTel / metrics init, listener startup, fatal errors) now emit the
  same structured records as the rest of the runtime (access logs, error hooks)
  - e.g. `{"level":"info","msg":"listening","addr":":8080"}` - rather than plain
  `2006/01/02 ... ` text, so boot output is consistent and machine-parseable.
  `main.go` is gen-once, so existing projects keep theirs.

### Fixed

- **Conflicting routes are now rejected at gen time instead of panicking at
  server boot.** Two routes that overlap with neither more specific - e.g.
  `GET /orders/{id}/track` and `GET /orders/by-status/{status}`, which both match
  `/orders/by-status/track` - are rejected by Go 1.22's `net/http.ServeMux` at
  registration, so the generated server *compiled and shipped* but crashed on
  startup. `craftgo gen` now detects this across every service in the project
  (one mux backs `RegisterAll`) and fails with a message naming both routes and
  suggesting a fix. The ecommerce example's `FilterOrders` is updated to take
  `status` as `@query` (`GET /orders/by-status?status=...`) so it no longer
  collides with the `/orders/{id}/...` actions.

## [1.3.7] - 2026-06-07

### Fixed

- **An `extend service` block without its own `@group` now inherits the primary
  block's `@group`.** Previously each block was grouped strictly independently,
  so a primary `@group("admin")` left an un-decorated extend's methods at the
  ungrouped service root - splitting one service across `admin/` and the
  service-name folder. The service-level `@group` is now the default for every
  block; an extend still overrides by declaring its own `@group`. (Consistent
  with how extend blocks already inherit `@middlewares` / `@tags` / `@security`.)
- **Sized numeric types now emit their OpenAPI `format` / bounds.** `int32` →
  `format: int32`, `int64` → `format: int64`, `float32` → `format: float`,
  `float64` → `format: double`, and every unsigned type (`uint`, `uint8`…
  `uint64`) advertises `minimum: 0` (a user `@gte` still tightens it). Before,
  all integer widths collapsed to a bare `type: integer` and both floats to a
  bare `type: number`, so a client generator could not tell `int32` from `int64`
  or `float` from `double`, and unsigned fields advertised no lower bound.
  `int` / `int8` / `int16` stay bare - OpenAPI registers no standard format for
  those widths.

## [1.3.6] - 2026-06-07

### Added

- **`server.SetHandleUnknownError` - a swappable hook for service errors that
  are not craftgo typed errors.** When a handler returns a bare `errors.New` /
  `fmt.Errorf` (no `HTTPStatus()`), the framework now logs it at Error level
  with the request's trace context (`trace_id` / `span_id` / `request_id`) and
  responds 500 - and apps can replace that with `SetHandleUnknownError` to map a
  domain error to a status, redact, or return a uniform envelope. This closes
  the gap noted in the errors guide: validation (`SetDefaultValidationFailed`)
  and not-found (`SetHandleNotFound`) were already overridable; service errors
  now are too. The error contract is also named: `server.StatusError` (and the
  optional `server.ResponseHeaderWriter`) - every `@errors(...)` declaration
  implements it.

### Changed

- **Service-error rendering moved into the framework (`server.WriteError`),
  removing the duplicated per-package `errors.go`.** Each generated transport
  package (and, after per-group `@group` output, each group folder) emitted a
  byte-identical `writeError` helper. Generated handlers now call the single
  `server.WriteError`, so no `errors.go` is generated under `internal/transport`
  at all. Default rendering is unchanged: a typed error renders its declared
  status + body (or `{code, message}` envelope when bodyless); an unrecognised
  error goes through `SetHandleUnknownError`. (Typed-error *declaration* files in
  the types package are unaffected - those legitimately vary per error type.)

### Fixed

- **`@group` now splits the generated routes files per group, mirroring the
  transport handlers and service stubs.** A `@group` replaces the service-name
  segment on disk, so the handlers and stubs moved into the group folder
  (`internal/transport/<group>/`, `internal/service/<group>/`) - but the routes
  file stayed pinned at `internal/routes/<service-name>/`, so a service split
  across several groups had its folders out of sync. Routes now emit one file
  per group, each in its group's folder (`internal/routes/<group>/`), importing
  only that group's transport and registering only that group's methods. The
  umbrella `RegisterAll` dispatches to every group's `RegisterRoutes`, so the
  single-call registration path is unchanged; an ungrouped service is unchanged
  (one routes file at the service directory).

## [1.3.5] - 2026-06-05

### Added

- **`.cg` is now accepted as a short alias for the `.craftgo` source
  extension.** `craftgo gen`, `craftgo fmt`, and the language server (project
  walk + file watcher) all discover `.cg` files, and a single project may mix
  `.craftgo` and `.cg` freely - including cross-file and cross-package
  references. `.craftgo` remains the canonical extension. (Editors that launch
  the language server by file association need a `.cg` association configured on
  the client side - the bundled VS Code extension does this from v0.4.4.)

## [1.3.4] - 2026-06-05

### Fixed

- **Duplicate generic type-parameter names (`type Pair<T, T>`) are now rejected
  at parse time.** They lowered to `type Pair[T any, T any]`, which the Go
  compiler rejects (`T redeclared`) - so the design failed downstream with a
  confusing Go error instead of a clear diagnostic.
- **`craftgo fmt` no longer drops comments on enum values.** A `//` comment
  above an enum value (and a blank-isolated section comment between values) was
  silently deleted on format; both are now preserved and round-trip
  idempotently (`ast.EnumValue` gained a `Doc` field).
- **An `oauth2` security scheme now emits a valid OpenAPI `flows` object** from
  the manifest (`openapi.securitySchemes.<name>.flows`: `implicit` / `password`
  / `clientCredentials` / `authorizationCode`), and an `oauth2` scheme declared
  without any flow is rejected at gen time. Previously it emitted an oauth2
  scheme with no `flows`, which is invalid OpenAPI and crashed client
  generators such as `@hey-api/openapi-ts`.
- **A duplicate `request`/`response` clause in a method body is now rejected.**
  A second clause silently discarded the first with no diagnostic.
- **A type / enum / scalar / error named after a built-in type** (`scalar int`,
  `type string`, ...) is now rejected - the generated Go type shadowed the
  built-in and failed to compile. (Middleware names are exempt - separate Go
  namespace.)
- **`object` as a field type is now rejected** with a pointer to `any` - it was
  a broken half-alias whose Go renderer emitted an undefined type and a dangling
  OpenAPI `$ref`.
- **A multipart request now advertises its type-level cross-field constraints**
  (`@mutuallyExclusive` / `@requiresOneOf`) in the served `multipart/form-data`
  schema (via `allOf`), matching the JSON body schema. The runtime validator
  already enforced them, so the spec previously hid a rule the server kept.
- **`@uniqueItems` no longer false-rejects an array of structs that carry an
  optional non-collection field.** An optional field lowers to a pointer, which
  is comparable, but the comparability check treated it as non-comparable and
  rejected the otherwise valid `@uniqueItems` element type.
- **`craftgo fmt` no longer blanks a comment-only file.** A file with only `//`
  comments (no package, no declarations) had nothing to anchor the comments to
  and was reduced to zero bytes; the comments are now preserved and round-trip
  idempotently.
- **`@format(datetime)` now emits the standard OpenAPI / JSON Schema keyword
  `date-time`** instead of the non-standard `datetime`. The DSL keeps its own
  spelling, but validators and client generators (`@hey-api/openapi-ts`, ...)
  only recognise the hyphenated keyword. Other `@format` names without a
  differing standard keyword pass through unchanged.
- **A `file` field nested below the top level of a request body is now
  rejected.** The multipart binder reads only the resolved top-level request
  fields, so a `file` reached through a named struct field (`Req { wrapper Wrap }`
  where `Wrap` holds the `file`) was decoded as JSON, left the
  `*multipart.FileHeader` nil, and silently dropped the upload while gen and
  `go build` both succeeded. A top-level `file` (directly or flattened in via a
  mixin) and a `file` carried in a response are unaffected.

### Changed

- **`@default` on a non-optional field now warns** (`decorator/default-needs-optional`).
  The default fires when the value is absent, so the field is conceptually
  optional; `craftgo fmt` adds the `?`, after which types.go, validate.go, and
  the OpenAPI agree it is optional (the docs already described this warning, but
  none was emitted).
- **An optional map key (`map<K?, V>`) is now rejected at design time.** It
  rendered `map[*K]V`, which `encoding/json` cannot use as an object key
  (marshal/unmarshal fail) - caught for every underlying key kind, local and
  cross-package.
- **An empty `@pattern("")` is now rejected.** It is a valid RE2 (matches
  everything) so it passed the regex check, but it is a meaningless constraint
  and crashed the validator codegen (the regex interner has no name for it).
- **A numeric bound whose magnitude exceeds the `float32` range** (`@gte`/`@lte`/
  `@range` on a `float32` field or scalar) is now rejected - it previously
  generated a `float32` literal that overflowed and would not compile.

## [1.3.3] - 2026-06-04

### Added

- **LSP: live cross-file diagnostics when design files change on disk.** The
  language server now registers a `**/*.craftgo` watcher, so creating, deleting,
  or changing a design file (including outside the editor, or a file the user
  never opened) re-runs the project diagnostics pass on every open document - a
  reference to a just-deleted type starts erroring, and a re-added one stops,
  without the user editing the dependent file. On-demand features
  (go-to-definition, completion) already re-read the disk per request. The
  refresh analyses once per design root, not once per open file.

### Fixed

- **LSP: a `.craftgo` file deleted while still open keeps contributing its live
  buffer.** Project resolution walked only the disk, so an open-but-deleted file
  vanished from the project - dependent open files reported spurious
  "unknown type" errors and the file itself lost its own diagnostics. The walk
  now re-adds every open buffer the disk no longer holds, honouring the editor
  cache over disk presence.

## [1.3.2] - 2026-06-04

### Changed

- **`@group`'s output path now replaces the service-name segment** instead of
  nesting under it: `@group("v2")` on any service emits to
  `internal/transport/v2/` (not `internal/transport/<service>/v2/`), giving the
  author full control of the layout. Because the group replaces the service
  name it is a **global namespace** - two services that share a group land in
  the same directory and Go package, so keep groups unique per service (embed
  the service name in the group when in doubt).

### Added

- **`@group` is now accepted on an `extend service` block** (per-block
  grouping). Each block's `@group` groups only that block's methods, so one
  service can split its code across version/feature folders (e.g. a primary
  block plus an `@group("checkout/v2")` extend) while a single routes file
  imports each group's transport package and registers every method under the
  one service. Each group folder is a self-contained package with its own
  `writeError` helper - no cross-package imports. `@prefix` remains
  primary-only.

### Fixed

- **LSP: the decorator completion popup above a `service` now lists the
  service-level decorators** (`@prefix`, `@group`, `@middlewares`, `@tags`,
  `@security`). While typing the leading `@` the parser swallowed the following
  keyword as the decorator name, so the site was misread as file scope; a
  token-level scan now recovers the pending declaration's level. Above an
  `extend service` block the same popup omits `@prefix` (primary-only) while
  keeping `@group`.
- **LSP: go-to-definition on an enum value inside `@default(...)` / `@example(...)`**
  now jumps to that value's declaration (it previously resolved nothing, since
  an enum value is a member, not a top-level decl).
- **A service with no methods no longer emits an unused `transport` import** in
  its generated routes file (it failed to compile).

## [1.3.1] - 2026-06-04

### Changed

- **`@group` now nests generated files (and tags), not the URL.**
  `@group("admin/ops")` on a service writes that service's handlers and service
  stubs under `internal/transport/<service>/admin/ops/` and
  `internal/service/<service>/admin/ops/`, and adds its value as an OpenAPI tag
  (appended to any explicit `@tags`, deduped; `@ignoreTags` drops it). It no
  longer injects the group into the HTTP route or the OpenAPI **path** - use
  `@prefix` to shape the URL. The value may be nested (`admin/ops`) and is
  validated as a plain relative path (no `.`/`..`/absolute forms). **Breaking
  for designs that relied on `@group` appearing in the route**: move that
  segment into `@prefix`. Because the move changes where the (user-owned)
  service stub is generated, add `@group` before filling in business logic.

### Fixed

- **`craftgo fmt` no longer drops comments written between decorators.** A `//`
  comment placed between two service or method decorators (or between the last
  decorator and the keyword) was silently deleted on format; it is now
  preserved in place and round-trips idempotently.

## [1.3.0] - 2026-06-04

### Deprecated

- The DSL `import "<subfolder>"` statement is **deprecated and no longer
  required**. Cross-package types resolve automatically - reference a
  declaration from another folder by qualifying it with that package's name
  (`shared.Type`), and the codegen wires the matching Go import on its own.
  `import` lines are still accepted (so existing designs keep working) but are
  removed from all docs and examples and will be removed entirely in a future
  major release. The LSP resolves cross-package references with or without
  them.

### Fixed

- A **bare scalar or enum request type** (`request Token` where `Token` is a
  scalar/enum) is now rejected - a fieldless type has nothing to bind or decode
  as a body, so the payload was silently dropped (and a constraint-free scalar
  produced non-compiling Go). Wrap the value in a `type`.
- An **integral-float numeric bound** (`@gte(300.0)`) is now capacity-checked
  like its integer form, and `@default` on a `file` field is rejected - both
  previously produced non-compiling Go.
- An **auto-bound path field with a non-bindable type** (struct/map/array/
  generic) is rejected, matching the explicit `@path` form (it was silently
  dropped and emitted an invalid non-scalar path parameter).
- Repeated `@errors` decorators are no longer false-rejected as a duplicate -
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
  `@middlewares([A, B])`) are now honoured by codegen - they were accepted by
  the analyzer but silently contributed nothing, dropping error responses,
  tags, and the entire middleware chain. (`@security([...])` already worked.)
- **OpenAPI exclusive bounds intersect** instead of overwriting: stacking
  `@gt(5) @positive` (or `@lt(-5) @negative`) now advertises the tightest bound
  (`exclusiveMinimum: 5`) the order-invariant validator enforces, rather than
  the looser last-writer value.
- A request field diverted to `@query`/`@header`/`@cookie`/`@body` no longer
  satisfies the path-coverage check for a same-named `{segment}` - the segment
  is reported missing instead of producing an OpenAPI spec with no `in: path`
  parameter and a handler that never reads the value.
- Explicit `@path @default` is rejected (a matched route always supplies the
  segment), matching the auto-`@path` form, and a no-content success status
  (`@status(204)`/`304`/`1xx`) on a body-returning method is rejected.
- The multipart handler no longer emits an unused (or duplicate) `types`
  import when the request type lives in another package - it now carries the
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
  redundant pointer (`*Blob`) - the named slice already holds nil, exactly like
  a raw `bytes` field. The pointer decision is resolved once through the scalar
  table so the struct field, the validator nil-guards, and the field-level
  checks agree; a null / absent value still skips the scalar's own `Validate()`.
  A scalar over a nilable primitive can no longer be a cross-field-group member
  (its presence is emptiness, not a clean `!= nil`), matching raw `bytes` / `any`.
- A field-level `@doc` / `@example` on a field whose type is a **named ref**
  (a component `$ref`) is now carried onto an `allOf` / `anyOf` wrapper instead
  of being silently dropped - a bare `$ref` cannot hold sibling keywords
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
    the JSON body decode (previously skipped - the body was never read and
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
  (not appends) when present - `server.BindValues` resets before binding and the
  string paths are presence-guarded.
- An **enum-array** `@query` / `@header` parameter with a `@default` corrupted
  the default: the prefill resolved the member array (`@default([Red, Blue])` →
  `[]Color{ColorRed, ColorBlue}`) but the binder's has-default test could not
  (it routed the array literal through a converter with no enum-member case and
  returned "no default"), so the field used the bare appending shape - `?colors=
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
  field on a body-less verb (`get`/`delete`) - the latter previously slipped past
  the depth check and shipped non-compiling Go. The check is structural
  (independent of the element type), so cross-package element types are caught
  too. Single-level arrays and multi-dim arrays in the JSON body are unaffected.
- A cross-field group (`@requiresOneOf` / `@mutuallyExclusive`) referencing a
  field promoted by a **cross-package mixin** is no longer falsely rejected as
  "not a field of this type" - the per-package pass defers (it can't expand the
  foreign mixin) and the field set is resolved project-wide. The deferral is now
  backed by a **project-level re-check**: a member that no field provides -
  including a typo sitting alongside a legitimately-promoted one - is rejected at
  design time instead of slipping through to codegen, which substituted a literal
  `false` and emitted a validator that silently never fired (the whole group,
  De-Morgan'd, went dead). The re-check resolves nested cross-package mixins too.
  As a backstop, codegen now emits an undefined identifier (a loud `go build`
  failure naming the member) rather than `false` for any group member it still
  can't resolve, so a future resolver gap can't ship as a no-op validator. The
  re-check also re-applies the per-field quality rules to a cross-package-promoted
  member (must be optional / `@nullable`, not `@sensitive`, not wire-bound, not
  `@default`) - extracted into one shared helper both passes call - so a plain or
  otherwise-ineligible promoted member is rejected exactly as a local one is,
  rather than only its name being checked.
- A numeric **`@default` outside the field primitive's capacity** - a negative on
  an unsigned type (`uint @default(-5)`) or an out-of-range magnitude on a narrow
  int (`int8 @default(200)`) - is now rejected at design time. Codegen otherwise
  emitted a pre-fill cast (`uint(-5)` / `int8(200)`) that failed `go build` with
  `constant overflows`, and OpenAPI advertised the out-of-range default. The
  capacity check reuses the same range logic the numeric-bound guard uses.
- A **`@default` on a `bytes` field** is now rejected. A bytes value has no
  unambiguous literal form - the Go side needs `[]byte(...)` while OpenAPI's
  `format: byte` default is base64, and the only literal kind the gate accepted
  (string) compiled straight into the `[]byte` slot as a bare quoted string,
  which never built. `bytes[]` is rejected identically.
- A **multi-dimensional array `@default`** (`int[][]? @default([[1, 2], [3, 4]])`,
  `Color[][]? @default(...)`) is now rejected at design time. `@default` targets a
  primitive, scalar, enum, or a single-level array of those - a nested-array
  default has no real use and an exotic nested-literal form. The check is
  structural (array depth), so it fires for cross-package element types too.
  Single-level array defaults are unaffected.
- A required **`any @sensitive`** field made its endpoint reject every request
  with `400 … required`. A `@sensitive` field is `json:"-"` (dropped before
  decode), yet it still received the runtime presence check a required field
  gets - an unsatisfiable gate, since the client can never send the value. The
  presence check now excludes `@sensitive` fields, matching their exclusion from
  the wire body.
- A `@query` / `@header` / `@cookie` / `@form` / `@path` **binding rejection over
  a `map` field** rendered the offending type as a bare `?` (`got ?`); the
  diagnostic now renders the map type (`got map<string, int>`). The rejection
  itself was already correct.
- An **empty `@path("")` wire-name argument** no longer false-rejects the
  path-param check with a nonsensical `field ""` message - it falls back to the
  field name, mirroring the explicit-name fallback every other binding decorator
  already applies.
- A **cross-package qualified request type** (`request shared.Holder`) silently
  dropped every field of its bare nested mixins from the transport binder: a
  `@query` member never bound and a body member never decoded, while the
  validator (and the semantic path-param check, which derived the package
  correctly) still enforced them - so a conformant request failed validation
  against zero values. The request-field resolver now derives the flatten prefix
  from the qualified request name, so bare mixins resolve in the request type's
  home package, matching the semantic side.
- `@uniqueItems` over a **cross-package element that is only transitively
  non-comparable** - reached through a bare member of the foreign struct that
  itself holds a slice / map - was accepted, then emitted a non-compiling
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
  decl's fields - mirroring the same-package twin - so a `T` field is judged
  against its concrete argument; comparable instances (`Box<string>`) still pass.
- A required **cross-package enum body field** got no field-named presence check
  (only the enum's own value-set rejection), so an omitted field reported
  `"Sev: invalid Sev value"` instead of `"field: required"`, and the check ran in
  a different order than a local enum's. The required-check now resolves a
  qualified enum through the project resolver, matching the local-enum path. (The
  accept/reject decision was already correct - this is the diagnostic + ordering.)
- The project binding-type pass **double-visited every request body** (request
  types are already in the package's type set), emitting byte-identical duplicate
  diagnostics - N+1× for a type reused across N methods. The redundant second
  pass is removed; each binding error now reports once.
- The per-request / per-response codegen passes (field resolver, default
  pre-fill, **import collector**, **response header/cookie writers**) each
  resolved the method's type via the bare-keyed local `pkg.Types` and bailed on
  the qualified cross-package form, so a qualified type was silently dropped by
  one stage while a sibling emitted it. They now share one `lookupMethodType`
  helper (local then project resolver, with the home-package flatten prefix),
  fixing two more leaks:
  - a **qualified cross-package response** (`response shared.Resp`) now writes
    its `@header` / `@cookie` fields - previously the writers were dropped (the
    fields are `json:"-"`, so the values went to neither header/cookie nor body)
    while OpenAPI still advertised them;
  - a **qualified request whose field reaches a third package** (`request
    b.Holder`, `b.Holder.cid` typed `c.CID`) now imports that third package for
    the cast / `@default` pre-fill - previously the import was dropped →
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
  substitutes the outer type-args into a mixin ref before descending - the
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
  or a struct containing a slice - previously a non-compiling `map[T]struct{}`),
  and a `map` whose key is a bool / float / struct / bytes scalar (not a usable
  JSON object key). Each fires only on the qualified form, matching the
  bare/local behaviour without double-reporting.
- A **redundant self-qualification** (`design.Email` inside the `design`
  package) is now rejected with the bare-name fix, instead of emitting a
  self-import the package can't satisfy (`undefined: design`) and dropping the
  field's validator.
- Two mixins that **lower to the same Go embedded-field name** - a local `Leaf`
  and an imported `shared.Leaf`, or `shared.Leaf` and `other.Leaf` - are now
  rejected together with the exact-duplicate case; all would redeclare the field
  `Leaf` in the generated struct. (The duplicate-embed check now keys on the
  unqualified leaf name, not the dotted reference.)
- A **mixin embedded more than once** in one type body is now rejected at design
  time - the generated Go struct would declare the embedded type twice and fail
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
  `shared.Inner`) was silently dropped from the generated handler - it
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
  mixin resolution - the same flattening the codegen binder already does,
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
  the identifier - `type string @pattern(...)` and `enum Kind { type ... }`
  parse, and lower to exported Go fields (`Type`) with the keyword as the JSON
  tag.
- Optional non-string primitives bind to `@query` / `@header` / `@cookie`
  (`page int? @query`, `active bool? @header`, ...). The binder writes a
  pointer on presence and leaves it nil when the param is absent or empty
  (`?page=`); a present-but-unparseable value is a 400. Previously only
  optional strings were allowed on those sources.
- `@path` accepts any wire-bindable type - `int*` / `uint*` / `float*` /
  `bool`, or a scalar / enum over one - not just strings. `/users/{id}`
  with `id int` parses the segment through the same `server.Parse*`
  helper a numeric `@query` field uses; the OpenAPI path parameter is
  typed (`type: integer`, or a `$ref` to the scalar/enum) and the client
  follows. Optional and array `@path` fields stay rejected - a matched
  route always supplies exactly one value per segment.
- A required (non-optional, no-`@default`) single-value `@query` / `@header`
  parameter now returns 400 when its key is absent, matching the
  `required: true` the OpenAPI spec already advertised - previously the
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
  counts characters) and a Postgres `varchar(n)` - previously the validator
  rejected a value the spec advertised as valid. A `bytes` field still counts
  bytes (binary length, not advertised in OpenAPI); use `@maxBodySize` to cap
  raw network size.
- Wire-bind parse failures (`?page=abc`) and JSON body-decode failures now go
  through `server.WriteValidationError` - the same swappable hook as
  `req.Validate()` failures - so all request-input errors share one response
  envelope. The default hook still writes a plain 400.
- Generated handlers bind parsed primitives through the generic `server.Bind*`
  / `server.Parse*` helpers (one call per field) instead of an inline
  strconv block, so the handlers no longer import `strconv` / `errors` and
  shrink by ~a third. No reflection - the helpers are compile-time
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
  is now rejected by the semantic analyser - the same combination the
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
  - the dereferenced primitive local was treated as a pointer
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
  schema - mixins flattened via `allOf`, a generic instance `$ref`-ing its
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
  generated **non-compiling** `validate.go` - the map walk ran on the
  slice and called `Validate()` on a whole map. The array dimensions are
  now peeled before the map is ranged.
- A `@nullable` string carrying `@format` (`a string @nullable
@format(email)`) dereferenced the pointer without a nil-guard, panicking
  the validator on `{"a": null}`. The guard now keys on the field's
  pointer-ness, not just the `?` suffix.
- Integer bounds beyond 2^53 (`@gte(9007199254740993)`,
  `@gte(math.MaxInt64)`) lost precision in OpenAPI - the float64 the spec
  carried rounded, so the advertised bound disagreed with the exact int64
  the validator enforced (at the extreme an unsatisfiable spec). Large
  integer bounds - and `@multipleOf` divisors - now emit as exact
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
  mixin-promoted field - the validator resolves it through field promotion
  instead of emitting a no-op check.
- Embedding an instantiated generic as a mixin (`type Host { Page<Item> }`)
  generated a struct embedding the bare, un-instantiable `Page` (a Go
  compile error) and a dangling OpenAPI `$ref`. The embed now monomorphises
  to `Page[Item]` - which Go accepts and promotes the fields of - and the
  schema `allOf`-refs the `PageOfItem` component.
- A `@sensitive` field with no explicit binding auto-promoted to `@query`
  on a non-body verb - reading a server-only value from the URL and adding
  a required parameter the OpenAPI never documents. The binder now skips
  `@sensitive` fields entirely, matching the `json:"-"` / schema exclusion.
- `@multipleOf` on a float scalar, `@pattern` / `@format` on a `bytes`
  field or scalar, and any value constraint (`@gte`, `@maxLength`, …) on a
  generic type-parameter field are rejected at design time: each was
  advertised in OpenAPI but unenforceable by the generated validator.
- A field whose Go field-name equals an embedded mixin's type name
  (`type Host { Pagination  pagination int }` - the embed and the field
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
  Go field while the validator nil-guarded it - non-compiling Go - and
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
  defaulted field - wire param or body - is no longer marked required, so
  the spec stops contradicting the `default` it carries.
- A required `@cookie` was advertised `required: true` but the transport
  never enforced presence. A required cookie now returns 400 when absent,
  matching `@query` / `@header` (a present-but-empty value still passes).
- A user-declared `code` / `message` error field - which is marshalled on
  the wire and validated - was excluded from the OpenAPI error schema
  along with its constraints. It is now emitted like any other property.
- A constraint declared on the element of a composite generic argument
  (`Page<map<string, Item>>`, `Page<Item[]>`) was advertised in OpenAPI
  but never enforced: the parametric `any(x).(Validate)` probe can't reach
  a map / slice element. A generic type-parameter validator now falls
  back to a reflection walk (`validateValue`) that validates each leaf -
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

- A map keyed by a non-comparable type - a generic type-parameter
  (`map<K, V>` → `map[K any]`) or a struct / generic containing a slice /
  map / `bytes` - was accepted by gen but emitted Go that does not compile
  (`invalid map key type`). The key's comparability is now checked at
  design time, like `@uniqueItems` element comparability.
- A route template repeating a path variable (`/items/{id}/x/{id}`) and
  two fields binding to the same wire name on one source (`a @query("x")
b @query("x")`) are rejected: the first panics net/http's ServeMux at
  registration, the second emits a duplicate OpenAPI parameter.
- `@requiresOneOf` / `@mutuallyExclusive` over a wire-bound (`@query` /
  `@header` / `@cookie`) or `@default` member is rejected - the wire field
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
  longer emits `propertyNames: {type: integer}` - a conformant 3.1
  validator rejects it because JSON object keys are strings. A numeric key
  bound has no consumable spec form, so it is left to the runtime; a
  string scalar key still carries its length / pattern / format.
- `craftgo fmt` no longer silently drops or corrupts comments: a trailing
  `//` on a non-last decorator in a multi-line chain (`@minLength(1) //
note` above `@maxLength(5)`) folded the following decorators into the
  comment - **deleting a real constraint** - and now lands the comment at
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
  items stay a non-null `$ref` - the two stages disagree, and the AST's
  single optionality flag can't distinguish "array of nullable element"
  from "nullable array". Declare the nullability on a concrete field of the
  generic instead (`type Box<T> { item T? }`, used as `Box<Item>`), where
  it lowers to a clean pointer on both sides.

- An error body that embeds a mixin now validates the mixin's promoted
  fields. The body struct embeds the mixin and the OpenAPI `allOf`
  advertises its constraints, but `<Error>Body.Validate()` skipped them -
  it walked the error's direct fields only. It now dispatches to the
  mixin's `Validate()`, the same as any other type that embeds a mixin.
- `@multipleOf` with a fractional divisor (`@multipleOf(2.5)`) on an
  integer field or scalar is rejected. Go's modulus is integer-only, so
  the validator can't enforce a fractional divisor while the OpenAPI
  advertises it - the same rule already applied to a whole-valued float
  literal is now extended to a genuinely fractional one.
- A `@requiresOneOf` / `@mutuallyExclusive` member that is `@sensitive`
  (server-only, `json:"-"`, excluded from the schema) or whose Go type is
  nilable-but-not-a-pointer (a slice / map, or a raw `bytes` / `any`) is
  rejected. A `@sensitive` member names a property the public schema never
  carries; the nilable-non-pointer members have no clean `!= nil` presence
  check - a slice / map is checked by emptiness (`len(...) > 0`, so an empty
  `[]` / `{}` reads as absent) and a `bytes` / `any` member is always treated
  as present - so both diverge from the group's OpenAPI present-and-non-null.
  (A pointer-backed field - string, number, bool, struct, enum, or a scalar -
  is the unambiguous case the group needs.)
- A map keyed by a type that compiles but `encoding/json` can't marshal
  as an object key - `bool`, `float*`, or a scalar over them - is now
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
  and the spec - the bound was corrupted identically on both sides, so it
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
spec - all from one source.

### DSL

- `type`, `scalar` (with validators that inherit to every field of that type),
  `enum` (string- and int-backed), and typed `error` categories.
- Generics (`Page<User>`), cross-package references (`import` + `pkg.Name`),
  and mixins (cross-package field composition).
- `service` blocks with `extend service` for splitting a service across files;
  per-block decorator inheritance with `@ignoreMiddleware` / `@ignoreSecurity`
  / `@ignoreTags` opt-outs.

### Validation

- Declarative validators compiled to plain Go `if` statements - no reflection,
  no runtime struct tags. `Validate()` is fail-fast.
- String: `@length`, `@minLength`, `@maxLength`, `@pattern`, `@format`.
- Numeric: `@gt`, `@gte`, `@lt`, `@lte`, `@range`, `@positive`, `@negative`,
  `@multipleOf`.
- Array: `@minItems`, `@maxItems`, `@uniqueItems`.
- File upload: `@maxSize`, `@mimeTypes`.
- Cross-field: `@requiresOneOf`, `@mutuallyExclusive`.

### Wire binding

- `@path`, `@query`, `@header`, `@cookie`, `@body`, `@form` - including
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

- `pkg/server`: a thin `net/http` wrapper - `*http.ServeMux`, a middleware
  `Chain`, a swappable JSON codec, health checks, CORS, and per-method timeout
  / body-size limits.
- `pkg/log`, `pkg/metrics`, `pkg/otel` for logging, metrics, and tracing.

### Tooling

- CLI: `craftgo init`, `craftgo gen`, `craftgo fmt`.
- Language server (`craftgo-lsp`) and a VS Code extension: completion, hover,
  go-to-definition, rename, live diagnostics, and formatting.

[1.0.0]: https://github.com/craftgodotdev/craftgo/releases/tag/v1.0.0
