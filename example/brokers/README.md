# brokers

Six design packages, three transports. The generated publisher, consumer and
subscriptions are identical whichever broker runs underneath — only the
transport line in `main.go` changes.

```sh
go run . -transport memory
go run . -transport nats  -addr nats://127.0.0.1:4222
go run . -transport kafka -addr localhost:9092
go run . -transport kafka -addr localhost:9092 -kafka-topic orders
```

## The design

Six packages, each one a unit that could belong to a different team:

```
design/money/          scalars OrderID, Amount        no service, no event
design/orders/         OrderPlaced, OrderShipped      service OrderService  → publishes
design/payments/       Settlement                     file-level event      → no publisher
design/notifications/  NotificationService            → consumes orders.Placed, orders.Shipped
design/analytics/      AnalyticsService               → consumes orders.Placed
design/ledger/         LedgerService                  → consumes orders.Placed, payments.Settled
```

The split is the point. A package here owns one thing, and the edges between
them are what the generated code has to get right:

- **Three consumer groups on one contract, in three packages.**
  `NotificationService`, `AnalyticsService` and `LedgerService` all consume
  `orders.Placed`, so every publish is delivered three times — once per group.
  Replicas sharing a consumer name split the work instead; separate services
  each get their own copy.
- **A contract declared outside any service.** `payments.Settled` is published
  by the payments platform, not here. Declaring the event at file level is how
  you subscribe without pretending to be its producer: craftgo emits no
  publisher for it, puts nothing on the `ServiceContext`, and the AsyncAPI
  document carries a `receive` operation with no `send`. `main.go` publishes it
  straight on the bus to stand in for the upstream system.
- **A shared vocabulary package.** `orders` and `payments` both build their
  payloads out of `money.OrderID` and `money.Amount`, so the two stay
  comparable without either importing the other. The generated Go follows:
  `internal/types/orders` and `internal/types/payments` both import
  `internal/types/money`, and their validators delegate to the scalar's.
- **Packages with a service and no types of their own.** `notifications`,
  `analytics` and `ledger` declare only consumers; their payload types come
  from `orders` and `payments`.
- **A batch mixing contracts**, which is the shape an outbox drains.

## What runs

```
$ go run . -transport memory
published 3 + 1 upstream over memory
  [notifications/SendReceipt]   order=order-1 total=4200
  [ledger/BookOrder]            order=order-1 total=4200
  [analytics/CountOrder]        order=order-1 total=4200
  [notifications/SendReceipt]   order=order-2 total=900
  [ledger/BookOrder]            order=order-2 total=900
  [analytics/CountOrder]        order=order-2 total=900
  [notifications/SendDispatch]  order=order-2 carrier=dhl
  [ledger/RecordSettlement]     order=order-1 amount=4200 (upstream contract)
```

Two orders, each seen by all three groups; one shipment, seen only by the
service that asked for it; one upstream payment. Delivery order across groups
is the transport's business, so the lines interleave differently per run.

## Brokers, locally

```sh
docker run -d -p 4222:4222 nats:2-alpine

docker run -d -p 9092:9092 \
  -e KAFKA_NODE_ID=0 -e KAFKA_PROCESS_ROLES=broker,controller \
  -e KAFKA_LISTENERS=PLAINTEXT://:9092,CONTROLLER://:9093 \
  -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://127.0.0.1:9092 \
  -e KAFKA_CONTROLLER_QUORUM_VOTERS=0@localhost:9093 \
  -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
  -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
  -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
  -e CLUSTER_ID=craftgo apache/kafka:3.8.0
```

## The Kafka topic question

`-kafka-topic orders` collapses every contract onto one topic. That is not a
detail: Kafka orders within a partition, so `orders.Placed` and
`orders.Shipped` on two topics have **no order between them** for the same
order. One topic keyed by order gives you that order back. The contract
still travels in the `craftgo-event` header, so each consumer picks out its
own and skips the rest — run it both ways and the output is the same.

The key is the other half of that. `main.go` passes
`craftevents.WithKey(string(order.OrderID))` at every publish, which is what
puts one order in one partition; a publish without it is keyless and Kafka
round-robins it across them.

`WithAutoCreateTopics(true)` is on here for convenience. Leave it off in
production: a real topic is provisioned with a partition count and
replication factor worth choosing, and a typo in a contract name should fail
rather than quietly open a new topic.
