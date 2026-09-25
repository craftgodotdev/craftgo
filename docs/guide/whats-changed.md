---
title: What changed
description: What is new since craftgo 1.7.1 - events in the DSL, the event runtime, the generated wiring call, the manifest keys that are now rejected, and what to do when you upgrade.
---

# What changed since 1.7.1

Grouped by where it lands, from the
[changelog](https://github.com/craftgodotdev/craftgo/blob/main/CHANGELOG.md).
Skip to [Upgrading](#upgrading) if you only need the steps.

## DSL

- `event Name { payload T }` declares a contract at file level; `@contract("subject")` sets its wire identity, which defaults to `<package>.<Event>`.
- A payload may be an array of a declared type, `payload T[]`: the descriptor is typed on the slice and every element is validated in turn.
- A `service` holds HTTP methods only, and the design names no listener - which events a deployable listens to, on which group, behind which middleware, is Go code in that deployable.
- Two reserved words, `event` and `payload`, are still usable as identifiers where unambiguous, as is every other reserved word.
- `datetime` is a `time.Time` in Go and an RFC 3339 string in JSON (`format: date-time`); body fields only, no validators, no `@default`.
- `@json("key")` on a field sets the JSON key when it is not the field name; the Go tag, the OpenAPI document and validation messages follow it.

## Generated output

- One file per DSL package that declares an event, `events.go` under `events.targets[].out`: a `<Name>Contract` constant and an `events.Event[T]` descriptor per event, its `@doc` as the Go comment.
- `output.kind: contracts` generates only payload types and this library, for a design several deployables import.
- `main.go` attaches the design through one generated call, `wiring.Register`, in a `wiring` package (`output.wiring`) whose surface does not change with the design; the scaffold no longer lists routes.
- A design package that declares no type gets no types package, and `validate.go` is written only where a type has something to validate.
- Stale output is pruned: inside the output directories the manifest names, every file carrying a generated header that the run did not write is deleted, and emptied directories with it.

## Runtime

- `pkg/events` is a new module: `Bus` (`Use`, `Register`, `Start`, `Plan`), typed `Group`, `Event[T]` descriptors that decode and validate before a handler runs, publish options, batch publishing, dispositions, a JSON codec, an in-process transport and access-log middleware.
- `pkg/events/nats` adds core NATS and JetStream, with one durable per group carrying the group's filter subjects, per-group settings, redelivery backoff, a delivery cap and NAK hand-back during a rolling deploy.
- `pkg/events/kafka` (franz-go, Go 1.25) adds classic and share groups, TLS and SASL options, `WithClientOptions` and `RecordFrom`.
- `log.Slog()` returns a `*slog.Logger` writing through craftgo's own logger.

## Manifest

- Output keys are checked as a set: `output.transport` may be named anything, two keys may not name one directory, and `-` is rejected on a key that has no disabled mode.
- The routes umbrella is removed with the last route.
- `events.targets[]` names the languages event artefacts are generated for; omit the block and a design that declares events gets one Go target at `./internal/events`, or `./gen/events` under `output.kind: contracts`.

## Removed, or never shipped

![The design owns the event declaration, the payload types and one folder per version; the group, the middleware and the folder layout belong to the application.](/diagrams/responsibilities.svg)

The strip under the diagram lists what the design no longer carries; none of it ever reached a release.

- `pkg/otel` and `pkg/metrics` are gone - alias-only shims over `pkg/telemetry`; import `pkg/telemetry` directly.
- The listener declaration, the decorator that named its group, the decorator that named its ordering key and the one that named its middleware chain never shipped: the group is an argument to `Subscribe`, the key an argument to `Publish`, the chain `bus.Use(...)` where the bus is built.
- Projections, the AsyncAPI document and the `.craftgo-gen/` bookkeeping folder never shipped either.

## Fixed

- `craftgo-lsp` exits on `exit`, with status 1 when no `shutdown` preceded it, instead of waiting for the client to close its stdin.
- The editor reports what `craftgo gen` reports, and a design file without a `package` declaration resolves the same way in both.
- A type whose fields all delegate to another package compiles.
- A `@group` whose name ends in `time`, a service named `Craft`, a package whose name ends in `types`: none breaks its generated files.
- `@security` scheme names are listed in a stable order.

## Upgrading

**Drop the manifest keys that are gone.** `design`, `output.services`, `output.consumeMiddleware` and `events.asyncapi` are rejected while the manifest is read, each naming what replaced it (`<key> is no longer a manifest key - <note>; drop it`). Any other key the manifest does not declare only gets a warning.

**Delete every `.craftgo-gen/` folder.** Nothing reads them. The `// Code generated by craftgo. DO NOT EDIT.` header is the whole record now: at the end of a run craftgo deletes every headed file in its output directories that the run did not write.

**`wiring.Register` is optional for an existing `main.go`.** It is generated for every project, but `main.go` is gen-once and is never rewritten, so a project that already registers its routes keeps working unchanged. Switch the registration line to `wiring.Register(ctx, srv, svcCtx)` to pick up the startup check that fails for every middleware the design applies and `svcCtx` leaves nil.

**`datetime` and `@json("key")` are available.** Both are body-field features; see [Types and Scalars](/guide/types-and-scalars).

**Nothing else needs doing for events.** A design that declares no `event` generates nothing new. When you add one, read [Events](/guide/events) - the contract goes in the design, and the group, the middleware and the transport go in the deployable.
