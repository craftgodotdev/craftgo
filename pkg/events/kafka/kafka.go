// Package kafka adapts craftgo's event runtime to Kafka.
//
// A contract maps onto a topic, the contract name unchanged by default, and
// travels in the [HeaderEvent] header. The ordering key becomes the record
// key, so Kafka orders one entity's messages within one contract; nothing
// orders messages across contracts. The deduplication ID travels in
// [HeaderDedupID].
//
// A subscription's group is a classic consumer group, which takes every
// delivery as done, or with [WithShareGroup] a share group, which can also
// redeliver and reject.
//
// [RecordFrom] exposes the record behind a delivery. Decide through
// [events.Message]; do not ack the record or keep it past the handler.
package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// HeaderEvent carries the contract name.
const HeaderEvent = "craftgo-event"

// HeaderKey carries the ordering key, which is also the record key.
const HeaderKey = "craftgo-key"

// HeaderDedupID carries [events.Message.DedupID], under [events.MetaPrefix]
// so [events.WithHeader] cannot forge it. Neither Kafka nor this adapter
// deduplicates on it; a consumer can.
const HeaderDedupID = "craftgo-dedup-id"

// Adapter is the name [events.WithAdapterOption] addresses this adapter by.
const Adapter = "kafka"

// OptionTimestamp is the [events.WithAdapterOption] key that sets a record's
// timestamp; its value must be a [time.Time]. Without it the record carries
// the time it was produced.
const OptionTimestamp = "timestamp"

// ErrClosed is what a publish or subscribe returns after [Transport.Close].
var ErrClosed = errors.New("transport closed")

// Share-group API keys, probed before a share subscription starts.
const (
	apiShareGroupHeartbeat = 76
	apiShareFetch          = 78
	apiShareAcknowledge    = 79
)

var (
	_ events.Publisher      = (*Transport)(nil)
	_ events.Subscriber     = (*Transport)(nil)
	_ events.BatchPublisher = (*Transport)(nil)
	_ events.OptionAware    = (*Transport)(nil)
	_ events.Dispositioner  = (*Transport)(nil)
)

// Transport publishes and consumes over Kafka.
type Transport struct {
	brokers       []string
	topic         func(contract string) string
	onError       func(sub events.Subscription, msg *events.Message, err error)
	autoTopic     bool
	share         bool
	maxDeliveries int
	lockRenew     time.Duration
	// dial is applied to every client: TLS, SASL and WithClientOptions.
	dial []kgo.Opt

	mu       sync.Mutex
	closed   bool
	producer *kgo.Client
	clients  []*kgo.Client
	// held is the contract each group reads each topic for.
	held map[groupTopic]*topicClaim
	// shareOK caches a successful share-API probe; a failed one is retried.
	shareOK bool
}

// groupTopic is one consumer group's claim on one topic.
type groupTopic struct{ group, topic string }

// topicClaim is the contract a group reads a topic for, and its live readers.
type topicClaim struct {
	contract string
	readers  int
}

// Option configures a Transport.
type Option func(*Transport)

// WithTopic replaces the contract-to-topic mapping; the default is the
// contract name unchanged. [Transport.Subscribe] refuses one group reading
// two contracts mapped onto one topic.
func WithTopic(fn func(contract string) string) Option {
	return func(t *Transport) { t.topic = fn }
}

// WithAutoCreateTopics lets the broker create a missing topic on first
// publish. Off by default.
func WithAutoCreateTopics(on bool) Option {
	return func(t *Transport) { t.autoTopic = on }
}

// WithErrorHandler installs a callback for handler errors and read-loop
// failures. A classic group takes a failed message as done, so this is its
// only record.
func WithErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) Option {
	return func(t *Transport) { t.onError = fn }
}

// WithShareGroup consumes through a KIP-932 share group, which can redeliver
// and reject. It needs Kafka 4.2, or 4.1 with [WithLockRenewInterval] zero.
// A new share group starts at the end of the topic (share.auto.offset.reset).
func WithShareGroup() Option {
	return func(t *Transport) { t.share = true }
}

// WithMaxDeliveries caps the deliveries of one record: at the cap, a
// Redeliver the chain asked for becomes a reported reject. Default 5; zero
// is unbounded. Share mode only.
func WithMaxDeliveries(n int) Option {
	return func(t *Transport) { t.maxDeliveries = n }
}

// WithLockRenewInterval sets how often a share-group record's lock is renewed
// while its handler runs. Default 10s, under the broker's 30s lock; zero stops
// renewing. Renewal needs Kafka 4.2, which [Transport.Subscribe] checks.
func WithLockRenewInterval(d time.Duration) Option {
	return func(t *Transport) { t.lockRenew = d }
}

// WithClientOptions passes opts to every franz-go client the transport
// opens, before craftgo's group and topic options. A client whose group or
// topics still differ from the transport's fails to open.
func WithClientOptions(opts ...kgo.Opt) Option {
	return func(t *Transport) { t.dial = append(t.dial, opts...) }
}

// WithTLS dials the brokers over TLS using cfg; a nil or empty cfg verifies
// the brokers against the system roots.
func WithTLS(cfg *tls.Config) Option {
	if cfg == nil {
		cfg = new(tls.Config)
	}
	return func(t *Transport) { t.dial = append(t.dial, kgo.DialTLSConfig(cfg)) }
}

// WithSASLPlain authenticates with SASL/PLAIN, which sends the password in
// clear text; pair it with [WithTLS].
func WithSASLPlain(user, pass string) Option {
	return func(t *Transport) {
		t.dial = append(t.dial, kgo.SASL(plain.Auth{User: user, Pass: pass}.AsMechanism()))
	}
}

// WithSASLSCRAMSHA256 authenticates with SASL/SCRAM-SHA-256.
func WithSASLSCRAMSHA256(user, pass string) Option {
	return func(t *Transport) {
		t.dial = append(t.dial, kgo.SASL(scram.Auth{User: user, Pass: pass}.AsSha256Mechanism()))
	}
}

// WithSASLSCRAMSHA512 authenticates with SASL/SCRAM-SHA-512.
func WithSASLSCRAMSHA512(user, pass string) Option {
	return func(t *Transport) {
		t.dial = append(t.dial, kgo.SASL(scram.Auth{User: user, Pass: pass}.AsSha512Mechanism()))
	}
}

// New binds a Transport to a broker list.
func New(brokers []string, opts ...Option) *Transport {
	t := &Transport{
		brokers:       brokers,
		topic:         func(c string) string { return c },
		maxDeliveries: 5,
		lockRenew:     10 * time.Second,
		held:          map[groupTopic]*topicClaim{},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: this adapter reads
// [OptionTimestamp], and any other option addressed to it fails the publish.
func (t *Transport) KnownOptions() []string { return []string{OptionTimestamp} }

// CanDisposition implements [events.Dispositioner]. Redeliver and reject
// need [WithShareGroup].
func (t *Transport) CanDisposition(d events.Disposition) bool {
	switch d {
	case events.DispositionSettle:
		return true
	case events.DispositionRedeliver, events.DispositionReject:
		return t.share
	}
	return false
}

// newClient opens a client and refuses one whose group or topics are not the
// transport's; wantGroup is "" for a client that must not consume.
func (t *Transport) newClient(wantGroup string, extra ...kgo.Opt) (*kgo.Client, error) {
	cl, err := kgo.NewClient(t.clientOpts(extra...)...)
	if err != nil {
		return nil, err
	}
	if bad := unexpectedIdentity(cl, wantGroup); bad != "" {
		cl.Close()
		return nil, fmt.Errorf("kafka: client options set %s - a client's group and topics are the transport's to decide, not WithClientOptions'", bad)
	}
	return cl, nil
}

// unexpectedIdentity names how cl's group or topics differ from wantGroup.
func unexpectedIdentity(cl *kgo.Client, wantGroup string) string {
	consumer, _ := cl.OptValue(kgo.ConsumerGroup).(string)
	share, _ := cl.OptValue(kgo.ShareGroup).(string)
	if consumer != "" && share != "" {
		return fmt.Sprintf("both a consumer group %q and a share group %q", consumer, share)
	}
	joined := consumer
	if share != "" {
		joined = share
	}
	if joined != wantGroup {
		return fmt.Sprintf("group %q, want %q", joined, wantGroup)
	}
	if wantGroup == "" && consumesAnything(cl) {
		return "topics to consume on a client that must not consume"
	}
	return ""
}

// consumesAnything reports whether cl was told to consume topics. An unknown
// value type counts as yes: franz-go documents a []string but returns a map.
func consumesAnything(cl *kgo.Client) bool {
	switch v := cl.OptValue(kgo.ConsumeTopics).(type) {
	case nil:
		return false
	case map[string]*regexp.Regexp:
		return len(v) > 0
	case []string:
		return len(v) > 0
	default:
		return true
	}
}

// clientOpts returns the shared client options followed by extra.
func (t *Transport) clientOpts(extra ...kgo.Opt) []kgo.Opt {
	opts := make([]kgo.Opt, 0, len(t.dial)+len(extra)+2)
	opts = append(opts, kgo.SeedBrokers(t.brokers...))
	opts = append(opts, t.dial...)
	if t.autoTopic {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}
	return append(opts, extra...)
}

// Publish sends one message and waits for the broker to acknowledge it.
func (t *Transport) Publish(ctx context.Context, msg *events.Message) error {
	rec, err := t.encode(msg)
	if err != nil {
		return err
	}
	cl, err := t.producerClient()
	if err != nil {
		return err
	}
	if err := cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return fmt.Errorf("kafka: publish %s: %w", msg.Event, err)
	}
	return nil
}

// PublishBatch produces the whole batch in one call. A message it cannot
// encode fails the batch before anything is sent; a failure after that
// names each unsent message, which need not be a tail.
func (t *Transport) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	recs := make([]*kgo.Record, 0, len(msgs))
	index := make(map[*kgo.Record]int, len(msgs))
	for i, msg := range msgs {
		rec, err := t.encode(msg)
		if err != nil {
			return err
		}
		index[rec] = i
		recs = append(recs, rec)
	}
	cl, err := t.producerClient()
	if err != nil {
		return err
	}
	results := cl.ProduceSync(ctx, recs...)

	var unsent []int
	var firstErr error
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = r.Err
		}
		if i, ok := index[r.Record]; ok {
			unsent = append(unsent, i)
		}
	}
	if firstErr == nil {
		return nil
	}
	if len(unsent) == 0 {
		// No failure named a record of this batch, so no index is known.
		return fmt.Errorf("kafka: publish batch of %d: %w", len(msgs), firstErr)
	}
	return events.UnsentAt(unsent, msgs, fmt.Errorf("kafka: %w", firstErr))
}

// encode maps msg onto a Kafka record. Metadata never overrides the adapter's
// own headers, since decode reads the last header of a name.
func (t *Transport) encode(msg *events.Message) (*kgo.Record, error) {
	headers := []kgo.RecordHeader{{Key: HeaderEvent, Value: []byte(msg.Event)}}
	if msg.Key != "" {
		headers = append(headers, kgo.RecordHeader{Key: HeaderKey, Value: []byte(msg.Key)})
	}
	if msg.DedupID != "" {
		headers = append(headers, kgo.RecordHeader{Key: HeaderDedupID, Value: []byte(msg.DedupID)})
	}
	for k, v := range msg.Metadata {
		if k == HeaderEvent || k == HeaderKey || k == HeaderDedupID {
			continue
		}
		headers = append(headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	// A keyless record needs a nil Key: the default partitioner hashes any
	// non-nil key, []byte("") included, onto one partition.
	var key []byte
	if msg.Key != "" {
		key = []byte(msg.Key)
	}
	rec := &kgo.Record{Topic: t.topic(msg.Event), Key: key, Value: msg.Payload, Headers: headers}
	if v, ok := msg.AdapterOption(Adapter, OptionTimestamp); ok {
		ts, ok := v.(time.Time)
		if !ok {
			return nil, fmt.Errorf("kafka: option %q on %s is %T, want time.Time", OptionTimestamp, msg.Event, v)
		}
		rec.Timestamp = ts
	}
	return rec, nil
}

// producerClient returns the producer, opening it on first use.
func (t *Transport) producerClient() (*kgo.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, fmt.Errorf("kafka: open producer: %w", ErrClosed)
	}
	if t.producer != nil {
		return t.producer, nil
	}
	cl, err := t.newClient("")
	if err != nil {
		return nil, fmt.Errorf("kafka: open producer: %w", err)
	}
	t.producer = cl
	return cl, nil
}

// Subscribe starts a read loop per subscription, each running until ctx is
// cancelled. The first failure stops registration and is returned; loops
// already started stay live.
func (t *Transport) Subscribe(ctx context.Context, subs []events.Subscription) error {
	for _, sub := range subs {
		if err := t.subscribeOne(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}

// subscribeOne claims sub's topic for its group and reads until ctx ends.
func (t *Transport) subscribeOne(ctx context.Context, sub events.Subscription) error {
	group, topic := string(sub.Group), t.topic(sub.Event)
	if err := t.claim(group, topic, sub.Event); err != nil {
		return err
	}
	cl, err := t.openConsumer(ctx, group, topic)
	if err != nil {
		t.release(group, topic)
		return err
	}
	go func() {
		defer t.release(group, topic)
		t.consume(ctx, cl, sub)
	}()
	return nil
}

// openConsumer opens one subscription's client, probing share support first;
// after [Transport.Close] it opens nothing.
func (t *Transport) openConsumer(ctx context.Context, group, topic string) (*kgo.Client, error) {
	if t.isClosed() {
		return nil, fmt.Errorf("kafka: open consumer for %q: %w", topic, ErrClosed)
	}
	mode := kgo.ConsumerGroup(group)
	if t.share {
		if err := t.probeShareAPIs(ctx); err != nil {
			return nil, err
		}
		mode = kgo.ShareGroup(group)
	}
	cl, err := t.newClient(group, mode, kgo.ConsumeTopics(topic))
	if err != nil {
		return nil, fmt.Errorf("kafka: open consumer for %q: %w", topic, err)
	}
	if !t.track(cl) {
		cl.Close()
		return nil, fmt.Errorf("kafka: open consumer for %q: %w", topic, ErrClosed)
	}
	return cl, nil
}

func (t *Transport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// track records cl for [Transport.Close], or reports false when Close ran while cl opened.
func (t *Transport) track(cl *kgo.Client) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.clients = append(t.clients, cl)
	return true
}

// probeShareAPIs refuses, before any read loop starts, a broker that cannot
// serve a share subscription. Only success is cached.
func (t *Transport) probeShareAPIs(ctx context.Context) error {
	t.mu.Lock()
	ok := t.shareOK
	t.mu.Unlock()
	if ok {
		return nil
	}

	cl, err := t.newClient("")
	if err != nil {
		return fmt.Errorf("kafka: probe share support: %w", err)
	}
	defer cl.Close()

	resp, err := cl.Request(ctx, kmsg.NewPtrApiVersionsRequest())
	if err != nil {
		return fmt.Errorf("kafka: probe share support: %w", err)
	}
	versions, isVersions := resp.(*kmsg.ApiVersionsResponse)
	if !isVersions {
		return fmt.Errorf("kafka: probe share support: broker answered %T", resp)
	}
	served := kversion.FromApiVersionsResponse(versions)
	for _, api := range []struct {
		key  int16
		name string
	}{
		{apiShareGroupHeartbeat, "ShareGroupHeartbeat"},
		{apiShareFetch, "ShareFetch"},
		{apiShareAcknowledge, "ShareAcknowledge"},
	} {
		if !served.HasKey(api.key) {
			return fmt.Errorf("kafka: WithShareGroup needs %s (API key %d), which this broker does not serve - share groups are Kafka 4.1 and newer; drop the option to consume as a classic consumer group, which cannot redeliver or reject",
				api.name, api.key)
		}
	}

	// The renew flag is a ShareAcknowledge v2 field; Kafka 4.1 serves v1,
	// which drops the flag and lets the lock lapse unreported.
	if t.lockRenew > 0 {
		if v, _ := served.LookupMaxKeyVersion(apiShareAcknowledge); v < 2 {
			return fmt.Errorf("kafka: WithLockRenewInterval needs ShareAcknowledge v2 (API key %d), and this broker serves v%d - renewing a record's acquisition lock is Kafka 4.2 and newer, and on an older one a handler slower than the lock is delivered again with nothing reporting it; pass WithLockRenewInterval(0) to consume without renewal",
				apiShareAcknowledge, v)
		}
	}

	t.mu.Lock()
	t.shareOK = true
	t.mu.Unlock()
	return nil
}

// claim records that group reads topic for contract. Another reader of the
// same contract joins the claim; one for a different contract is refused.
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
		return fmt.Errorf("kafka: group %q already reads topic %q for contract %q, so it cannot also read %q there - the two readers would be two members of the group, dividing the topic between them while each skips the other's contract, and both would lose messages; map the contracts onto separate topics or give this subscription its own group",
			group, topic, held.contract, contract)
	}
	held.readers++
	return nil
}

// release drops one reader from the claim, freeing it after the last.
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

// consume is one subscription's read loop. A classic group commits every
// polled record, failed or not: its offset is a per-partition high-water mark.
func (t *Transport) consume(ctx context.Context, cl *kgo.Client, sub events.Subscription) {
	defer t.releaseClient(cl)
	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return
		}
		fetches.EachError(func(topic string, _ int32, err error) {
			if ctx.Err() == nil {
				t.report(sub, nil, fmt.Errorf("kafka: fetch %s: %w", topic, err))
			}
		})

		var polled []*kgo.Record
		fetches.EachRecord(func(rec *kgo.Record) {
			polled = append(polled, rec)
			t.deliver(ctx, cl, sub, rec)
		})
		if !t.share && len(polled) > 0 {
			if err := cl.CommitRecords(ctx, polled...); err != nil && ctx.Err() == nil {
				t.report(sub, nil, fmt.Errorf("kafka: commit: %w", err))
			}
		}
	}
}

// deliver hands one record to the subscription and answers for it.
func (t *Transport) deliver(ctx context.Context, cl *kgo.Client, sub events.Subscription, rec *kgo.Record) {
	msg := decode(sub.Event, rec)
	msg.SetDeliveries(int(rec.DeliveryCount()))

	// A record of another contract on this topic is reported and skipped.
	if msg.Event != sub.Event {
		t.report(sub, msg, fmt.Errorf("kafka: topic %s carried %s, which %s does not consume - skipped", rec.Topic, msg.Event, sub.Consumer))
		if t.share {
			rec.Ack(kgo.AckAccept)
		}
		return
	}

	stop := t.holdOpen(ctx, cl, rec)
	err := sub.Handle(withRecord(ctx, rec), msg)
	stop()

	if err != nil {
		t.report(sub, msg, err)
	}
	if t.share {
		// The chain never sees the cap turn its Redeliver into a reject.
		if t.capped(msg) {
			t.report(sub, msg, fmt.Errorf("kafka: giving up on %s after %d deliveries - the chain asked for another and WithMaxDeliveries is %d", sub.Event, msg.Deliveries(), t.maxDeliveries))
		}
		rec.Ack(t.ackFor(msg))
	}
}

func (t *Transport) report(sub events.Subscription, msg *events.Message, err error) {
	if t.onError != nil {
		t.onError(sub, msg, err)
	}
}

// holdOpen renews rec's acquisition lock until the returned func is called,
// flushing each renewal at once so it arrives before the lock lapses.
func (t *Transport) holdOpen(ctx context.Context, cl *kgo.Client, rec *kgo.Record) func() {
	if !t.share || t.lockRenew <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(t.lockRenew)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				rec.Ack(kgo.AckRenew)
				_ = cl.FlushAcks(ctx)
			}
		}
	}()
	return func() { close(done); <-stopped }
}

// capped reports whether [WithMaxDeliveries] overrides the chain's Redeliver.
func (t *Transport) capped(msg *events.Message) bool {
	return msg.Disposition() == events.DispositionRedeliver &&
		t.maxDeliveries > 0 && msg.Deliveries() >= t.maxDeliveries
}

// ackFor maps the chain's disposition to an ack status; unset accepts.
func (t *Transport) ackFor(msg *events.Message) kgo.AckStatus {
	switch msg.Disposition() {
	case events.DispositionRedeliver:
		if t.capped(msg) {
			return kgo.AckReject
		}
		return kgo.AckRelease
	case events.DispositionReject:
		return kgo.AckReject
	}
	return kgo.AckAccept
}

// decode rebuilds a message from rec. The contract comes from [HeaderEvent],
// or from the subscription for a record without one.
func decode(contract string, rec *kgo.Record) *events.Message {
	out := &events.Message{Event: contract, Key: string(rec.Key), Payload: rec.Value, Metadata: map[string]string{}}
	for _, h := range rec.Headers {
		switch h.Key {
		case HeaderEvent:
			out.Event = string(h.Value)
		case HeaderKey:
			out.Key = string(h.Value)
		case HeaderDedupID:
			out.DedupID = string(h.Value)
		default:
			out.Metadata[h.Key] = string(h.Value)
		}
	}
	return out
}

// Close shuts every client this transport opened and drops its group claims;
// a publish or subscribe after it returns [ErrClosed].
func (t *Transport) Close() error {
	t.mu.Lock()
	t.closed = true
	producer, clients := t.producer, t.clients
	t.producer, t.clients = nil, nil
	t.held = map[groupTopic]*topicClaim{}
	t.mu.Unlock()

	if producer != nil {
		producer.Close()
	}
	for _, cl := range clients {
		cl.Close()
	}
	return nil
}

// releaseClient closes cl unless [Transport.Close] already took it;
// kgo.Client.Close has no guard against a second call.
func (t *Transport) releaseClient(cl *kgo.Client) {
	t.mu.Lock()
	held := false
	for i, c := range t.clients {
		if c == cl {
			t.clients = append(t.clients[:i], t.clients[i+1:]...)
			held = true
			break
		}
	}
	t.mu.Unlock()
	if held {
		cl.Close()
	}
}
