// Package kafka adapts craftgo's event runtime to Kafka.
//
// # Topics
//
// The default maps one contract to one topic. A message's ordering key -
// [events.WithKey] at the publish call - becomes the Kafka message key,
// so one entity's messages land in one partition and Kafka orders them,
// within that one contract. A message published without a key is
// round-robined across the partitions instead.
//
// [WithTopic] replaces the mapping when the broker's naming is not yours
// to choose:
//
//	kafka.New(brokers, kafka.WithTopic(func(c string) string { return "app." + c }))
//
// The contract always travels in the [HeaderEvent] header, so a topic
// carrying several contracts stays self-describing.
//
// # Ordering across contracts is not supported
//
// Two contracts about one entity have no order between them, and no
// configuration of this adapter gives them one. Collapsing them onto one
// topic does not: [Transport.Subscribe] refuses one group reading two
// different contracts on one topic, because the readers would split that
// topic's partitions and each commit-and-skip the other's contract. One
// reader over several topics does not either: kafka-go drains one topic
// before the next, so a keyed pair arrives in the wrong order. Separate
// groups on one topic are separate readers, so they are not ordered
// either. Use a group for scale and for failure isolation; do not use one
// expecting cross-contract order.
//
// A subscription's group is the Kafka consumer group, so replicas
// sharing one group share the partitions and a different group gets its
// own copy.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	kgo "github.com/segmentio/kafka-go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// HeaderEvent carries the contract name, so a topic holding several
// contracts remains self-describing.
const HeaderEvent = "craftgo-event"

// HeaderKey carries the ordering key for consumers that want it without
// decoding the payload. The same value is the Kafka message key.
const HeaderKey = "craftgo-key"

// A Transport is a full transport: it publishes, subscribes, takes a
// batch in one call, and names itself to the per-message option check.
// Asserted here so a change to the runtime interfaces fails this package
// rather than a user's wiring.
var (
	_ events.Publisher      = (*Transport)(nil)
	_ events.Subscriber     = (*Transport)(nil)
	_ events.BatchPublisher = (*Transport)(nil)
	_ events.OptionAware    = (*Transport)(nil)
)

// Adapter is the name [events.WithAdapterOption] addresses this adapter
// by.
const Adapter = "kafka"

// OptionTimestamp sets one record's Kafka timestamp, as a [time.Time]:
//
//	craftevents.WithAdapterOption(kafka.Adapter, kafka.OptionTimestamp, occurred)
//
// The broker stamps its own arrival time otherwise, which is what a
// message replayed from an outbox does not want.
const OptionTimestamp = "timestamp"

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: the per-message options
// this adapter reads. Anything else addressed to `kafka` fails the
// publish rather than being dropped.
func (t *Transport) KnownOptions() []string { return []string{OptionTimestamp} }

// Transport publishes and consumes over Kafka.
type Transport struct {
	brokers   []string
	topic     func(contract string) string
	onError   func(sub events.Subscription, msg *events.Message, err error)
	autoTopic bool

	mu      sync.Mutex
	writers map[string]*kgo.Writer
	readers []*kgo.Reader
	// held records what each (group, topic) pair is being read for, so a
	// second reader asking for a DIFFERENT contract is refused rather
	// than silently splitting the partitions with the first.
	held map[groupTopic]*topicClaim
}

// groupTopic is one consumer group's claim on one topic.
type groupTopic struct{ group, topic string }

// topicClaim is what a group is reading one topic for, and how many live
// readers hold it. Replicas of one subscription share a claim, so the
// count decides when it is free again.
type topicClaim struct {
	contract string
	readers  int
}

// Option configures a Transport.
type Option func(*Transport)

// WithTopic replaces the contract-to-topic mapping. The default is the
// contract name unchanged; see the package doc for when to change it.
func WithTopic(fn func(contract string) string) Option {
	return func(t *Transport) { t.topic = fn }
}

// WithAutoCreateTopics lets the broker create a missing topic on first
// publish. Off by default: a production topic is provisioned with a
// partition count and replication factor worth choosing deliberately, and
// a typo in a contract name should fail rather than silently open a new
// topic. Convenient for local development.
func WithAutoCreateTopics(on bool) Option {
	return func(t *Transport) { t.autoTopic = on }
}

// WithErrorHandler installs a callback for a handler that returns an
// error. The message is committed and dropped either way (see
// [Transport.Subscribe]), so this callback is the only record that it
// arrived - without one, a failing consumer is observed by nothing.
func WithErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) Option {
	return func(t *Transport) { t.onError = fn }
}

// New binds a Transport to a broker list.
func New(brokers []string, opts ...Option) *Transport {
	t := &Transport{
		brokers: brokers,
		topic:   func(c string) string { return c },
		writers: map[string]*kgo.Writer{},
		held:    map[groupTopic]*topicClaim{},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Publish sends one message.
func (t *Transport) Publish(ctx context.Context, msg *events.Message) error {
	rec, err := t.encode(msg)
	if err != nil {
		return err
	}
	return t.write(ctx, t.topic(msg.Event), rec)
}

// write sends records to one topic.
//
// With auto-creation on, the broker creates a missing topic while
// refusing the write that triggered it, so the first publish to a new
// topic fails with UnknownTopicOrPartition. Retrying briefly turns that
// into the behaviour a caller expects; without auto-creation a missing
// topic is a real error and is returned unchanged.
func (t *Transport) write(ctx context.Context, topic string, recs ...kgo.Message) error {
	w := t.writerFor(topic)
	err := w.WriteMessages(ctx, recs...)
	if !t.autoTopic || !isUnknownTopic(err) {
		return err
	}
	for attempt := 0; attempt < 10; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if err = w.WriteMessages(ctx, recs...); !isUnknownTopic(err) {
			return err
		}
	}
	return err
}

// isUnknownTopic reports the broker's "topic does not exist" answer.
func isUnknownTopic(err error) bool {
	return err != nil && errors.Is(err, kgo.UnknownTopicOrPartition)
}

// PublishBatch groups the batch by topic and writes each group in one
// call, which is what lets the client batch them on the wire. Every
// record is built first, so a message this adapter cannot encode fails
// the batch before anything is sent.
func (t *Transport) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	byTopic := map[string][]kgo.Message{}
	order := []string{}
	for _, msg := range msgs {
		rec, err := t.encode(msg)
		if err != nil {
			return err
		}
		topic := t.topic(msg.Event)
		if _, seen := byTopic[topic]; !seen {
			order = append(order, topic)
		}
		byTopic[topic] = append(byTopic[topic], rec)
	}
	sent := 0
	for _, topic := range order {
		group := byTopic[topic]
		if err := t.write(ctx, topic, group...); err != nil {
			return &events.PartialPublishError{Sent: sent, Event: topic, Err: err}
		}
		sent += len(group)
	}
	return nil
}

// encode maps a craftgo message onto a Kafka record. Metadata becomes
// headers beside the contract and the key, which keep their own.
//
// [HeaderEvent] and [HeaderKey] are this adapter's, so a metadata entry
// under either name is skipped rather than written a second time: decode
// reads the last header of a name, so a duplicate would rename the
// message or move it to another entity. The runtime drops those keys
// before a message gets here; a hand-built [events.Message] does not go
// through it.
func (t *Transport) encode(msg *events.Message) (kgo.Message, error) {
	headers := []kgo.Header{{Key: HeaderEvent, Value: []byte(msg.Event)}}
	if msg.Key != "" {
		headers = append(headers, kgo.Header{Key: HeaderKey, Value: []byte(msg.Key)})
	}
	for k, v := range msg.Metadata {
		if k == HeaderEvent || k == HeaderKey {
			continue
		}
		headers = append(headers, kgo.Header{Key: k, Value: []byte(v)})
	}
	// A keyless message must carry a nil Key, not an empty one: the Hash
	// balancer round-robins only on nil, and hashes []byte("") to a single
	// partition for every message that has no key.
	var key []byte
	if msg.Key != "" {
		key = []byte(msg.Key)
	}
	rec := kgo.Message{Key: key, Value: msg.Payload, Headers: headers}
	if v, ok := msg.AdapterOption(Adapter, OptionTimestamp); ok {
		ts, ok := v.(time.Time)
		if !ok {
			return kgo.Message{}, fmt.Errorf("kafka: option %q on %s is %T, want time.Time", OptionTimestamp, msg.Event, v)
		}
		rec.Time = ts
	}
	return rec, nil
}

// writerFor returns the writer for one topic, creating it once.
func (t *Transport) writerFor(topic string) *kgo.Writer {
	t.mu.Lock()
	defer t.mu.Unlock()
	if w, ok := t.writers[topic]; ok {
		return w
	}
	w := &kgo.Writer{
		Addr:                   kgo.TCP(t.brokers...),
		Topic:                  topic,
		Balancer:               &kgo.Hash{}, // the key decides the partition, so a keyed message orders per entity
		AllowAutoTopicCreation: t.autoTopic,
	}
	t.writers[topic] = w
	return w
}

// Subscribe joins the consumer group named by sub.Group and reads until
// ctx is cancelled. A handler error is reported and the message dropped;
// see [Transport.consume] for why it cannot be held.
//
// One group may read several contracts, but not two of them on one
// topic: the two readers would be two members of the group, splitting
// that topic's partitions while each commits-and-skips the contract the
// other asked for, so both lose messages. That pair is refused. It is
// reachable only through a [WithTopic] mapping that puts two contracts
// on one topic; under the default mapping the check never fires.
//
// Replicas are not that case. Two readers in one group on one topic for
// the SAME contract are ordinary group members dividing the partitions,
// which is what the in-process transport does with two identical
// subscriptions, so they are allowed.
func (t *Transport) Subscribe(ctx context.Context, sub events.Subscription) error {
	group, topic := sub.GroupName(), t.topic(sub.Event)
	if err := t.claim(group, topic, sub.Event); err != nil {
		return err
	}
	r := kgo.NewReader(kgo.ReaderConfig{
		Brokers: t.brokers,
		Topic:   topic,
		GroupID: group,
	})
	t.mu.Lock()
	t.readers = append(t.readers, r)
	t.mu.Unlock()

	go func() {
		defer t.release(group, topic)
		t.consume(ctx, r, sub)
	}()
	return nil
}

// claim records that group reads topic for contract. A second reader of
// the same contract joins the claim; one asking for a different contract
// is refused.
func (t *Transport) claim(group, topic, contract string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := groupTopic{group: group, topic: topic}
	held := t.held[key]
	if held == nil {
		t.held[key] = &topicClaim{contract: contract, readers: 1}
		return nil
	}
	if held.contract != contract {
		return fmt.Errorf("kafka: consumer group %q already reads topic %q for contract %q, so it cannot also read %q there - the two readers would be two members of the group, splitting the topic's partitions while each commits-and-skips the other's contract, and both would lose messages; map the contracts onto separate topics or give this subscription its own group",
			group, topic, held.contract, contract)
	}
	held.readers++
	return nil
}

// release drops one reader's hold when its read loop ends, freeing the
// claim once the last replica is gone.
func (t *Transport) release(group, topic string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := groupTopic{group: group, topic: topic}
	held := t.held[key]
	if held == nil {
		return
	}
	if held.readers--; held.readers <= 0 {
		delete(t.held, key)
	}
}

// consume is the per-subscription read loop. Every record it fetches is
// committed - handled, failed, or addressed to another contract.
//
// A handler error is REPORTED AND THE MESSAGE DROPPED; it is not
// redelivered. Leaving it uncommitted would not hold it: a group offset
// is a per-partition high-water mark and kafka-go commits offset+1, so
// the next message that succeeds on that partition commits past the
// failure anyway. Nor can the loop stop advancing that partition -
// FetchMessage reads one channel fed by every partition the reader owns,
// so blocking would stall all of them. Committing immediately at least
// makes the drop happen at a defined point instead of whenever an
// unrelated message happens to succeed. Install [WithErrorHandler]:
// it is the only record that the message arrived.
func (t *Transport) consume(ctx context.Context, r *kgo.Reader, sub events.Subscription) {
	defer func() { _ = r.Close() }()
	for {
		rec, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			if t.onError != nil {
				t.onError(sub, nil, fmt.Errorf("kafka: fetch %s: %w", r.Config().Topic, err))
			}
			continue
		}
		msg := decode(sub.Event, rec)
		// A topic may carry several contracts; skip what this
		// subscription did not ask for rather than handing a handler a
		// payload of the wrong type.
		if msg.Event == sub.Event {
			if err := sub.Handle(ctx, msg); err != nil && t.onError != nil {
				t.onError(sub, msg, err)
			}
		}
		if err := r.CommitMessages(ctx, rec); err != nil && t.onError != nil {
			t.onError(sub, msg, fmt.Errorf("kafka: commit: %w", err))
		}
	}
}

// decode rebuilds a craftgo message from a Kafka record. The contract
// comes from the header, falling back to the subscription's own contract
// for a record written by something that does not set it.
func decode(contract string, rec kgo.Message) *events.Message {
	out := &events.Message{Event: contract, Key: string(rec.Key), Payload: rec.Value, Metadata: map[string]string{}}
	for _, h := range rec.Headers {
		switch h.Key {
		case HeaderEvent:
			out.Event = string(h.Value)
		case HeaderKey:
			out.Key = string(h.Value)
		default:
			out.Metadata[h.Key] = string(h.Value)
		}
	}
	return out
}

// Close shuts every writer and reader this transport opened.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []error
	for _, w := range t.writers {
		errs = append(errs, w.Close())
	}
	for _, r := range t.readers {
		errs = append(errs, r.Close())
	}
	t.writers = map[string]*kgo.Writer{}
	t.readers = nil
	t.held = map[groupTopic]*topicClaim{}
	return errors.Join(errs...)
}
