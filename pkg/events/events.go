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
// calls accumulate. A value the call's options or the [Envelope] set wins: a per-call
// WithKey("") clears a default key, an Envelope's empty Key keeps it.
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
