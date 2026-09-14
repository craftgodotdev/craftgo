---
title: Events
description: Declare event contracts and consumers in the DSL, and generate the contract descriptors and handler interfaces an application publishes and consumes through.
---

# Events

craftgo generates event code the way protoc generates gRPC code: payload types and
their validation, one **descriptor** per contract, one **handler interface** per
consuming service. Everything about delivery - which group a consumer joins, which
middleware wraps it, which deployable runs it - is the application's, and reaches
the generated code as an argument.

## The model

The DSL declares a **contract** (`event`) and who handles it (`consume`). craftgo
turns the first into a **descriptor** - publish, decode, validate, build a
subscription - and the second into a **handler interface**. The group a consumer
joins, the middleware around it and the transport under it are your Go code's. The
generated library imports the event runtime and the payload types and nothing else,
so one contract package serves every deployable that imports it, each wired to its
own bus: the design is a contract catalogue plus a set of interfaces, and it never
names a broker, a group, a codec or a process.

## Declaring events

```craftgo
package orders

// Emitted once an order is accepted.
event Placed {
    payload OrderPlaced
}
```

An `event` may be written at file level or inside a `service` body; the position
changes nothing about what is generated. `payload` must name a `type` declaration,
so every contract has named fields and its own `Validate()`.

A contract's wire identity defaults to `<package>.<Event>` - `orders.Placed` above -
and `@contract("order.placed.v2")` overrides it to interoperate with a name another
system already publishes. Both sides address the contract by that string, and how a
transport maps it onto a topic, subject or queue is the transport's business. Two
events resolving to one identity are rejected at design time.

A consumer is a `consume` block inside a service, and each one becomes a method
on that service's handler interface:

```craftgo
package notifications

service Notifier {
    // Emails the customer a receipt.
    consume SendReceipt {
        event orders.Placed
    }

    consume SendDispatchNote {
        event orders.Shipped
    }
}
```

`event Ref` is bare for a contract this package declares and `pkg.Event` for
another package's. Consume names are unique within their service. Nothing else
about a consumer is declared: a group or a middleware chain in the design would
tie the shared contract to one deployable's operations. The only event decorators
are `@contract`, `@doc` and `@deprecated` - the ones that used to name a
consumer's broker group, its middleware chain or a payload's ordering key are
gone, and a design still carrying one is told what replaces it.

## What is generated

One directory per DSL package under the Go event target, named after the package:
`events.go` when the package declares an event, `handlers.go` when a service consumes one.

```go
// <events out>/orders/events.go - one constant and one descriptor per event
const PlacedContract = "orders.Placed"

var Placed = craftevents.NewEvent[types.OrderPlaced](PlacedContract, (*types.OrderPlaced).Validate)
```

```go
// <events out>/notifications/handlers.go - one set per consuming service
type NotifierHandler interface {
	// Emails the customer a receipt.
	SendReceipt(ctx context.Context, payload *orders.OrderPlaced) error
	SendDispatchNote(ctx context.Context, payload *orders.OrderShipped) error
}

type NotifierGroups struct {
	Default          craftevents.Group
	SendReceipt      craftevents.Group
	SendDispatchNote craftevents.Group
}

func RegisterNotifierHandler(bus *craftevents.Bus, h NotifierHandler,
	chain craftevents.Chain, groups NotifierGroups) error
```

`Register` makes one `bus.Register` call per consume, the consume's name as the
consumer name and the descriptor building the subscription; a `Groups` field left
empty falls back to `Default`, and `Default` empty with a field missing is an error
naming the service. That is everything: payload types keep their own package under
`output.types`, and there is no publisher type, no transport adapter, no logic stub,
no middleware scaffold and no event wiring - `wiring.Register`, `svccontext` and
`main.go` keep only their HTTP duties.

## Publishing

The descriptor publishes, and takes the bus at the call:

```go
err := orders.Placed.Publish(ctx, bus, &types.OrderPlaced{OrderID: order.ID, Total: order.Total},
	craftevents.WithKey(string(order.ID)))
```

`Publish` validates first: a payload that does not validate is a `*PayloadError` and
nothing goes on the wire - the contract is refused where it is broken rather than at
every consumer. The trailing options fill in everything beside the payload - `WithKey`
(the entity a message is about, used by a transport that orders per entity),
`WithDedupID`, `WithHeader`, `WithAdapterOption`. `WithPublishDefaults` on the bus
sets options every publish starts from, and per-call options are applied after, so
they win. `bus.PublishAll(ctx, envs)` publishes a batch - the shape an outbox drains -
reporting a partial failure as a `*PartialPublishError` whose `Unsent` holds the
indices that did not go out; `bus.Publish(ctx, contract, payload, opts...)` is the
untyped path for a contract with no descriptor to hand.

## Consuming

Implement the interface on one struct per module, then register and start:

```go
if err := notifications.RegisterNotifierHandler(bus, notifier.New(svcCtx),
	craftevents.NewChain(deadLetter, retry, timeout),
	notifications.NotifierGroups{Default: OrderWorker}); err != nil {
	return err
}
return bus.Start(ctx)
```

`Register` records a subscription and checks what the bus can check on its own: a
handler, a group, a codec for the contract, the dispositions the bus requires, and
no earlier subscription for the same contract and group. `Start` hands the whole
batch to the transport in **one** call, sorted by group, contract then consumer,
every handler wrapped; whether the broker accepts the set is answered there. A
second `Start`, or a `Register` after one, is `ErrStarted`, including after a
`Start` that failed - the transport may already have taken part of the batch.

### Groups

A group is the broker identity a subscription consumes under: the Kafka consumer
group, the NATS queue group, the JetStream durable. Subscriptions sharing one
divide the stream between them, so a group is the unit of scaling and of failure
isolation - not of ordering, which no transport here gives across contracts.

`events.Group` is a named type so an application declares its groups **once, in one
file** - `const OrderWorker craftevents.Group = "order-worker"` in a `groups.go` per
deployable - and hands them around as values rather than as loose strings.

There is no derived default and no fallback: `Register` refuses an empty group. On
a transport that remembers a position per group - Kafka, JetStream - the name is
where those consumers resume, and one the broker has never seen has no position at
all: **if it has an offset, write the name down**. Core NATS keeps no position, so
there the name only decides who competes for a message. Two deployables of one
design need not agree on a group, which is why the name is not in the design.

### Middleware

A consumer middleware is ordinary Go, never a declaration:

```go
type Middleware func(sub craftevents.Subscription, next craftevents.Handler) craftevents.Handler
```

`sub` carries the contract, the consumer and the group being wrapped, so one chain
can behave differently per group. Chains compose **outermost first**:
`NewChain(A, B, C)` wraps a handler as `A(B(C(h)))`, so a message flows A → B → C →
handler and the return travels back in reverse. `events.WithMiddleware(mws...)` on
the bus wraps every subscription registered through it, whichever design built it;
a subscription's own `Chain` - the `chain` argument the generated `Register` takes
- is applied **inside** that, so a bus-wide concern like logging still sees what a
per-consumer chain did. `Start` recovers on both sides of the bus chain, so a
panicking handler reaches your middleware as an ordinary `*PanicError` and a panic in
the chain itself is caught too; `events.Recover()` is only for a chain you fold
yourself with `Chain.Apply`, which the bus wraps from outside as one opaque handler -
put it at that chain's innermost end. `pkg/events/logging.AccessLog(logger)` is the
shipped middleware.

Three failures arrive at a chain with a type of their own. The descriptor decodes and
validates before your method runs, so a payload that fails either is a
`*PayloadError`, which a chain picks out with `errors.As` and gives up on rather than
retries - the same bytes fail the same way on every delivery. A message stamped with a
codec the consumer is not configured for is `ErrCodecMismatch` instead: a
configuration mistake, not a poison payload. A handler that panicked is a `*PanicError`
carrying the contract, the consumer, the group and the stack. Nothing else is
classified - what to do with a failure is the chain's decision.

## Dispositions

A middleware can ask for something other than "done", through the message, because
a decision about one delivery is not a value the transport carries:

| call | what it asks for |
| --- | --- |
| `msg.Settle()` | take this delivery as done |
| `msg.Redeliver()` | hand it back; the same message returns |
| `msg.Reject()` | give it up - no attempt will handle it |

Asking for nothing leaves `DispositionUnset`, which settles. `msg.Deliveries()` is the
broker's count of how many times it has handed this message over. The chain returns
innermost first, so the outermost middleware decides last and can clear what
everything below it asked for; decide from the handler's goroutine, before the chain
returns.

::: danger Only some transports can honour this
Redeliver and reject need the broker to track each record. JetStream and a Kafka
share group can; a classic Kafka consumer group, core NATS and the in-process
transport cannot, and there **`Redeliver` settles instead** - losing every message
the chain meant to retry, silently. Name the disposition you need at the bus with
`WithDispositionRequired` and `Register` refuses on a transport that cannot honour
it, which reaches you as a boot failure. An adapter says what it can do by
implementing `events.Dispositioner`.
:::

## Wiring the bus

Two runtime choices, made where the application starts: the transport that moves the
messages, the codec that encodes them.

```go
bus := craftevents.New(
	craftevents.WithTransport(js),
	craftevents.WithCodec(codecjson.Codec{}),
	craftevents.WithMiddleware(logging.AccessLog(logger)),
	craftevents.WithDispositionRequired(craftevents.DispositionRedeliver),
)
```

There is no default codec - a bus built without one fails rather than picking an
encoding - and `WithCodecFor(contract, c)` overrides it for a single contract.

## The plan

No generated file states what a deployable consumes any more - the groups are the
application's - so `bus.Plan()` reports it instead, before or after `Start`:

```json
{ "groups": [ { "name": "order-worker", "consumers": [
  { "event": "orders.Placed", "consumer": "SendReceipt" },
  { "event": "orders.Shipped", "consumer": "SendDispatchNote" } ] } ] }
```

Groups are ordered by name and consumers by contract then consumer, and
`MarshalJSON` renders that order whatever order the plan was built in. Pin it in a
golden file and a rename, a lost consumer or a group that drifted between two
deployables fails a test rather than a deploy.

## NATS JetStream

`nats.NewJetStream(conn, opts...)` consumes from a stream, and is what makes
`Redeliver` and `Reject` mean something on NATS. Its wire format is the core
transport's, so a message published through either arrives identically; it is a
separate type because a JetStream delivery is not a `*nats.Msg` - reach it with
`nats.JetStreamMsgFrom(ctx)`.

### One durable per group

A group is the durable consumer's name, and one durable filters every subject the
group consumes; a delivery is dispatched to its consumer by subject. Replicas sharing
a group share the durable and divide its work, and the position the group reached
stays under its name. A durable reads one stream, so a group's subjects have to sit
on one: a group spanning two streams is refused at start-up, naming both, as are a
group claimed twice in one process and a duplicate subject within a group.
`Bus.Start` hands the whole batch over at once, which is what a durable filtering
several subjects needs.

### Adopting a durable that exists

An existing durable is verified, not reshaped: its filter set is compared with the
plan this process carries.

| carried filter vs plan | what happens |
| --- | --- |
| equal | adopted as is |
| a strict subset of the plan | re-pointed and logged - this version added a consumer, nothing stops being consumed |
| anything else | **refused**, naming both sets |

"Anything else" is a plan that drops subjects, one that only partly overlaps, or a
durable carrying no filter at all - every subject on its stream. Re-pointing any of
those stops those subjects reaching anyone, so it is refused unless the group asks for
it with `nats.AllowNarrow()`, which is what a deliberate removal of a consumer looks
like. A durable that does not acknowledge explicitly is refused too; one that does not
exist is created and logged at Info with its config.

### Per-group settings

Delivery settings belong to the group, because the group is what the server keeps
them on: a durable consuming a slow contract wants a different `AckWait` from one
consuming a fast one, and both may run in the same process.

```go
js, err := nats.NewJetStream(conn,
	nats.WithGroupConfig(PriceVariants, nats.DeliverPolicy(jetstream.DeliverNewPolicy),
		nats.MaxInFlight(8), nats.AckWait(2*time.Minute)))
```

`MaxInFlight`, `AckWait`, `DeliverPolicy`, `ConsumerConfig` and `AllowNarrow` are
the group options, and repeated calls for one group accumulate. The last three
apply when the durable is **created** - where a consumer resumes is not something a
redeploy may move. `WithMaxInFlight` and `WithAckWait` stay as the transport-wide
defaults for every group that does not name its own.

### Prefetch, ack wait and redelivery

`MaxInFlight` is how many messages one durable's pull keeps buffered in this
process. The default is 1, deliberately: messages are handled one at a time, so a
buffered message waits for every handler ahead of it with the server's `AckWait`
clock already running. **Raise it only where `n` × the slowest handler stays under
`AckWait`** - otherwise a message is redelivered while it still sits in the buffer,
a duplicate nothing reports.

`WithMaxDeliveries` (default 5, zero unbounded) caps a redelivery loop the chain keeps
asking for; the message is terminated once the server's count reaches it, and a
delivery that succeeds on the last attempt is still taken as done.
`WithRedeliverBackoff(fn)` delays a redelivery by `fn(deliveries)` - without one a
transient failure burns the whole cap in milliseconds.

### Rolling deploys

During a rolling deploy two versions of the design share a durable. A subject no
consumer in this process handles is **handed back** - NAK'd with the group's `AckWait`
as the delay - so the replica that does consume it gets it, and never terminated: the
transport drops no message. Every hand-back is reported through
`WithJetStreamErrorHandler` with only `sub.Group` set, the failure belonging to the
group rather than to one consumer.
## Kafka, core NATS and memory

- `pkg/events/kafka` maps one contract to one topic, with the ordering key as the
  record key. The default is a classic consumer group, which can only take a delivery
  as done; `WithShareGroup` asks for a KIP-932 share group, where `Redeliver` and
  `Reject` mean something. The mode is never detected - a broker that cannot serve
  one is refused rather than quietly consumed as a classic group.
- `pkg/events/nats` (core) maps a contract to a subject and a group to a queue group.
  It delivers at most once and has no nack, so install `nats.WithErrorHandler` or a
  failed message is observed by nothing.
- `pkg/events/memory` is the in-process transport for tests and single-binary
  deployments; `Drain()` waits for in-flight deliveries. All three take the batch
  `Subscribe` and loop over it.
## Configuration

```yaml
events:
  targets:
    - lang: go
      out: ./internal/events
```

Go is a row in `targets`, not an implied default: the manifest states where the event
artefacts land the same way it would for any other language, and the Go target places
them through `output:`, so it rejects a per-target `layout:` rather than ignoring it.
Omit the block and a design that declares events gets one Go target at
`./internal/events` - or `./gen/events` under `output.kind: contracts`, since Go
forbids importing `internal/` across modules. A design that declares none generates
nothing either way, and `out: "-"` skips the target. Transport and codec are
deliberately absent: they are runtime wiring, not design-time facts. See
[Configuration](/guide/configuration) for the rest of the manifest.
