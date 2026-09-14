// Package kafka adapts craftgo's event runtime to Kafka.
//
// # Topics
//
// The default maps one contract to one topic. A message's ordering key -
// [events.WithKey] at the publish call - becomes the Kafka record key, so
// one entity's messages land in one partition and Kafka orders them,
// within that one contract.
//
// [WithTopic] replaces the mapping when the broker's naming is not yours
// to choose:
//
//	kafka.New(brokers, kafka.WithTopic(func(c string) string { return "app." + c }))
//
// The contract always travels in the [HeaderEvent] header, so a topic
// carrying several contracts stays self-describing.
//
// # Two modes
//
// The default is a classic consumer group: the client owns partitions,
// offsets advance as a high-water mark, and a delivery can only be taken
// as done. [WithShareGroup] switches to a KIP-932 share group, where the
// broker tracks each record and a consumer can hand one back for
// redelivery or give it up as poison - which is what makes
// [events.Message.Redeliver] and [events.Message.Reject] mean anything
// here.
//
// The mode is never detected. A delivery guarantee that depended on which
// broker answered would change under a failover with nothing to see it,
// so a share group is asked for and, if the broker cannot serve one,
// [Transport.Subscribe] refuses rather than quietly consuming as a
// classic group. Share groups need Kafka 4.2 or newer.
//
// # Ordering across contracts is not supported
//
// Two contracts about one entity have no order between them, and no
// configuration of this adapter gives them one. Collapsing them onto one
// topic does not: [Transport.Subscribe] refuses one group reading two
// different contracts on one topic, because the members would divide that
// topic between them and each skip the other's contract. Separate groups
// on one topic are separate readers, so they are not ordered either. Use a
// group for scale and for failure isolation; do not use one expecting
// cross-contract order.
//
// A subscription's group is the Kafka group - consumer or share - so
// replicas sharing one share the work and a different group gets its own
// copy.
package kafka

import (
	"context"
	"crypto/tls"
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

// HeaderEvent carries the contract name, so a topic holding several
// contracts remains self-describing.
const HeaderEvent = "craftgo-event"

// HeaderKey carries the ordering key for consumers that want it without
// decoding the payload. The same value is the record key.
const HeaderKey = "craftgo-key"

// HeaderDedupID carries [events.Message.DedupID]. Kafka does not
// deduplicate on it, and neither does this adapter: carrying it lets a
// CONSUMER recognise a repeat for itself, which destroying it made
// impossible.
//
// The producer's idempotence is not that feature and does not stand in
// for it. It covers a request this client reissued after a transient
// network failure, keyed on a producer ID and a sequence number that
// craftgo never sets - two separate Publish calls carrying one dedup ID
// are two records through one client, not one.
//
// It sits under [events.MetaPrefix], so a caller cannot forge one through
// [events.WithHeader]: the runtime drops a metadata entry under that name
// before the message reaches this adapter.
const HeaderDedupID = "craftgo-dedup-id"

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

// The share-group API keys this adapter needs, probed before a share
// subscription is registered. Named because the refusal quotes them.
const (
	apiShareGroupHeartbeat = 76
	apiShareFetch          = 78
	apiShareAcknowledge    = 79
)

// A Transport is a full transport: it publishes, subscribes, takes a
// batch in one call, names itself to the per-message option check, and
// says which dispositions it can honour. Asserted here so a change to the
// runtime interfaces fails this package rather than a user's wiring.
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
	// dial carries the connection settings every client this transport
	// opens is built with - TLS, SASL.
	dial []kgo.Opt

	mu       sync.Mutex
	producer *kgo.Client
	clients  []*kgo.Client
	// held records what each (group, topic) pair is being read for, so a
	// second reader asking for a DIFFERENT contract is refused rather
	// than silently splitting the topic with the first.
	held map[groupTopic]*topicClaim
	// shareOK caches a successful share-API probe. Only success is
	// cached: a probe that failed on a network blip must be retried, and
	// one that failed because the broker is too old will fail again.
	shareOK bool
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
// error, and for the failures a read loop meets on its own. In a classic
// group the message is taken as done either way, so this callback is the
// only record that it arrived.
func WithErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) Option {
	return func(t *Transport) { t.onError = fn }
}

// WithShareGroup consumes through a Kafka share group (KIP-932) instead
// of a classic consumer group. The broker tracks each record, so a
// middleware calling [events.Message.Redeliver] gets the record back and
// one calling [events.Message.Reject] gives it up.
//
// Requires a broker serving ShareGroupHeartbeat, ShareFetch and
// ShareAcknowledge - Kafka 4.2 or newer. [Transport.Subscribe] probes for
// all three and refuses rather than consuming as a classic group, because
// a delivery guarantee that changed with the broker would change under a
// failover with nothing to see it. Off by default.
//
// A share group starts at the END of the topic unless the group config
// share.auto.offset.reset says otherwise, so a group joining a topic that
// already holds records sees none of them until it is set.
func WithShareGroup() Option {
	return func(t *Transport) { t.share = true }
}

// WithMaxDeliveries caps how many times the broker may hand one record
// over before this adapter gives it up rather than asking for it again.
// It bounds a redelivery loop: a middleware that keeps calling
// [events.Message.Redeliver] on a record nothing can handle stops being
// obeyed once the count is reached, and the record is rejected. A
// delivery that SUCCEEDS on the last attempt is still taken as done.
//
// Default 5. Zero is unbounded and has to be chosen. Share mode only -
// a classic group has no delivery count to read.
func WithMaxDeliveries(n int) Option {
	return func(t *Transport) { t.maxDeliveries = n }
}

// WithClientOptions passes options straight to every franz-go client this
// transport opens - a compression codec, a client ID, a request timeout,
// anything construction-time that craftgo does not wrap.
//
// It is the same shape as [WithTLS] and the SASL options, which are each
// one kgo.Opt appended to the same list; this is the general form of them.
//
// craftgo's own options are applied AFTER these, so an option that would
// change what a client IS - the group it joins, the topics it consumes -
// does not take effect and fails construction instead. Use the transport's
// own options for those: the adapter refuses one group reading two
// contracts on a topic, and a client that joined a group behind its back
// would be outside that guard.
func WithClientOptions(opts ...kgo.Opt) Option {
	return func(t *Transport) { t.dial = append(t.dial, opts...) }
}

// WithTLS dials the brokers over TLS. A nil config uses the system roots.
func WithTLS(cfg *tls.Config) Option {
	return func(t *Transport) { t.dial = append(t.dial, kgo.DialTLSConfig(cfg)) }
}

// WithSASLPlain authenticates with SASL/PLAIN. Pair it with [WithTLS]:
// PLAIN sends the password where anything on the path can read it.
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
		held:          map[groupTopic]*topicClaim{},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: the per-message options
// this adapter reads. Anything else addressed to `kafka` fails the
// publish rather than being dropped.
func (t *Transport) KnownOptions() []string { return []string{OptionTimestamp} }

// CanDisposition implements [events.Dispositioner]. Redeliver and reject
// need the broker to be tracking each record, which is what a share group
// does and a classic consumer group does not - so the answer depends on
// how THIS transport was built, and is fixed once it is.
func (t *Transport) CanDisposition(d events.Disposition) bool {
	switch d {
	case events.DispositionSettle:
		return true
	case events.DispositionRedeliver, events.DispositionReject:
		return t.share
	}
	return false
}

// newClient opens a client and refuses one whose consuming identity is not
// what this transport meant it to be.
//
// wantGroup is the group the client is supposed to join, empty for a
// client that must not consume at all - the producer and the share-API
// probe. Caller options arrive through [WithClientOptions] and craftgo's
// own are applied after them, so a group opt from a caller never takes
// effect; this turns "did not take effect" into a construction failure,
// because a producer that quietly joined a consumer group would be a
// second member splitting the stream, outside the claim guard that exists
// to prevent exactly that. Even the probe matters: joining a group for a
// moment rebalances the live one.
//
// Every client goes through here, including the legitimate consumer - it
// passes its own group and is checked against it rather than excused, so
// a fourth construction site cannot inherit no check at all.
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

// unexpectedIdentity names how a client's consuming identity differs from
// wantGroup, or "" when it matches. A client joins at most one kind of
// group, so the one it joined is the one that must match.
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

// consumesAnything reports whether the client was told to consume topics.
//
// An unrecognised shape counts as YES. franz-go's own OptValues doc says
// this option reads back as a []string and the field behind it is a map,
// so a type switch that fell through to "no" is how this check silently
// passed the first time it was written.
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

// clientOpts returns the options every client this transport opens shares.
func (t *Transport) clientOpts(extra ...kgo.Opt) []kgo.Opt {
	opts := make([]kgo.Opt, 0, len(t.dial)+len(extra)+2)
	opts = append(opts, kgo.SeedBrokers(t.brokers...))
	opts = append(opts, t.dial...)
	if t.autoTopic {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}
	return append(opts, extra...)
}

// Publish sends one message.
func (t *Transport) Publish(ctx context.Context, msg *events.Message) error {
	rec, err := t.encode(msg)
	if err != nil {
		return err
	}
	cl, err := t.producerClient()
	if err != nil {
		return err
	}
	return cl.ProduceSync(ctx, rec).FirstErr()
}

// PublishBatch hands the whole batch to one ProduceSync, which is what
// lets the client batch them on the wire. Every record is built first, so
// a message this adapter cannot encode fails the batch before anything is
// sent.
//
// A failure partway names exactly which messages did not go out. They are
// not a tail: franz-go produces to every partition at once and reports in
// completion order, so a batch mixing two topics can fail on one and
// deliver the other, leaving gaps. Each result carries the record it is
// for, which is what makes the indices recoverable.
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
		// Every result failed to name its record - nothing is known to
		// have landed, so say so rather than claiming a partial send.
		return firstErr
	}
	// UnsentAt and not UnsentFrom: these indices have gaps in them.
	return events.UnsentAt(unsent, msgs, firstErr)
}

// encode maps a craftgo message onto a Kafka record. Metadata becomes
// headers beside the contract and the key, which keep their own.
//
// [HeaderEvent], [HeaderKey] and [HeaderDedupID] are this adapter's, so a
// metadata entry under any of their names is skipped rather than written
// a second time: decode reads the last header of a name, so a duplicate
// would rename the message, move it to another entity, or give it another
// message's deduplication identity. The runtime drops those keys before a
// message gets here; a hand-built [events.Message] does not go through
// it.
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
	// A keyless message must carry a nil Key, not an empty one. The
	// default partitioner keys on `r.Key != nil`, so []byte("") counts as
	// a key and hashes every keyless record onto one partition.
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

// producerClient returns the transport's producer, opening it once. One
// client serves every topic: a record carries its own.
func (t *Transport) producerClient() (*kgo.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
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

// Subscribe joins the group named by sub.Group and reads until ctx is
// cancelled.
//
// One group may read several contracts, but not two of them on one topic:
// the two readers would be two members of the group, dividing that topic
// between them while each skips the contract the other asked for, so both
// lose messages. That pair is refused. It is reachable only through a
// [WithTopic] mapping that puts two contracts on one topic; under the
// default mapping the check never fires.
//
// Replicas are not that case. Two readers in one group on one topic for
// the SAME contract are ordinary members dividing the work, which is what
// the in-process transport does with two identical subscriptions, so they
// are allowed.
//
// In share mode the broker is probed for the share APIs first, and
// Subscribe returns an error if it does not serve them. The probe is the
// only thing standing between an old broker and a deployable that boots,
// serves HTTP, passes readiness and consumes nothing: franz-go reports
// the lack on the first poll, which happens on a goroutine nobody is
// waiting on.
func (t *Transport) Subscribe(ctx context.Context, sub events.Subscription) error {
	group, topic := sub.GroupName(), t.topic(sub.Event)
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

// openConsumer probes for the share APIs when they are needed and returns
// the client for one subscription.
func (t *Transport) openConsumer(ctx context.Context, group, topic string) (*kgo.Client, error) {
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
	t.mu.Lock()
	t.clients = append(t.clients, cl)
	t.mu.Unlock()
	return cl, nil
}

// probeShareAPIs asks the broker what it serves and refuses a share
// subscription it could not honour. It runs before the subscription is
// registered, so the refusal reaches the caller rather than a read loop.
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
			return fmt.Errorf("kafka: WithShareGroup needs %s (API key %d), which this broker does not serve - share groups are Kafka 4.2 and newer; drop the option to consume as a classic consumer group, which cannot redeliver or reject",
				api.name, api.key)
		}
	}

	t.mu.Lock()
	t.shareOK = true
	t.mu.Unlock()
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
		return fmt.Errorf("kafka: group %q already reads topic %q for contract %q, so it cannot also read %q there - the two readers would be two members of the group, dividing the topic between them while each skips the other's contract, and both would lose messages; map the contracts onto separate topics or give this subscription its own group",
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

// consume is the per-subscription read loop.
//
// In a classic group every record is taken as done - handled, failed, or
// addressed to another contract. Leaving one uncommitted would not hold
// it: a group offset is a per-partition high-water mark, so the next
// record that succeeds on that partition commits past the failure anyway.
// Install [WithErrorHandler]; it is the only record that the message
// arrived.
//
// In a share group the broker holds each record until this loop answers
// for it, so what a middleware asked for through [events.Message] is what
// the record gets.
func (t *Transport) consume(ctx context.Context, cl *kgo.Client, sub events.Subscription) {
	defer cl.Close()
	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return
		}
		fetches.EachError(func(topic string, _ int32, err error) {
			if ctx.Err() != nil || t.onError == nil {
				return
			}
			t.onError(sub, nil, fmt.Errorf("kafka: fetch %s: %w", topic, err))
		})

		var polled []*kgo.Record
		fetches.EachRecord(func(rec *kgo.Record) {
			polled = append(polled, rec)
			t.deliver(ctx, sub, rec)
		})
		if !t.share && len(polled) > 0 {
			if err := cl.CommitRecords(ctx, polled...); err != nil && t.onError != nil && ctx.Err() == nil {
				t.onError(sub, nil, fmt.Errorf("kafka: commit: %w", err))
			}
		}
	}
}

// deliver hands one record to the subscription and answers for it.
func (t *Transport) deliver(ctx context.Context, sub events.Subscription, rec *kgo.Record) {
	msg := decode(sub.Event, rec)
	msg.SetDeliveries(int(rec.DeliveryCount()))

	// A topic may carry several contracts; a record this subscription did
	// not ask for is not handed to a handler that would decode it as the
	// wrong type. It is reported rather than passed over in silence -
	// being sent one is a mapping mistake somebody has to hear about.
	if msg.Event != sub.Event {
		if t.onError != nil {
			t.onError(sub, msg, fmt.Errorf("kafka: topic %s carried %s, which %s does not consume - skipped", rec.Topic, msg.Event, sub.Consumer))
		}
		if t.share {
			rec.Ack(kgo.AckAccept)
		}
		return
	}

	if err := sub.Handle(withRecord(ctx, rec), msg); err != nil && t.onError != nil {
		t.onError(sub, msg, err)
	}
	if t.share {
		rec.Ack(t.ackFor(msg))
	}
}

// ackFor turns what the chain asked for into the broker's answer. An
// unset disposition settles: a middleware that decided nothing is not
// asking for the record back.
func (t *Transport) ackFor(msg *events.Message) kgo.AckStatus {
	switch msg.Disposition() {
	case events.DispositionRedeliver:
		if t.maxDeliveries > 0 && msg.Deliveries() >= t.maxDeliveries {
			return kgo.AckReject
		}
		return kgo.AckRelease
	case events.DispositionReject:
		return kgo.AckReject
	}
	return kgo.AckAccept
}

// decode rebuilds a craftgo message from a Kafka record. The contract
// comes from the header, falling back to the subscription's own contract
// for a record written by something that does not set it.
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

// Close shuts every client this transport opened.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.producer != nil {
		t.producer.Close()
		t.producer = nil
	}
	for _, cl := range t.clients {
		cl.Close()
	}
	t.clients = nil
	t.held = map[groupTopic]*topicClaim{}
	return nil
}
