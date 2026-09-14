---
title: Events
description: Declare event contracts and consumers on a craftgo service, and generate the publisher, the subscriptions and the logic stubs.
---

# Events

A craftgo service publishes an HTTP API and, when you declare them, **event
contracts**. The design says what an event *is*; craftgo generates the typed
publisher, the consumer subscriptions, and the logic stub each consumer needs.

```craftgo
@prefix("/v1")
service OrderService {
    post PlaceOrder /orders {
        request  PlaceOrderReq
        response Order
    }

    // Emitted once an order is accepted.
    event OrderPlaced {
        payload OrderPlacedPayload
    }
}
```

```craftgo
package notifications

service NotificationService {
    // Emails the customer a receipt.
    consume SendReceipt {
        event orders.OrderPlaced
    }
}
```

That is the whole design surface. There is no publisher declaration: the
publisher is derived from the contract, so producer and consumer can never drift
apart.

### One event, several tasks

Each consumer joins a **consumer group** - the identity the broker knows it by.
Every group receives every message, and replicas sharing a group split the work
between them, so one event drives as many independent tasks as you declare
consumers for it:

```craftgo
package orders

service Orders {
    event Placed { payload OrderPlaced }

    // Rides in the same binary as the other Orders consumers.
    consume RecordPlaced { event Placed }
}

// Its own service, so it can be deployed and scaled on its own.
service OrderNotifier {
    consume SendConfirmation { event orders.Placed }
}
```

`RecordPlaced` and `SendConfirmation` each see all of `orders.Placed`, as do the
consumers other packages declare on it: each has its own group. A service
consumes a given contract once, and two consumers of one contract may not share
a group - they would split the stream instead of each receiving it, and craftgo
rejects that. [Consumer groups](#consumer-groups-if-it-has-an-offset-write-the-name-down)
below covers naming one yourself.

## What gets generated

Events follow the same layer split as the HTTP side.

```
design/*.craftgo ──craftgo gen──▶ internal/events/<svc>/publisher.go       typed publisher
                                  internal/events/<svc>/consumers.go       handler interface + subscriptions
                                  internal/transport/<svc>_consumers.go    handler set bound to logic
                                  internal/service/<seg>/<name>.go         logic stub (you edit)
                                  internal/transport/events.go             SubscribeAll
                                  svccontext/events.go                     the publishers container
                                  docs/asyncapi.yaml                       AsyncAPI projection

Everything under the events output imports only the event runtime and the
payload types - never the transport, the service stubs or the
`ServiceContext`. That is what lets you point `events.targets[].out` and
`output.types` at one folder and publish it as a library. `SubscribeAll`
is application wiring, so it lives at the transport root instead.
```

`<seg>` is the service's directory, or its `@group` when it declares one - the
same segment the HTTP artefacts use. The consumer stub lands next to your method
stubs because it *is* service logic.

The handler set lands at the ROOT of the transport layer, named after the
service rather than the segment. `@group` may be declared per `extend service`
block, so one service's consumers can end up in two logic packages, while the
contract declares a single `Consumers` interface covering all of them - only a
package outside both can satisfy it. `@group` still places the logic stubs it
was asked to place; it just no longer splits the handler set.

`@group` applies to consumers, which are per-member files, but **not** to the
publisher: a publisher is one file for a whole service, so it always lands in the
service's own directory. Two services sharing a group would otherwise claim the
same filename.

## Publishing

The generated publishers hang off your `ServiceContext` on its `Events` field,
so logic publishes with one call:

```go
func (l *PlaceOrderService) PlaceOrder(req *types.PlaceOrderReq) (*types.Order, error) {
	order, err := l.svcCtx.Store.PlaceOrder(req)
	if err != nil {
		return nil, err
	}
	if err := l.svcCtx.Events.OrderService.PublishOrderPlaced(l.ctx, &types.OrderPlacedPayload{
		OrderID: order.ID,
		Total:   order.Total,
	}, craftevents.WithKey(string(order.ID))); err != nil {
		// The order exists; a failed announcement is worth a line in
		// the log, not a 500 to the caller.
		l.Error("publish OrderPlaced", log.Err(err))
	}
	return order, nil
}
```

The trailing arguments are [publish options](#publish-options). `WithKey` is
the one most publishes want: it is what keeps one order's events in order on a
transport that can do that. Drop it and the message is keyless, which is the
right default for a contract nothing needs ordered.

### Publishing several at once

A batch reaches the transport in one call, and may mix contracts from
different services - which is the shape an outbox drains:

```go
b := l.svcCtx.Events.Batch()
b.OrderService().OrderPlaced(placed)
b.InventoryService().StockReserved(reserved)
if err := b.Publish(l.ctx); err != nil {
	return err
}
```

One call is all it promises. A transport that implements
`events.BatchPublisher` receives the whole slice; one that does not gets the
messages one at a time, in order. Either way the batch is **not** a
transaction: Kafka groups by topic-partition, an SQS batch is capped at one
queue, and whether anything arrives atomically is the transport's business.

Encoding happens before anything is sent, so a payload that cannot be
encoded fails the whole batch without a partial publish.

On a transport without the batch upgrade, a failure partway leaves the
earlier messages **already sent**. The error is a `*events.PartialPublishError`
naming exactly which envelopes did not go out:

```go
if err := b.Publish(ctx); err != nil {
	var partial *craftevents.PartialPublishError
	if errors.As(err, &partial) {
		// partial.Unsent holds the indices that did not go out, so
		// retrying exactly those sends nothing twice.
		retry := make([]craftevents.Envelope, 0, len(partial.Unsent))
		for _, i := range partial.Unsent {
			retry = append(retry, envs[i])
		}
	}
	return err
}
```

A successful `Publish` empties the batch, so calling it twice sends nothing
the second time instead of replaying. A batch is not safe for concurrent
use - build it on one goroutine.

### Message metadata

Every transport carries side-band values beside the payload - Kafka record
headers, NATS headers, a copied map in-process. A publisher sets them with
`WithHeader`, on the ordinary publish:

```go
if err := l.svcCtx.Events.OrderService.PublishOrderPlaced(l.ctx, placed,
	craftevents.WithKey(string(placed.OrderID)),
	craftevents.WithHeader("hops", "1")); err != nil {
	return err
}
```

The batch builder takes the same options per entry, and
`svccontext.NewEvents(bus, opts...)` takes them as defaults for every message
the application publishes - see [publish options](#publish-options).

A [contract declared outside a service](#a-contract-you-do-not-publish) has no
typed publisher and no exported contract constant, so the bus takes its wire
name directly - the same options apply:

```go
if err := l.svcCtx.Events.Bus.Publish(l.ctx, "payments.settled.v1", settled,
	craftevents.WithKey(string(settled.OrderID))); err != nil {
	return err
}
```

::: warning Some keys are the runtime's, not yours
`events.IsReservedMeta(key)` names them in one place:

| Key | Owner |
| --- | --- |
| `content-codec` (`events.MetaCodec`) | the runtime - the codec that encoded the payload |
| `craftgo-event` | the Kafka adapter - the contract name |
| `craftgo-key` | the Kafka adapter - the ordering key |
| `Craftgo-Key` | the NATS adapter - the ordering key |

Anything under `events.MetaPrefix` (`craftgo-`) is reserved for adapters,
case-insensitively. **An entry under a reserved key is dropped silently** and
the runtime's or the adapter's own value takes its place, so a publisher cannot
forge the codec stamp or rename its own message. Every other key is carried
untouched.
:::

Reading it back is the consumer chain's job, not the stub's: a generated
consumer is handed the decoded payload, while a
[middleware](#consumer-middleware) is handed the `Message`.

```go
func HopGuard(max int) craftevents.Middleware {
	return func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			if hops, _ := strconv.Atoi(msg.Metadata["hops"]); hops > max {
				return errors.New("too many hops")
			}
			return next(ctx, msg)
		}
	}
}
```

A transport may drop entries it has nowhere to put, so only `content-codec` is
guaranteed to survive in general; the three adapters craftgo ships carry every
entry both ways.

### The dual write

Writing to your store and publishing are two operations with no shared
transaction. A crash between them leaves an order nobody was told about;
returning the publish error to the caller leaves an order the caller
thinks failed. craftgo does not solve this - there is no outbox, and the
publish is a plain call.

Pick the failure you can live with and make it explicit. The common
answers are: log and continue (above), publish from an outbox table
written in the same transaction as the order, or make the consumer
tolerate a missing event by reconciling against the API.

## Consuming

The stub is yours; the framework has already decoded the payload and run its
`Validate()` before you see it, the same way an HTTP handler binds and validates
a request.

Alongside the stub, each consuming service gets a library-side API next to its
publisher - a handler interface and a subscription builder that take the
handlers as an argument:

```go
type Consumers interface {
	SendReceipt(ctx context.Context, payload *types.OrderPlacedPayload) error
}

func Subscriptions(bus *craftevents.Bus, h Consumers) []craftevents.Subscription
```

Nothing in it touches your `ServiceContext`, so a different codebase can import
it and consume the contract without inheriting your application.

Decoding and validating live in `Subscriptions`, and nowhere else. Your own
application reaches the same builder: `internal/transport/<svc>_consumers.go`
holds a `<Svc>Consumers` implementing that interface by forwarding each payload
to its stub, and `SubscribeAll` hands it over.

```go
// SendReceipt handles orders.OrderPlaced.
func (l *SendReceiptConsumer) SendReceipt(payload *types.OrderPlacedPayload) error {
	return l.svcCtx.Mailer.SendReceipt(l.ctx, payload.OrderID)
}
```

Returning an error tells the transport the message was not processed. What the
transport does about that - retry, nack, dead-letter - is the transport's
policy, not craftgo's. The in-process transport reports it to the error handler
you install and drops the message; install one, or a failing consumer is
observed by nothing.

### A panicking consumer does not end the process

`Bus.Subscribe` wraps every handler it registers in a recover, the way
`Server.Start` puts [`Recovery`](/reference/runtime-api#built-in-middleware) outermost in
the HTTP chain: unconditional, nothing to declare, and the same rule for every
transport. It has to sit on the handler - the panic fires on a goroutine the
transport spawned, and a `recover` anywhere on the registration side has long
since returned.

A recovered panic comes back as a `*craftevents.PanicError` naming the consumer,
its group and the contract, and is handed to the transport as an ordinary
handler error - so it reaches the error handler you installed, and delivery
carries on with the next message. Nothing is redelivered.

The stack trace is a field on the error rather than part of its message, the way
the HTTP side logs it as its own field:

```go
memory.WithErrorHandler(func(sub craftevents.Subscription, _ *craftevents.Message, err error) {
	fields := []log.Field{log.String("consumer", sub.Consumer), log.Err(err)}
	var panicked *craftevents.PanicError
	if errors.As(err, &panicked) {
		fields = append(fields, log.String("stack", string(panicked.Stack)))
	}
	log.Default().Error("consumer failed", fields...)
})
```

A consumer this catches is still a bug in your handler. Recovery keeps one bad
message from taking down the API and every other consumer sharing the binary;
it does not make the message succeed.

If you install [consumer middleware](#consumer-middleware), it sees a panicking
handler as an ordinary error rather than being unwound past it - the bus puts a
recover on both sides of your chain. Nothing to configure.

### Telling one failure from another

craftgo classifies nothing for you. A handler error is an error; what to do
about it is a decision, and a decision belongs to the
[middleware](#consumer-middleware) you write rather than to the runtime.

What the runtime does give you is enough to decide with. A recovered panic
arrives as a `*craftevents.PanicError`, so `errors.As` picks one out; a decode
or `Validate()` failure names the contract it arrived on; and
`msg.Reached()` separates a message the handler failed on from a chain that
broke before the handler ran.

```go
var panicked *craftevents.PanicError
switch {
case errors.As(err, &panicked):
	// the same bytes run the same code and panic again
case !msg.Reached():
	// the chain refused it; the handler never saw these bytes
}
```

Delivery context and tracing are also the transport's: the in-process transport
starts a fresh context per message, so a trace opened in the publishing request
does not continue into the consumer. A broker adapter that propagates
`traceparent` through `Message.Metadata` can carry it across.

## Consumer middleware

Logging, metrics and tracing for consumers are cross-cutting the way they are
for routes. They go on the **bus**, at the same call that chooses the transport
and the codec:

```go
bus := craftevents.New(
	craftevents.WithTransport(memory.New()),
	craftevents.WithCodec(codecjson.Codec{}),
	craftevents.WithMiddleware(Logging(logger), Metrics(reg)),
)
```

Nothing generated changes, and there is nothing to declare in the design.

```go
type Middleware func(sub craftevents.Subscription, next craftevents.Handler) craftevents.Handler
```

There is no decorator to write. An `http.Handler` gives a middleware no handle
on the route it wraps, so the HTTP side needs `@middlewares(...)` to name one; a
consumer middleware is handed the `Subscription` as an argument, and it already
carries the contract, the consumer and the group. What HTTP solves with a
decorator, events solves with a parameter - so one chain can behave differently
per consumer group without the design knowing it exists.

```go
func Logging(logger log.Logger) craftevents.Middleware {
	return func(sub craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			start := time.Now()
			err := next(ctx, msg)
			logger.WithContext(ctx).Info("consumed",
				log.String("event", sub.Event),
				log.String("consumer", sub.Consumer),
				log.String("group", sub.GroupName()),
				log.Duration("latency", time.Since(start)),
				log.Err(err))
			return err
		}
	}
}
```

`WithMiddleware(A, B, C)` folds outermost-first - a message flows A → B → C →
your handler and the error travels back in reverse - which is exactly
[`server.Chain`](/reference/runtime-api#chain) on the HTTP side. Nil entries are
skipped, and repeating the option appends rather than replaces.

### Why the bus and not the generated wiring

Because the bus is the only thing every subscription passes through. A chain
installed in the generated `SubscribeAll` would cover what that function can
see, and a real deployable has more:

```go
subs := append(
	billingevents.Subscriptions(bus, transport.NewBillingConsumers(billing)),
	settlementevents.Subscriptions(bus, upstream.NewSettlements(billing))...,
)
```

The second half consumes another design's contracts. No generated `SubscribeAll`
can name it, so a chain applied there would silently not wrap it - and with
`output.services` projections there is one `SubscribeAll` per manifest, each over
its own `ServiceContext`, so a process running several would have to be
configured once per container with no compiler help for the one you forget.

One bus, one chain, every subscription. A project wanting two different chains
builds two buses, which it already can.

### Panics

**A panicking consumer reaches your own middleware as an ordinary error, and the
process survives. There is nothing to add to the chain.**

`Bus.Subscribe` installs recovery on *both* sides of your chain. The inner one
turns a panicking handler into a `*PanicError` before your middleware sees the
result, so a logger written as `err := next(ctx, msg)` logs the failure it most
needs to. The outer one catches a panic in the chain itself, which the inner one
sits beneath and can never see - so a bug in your own `Logging` middleware still
cannot take the process down.

Only one `PanicError` is ever built per panic: once the inner recover catches,
no panic is in flight, so the outer `recover()` returns nil and passes the error
through untouched.

The one thing that is not observable is a panic in your own chain, which is
caught by the outer recover and reported to the transport's error handler, but
never returns through the middleware below it - that middleware's frame was
unwound before the recovery ran.

A bus with no middleware installs the inner recover alone, which is the single
wrap it has always applied.

The one case that needs a line from you is a chain folded by `Chain.Apply` rather
than installed with `WithMiddleware`: the bus wraps that from outside, as one
opaque handler, so put `craftevents.Recover()` at its innermost end to get the
same visibility. A chain on the bus needs nothing.

### Reaching the broker's own message

`events.Message` carries what every transport has. When a middleware needs what
only one of them has - a Kafka partition and offset, a NATS reply subject, a
header craftgo did not map - the adapter hands it over:

```go
func PartitionLog(logger log.Logger) craftevents.Middleware {
	return func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			if rec, ok := kafka.RecordFrom(ctx); ok {
				logger.Info("delivery", log.Int("partition", int(rec.Partition)))
			}
			return next(ctx, msg)
		}
	}
}
```

`kafka.RecordFrom(ctx)` gives a `*kgo.Record`, `nats.MsgFrom(ctx)` a `*nats.Msg`.
Each reports `false` on a delivery from any other transport - the key is private
to its adapter, so a Kafka-typed read on a NATS delivery cannot find anything.
`MustRecord` / `MustMsg` panic instead, which the bus turns into a
`*PanicError` naming the consumer, the group and the contract: use those when
running elsewhere is a wiring mistake you want to hear about on the first
message.

The in-process transport has no raw message and so has **no accessor at all** -
a middleware that reads one will not compile against it, which is the mistake
caught as early as it can be.

::: warning Read it, do not keep it
Take what you need and let the record go. In a Kafka share group the next poll
finalises the previous one, so a record held past the handler's return reports a
delivery count of zero and its `Ack` does nothing, both silently. Decide through
`msg.Settle()` / `Redeliver()` / `Reject()` instead - that is answered for at the
right moment whatever the transport.
:::

### Deciding what happens to a delivery

A middleware can ask for something other than "done" - through the message,
because a decision about one delivery is not a value the transport carries:

```go
func RetryOnce(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
	return func(ctx context.Context, msg *craftevents.Message) error {
		err := next(ctx, msg)
		if err != nil && msg.Deliveries() < 2 {
			msg.Redeliver()
		}
		return err
	}
}
```

| call | what it asks for |
| --- | --- |
| `msg.Settle()` | take this delivery as done |
| `msg.Redeliver()` | hand it back; the same message returns |
| `msg.Reject()` | give it up - no attempt will handle it |
| nothing | the zero value, `DispositionUnset`, which settles |

`msg.Deliveries()` is the broker's count of how many times it has handed this
message over, and `msg.Reached()` separates a message the handler failed on from
a chain that broke before the handler ran.

**The last writer wins, and clearing is allowed.** The chain returns innermost
first, so the outermost middleware decides last and can see what everything
below it asked for. Decide from the handler's goroutine and before the chain
returns - a decision written from a goroutine of your own is both a race and a
lost write.

::: danger Only some transports can honour this
Redeliver and reject need the broker to be tracking each record. A
[Kafka share group](#two-modes) can; a classic consumer group, core NATS and the
in-process transport cannot, and there **Redeliver settles instead** - which
loses every message the chain meant to retry, silently.

Say what you need at the bus and find out at startup:

```go
bus := craftevents.New(
	craftevents.WithTransport(tr),
	craftevents.WithCodec(codecjson.Codec{}),
	craftevents.WithDispositionRequired(craftevents.DispositionRedeliver),
)
```

`Subscribe` then refuses on a transport that cannot, and the error travels out
of the generated `SubscribeAll` and out of `main`. An adapter declares what it
can do by implementing `craftevents.Dispositioner`; one that does not implement
it settles and nothing else.
:::

## Wiring it up

Events need two runtime choices you make in `main.go`: **which transport** moves
the messages and **which codec** encodes them.

```go
bus := craftevents.New(
	craftevents.WithTransport(memory.New(memory.WithErrorHandler(
		func(sub craftevents.Subscription, _ *craftevents.Message, err error) {
			log.Default().Error("consumer failed",
				log.String("consumer", sub.Consumer), log.Err(err))
		}))),
	craftevents.WithCodec(codecjson.Codec{}),
)
svc.Events = svccontext.NewEvents(bus)

if err := transport.SubscribeAll(ctx, bus, svc); err != nil {
	log.Default().Error("start consumers", log.Err(err))
	os.Exit(1)
}
```

`craftgo gen` scaffolds exactly this into a new project's `main.go`. Nothing
generated mentions a broker or an encoding, so swapping either is a change to
these lines alone.

### Transports

`pkg/events` defines two small interfaces; a transport implements one or both.

```go
type Publisher interface {
	Publish(ctx context.Context, msg *Message) error
}

type Subscriber interface {
	Subscribe(ctx context.Context, sub Subscription) error
}
```

`pkg/events/memory` ships an in-process transport for tests, local development
and single-binary deployments. Subscriptions that share a group for one contract
form one competing-consumer group, mirroring what a broker's consumer group
does, so behaviour does not change shape when you move to one.

craftgo ships two broker adapters, each its own Go module so the runtime -
and any contract package that depends on it - stays free of a broker client.
Import only the one you use:

```go
import "github.com/craftgodotdev/craftgo/pkg/events/nats"
import "github.com/craftgodotdev/craftgo/pkg/events/kafka"
```

Anything else (RabbitMQ, SQS, Pub/Sub, Redis Streams, …) is an external
package implementing the two methods above. The core still carries no broker
dependency: a generated contract library pulls in `pkg/events` and nothing
else.

#### NATS

```go
bus := craftevents.New(
	craftevents.WithTransport(nats.New(conn)),
	craftevents.WithCodec(codecjson.Codec{}),
)
```

A contract maps onto a subject unchanged - contract names are already
dot-shaped, so `orders.>` keeps working. A subscription's group is the queue
group, so replicas sharing one share the work and a different group gets its own
copy. Pass `nats.WithSubject(...)` when the broker's naming is not yours to
choose.

NATS core is fire-and-forget: a nil error means the bytes reached the
connection, not that a subscriber got them. Install `nats.WithErrorHandler`
or a failing handler is observed by nothing.

Core NATS also keeps no committed position, so a group name here is not state:
it decides who competes for a message while its members are connected, and
renaming it costs nothing. The contract rides the subject rather than a header,
so a custom `WithSubject` mapping has to keep contracts apart.

#### Kafka

```go
bus := craftevents.New(
	craftevents.WithTransport(kafka.New([]string{"localhost:9092"})),
	craftevents.WithCodec(codecjson.Codec{}),
)
```

The default maps one contract to one topic. A message's `WithKey` value becomes
the Kafka message key, which is what puts one entity in one partition, so Kafka orders
that entity's messages - within one contract. The contract always travels in the
`craftgo-event` header, so a topic carrying several contracts stays
self-describing. `WithTopic` replaces the mapping when the broker's naming is
not yours to choose:

```go
kafka.New(brokers, kafka.WithTopic(func(c string) string { return "app." + c }))
```

A subscription's group is the Kafka group.

### Two modes

The default is a **classic consumer group**: the client owns partitions, offsets
advance as a high-water mark, and a delivery can only be taken as done.

`kafka.WithShareGroup()` switches to a **share group** (KIP-932), where the
broker tracks each record. That is what makes `msg.Redeliver()` and
`msg.Reject()` mean anything - see [dispositions](#deciding-what-happens-to-a-delivery):

```go
kafka.New(brokers, kafka.WithShareGroup(), kafka.WithMaxDeliveries(5))
```

`WithMaxDeliveries` bounds a redelivery loop: a middleware that keeps asking for
a record nothing can handle stops being obeyed once the count is reached, and
the record is rejected. A delivery that *succeeds* on the last attempt is still
taken as done. The default is 5; zero is unbounded and has to be chosen.

::: warning Share groups need Kafka 4.2, and the mode is never detected
`Subscribe` asks the broker what it serves and **refuses** if the share APIs are
missing, rather than quietly consuming as a classic group. A delivery guarantee
that changed with whichever broker answered would change under a failover with
nothing to see it.

The refusal is at subscribe, not at the first message, which matters more than
it sounds: the client reports a missing API on its first poll, on a goroutine
nobody is waiting on - so without the check the deployable boots, serves HTTP,
passes readiness, and consumes nothing.

A share group also starts at the *end* of a topic unless the group config
`share.auto.offset.reset` says otherwise.
:::

::: warning Ordering across contracts is not supported
Two contracts about one entity have no order between them, and no configuration
of this adapter gives them one. Collapsing them onto one topic does not:
`Subscribe` refuses one group reading two different contracts on one topic,
because the members would divide that topic between them and each skip the
other's contract. Separate groups on one topic are separate readers, so they are
not ordered either.

Use a group for scale and for failure isolation. Do not use one expecting
cross-contract order.
:::

::: danger In a classic group a failed message is dropped, not redelivered
When a handler returns an error, the adapter reports it to
`kafka.WithErrorHandler` and takes the message as done anyway. Leaving it
uncommitted would not hold it: a group offset is a per-partition high-water
mark, so the next message that succeeds on that partition commits past the
failure regardless.

Install an error handler. It is the only record that the message arrived. Or use
a share group, where the broker holds each record until the chain answers for it.
:::

### TLS and SASL

```go
kafka.New(brokers,
	kafka.WithTLS(nil),                       // nil uses the system roots
	kafka.WithSASLSCRAMSHA256(user, pass))
```

`WithSASLPlain` and `WithSASLSCRAMSHA512` are the other two. PLAIN sends the
password where anything on the path can read it, so pair it with `WithTLS`.

### Codecs

```go
type Codec interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}
```

There is no default: a bus built without a codec fails rather than picking an
encoding for you. `pkg/events/codecjson` provides JSON; Protobuf, MessagePack,
Avro, CBOR or your own is the same one-line change. `WithCodecFor(contract, c)`
overrides the codec for a single contract, so a high-volume event can carry a
different encoding from the rest.

Every published message is stamped with its codec name, and a consumer rejects a
message encoded with a codec it is not configured for rather than handing
mismatched bytes to `Unmarshal`.

## A contract you do not publish

An `event` may also stand on its own, outside any service:

```craftgo
package upstream

// Published by the payments platform; the wire name is theirs.
@contract("payments.settled.v1")
event PaymentSettled {
    payload PaymentSettledPayload
}

service LedgerService {
    consume RecordSettlement {
        event upstream.PaymentSettled
    }
}
```

Inside a service, `event` says two things: here is the contract, **and this
service publishes it**. At file level it says only the first. So craftgo emits
no publisher, puts nothing on your `ServiceContext`, and the AsyncAPI document
describes the channel and a `receive` operation but never claims your
application sends it.

Use it for a contract another system owns. Consumers reference it exactly as
they reference any other.

## The contract name

An event's identity on the wire is `<package>.<Event>` - `orders.OrderPlaced`
above. Both sides address the contract by that string; how a transport maps it
onto a topic, subject or queue is the transport's business.

To interoperate with a name another system already publishes, set it explicitly:

```craftgo
@contract("order.placed.v2")
event OrderPlaced {
    payload OrderPlacedPayload
}
```

Two events resolving to one contract name is rejected at design time - publisher
and consumer could not tell them apart.

## Consumer groups: if it has an offset, write the name down

A group name is an identity your broker remembers - it is where your consumers
left off: the Kafka consumer group, the JetStream durable. craftgo derives one
from your declaration so a new design runs without ceremony, but the derivation
is a starting value, not a promise. Rename the consumer, its service, or its
package and the derived name moves with them; a broker that has never seen the
new name has no position for it, and your consumers start again from the
beginning of the stream. Once a group has run somewhere you care about, write
its name down with `@consumerGroup` and change it only when you mean to.

The derived name is `<package>-<Service>-<Consumer>`, so the `SendReceipt`
consumer of `NotificationService` in package `notifications` joins
`notifications-NotificationService-SendReceipt`. It reaches the transport on the
subscription, verbatim:

```go
{
	Event:    "orders.Placed",
	Consumer: "SendReceipt",
	Group:    "notifications-NotificationService-SendReceipt",
	...
}
```

`Consumer` is the handler's own name, for diagnostics. `Group` is the only thing
the broker sees.

How much a rename costs depends on the transport, so be clear about the one you
run. Kafka and JetStream remember a position per group, and there a name is
state: change it and there is nothing to resume from. Core NATS remembers
nothing - a queue group only shares the messages arriving while its members are
connected - and the in-process transport the same. On those, the name still
decides who competes with whom, but nothing is lost by changing it.

`@consumerGroup` sets it. On a consumer it names that one group; on a service it
is the default for every `consume` in the body, which a consumer may override:

```craftgo
package notifications

@consumerGroup("order-worker")
service NotificationService {
    consume SendReceipt { event orders.Placed }

    consume SendDispatchNote { event orders.Shipped }

    @consumerGroup("returns-worker")
    consume SendReturnLabel { event orders.Returned }
}
```

`SendReceipt` and `SendDispatchNote` share `order-worker`: **one group over
several contracts**, which is the point. A group is a unit of scaling and of
failure isolation, not a per-contract label - the replicas that join
`order-worker` divide all three contracts' work between them, and the consumers
in it stop and resume together. `SendReturnLabel` takes its own, so it scales
and fails on its own.

It buys you nothing in ordering. Two contracts about one entity have no order
between them on any transport craftgo ships, whether or not they share a group -
see the Kafka notes above.

Three rules, all checked at design time:

- Two consumers of **one contract** may not share a group. A group's members
  divide a contract between them, so the two would each get part of it instead
  of each receiving every message (`consumer/group-collision`).
- Two **services** may not share a group (`consumer/group-cross-service`). This
  one is not hygiene. A shared group requires every process that joins it to
  register the same consumers; inside one service that holds by construction,
  because its consumers are built into one `Subscriptions()` and registered
  together. Two services deploy as separate binaries, so each would join the
  group handling only its own contracts and skip the rest - losing those
  messages with nothing to show for it.
- An authored name may not be empty and may not contain a dot or whitespace
  (`consumer/group-format`): NATS JetStream refuses a durable name with either,
  so the group could never be created. That is also why the derived name joins
  its parts with `-`.

`@group` is unrelated. It decides which folder generated files land in; it never
reaches the group identity, so moving a service under a `@group` leaves its
consumers reading from exactly where they were.

A consumer declared in an `extend service` block belongs to the service it
extends, so it derives that service's name and inherits its `@consumerGroup`.

The AsyncAPI document carries each consumer's group as `x-craftgo-group` on its
`receive` operation, and [`craftgo check`](#checking-an-upgrade) reports a
changed one as breaking - the one signal that tells you a rename is about to
cost a consumer its position, on a transport that keeps one.

## Publish options

Everything about a message beside its contract and its payload is decided where
it is published. The generated publisher and the batch builder both end in
`opts ...craftevents.PublishOption`:

```go
svcCtx.Events.OrderService.PublishOrderPlaced(ctx, placed,
	craftevents.WithKey(string(placed.OrderID)),
	craftevents.WithHeader("trace-parent", tp))

b := svcCtx.Events.Batch()
b.OrderService().OrderShipped(shipped, craftevents.WithKey(string(shipped.OrderID)))
```

| Option | What it sets |
| --- | --- |
| `WithKey(string)` | The key a transport places the message by - the Kafka partition key, a subject suffix. Two messages under one key keep their order, within one contract. |
| `WithDedupID(string)` | The identity a broker that de-duplicates recognises a repeat by: the SQS FIFO deduplication ID, the Azure Service Bus message ID, the NATS `Nats-Msg-Id` header. |
| `WithHeader(k, v)` | One side-band value carried beside the payload. |
| `WithAdapterOption(adapter, k, v)` | A value one named adapter reads - the escape hatch for a broker feature that does not generalise. |

Options apply in order, so the last one setting a given value wins.

::: warning The two portable options are not guarantees
`WithHeader` and `WithAdapterOption` behave the same on every transport. The
other two ask for a **broker feature**, so what they do depends on which
adapter is wired up:

| | `WithKey` | `WithDedupID` |
| --- | --- | --- |
| kafka | partitions on it, so one key is one partition and its messages are ordered within that contract | carried as a header; nothing deduplicates |
| nats | carried as a header; routing is by subject, so nothing is ordered | carried as `Nats-Msg-Id`; core NATS does not deduplicate, a JetStream stream with a duplicate window does |
| memory | carried; deliveries run concurrently, so nothing is ordered | carried; nothing deduplicates |

**No transport craftgo ships deduplicates**, and only Kafka orders on a key. But
all three **carry** both values to the consumer, which is the difference between
a feature a transport has not got and a value it destroys: a consumer handed the
ID can recognise a repeat itself even where the broker will not.

Kafka's idempotent producer is not that feature. It covers a request the client
reissued after a network failure, keyed on a producer ID and sequence craftgo
never sets - two `Publish` calls sharing one `WithDedupID` are two records.
:::

::: tip The key is not in the design
A contract says what a message *is*; which entity it belongs to is a property
of the message, and one publisher may key the same contract differently from
another. Nothing fills a key in on your behalf, so **a publish without
`WithKey` is keyless** - Kafka round-robins those across the partitions rather
than ordering them per entity.
:::

### Defaults for every message

A publisher and the `Events` container take the same options as defaults,
applied to every message before the options of the call itself - so a tenant
header is set once at wiring time and a per-call option of the same kind still
wins:

```go
svc.Events = svccontext.NewEvents(bus, craftevents.WithHeader("tenant", tenant))
```

Batches started from `svcCtx.Events.Batch()` carry them too, so the two ways to
publish cannot disagree.

### An adapter's own options

`WithAdapterOption` is namespaced by adapter, because the feature it reaches
for exists on one broker and not the others:

```go
svcCtx.Events.OrderService.PublishOrderPlaced(ctx, placed,
	craftevents.WithAdapterOption(kafka.Adapter, kafka.OptionTimestamp, occurred))
```

An option addressed to an adapter **other** than the configured one is ignored,
so the same code publishes through whichever broker is wired up. One addressed
to the configured adapter under a key it does **not** read **fails the
publish**, naming what it does read - a per-message option that goes nowhere is
a message delivered differently from how its caller asked.

An adapter opts into both rules by implementing `events.OptionAware`
(`AdapterName() string`, `KnownOptions() []string`); the three craftgo ships do.
Of them only Kafka reads an option of its own, `kafka.OptionTimestamp`, which
sets the record timestamp a replayed message would otherwise lose.

## AsyncAPI

`craftgo gen` also writes an AsyncAPI 3.0 document at `events.asyncapi`
(`./docs/asyncapi.yaml` by default) with one channel per contract, a `send`
operation for the declaring service and a `receive` operation for every
consumer. Each `receive` carries its consumer's group as `x-craftgo-group`.
Payload schemas come from the same builder the OpenAPI document uses, so a type
is described identically in both.

The projection is one-way. craftgo never reads AsyncAPI back, and no AsyncAPI
concept shapes the event model - the document is for documentation, tooling and
interoperability.

### Checking an upgrade

Because the document describes the contracts you published, it is also what an
upgrade has to be checked against:

```sh
craftgo check -against ./published/asyncapi.yaml
```

It prints every difference and exits non-zero when one of them would break an
existing publisher or consumer - a contract removed, a field removed, a type
changed, a field that became required, an enum value dropped, or a consumer
group renamed. Adding an optional field or relaxing a requirement is reported
but passes. Run it in CI against the document from your last release; nothing
else is needed, and no schema registry has to be running.

## Configuration

```yaml
events:
  targets:
    - lang: go
      out: ./internal/events
  asyncapi: ./docs/asyncapi.yaml
```

Go is a row in `targets`, not an implied default: the manifest states where the
event artefacts land the same way it would for any other language. Within that
row the Go target places its individual artefacts through `output:`, so it
rejects a per-target `layout:` block rather than ignoring it.

Omit the block entirely and a design that declares events gets one Go target at
`./internal/events`; a design that declares none generates nothing either way.
Set a target's `out` to `"-"` to skip it, and `asyncapi: "-"` to skip the
projection.

Transport and codec are deliberately absent: they are runtime wiring, not
design-time facts.

## Generating one target

`craftgo gen` runs every target. Narrow it when you only want one:

```sh
craftgo gen --target docs    # just the OpenAPI/AsyncAPI documents
craftgo gen --target go      # just the Go source
```

The names are `go` and `docs`; the flag repeats and defaults to all. Each
target removes only the generated files it owns, so a narrowed run never
deletes another target's output - regenerating just the documents leaves the
Go events exactly where they were.

## Adding a language

Go is the only language target today. Adding another is one row in
`LangTargets` (`internal/codegen/codegen.go`), one name in
`config.SupportedLangs`, and one package beside `internal/codegen/golang` and
`internal/codegen/docs` that reads `*semantic.Project` and writes files. The two
lists are asserted to match, so forgetting either half fails a test rather than
silently generating nothing.

The semantic event model does not change to accommodate a new language, and no
target reads another target's code.
