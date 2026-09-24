// Package events is craftgo's event runtime: the transport- and codec-neutral API that
// generated contract packages and their listeners use.
//
// A [Bus] binds a transport ([Publisher], [Subscriber]) to a [Codec]. An [Event]
// publishes one contract through a bus and subscribes to it. [Bus.Start] hands every
// registered [Subscription] to the transport wrapped in [Middleware], and the chain
// decides each delivery's [Disposition].
package events

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Message is the envelope a transport moves. Payload is already encoded: the [Bus] runs
// the [Codec] on the way in and out.
type Message struct {
	// Event is the contract name; a transport maps it onto its own addressing.
	Event string
	// Key identifies the entity the event is about; see [WithKey].
	Key string
	// DedupID lets a transport that deduplicates recognise a repeat; see [WithDedupID].
	DedupID string
	Payload []byte
	// Metadata carries side-band values ([WithHeader]). Only [MetaCodec] is sure to
	// survive a transport.
	Metadata map[string]string
	// AdapterOptions holds [WithAdapterOption] values; read one with [Message.AdapterOption].
	AdapterOptions map[string]map[string]any

	// The state of one delivery; every delivery gets its own Message.
	disposition Disposition
	deliveries  int
}

// MetaCodec is the [Message.Metadata] key naming the codec that encoded the payload.
// [Bus.Decode] refuses a message stamped with a codec other than its own.
const MetaCodec = "content-codec"

// MetaPrefix begins every [Message.Metadata] key that belongs to a transport adapter.
const MetaPrefix = "craftgo-"

// IsReservedMeta reports whether key is reserved, ignoring case: [MetaCodec], or a key
// under [MetaPrefix]. The bus drops a reserved key from the metadata it publishes.
func IsReservedMeta(key string) bool {
	if strings.EqualFold(key, MetaCodec) {
		return true
	}
	return len(key) >= len(MetaPrefix) && strings.EqualFold(key[:len(MetaPrefix)], MetaPrefix)
}

// Codec turns a payload value into bytes and back. An implementation must:
//   - be safe for concurrent use;
//   - embed a pkg/wire.Raw value's bytes where the value belongs, without re-encoding them.
//
// pkg/wire/codectest checks a codec against this.
type Codec interface {
	// Name identifies the encoding on the wire; see [MetaCodec].
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// Publisher sends a message; transport adapters implement it. A nil error means the
// adapter has taken responsibility for the message, not necessarily that a broker stored it.
type Publisher interface {
	Publish(ctx context.Context, msg *Message) error
}

// BatchPublisher is the optional upgrade for a transport that takes a whole batch in one
// call; [Bus.PublishAll] uses it when present. PublishBatch need not be atomic. It returns:
//   - nil when every message was handed over;
//   - a plain error only when nothing went out;
//   - after a partial send, a [*PartialPublishError] built with [UnsentFrom] or [UnsentAt].
//
// A message counts as sent once the adapter has confirmed it. Wait for the outcome of
// every message already on the wire, and count one whose outcome cannot be learned as
// unsent.
type BatchPublisher interface {
	PublishBatch(ctx context.Context, msgs []*Message) error
}

// Subscriber delivers messages to the handlers of a batch of subscriptions. Subscribe:
//   - registers the batch, sorted and wrapped as [Bus.Start] describes, and returns
//     without blocking;
//   - delivers until ctx is cancelled;
//   - answers each delivery per [Message.Disposition] once the handler has returned,
//     settling an unset one.
type Subscriber interface {
	Subscribe(ctx context.Context, subs []Subscription) error
}

// Handler processes one delivered message.
type Handler func(ctx context.Context, msg *Message) error

// Group is the broker identity a subscription consumes under. Subscriptions sharing a
// group divide the stream between them; on a transport that keeps a position per group,
// the name is where its consumers resume, so renaming a group loses the position.
type Group string

// Subscription is one consumer's interest in one contract.
type Subscription struct {
	// Event is the contract name, matching [Message.Event].
	Event string
	// Consumer labels the handler in diagnostics and [Bus.Plan]; Event and Group identify
	// the subscription.
	Consumer string
	// Group is the broker identity; [Bus.Register] refuses an empty one.
	Group Group
	// Chain is this subscription's own middleware, run inside the bus chain; see [Bus.Start].
	Chain Chain
	// Handle processes one message; [Bus.Start] wraps it in a recover.
	Handle Handler
}

// Bus binds a transport to a codec. It is assembled until [Bus.Start] and fixed after;
// Register, Use and Plan are safe to call from several goroutines.
type Bus struct {
	pub Publisher
	sub Subscriber

	chain    Chain
	required []Disposition
	defaults []PublishOption

	codec    Codec
	perEvent map[string]Codec

	mu      sync.Mutex
	subs    []Subscription
	claimed map[registration]bool
	started bool
}

// registration identifies a subscription: a contract under a group.
type registration struct {
	event string
	group Group
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

// WithCodec sets the codec every contract uses unless [WithCodecFor] overrides it. There
// is no default: a contract with no codec fails with [ErrNoCodec].
func WithCodec(c Codec) Option { return func(b *Bus) { b.codec = c } }

// WithMiddleware appends mws to the bus chain that wraps every subscription, outermost
// first; see [Bus.Start] for the full wrap order.
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

// WithPublishDefaults sets options every publish through this bus starts from; repeated
// calls accumulate. A value the call's options or the [Envelope] set wins.
func WithPublishDefaults(opts ...PublishOption) Option {
	return func(b *Bus) { b.defaults = append(b.defaults, opts...) }
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

// ErrNoPublisher is returned by [Bus.Publish] and [Bus.PublishAll] on a Bus with no
// publish half.
var ErrNoPublisher = errors.New("events: no publisher configured")

// ErrNoSubscriber is returned by [Bus.Start] when subscriptions are registered on a Bus
// with no subscribe half.
var ErrNoSubscriber = errors.New("events: no subscriber configured")

// CodecFor returns the codec in effect for event, or an error wrapping [ErrNoCodec].
func (b *Bus) CodecFor(event string) (Codec, error) {
	if b == nil {
		return nil, fmt.Errorf("%w for %q", ErrNoCodec, event)
	}
	if c, ok := b.perEvent[event]; ok && c != nil {
		return c, nil
	}
	if b.codec == nil {
		return nil, fmt.Errorf("%w for %q", ErrNoCodec, event)
	}
	return b.codec, nil
}

// Publish encodes payload with the contract's codec and hands it to the transport;
// [Event.Publish] is the typed form. A context already cancelled when the call starts
// publishes nothing and returns its error, whatever the transport does with ctx.
func (b *Bus) Publish(ctx context.Context, event string, payload any, opts ...PublishOption) error {
	if b == nil || b.pub == nil {
		return ErrNoPublisher
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	env := Envelope{Event: event, Payload: payload}
	env.Apply(JoinOptions(b.defaults, opts)...)
	msg, err := b.encode(env)
	if err != nil {
		return err
	}
	return b.pub.Publish(ctx, msg)
}

// PartialPublishError reports a batch that stopped partway. Resending exactly the Unsent
// envelopes sends none twice.
type PartialPublishError struct {
	// Sent is Unsent[0]: every envelope before it was published.
	Sent int
	// Unsent holds the ascending indices, into the batch, of the envelopes that did not go out.
	Unsent []int
	// Event is the contract of the first unsent envelope.
	Event string
	Err   error
}

func (e *PartialPublishError) Error() string {
	return fmt.Sprintf("events: publish %s (%d of the batch unsent, first unsent at index %d): %v",
		e.Event, len(e.Unsent), e.Sent, e.Err)
}

func (e *PartialPublishError) Unwrap() error { return e.Err }

// UnsentFrom reports a batch that stopped at index i: every message from i onward did not
// go out. Use [UnsentAt] when the failures are scattered.
func UnsentFrom(i int, msgs []*Message, err error) *PartialPublishError {
	indices := make([]int, 0, max(len(msgs)-i, 0))
	for j := i; j < len(msgs); j++ {
		indices = append(indices, j)
	}
	return UnsentAt(indices, msgs, err)
}

// UnsentAt reports a batch whose failures are scattered: indices names the messages that
// did not go out, in any order. It sorts a copy and derives Sent and Event from the lowest.
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

// PublishAll encodes every envelope, then publishes them in one [BatchPublisher] call or
// one at a time in order. It is not atomic: a partial failure is a [*PartialPublishError],
// and a batch report that cannot be true becomes one naming every envelope unsent. A
// cancelled ctx is refused as on [Bus.Publish].
func (b *Bus) PublishAll(ctx context.Context, envs []Envelope) error {
	if len(envs) == 0 {
		return nil
	}
	if b == nil || b.pub == nil {
		return ErrNoPublisher
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	msgs := make([]*Message, 0, len(envs))
	for i, env := range envs {
		if env.Event == "" {
			return fmt.Errorf("events: envelope %d has no contract name", i)
		}
		msg, err := b.encode(b.withDefaults(env))
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

// checkedBatch replaces an impossible partial report with one naming the whole batch
// unsent; any other error passes through.
func (b *Bus) checkedBatch(err error, msgs []*Message) error {
	var partial *PartialPublishError
	if err == nil || !errors.As(err, &partial) {
		return err
	}
	if bad := validateUnsent(partial, len(msgs)); bad != "" {
		return UnsentFrom(0, msgs, fmt.Errorf("events: transport %s reported an impossible partial publish (%s); treating the whole batch as unsent: %w",
			adapterLabel(b.pub), bad, partial.Err))
	}
	partial.Sent = partial.Unsent[0]
	return err
}

// validateUnsent names the way a partial report is impossible, or "" when it holds.
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

// adapterLabel names a transport for a diagnostic: its adapter name, else its Go type.
func adapterLabel(p Publisher) string {
	if aware, ok := p.(OptionAware); ok {
		return strconv.Quote(aware.AdapterName())
	}
	return fmt.Sprintf("%T", p)
}

// withDefaults layers env over [WithPublishDefaults] in a fresh envelope, leaving the
// caller's metadata map unwritten; a value env carries wins.
func (b *Bus) withDefaults(env Envelope) Envelope {
	if len(b.defaults) == 0 {
		return env
	}
	out := Envelope{Event: env.Event, Payload: env.Payload}
	out.Apply(b.defaults...)
	if env.Key != "" {
		out.Key = env.Key
	}
	if env.DedupID != "" {
		out.DedupID = env.DedupID
	}
	for k, v := range env.Metadata {
		if out.Metadata == nil {
			out.Metadata = make(map[string]string, len(env.Metadata))
		}
		out.Metadata[k] = v
	}
	for adapter, opts := range env.AdapterOptions {
		for k, v := range opts {
			if out.AdapterOptions == nil {
				out.AdapterOptions = map[string]map[string]any{}
			}
			if out.AdapterOptions[adapter] == nil {
				out.AdapterOptions[adapter] = map[string]any{}
			}
			out.AdapterOptions[adapter][k] = v
		}
	}
	return out
}

// encode resolves the contract's codec and builds the wire message, dropping reserved
// metadata keys. A bad adapter option fails here, before anything is sent.
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

// PanicError is the error a recovered panic in a handler or its chain becomes; see
// [Bus.Start].
type PanicError struct {
	// Event, Consumer and Group name the subscription that panicked.
	Event    string
	Consumer string
	Group    Group
	// Value is what was passed to panic.
	Value any
	// Stack is the trace captured where the panic fired; Error omits it.
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("events: panic in consumer %q (group %q, contract %q): %v",
		e.Consumer, e.Group, e.Event, e.Value)
}

// Unwrap returns the panic value when it is an error, else nil.
func (e *PanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// decorated wraps sub's handler in the order [Bus.Start] describes. busChain is the chain
// Start read under the lock; escaped is what the outer recover asks for.
func decorated(busChain Chain, sub Subscription, escaped Disposition) Handler {
	if sub.Handle == nil {
		return nil
	}
	h := recoverHandler(sub, sub.Handle, DispositionUnset)
	chain := busChain.Append(sub.Chain...)
	if len(chain) == 0 {
		return h
	}
	return recoverHandler(sub, chain.wrap(sub, h), escaped)
}

// recoverHandler turns a panic in h into a [*PanicError] naming sub and sets the message's
// disposition to escaped, voiding whatever the panicking frames asked for.
func recoverHandler(sub Subscription, h Handler, escaped Disposition) Handler {
	event, consumer, group := sub.Event, sub.Consumer, sub.Group
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
			if msg != nil {
				msg.disposition = escaped
			}
		}()
		return h(ctx, msg)
	}
}

// ErrStarted is returned by a second [Bus.Start], and by [Bus.Register] after one.
var ErrStarted = errors.New("events: the bus has already started")

// ErrNoGroup is returned by [Bus.Register] for a subscription with no group.
var ErrNoGroup = errors.New("events: a subscription needs a group")

// ErrNoHandler is returned by [Bus.Register] for a subscription with no handler.
var ErrNoHandler = errors.New("events: a subscription needs a handler")

// ErrDuplicateSubscription is returned by [Bus.Register] for a contract already
// registered under the same group.
var ErrDuplicateSubscription = errors.New("events: this contract is already registered under this group")

// RegisterError is what [Bus.Register] refuses with: the subscription, and the sentinel
// in Err.
type RegisterError struct {
	Event    string
	Consumer string
	Group    Group
	Err      error
}

func (e *RegisterError) Error() string {
	return fmt.Sprintf("events: register %s/%s in group %q: %v", e.Event, e.Consumer, e.Group, e.Err)
}

func (e *RegisterError) Unwrap() error { return e.Err }

// Use appends mws to the bus chain after construction; see [Bus.Start] for the wrap
// order. Use after [Bus.Start] panics.
func (b *Bus) Use(mws ...Middleware) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		panic("events: Bus.Use called after Bus.Start")
	}
	b.chain = b.chain.Append(mws...)
}

// Register records sub for [Bus.Start]. It refuses with a [*RegisterError] wrapping
// [ErrStarted], [ErrNoHandler], [ErrNoGroup], [ErrNoCodec], [ErrDispositionUnsupported] or
// [ErrDuplicateSubscription]; whether the broker accepts the batch is Start's answer.
func (b *Bus) Register(sub Subscription) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return registerError(sub, ErrStarted)
	}
	if sub.Handle == nil {
		return registerError(sub, ErrNoHandler)
	}
	if sub.Group == "" {
		return registerError(sub, ErrNoGroup)
	}
	if _, err := b.CodecFor(sub.Event); err != nil {
		return registerError(sub, err)
	}
	if err := b.requireDispositions(); err != nil {
		return registerError(sub, err)
	}
	if b.claimed == nil {
		b.claimed = map[registration]bool{}
	}
	key := registration{event: sub.Event, group: sub.Group}
	if b.claimed[key] {
		return registerError(sub, ErrDuplicateSubscription)
	}
	b.claimed[key] = true
	b.subs = append(b.subs, sub)
	return nil
}

func registerError(sub Subscription, err error) error {
	return &RegisterError{Event: sub.Event, Consumer: sub.Consumer, Group: sub.Group, Err: err}
}

// Start hands every registered subscription to the transport in one call, sorted by
// group, contract then consumer, and wraps each handler as: outer recover → bus chain →
// subscription chain → inner recover → handler. A panic in the chain asks for
// [DispositionRedeliver] where the transport can honour it; a panic in the handler leaves
// the decision to the chain. A second Start is [ErrStarted], even after a failed one.
func (b *Bus) Start(ctx context.Context) error {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return ErrStarted
	}
	b.started = true
	subs := sortedSubscriptions(b.subs)
	chain := b.chain
	b.mu.Unlock()

	if len(subs) == 0 {
		return nil
	}
	if b.sub == nil {
		return ErrNoSubscriber
	}
	escaped := DispositionUnset
	if canDisposition(b.sub, DispositionRedeliver) {
		escaped = DispositionRedeliver
	}
	for i := range subs {
		subs[i].Handle = decorated(chain, subs[i], escaped)
	}
	if err := b.sub.Subscribe(ctx, subs); err != nil {
		return fmt.Errorf("events: start %d subscription(s): %w", len(subs), err)
	}
	return nil
}

// sortedSubscriptions is a copy of subs in the order the transport receives them.
func sortedSubscriptions(subs []Subscription) []Subscription {
	out := make([]Subscription, len(subs))
	copy(out, subs)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Event != out[j].Event {
			return out[i].Event < out[j].Event
		}
		return out[i].Consumer < out[j].Consumer
	})
	return out
}

// ErrCodecMismatch is returned by [Bus.Decode] for a message stamped with a codec the
// consumer is not configured for: a configuration error, not a [*PayloadError].
var ErrCodecMismatch = errors.New("events: message encoded with a codec the consumer is not configured for")

// PayloadError is a payload that could not be decoded or failed its Validate(). The same
// bytes fail the same way on every delivery.
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
