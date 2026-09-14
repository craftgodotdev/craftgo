// Package events is craftgo's event runtime: the transport- and
// codec-neutral surface generated publishers and consumers are written
// against. It knows nothing about any message broker and nothing about
// any serialisation format.
//
//   - [Message] is the envelope a transport moves.
//   - [PublishOption] fills in everything beside the payload - the
//     ordering key, a deduplication ID, headers, one adapter's own
//     options. Generated publishers take them.
//   - [Codec] turns a payload value into bytes and back.
//   - [Publisher] and [Subscriber] are the two halves a transport
//     adapter implements.
//   - [Bus] binds a transport to a codec.
//   - [Chain] wraps a consumer's [Handler] in [Middleware]; install one
//     on the bus with [WithMiddleware].
//   - [Disposition] is what a chain asks for one delivery - take it,
//     hand it back, give it up. [WithDispositionRequired] refuses to
//     subscribe on a transport that cannot honour one.
//
// A broker integration is an external package implementing [Publisher]
// and/or [Subscriber]. `pkg/events/memory` ships an in-process one.
package events

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
)

// Message is the envelope a transport moves. Payload is already encoded -
// the [Bus] runs the [Codec] on the way in and out.
type Message struct {
	// Event is the contract name the design declared. A transport maps it
	// onto its own addressing (a topic, a subject, a queue).
	Event string
	// Key identifies the entity the event is about, set by [WithKey].
	// Transports that preserve per-entity ordering use it; the rest
	// ignore it.
	Key string
	// DedupID is the identity a broker that de-duplicates recognises a
	// repeat by, set by [WithDedupID]. A transport without the notion
	// ignores it.
	DedupID string
	// Payload is the encoded payload.
	Payload []byte
	// Metadata carries side-band values: a trace parent, a hop count, a
	// dead-letter marker. A publisher sets them with [WithHeader];
	// [MetaCodec] is the runtime's own and is always present.
	//
	// A transport may drop entries it has nowhere to put, so only
	// [MetaCodec] is guaranteed to survive; the three adapters craftgo
	// ships carry every entry both ways.
	Metadata map[string]string
	// AdapterOptions carries the values one named adapter reads, set by
	// [WithAdapterOption]. Read them with [Message.AdapterOption].
	AdapterOptions map[string]map[string]any

	// Each delivery owns its Message, so these are the state of one
	// attempt rather than of the message: what the chain asked for
	// ([Message.Settle] and friends) and the broker's delivery count.
	// They are unexported because a decision about a delivery is not a
	// value a transport carries from one side to the other.
	disposition Disposition
	deliveries  int
}

// MetaCodec is the [Message.Metadata] key naming the codec that encoded
// the payload. The consuming side rejects a message whose codec differs
// from the one it is configured for.
const MetaCodec = "content-codec"

// MetaPrefix is reserved for transport adapters, which use it for the
// values they carry beside the payload - `craftgo-event` and
// `craftgo-key` on Kafka, `Craftgo-Key` on NATS. An adapter naming its
// own headers outside this prefix is on its own for collisions.
const MetaPrefix = "craftgo-"

// IsReservedMeta reports whether key belongs to the runtime or to a
// transport adapter rather than to the caller: [MetaCodec], or anything
// under [MetaPrefix]. The comparison ignores case, because a header name
// is not case-sensitive on every broker.
//
// These are the only keys [Bus.PublishAll] refuses to carry. A publisher
// need not call this - a reserved entry on [Envelope.Metadata] is simply
// dropped - but a library building metadata for someone else can check
// before it silently loses a value.
func IsReservedMeta(key string) bool {
	if strings.EqualFold(key, MetaCodec) {
		return true
	}
	return len(key) >= len(MetaPrefix) && strings.EqualFold(key[:len(MetaPrefix)], MetaPrefix)
}

// Codec turns a payload value into bytes and back. Implementations must
// be safe for concurrent use.
type Codec interface {
	// Name identifies the encoding on the wire (see [MetaCodec]).
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// Publisher sends a message. Implemented by transport adapters.
//
// A nil error means the adapter has taken responsibility for the message,
// not necessarily that a broker has stored it: whether the call waits for
// an acknowledgement is the adapter's policy, the same way retry and
// dead-lettering are on the receive side. An adapter that reports delivery
// failures after the fact takes its own error handler at construction.
//
// Nothing is said here about ctx on purpose. An adapter may honour it or
// ignore it, and a [Bus] refuses an already-cancelled one before any
// adapter is reached - see [Bus.Publish] - so the guarantee a caller
// reads is the bus's rather than each transport's.
type Publisher interface {
	Publish(ctx context.Context, msg *Message) error
}

// BatchPublisher is the optional upgrade for a transport that can take
// several messages in one call. [Bus.PublishAll] uses it when the
// configured transport implements it and falls back to one [Publisher.Publish]
// per message otherwise.
//
// One call is all it promises. A broker may still split the batch - Kafka
// groups by topic-partition, SQS caps a batch at one queue - and nothing
// here makes the batch atomic; a transport that can do better says so in
// its own documentation.
//
// What an implementation owes its caller:
//
//   - nil means every message was handed over;
//   - a partial failure means a [*PartialPublishError] whose Unsent holds
//     the indices, ascending, into the slice it was GIVEN - build it with
//     [UnsentFrom] when the transport stopped at one message, [UnsentAt]
//     when the failures are scattered;
//   - a plain error means nothing went out. The converse does not hold: a
//     batch where nothing went out may be reported either way, since a
//     report naming every index says the same thing and a caller acts on
//     both identically;
//   - never a bare error after a partial send. The caller reads one as
//     "nothing arrived" and republishes what did;
//   - never call a message sent without this adapter's own confirmation,
//     and never turn one already on the wire into an unsent one by giving
//     up on learning its outcome. Confirmation means [Publisher]'s "handed
//     over", not "a broker stored it".
//
// An outcome the adapter could not learn counts as UNSENT. The two
// mistakes are not symmetrical: a caller retrying a message that did land
// publishes it twice and can see that it did, while one told a lost
// message arrived drops it with nothing downstream able to tell.
//
// A context already cancelled when the call starts is refused by the bus
// before any adapter is reached - see [Bus.Publish] - so every transport
// answers that the same way. An adapter reached directly - they are
// exported, and usable without a bus - answers for it itself.
//
// [Bus.PublishAll] checks the report against the batch and replaces one
// that cannot be true, naming the adapter - but it can only catch a
// report that contradicts itself, not one that is quietly short.
type BatchPublisher interface {
	PublishBatch(ctx context.Context, msgs []*Message) error
}

// Subscriber delivers messages for one contract to a handler.
//
// Subscribe registers the subscription and returns; it must not block.
// Delivery runs until ctx is cancelled - a push transport registers the
// callback, a pull transport starts its own loop. A handler error means
// the message was not processed; retry / nack / dead-letter is the
// transport's policy.
type Subscriber interface {
	Subscribe(ctx context.Context, sub Subscription) error
}

// BatchSubscriber is the optional upgrade for a transport that needs a
// whole [Bus.SubscribeAll] slice before it registers anything - a broker
// that binds one identity to several contracts, such as a JetStream
// durable filtering every subject its group consumes, cannot register
// the group one contract at a time. The slice arrives sorted by group,
// contract then consumer, every handler already wrapped. [Bus.Subscribe]
// never uses it.
type BatchSubscriber interface {
	SubscribeBatch(ctx context.Context, subs []Subscription) error
}

// Handler processes one message. It is the signature every generated
// consumer is built to, and the one a [Middleware] wraps.
type Handler func(ctx context.Context, msg *Message) error

// Subscription is one consumer's interest in one contract.
type Subscription struct {
	// Event is the contract name, matching [Message.Event].
	Event string
	// Consumer is the design-declared consumer name, used in diagnostics
	// and in a transport's own bookkeeping; nothing on the broker depends
	// on it. It does not name a subscription on its own - two services may
	// declare the same consumer for the same contract, so Event and Group
	// are the pair that does.
	Consumer string
	// Group is the broker identity: the Kafka consumer group, the NATS
	// queue group. Subscriptions sharing a group divide the stream
	// between them, so a group is the unit of scaling and of failure
	// isolation - not of ordering, which no transport here gives across
	// contracts.
	//
	// On a transport that remembers a position per group - Kafka, and
	// JetStream - the name is also where those consumers resume, and one
	// the broker has never seen has no position at all: if it has an
	// offset, write the name down. Core NATS keeps no position, so there
	// the name only decides who competes for a message.
	//
	// Empty falls back to Consumer, so a hand-written Subscription keeps
	// the behaviour it had.
	Group string
	// Handle processes one message. [Bus.Subscribe] wraps it so a panic
	// becomes a [*PanicError] the transport sees as an ordinary handler
	// error, instead of ending the process.
	Handle Handler
}

// GroupName returns the broker identity every transport keys on: Group,
// or Consumer when a hand-written Subscription leaves Group empty.
func (s Subscription) GroupName() string {
	if s.Group != "" {
		return s.Group
	}
	return s.Consumer
}

// Bus binds a transport to a codec. Generated publishers and consumer
// registrations take a *Bus and nothing else.
//
// A Bus is safe for concurrent use once constructed: every field is set
// in [New] and never written again.
type Bus struct {
	pub Publisher
	sub Subscriber

	chain    Chain
	required []Disposition

	codec    Codec
	perEvent map[string]Codec
}

// Option configures a Bus at construction time.
type Option func(*Bus)

// WithPublisher installs the publish half of the transport.
func WithPublisher(p Publisher) Option { return func(b *Bus) { b.pub = p } }

// WithSubscriber installs the subscribe half of the transport.
func WithSubscriber(s Subscriber) Option { return func(b *Bus) { b.sub = s } }

// WithTransport installs a value that is both halves.
func WithTransport(t interface {
	Publisher
	Subscriber
}) Option {
	return func(b *Bus) { b.pub, b.sub = t, t }
}

// WithCodec sets the codec every contract uses unless [WithCodecFor]
// overrides it. There is no default - a Bus built without one fails
// rather than picking an encoding.
func WithCodec(c Codec) Option { return func(b *Bus) { b.codec = c } }

// WithMiddleware installs the chain every subscription registered through
// this bus is wrapped in, outermost first. This does not give the Bus a
// new concern: [Bus.Subscribe] already applies exactly one middleware, the
// panic recover, and this generalises that into a configurable list.
//
// The bus is the seam because it is the only thing every subscription
// passes through - a generated SubscribeAll, a hand-built
// `Subscriptions(bus, h)` slice, and one built against another design's
// contracts all reach the broker through [Bus.Subscribe]. A chain applied
// anywhere narrower would silently miss the ones it cannot see.
//
// Each middleware is handed the [Subscription] it wraps, so one chain can
// read the contract, the consumer and the group it is running for. A
// project wanting two different chains builds two buses.
func WithMiddleware(mws ...Middleware) Option {
	return func(b *Bus) { b.chain = b.chain.Append(mws...) }
}

// WithCodecFor overrides the codec for one contract.
func WithCodecFor(event string, c Codec) Option {
	return func(b *Bus) {
		if b.perEvent == nil {
			b.perEvent = map[string]Codec{}
		}
		b.perEvent[event] = c
	}
}

// New returns a Bus configured by opts.
func New(opts ...Option) *Bus {
	b := &Bus{}
	for _, o := range opts {
		o(b)
	}
	return b
}

// ErrNoCodec is returned when no codec is configured for a contract.
var ErrNoCodec = errors.New("events: no codec configured")

// ErrNoPublisher is returned by Publish on a Bus with no publish half.
var ErrNoPublisher = errors.New("events: no publisher configured")

// ErrNoSubscriber is returned by Subscribe on a Bus with no subscribe half.
var ErrNoSubscriber = errors.New("events: no subscriber configured")

// CodecFor returns the codec in effect for event.
func (b *Bus) CodecFor(event string) (Codec, error) {
	if c, ok := b.perEvent[event]; ok && c != nil {
		return c, nil
	}
	if b.codec == nil {
		return nil, fmt.Errorf("%w for %q", ErrNoCodec, event)
	}
	return b.codec, nil
}

// Publish encodes payload with the contract's codec and hands the
// envelope to the transport. Application code calls the generated typed
// publisher instead; this is the path for a contract the design declares
// but does not publish, which has no generated publisher.
//
// opts fill in everything beside the payload - the ordering key, a
// deduplication ID, headers, one adapter's own options:
//
//	bus.Publish(ctx, orders.PlacedContract, payload,
//	    events.WithKey(string(payload.OrderID)),
//	    events.WithHeader("trace-parent", tp))
//
// # The cancelled-context rule
//
// A context already cancelled publishes nothing and returns its error.
// The bus decides this, not the transport: an adapter is free to ignore
// ctx - the in-process one takes it as `_` and always has - so a rule
// left to each of them is not one a caller can rely on. Every publish
// through a [Bus] answers the same way, whatever it is publishing
// through.
func (b *Bus) Publish(ctx context.Context, event string, payload any, opts ...PublishOption) error {
	if b == nil || b.pub == nil {
		return ErrNoPublisher
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	env := Envelope{Event: event, Payload: payload}
	env.Apply(opts...)
	msg, err := b.encode(env)
	if err != nil {
		return err
	}
	return b.pub.Publish(ctx, msg)
}

// PartialPublishError reports a batch that stopped partway. Unsent names
// exactly which envelopes did not go out, so retrying them sends nothing
// twice:
//
//	var partial *events.PartialPublishError
//	if errors.As(err, &partial) {
//	    retry := make([]events.Envelope, 0, len(partial.Unsent))
//	    for _, i := range partial.Unsent {
//	        retry = append(retry, envs[i])
//	    }
//	}
//
// A set rather than a count because a transport publishing to several
// partitions or topics at once does not fail in batch order: the
// envelopes that landed need not be the first ones. The bus's own
// one-at-a-time fallback DOES stop at the first failure, and reports the
// contiguous tail through the same field - a prefix is a set.
type PartialPublishError struct {
	// Sent is the length of the leading run that was published: every
	// envelope before it landed, and from it onward some did not. It is
	// Unsent[0], so a caller already reading `envs[Sent:]` resends from
	// the first gap rather than skipping past it.
	Sent int
	// Unsent holds the indices, ascending, of the envelopes that did not
	// go out. Indices are into the slice handed to [Bus.PublishAll].
	Unsent []int
	// Event is the contract of the first unsent envelope.
	Event string
	Err   error
}

func (e *PartialPublishError) Error() string {
	return fmt.Sprintf("events: publish %s (%d of the batch unsent, %d already sent): %v",
		e.Event, len(e.Unsent), e.Sent, e.Err)
}

func (e *PartialPublishError) Unwrap() error { return e.Err }

// UnsentFrom reports a batch that STOPPED at index i: every message from
// i onward did not go out. It is the shape a transport that publishes one
// message at a time produces, because it stops at the first failure.
//
// A transport whose failures are scattered must use [UnsentAt] instead.
// It cannot reach for this one by mistake - a set of indices does not fit
// an int - and that is deliberate: reporting a contiguous tail for a
// scattered failure claims messages went out that did not.
func UnsentFrom(i int, msgs []*Message, err error) *PartialPublishError {
	indices := make([]int, 0, max(len(msgs)-i, 0))
	for j := i; j < len(msgs); j++ {
		indices = append(indices, j)
	}
	return UnsentAt(indices, msgs, err)
}

// UnsentAt reports a batch whose failures are SCATTERED: indices names
// exactly the messages that did not go out, in any order. It is the shape
// a transport publishing to several partitions or topics at once
// produces, because the ones that landed need not be the first.
//
// The indices are sorted here, and Sent and Event are derived from the
// first of them, so the contract's invariants hold by construction for
// every adapter that builds its report through these two.
func UnsentAt(indices []int, msgs []*Message, err error) *PartialPublishError {
	sorted := append([]int(nil), indices...)
	sort.Ints(sorted)
	out := &PartialPublishError{Unsent: sorted, Err: err}
	if len(sorted) == 0 {
		return out
	}
	out.Sent = sorted[0]
	if i := sorted[0]; i >= 0 && i < len(msgs) {
		out.Event = msgs[i].Event
	}
	return out
}

// PublishAll encodes every envelope and hands the batch to the transport
// in one call when it implements [BatchPublisher], otherwise one message
// at a time in order. Encoding is done up front, so a payload that cannot
// be encoded fails before anything is sent.
//
// It is not atomic. An error partway through leaves the earlier messages
// sent and returns a [*PartialPublishError] whose Unsent names exactly
// which envelopes did not go out - on both paths, so a caller does not
// have to know which one ran to retry correctly.
//
// The cancelled-context rule is [Bus.Publish]'s, and it bites hardest
// here: a transport that published the batch anyway would report messages
// it has just sent as unsent, and a caller retrying those publishes every
// one of them a second time.
//
// The adapter's report is checked against the batch before it is
// returned. An adapter that names an index outside the batch, or one out
// of order, has its report replaced with "none of it was sent" and the
// error says which adapter broke the contract: an understated Unsent
// loses messages silently, which is the one failure a caller cannot
// detect for itself.
func (b *Bus) PublishAll(ctx context.Context, envs []Envelope) error {
	if len(envs) == 0 {
		return nil
	}
	if b == nil || b.pub == nil {
		return ErrNoPublisher
	}
	// One transport per broker would otherwise answer this differently,
	// and the ones that publish anyway report what they just sent as
	// unsent - which the caller retries, publishing all of it twice.
	if err := ctx.Err(); err != nil {
		return err
	}
	msgs := make([]*Message, 0, len(envs))
	for i, env := range envs {
		if env.Event == "" {
			return fmt.Errorf("events: envelope %d has no contract name", i)
		}
		msg, err := b.encode(env)
		if err != nil {
			return err
		}
		msgs = append(msgs, msg)
	}
	if batch, ok := b.pub.(BatchPublisher); ok {
		return b.checkedBatch(batch.PublishBatch(ctx, msgs), msgs)
	}
	for i, msg := range msgs {
		if err := b.pub.Publish(ctx, msg); err != nil {
			return UnsentFrom(i, msgs, err)
		}
	}
	return nil
}

// checkedBatch validates a [BatchPublisher]'s partial report against the
// batch it was handed. A report that cannot be true is replaced with one
// that is - every envelope unsent - because a caller acting on an
// understated Unsent loses the messages it names as delivered, and
// nothing downstream can tell that happened.
//
// Only a [*PartialPublishError] is inspected. Any other error means the
// adapter is not claiming a partial send, which the caller reads as
// "assume nothing arrived".
func (b *Bus) checkedBatch(err error, msgs []*Message) error {
	var partial *PartialPublishError
	if err == nil || !errors.As(err, &partial) {
		return err
	}
	if bad := validateUnsent(partial, len(msgs)); bad != "" {
		return &PartialPublishError{
			Sent:   0,
			Unsent: allIndices(len(msgs)),
			Event:  msgs[0].Event,
			Err: fmt.Errorf("events: transport %s reported an impossible partial publish (%s); treating the whole batch as unsent: %w",
				adapterLabel(b.pub), bad, partial.Err),
		}
	}
	partial.Sent = partial.Unsent[0]
	return err
}

// validateUnsent names the way a partial report is impossible, or "" when
// it holds. Sent is not checked: it is derived from Unsent on the way out,
// so an adapter that got it wrong is corrected rather than refused.
func validateUnsent(p *PartialPublishError, n int) string {
	if len(p.Unsent) == 0 {
		return "it names no unsent envelope, so it is not a partial publish"
	}
	prev := -1
	for _, i := range p.Unsent {
		if i < 0 || i >= n {
			return fmt.Sprintf("index %d is outside the batch of %d", i, n)
		}
		if i <= prev {
			return fmt.Sprintf("index %d does not follow %d, so the list is not ascending", i, prev)
		}
		prev = i
	}
	return ""
}

// allIndices is every index of a batch of n.
func allIndices(n int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, i)
	}
	return out
}

// adapterLabel names a transport for a diagnostic: the name it answers to
// under [WithAdapterOption] when it has one, else its Go type.
func adapterLabel(p Publisher) string {
	if aware, ok := p.(OptionAware); ok {
		return strconv.Quote(aware.AdapterName())
	}
	return fmt.Sprintf("%T", p)
}

// encode resolves the contract's codec and builds the wire envelope,
// merging the caller's metadata under the codec stamp. A reserved key
// belongs to the runtime or to a transport, so a caller's entry under one
// is dropped rather than carried - see [IsReservedMeta].
//
// The adapter options are checked here so a bad one fails before any
// message is on the wire, the same way an unencodable payload does.
func (b *Bus) encode(env Envelope) (*Message, error) {
	if err := b.checkAdapterOptions(env); err != nil {
		return nil, err
	}
	codec, err := b.CodecFor(env.Event)
	if err != nil {
		return nil, err
	}
	data, err := codec.Marshal(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("events: encode %s: %w", env.Event, err)
	}
	meta := make(map[string]string, len(env.Metadata)+1)
	for k, v := range env.Metadata {
		if IsReservedMeta(k) {
			continue
		}
		meta[k] = v
	}
	meta[MetaCodec] = codec.Name()
	return &Message{
		Event:          env.Event,
		Key:            env.Key,
		DedupID:        env.DedupID,
		Payload:        data,
		Metadata:       meta,
		AdapterOptions: env.AdapterOptions,
	}, nil
}

// PanicError is what a handler that panicked returns to the transport.
// The same message runs the same code and panics again, so a chain that
// decides what to do with a failure can treat one as its own case:
// `errors.As(err, &panicked)`.
type PanicError struct {
	// Event is the contract being delivered, Consumer the handler that
	// panicked and Group its broker identity - together they name the
	// subscription an operator has to find.
	Event    string
	Consumer string
	Group    string
	// Value is what the handler passed to panic.
	Value any
	// Stack is the trace captured where the panic fired, as
	// [runtime/debug.Stack] renders it. Error() does not carry it: log it
	// as its own field, the way pkg/server's Recovery middleware logs the
	// HTTP side's.
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("events: panic in consumer %q (group %q, contract %q): %v",
		e.Consumer, e.Group, e.Event, e.Value)
}

// Unwrap returns the panic value when the handler panicked with an error,
// so errors.Is and errors.As reach it. A panic with anything else unwraps
// to nil.
func (e *PanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// decorated is the handler the transport receives: the subscription's own,
// wrapped in a recover, the configured middleware, and a second recover.
// The deferred recover only works on the goroutine that runs the handler,
// which is one a transport spawns - so the wrappers go on the handler
// itself and not around any Subscribe call.
//
// Recovery sits on BOTH sides of the chain, and the order is the point.
// The inner one turns a panicking handler into a [*PanicError] the chain
// observes as an ordinary error, so a project's logging middleware sees
// the failure it most needs to. The outer one catches a panic in the chain
// itself, which the inner one sits beneath and can never see.
//
// Only one [*PanicError] is ever built per panic: once the inner recover
// catches, no panic is in flight, so the outer recover returns nil and
// passes the error through untouched. A bus with no middleware installs
// the inner one alone, which is the wrap this function has always applied.
func (b *Bus) decorated(sub Subscription) Handler {
	if sub.Handle == nil {
		return nil
	}
	h := recoverHandler(sub, sub.Handle)
	if len(b.chain) == 0 {
		return h
	}
	return recoverHandler(sub, b.chain.wrap(sub, h))
}

// recoverHandler wraps h so a panic becomes a [*PanicError] naming sub.
// It is the only place one is built.
//
// A middleware cannot see a panic raised by a middleware BELOW it: that
// panic unwinds its own frame and only a recover outside it catches. Put
// [Recover] between two middlewares to change that. A panicking handler is
// not affected - the wrap below the chain already turns one into an error.
func recoverHandler(sub Subscription, h Handler) Handler {
	event, consumer, group := sub.Event, sub.Consumer, sub.GroupName()
	return func(ctx context.Context, msg *Message) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			err = &PanicError{
				Event:    event,
				Consumer: consumer,
				Group:    group,
				Value:    r,
				Stack:    debug.Stack(),
			}
			// A frame that panicked did not finish deciding, so what it
			// asked for is void. Unset rather than settle: a middleware
			// above it still decides.
			if msg != nil {
				msg.disposition = DispositionUnset
			}
		}()
		return h(ctx, msg)
	}
}

// Subscribe registers sub with the transport, its handler wrapped in a
// recover and in any middleware [WithMiddleware] installed. Every
// transport inherits both, because the wrap happens before the
// subscription is handed over. A recovered panic is returned as a
// [*PanicError], which the transport sees as an ordinary handler error.
//
// It refuses here rather than at the first message when the transport
// cannot honour a disposition [WithDispositionRequired] named.
func (b *Bus) Subscribe(ctx context.Context, sub Subscription) error {
	prepared, err := b.prepare(sub)
	if err != nil {
		return err
	}
	return b.sub.Subscribe(ctx, prepared)
}

// prepare runs the checks every registration passes and wraps the handler.
func (b *Bus) prepare(sub Subscription) (Subscription, error) {
	if b.sub == nil {
		return sub, ErrNoSubscriber
	}
	if _, err := b.CodecFor(sub.Event); err != nil {
		return sub, err
	}
	if err := b.requireDispositions(); err != nil {
		return sub, err
	}
	sub.Handle = b.decorated(sub)
	return sub, nil
}

// SubscribeAll registers every subscription, in group, contract then
// consumer order. A transport that implements [BatchSubscriber] receives
// the whole slice in one call, after every entry passed the checks
// [Bus.Subscribe] makes; any other transport gets one Subscribe per
// entry, where the first failure stops the run and subscriptions already
// made stay live until ctx is cancelled.
//
// Group leads the key because it is the identity a transport can refuse a
// second claim on, so it decides which of two colliding subscriptions
// registers first. Contract and consumer complete the order: two services
// may name a consumer alike, and an order that is not total would move
// the refusal between runs.
func (b *Bus) SubscribeAll(ctx context.Context, subs []Subscription) error {
	ordered := make([]Subscription, len(subs))
	copy(ordered, subs)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].GroupName() != ordered[j].GroupName() {
			return ordered[i].GroupName() < ordered[j].GroupName()
		}
		if ordered[i].Event != ordered[j].Event {
			return ordered[i].Event < ordered[j].Event
		}
		return ordered[i].Consumer < ordered[j].Consumer
	})
	batch, batched := b.sub.(BatchSubscriber)
	if !batched {
		for _, sub := range ordered {
			if err := b.Subscribe(ctx, sub); err != nil {
				return subscribeError(sub, err)
			}
		}
		return nil
	}
	for i, sub := range ordered {
		prepared, err := b.prepare(sub)
		if err != nil {
			return subscribeError(sub, err)
		}
		ordered[i] = prepared
	}
	if len(ordered) == 0 {
		return nil
	}
	if err := batch.SubscribeBatch(ctx, ordered); err != nil {
		return fmt.Errorf("events: subscribe %d subscription(s): %w", len(ordered), err)
	}
	return nil
}

func subscribeError(sub Subscription, err error) error {
	return fmt.Errorf("events: subscribe %s/%s in group %s: %w", sub.Event, sub.Consumer, sub.GroupName(), err)
}

// ErrCodecMismatch is returned by [Bus.Decode] for a message stamped
// with a codec the consumer is not configured for. It is a configuration
// error rather than a bad payload: the same bytes decode once the two
// sides agree, which is why it is not a [*PayloadError].
var ErrCodecMismatch = errors.New("events: message encoded with a codec the consumer is not configured for")

// PayloadError is a payload the generated wrapper could not decode, or
// that failed its Validate(). The same bytes fail the same way on every
// delivery, so a chain deciding what to do with a failure can pick this
// one out with errors.As and give the message up rather than retry it.
type PayloadError struct {
	// Event is the contract the payload arrived on.
	Event string
	Err   error
}

func (e *PayloadError) Error() string {
	return fmt.Sprintf("events: payload of %s: %v", e.Event, e.Err)
}

func (e *PayloadError) Unwrap() error { return e.Err }

// Decode fills v from msg using the contract's codec. A message stamped
// with a different codec fails with [ErrCodecMismatch]; one the codec
// cannot decode fails with a [*PayloadError].
func (b *Bus) Decode(msg *Message, v any) error {
	codec, err := b.CodecFor(msg.Event)
	if err != nil {
		return err
	}
	if got := msg.Metadata[MetaCodec]; got != "" && got != codec.Name() {
		return fmt.Errorf("%w: %s carries %q, the consumer decodes %q", ErrCodecMismatch, msg.Event, got, codec.Name())
	}
	if err := codec.Unmarshal(msg.Payload, v); err != nil {
		return &PayloadError{Event: msg.Event, Err: err}
	}
	return nil
}
