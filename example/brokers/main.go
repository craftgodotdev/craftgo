// Command brokers wires the generated event code to a real broker.
//
// The design, the contract descriptors and the handler interfaces are
// identical whichever broker runs underneath - only the transport line
// changes:
//
//	go run . -transport memory
//	go run . -transport nats  -addr nats://127.0.0.1:4222
//	go run . -transport kafka -addr localhost:9092
//
// That is the point of the transport seam: nothing generated mentions a
// broker, so swapping one is a change to this file alone.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	natsclient "github.com/nats-io/nats.go"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftkafka "github.com/craftgodotdev/craftgo/pkg/events/kafka"
	"github.com/craftgodotdev/craftgo/pkg/events/logging"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
	craftlog "github.com/craftgodotdev/craftgo/pkg/log"

	"github.com/craftgodotdev/craftgo/example/brokers/internal/consumers"
	ordersevents "github.com/craftgodotdev/craftgo/example/brokers/internal/events/orders"
	paymentsevents "github.com/craftgodotdev/craftgo/example/brokers/internal/events/payments"
	"github.com/craftgodotdev/craftgo/example/brokers/internal/types/orders"
	"github.com/craftgodotdev/craftgo/example/brokers/internal/types/payments"
)

func main() {
	kind := flag.String("transport", "memory", "memory | nats | kafka")
	addr := flag.String("addr", "", "broker address (nats:// URL, or a Kafka broker)")
	topic := flag.String("kafka-topic", "", "collapse every contract onto this Kafka topic (see -h)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tr, closeFn, err := openTransport(*kind, *addr, *topic)
	if err != nil {
		log.Fatalf("transport: %v", err)
	}
	defer closeFn()

	bus := craftevents.New(
		craftevents.WithPublisher(tr),
		craftevents.WithSubscriber(tr),
		craftevents.WithCodec(codecjson.Codec{}),
		// One line per delivery, whichever broker is underneath: the
		// chain is on the BUS, so swapping the transport does not change
		// what is logged.
		craftevents.WithMiddleware(logging.AccessLog(craftlog.Slog())),
	)

	// The design says which handler interfaces exist; this binary says
	// which of them it runs and under what group. Registration records
	// them, Start hands the whole batch to the transport at once - a
	// broker that binds one identity to several contracts cannot register
	// a group one contract at a time.
	if err := consumers.RegisterAll(bus, nil); err != nil {
		log.Fatalf("register consumers: %v", err)
	}
	deliver, stopDelivery := context.WithCancel(ctx)
	defer stopDelivery()
	if err := bus.Start(deliver); err != nil {
		log.Fatalf("start consumers: %v", err)
	}

	// orders.Placed has three consumers in three groups, so each of the
	// two publishes below is delivered three times - once per group.
	//
	// WithKey is what puts one order's messages in one Kafka partition, so
	// Placed and Shipped for order-2 arrive in the order they were sent.
	// It is passed here, at the publish, because which entity a message
	// belongs to is a property of the message and not of the contract.
	placed := &orders.OrderPlaced{OrderID: "order-1", Total: 4200}
	if err := ordersevents.Placed.Publish(ctx, bus, placed,
		craftevents.WithKey(string(placed.OrderID))); err != nil {
		log.Fatalf("publish: %v", err)
	}
	// A batch mixing contracts, which is the shape an outbox drains: it
	// reaches the transport in one call, and a partial failure names the
	// entries that did not go out by index.
	if err := bus.PublishAll(ctx, []craftevents.Envelope{
		{
			Event:   ordersevents.PlacedContract,
			Key:     "order-2",
			Payload: &orders.OrderPlaced{OrderID: "order-2", Total: 900},
		},
		{
			Event:   ordersevents.ShippedContract,
			Key:     "order-2",
			Payload: &orders.OrderShipped{OrderID: "order-2", Carrier: "dhl"},
		},
	}); err != nil {
		log.Fatalf("publish batch: %v", err)
	}

	// payments.Settled is declared outside any service: this design
	// consumes it and the payments platform publishes it. The descriptor
	// is generated all the same - a contract is publishable by whoever
	// holds it - so standing in for that platform is one call.
	if err := paymentsevents.Settled.Publish(ctx, bus,
		&payments.Settlement{OrderID: "order-1", Amount: 4200},
		craftevents.WithKey("order-1")); err != nil {
		log.Fatalf("publish upstream: %v", err)
	}
	fmt.Printf("published 3 + 1 upstream over %s\n", *kind)

	// The in-process transport delivers synchronously once drained; a
	// broker needs a moment.
	if drain, ok := tr.(interface{ Drain() }); ok {
		drain.Drain()
	} else {
		time.Sleep(time.Second)
	}
	<-ctx.Done()
}

// transportKind is the half of a bus this example needs.
type transportKind interface {
	craftevents.Publisher
	craftevents.Subscriber
}

// openTransport builds the requested broker adapter. Every branch returns
// the same two interfaces, which is the whole seam.
func openTransport(kind, addr, topic string) (transportKind, func(), error) {
	onError := func(sub craftevents.Subscription, _ *craftevents.Message, err error) {
		log.Printf("consumer %s failed: %v", sub.Consumer, err)
	}
	switch kind {
	case "memory":
		return memory.New(memory.WithErrorHandler(onError)), func() {}, nil

	case "nats":
		if addr == "" {
			addr = natsclient.DefaultURL
		}
		conn, err := natsclient.Connect(addr)
		if err != nil {
			return nil, nil, fmt.Errorf("connect nats: %w", err)
		}
		return craftnats.New(conn, craftnats.WithErrorHandler(onError)), conn.Close, nil

	case "kafka":
		if addr == "" {
			addr = "localhost:9092"
		}
		// Convenient for a local broker; a real deployment provisions its
		// topics with a partition count worth choosing.
		opts := []craftkafka.Option{
			craftkafka.WithErrorHandler(onError),
			craftkafka.WithAutoCreateTopics(true),
		}
		if topic != "" {
			// Kafka orders within a partition, so two contracts about one
			// order on two topics have no order between them. Collapsing
			// them onto one topic keyed by order is the usual fix.
			opts = append(opts, craftkafka.WithTopic(func(string) string { return topic }))
		}
		t := craftkafka.New([]string{addr}, opts...)
		return t, func() { _ = t.Close() }, nil
	}
	return nil, nil, fmt.Errorf("unknown transport %q", kind)
}
