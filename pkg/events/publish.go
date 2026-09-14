package events

import (
	"fmt"
	"sort"
	"strings"
)

// Envelope is one message to publish, before encoding. The generated
// publishers and batch builders produce these; [Bus.PublishAll] encodes
// each with the codec its own contract resolves to.
//
// A [PublishOption] is the supported way to fill one in - the generated
// publisher builds the envelope and applies the caller's options to it.
type Envelope struct {
	// Event is the contract name.
	Event string
	// Key identifies the entity the message is about, set by [WithKey].
	// Transports that preserve per-entity ordering use it to place a
	// message; the rest ignore it. Empty is a message with no key, which
	// every transport is free to place where it likes.
	Key string
	// DedupID lets a broker that de-duplicates recognise a message it has
	// already taken, set by [WithDedupID]. A transport without the notion
	// ignores it.
	DedupID string
	// Payload is the value to encode.
	Payload any
	// Metadata is the caller's side-band values, merged into
	// [Message.Metadata] on the way out. Optional; nil and empty behave
	// alike.
	//
	// An entry whose key [IsReservedMeta] accepts is dropped: inbound
	// metadata carries the codec stamp, so forwarding it must not fail.
	// Everything else is carried untouched.
	Metadata map[string]string
	// AdapterOptions carries values one named transport adapter reads,
	// set by [WithAdapterOption] and keyed adapter name → option key. It
	// is the escape hatch for a broker feature no other broker has.
	//
	// Entries under an adapter OTHER than the configured one are ignored,
	// so a project that changes broker keeps publishing. An entry under
	// the configured adapter's OWN name that the adapter does not read
	// fails the publish - see [OptionAware].
	AdapterOptions map[string]map[string]any
}

// PublishOption configures one message before it is published. The
// generated publishers take them, as do their batch builders and
// [Bus.Publish]:
//
//	svcCtx.Events.OrderService.PublishPlaced(ctx, placed,
//	    craftevents.WithKey(string(placed.OrderID)))
//
// Options apply in order, so the last one setting a given value wins.
// That is what makes a publisher's defaults defaults: [JoinOptions] puts
// them first and the per-call options after.
type PublishOption func(*Envelope)

// WithKey sets the key identifying the entity the message is about.
//
// A transport that preserves per-entity ordering uses it to place the
// message - the Kafka partition key, a subject suffix - so two messages
// under one key keep their order within one contract. Nothing orders
// across contracts. A transport without the notion ignores it.
//
// A message published without a key is keyless, and a keyless message is
// placed wherever the transport likes: that is the right default for a
// contract nothing needs ordered, and the wrong one for a contract that
// does. Ordering is a property of how a message is published, not of the
// contract, so it is decided here rather than in the design.
func WithKey(key string) PublishOption {
	return func(env *Envelope) { env.Key = key }
}

// WithDedupID sets the identity a broker that de-duplicates recognises a
// repeat by: the SQS FIFO deduplication ID, the Azure Service Bus message
// ID, the NATS `Nats-Msg-Id` header. Publishing the same ID twice inside
// the broker's own window is one message, which is what makes a retry
// after an ambiguous failure safe.
//
// The window, and whether there is one at all, is the broker's; a
// transport without the notion ignores this.
func WithDedupID(id string) PublishOption {
	return func(env *Envelope) { env.DedupID = id }
}

// WithHeader sets one side-band value carried beside the payload - a
// trace parent, a tenant, a hop count. Calling it twice with one key
// keeps the last value.
//
// A key [IsReservedMeta] accepts belongs to the runtime or to a transport
// adapter and is dropped on the way out; see [Envelope.Metadata].
func WithHeader(key, value string) PublishOption {
	return func(env *Envelope) {
		if env.Metadata == nil {
			env.Metadata = map[string]string{}
		}
		env.Metadata[key] = value
	}
}

// WithAdapterOption sets a value that one named transport adapter reads:
// the escape hatch for a broker feature that does not generalise, such as
// a per-message Kafka record timestamp.
//
//	craftevents.WithAdapterOption("kafka", "timestamp", t)
//
// adapter is the adapter's own name ([OptionAware.AdapterName]). An
// option addressed to an adapter OTHER than the configured one is
// ignored - a project that swaps broker keeps compiling and keeps
// publishing. An option addressed to the configured adapter under a key
// it does not read fails the publish rather than going nowhere.
func WithAdapterOption(adapter, key string, value any) PublishOption {
	return func(env *Envelope) {
		if env.AdapterOptions == nil {
			env.AdapterOptions = map[string]map[string]any{}
		}
		if env.AdapterOptions[adapter] == nil {
			env.AdapterOptions[adapter] = map[string]any{}
		}
		env.AdapterOptions[adapter][key] = value
	}
}

// Apply applies opts to env, in order.
func (env *Envelope) Apply(opts ...PublishOption) {
	for _, o := range opts {
		if o != nil {
			o(env)
		}
	}
}

// JoinOptions returns defaults followed by opts as one list, so a later
// option overwrites what an earlier one set and the per-call options win.
// Neither input is written to.
//
// Generated publishers call it to combine the defaults they were built
// with and the options of one publish; a hand-written wrapper around a
// publisher wants the same order.
func JoinOptions(defaults, opts []PublishOption) []PublishOption {
	if len(defaults) == 0 {
		return opts
	}
	if len(opts) == 0 {
		return defaults
	}
	out := make([]PublishOption, 0, len(defaults)+len(opts))
	out = append(out, defaults...)
	return append(out, opts...)
}

// OptionAware is the optional upgrade for a transport that has a name in
// [WithAdapterOption]'s namespace. Implementing it buys two things the
// adapter would otherwise have to enforce itself, once per adapter:
//
//   - an option addressed to this adapter under a key it does not read
//     fails the publish, naming the keys it does read. Silently dropping
//     a per-message option is how a message goes out configured
//     differently from how its caller asked;
//   - an option addressed to another adapter is ignored, so the same
//     code publishes through whichever broker is wired up.
//
// The check runs before anything is encoded, so one bad option in a
// batch fails the batch with nothing sent.
//
// A transport that does not implement it gets neither: nothing can tell
// an option meant for it from one meant for somebody else.
type OptionAware interface {
	// AdapterName is the namespace [WithAdapterOption] addresses this
	// adapter by.
	AdapterName() string
	// KnownOptions lists every option key this adapter reads. Nil is an
	// adapter that reads none, which still earns the check: an option
	// under its name is then always a mistake.
	KnownOptions() []string
}

// UnknownOptionError reports an option addressed to the configured
// adapter under a key the adapter does not read.
type UnknownOptionError struct {
	// Adapter is the adapter the option named, which is the one the bus
	// publishes through.
	Adapter string
	// Key is the option key the adapter does not read.
	Key string
	// Event is the contract whose publish was refused.
	Event string
	// Known is what the adapter does read, sorted.
	Known []string
}

func (e *UnknownOptionError) Error() string {
	known := "it reads none"
	if len(e.Known) > 0 {
		known = "it reads " + strings.Join(e.Known, ", ")
	}
	return fmt.Sprintf("events: publish %s: adapter %q has no option %q - %s",
		e.Event, e.Adapter, e.Key, known)
}

// AdapterOption returns the value published for this adapter under key.
// A transport adapter reads its own options through it, so the shape of
// [Message.AdapterOptions] is not every adapter's business.
func (m *Message) AdapterOption(adapter, key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.AdapterOptions[adapter][key]
	return v, ok
}

// checkAdapterOptions applies the [OptionAware] rules to one envelope.
func (b *Bus) checkAdapterOptions(env Envelope) error {
	if len(env.AdapterOptions) == 0 {
		return nil
	}
	aware, ok := b.pub.(OptionAware)
	if !ok {
		return nil
	}
	name := aware.AdapterName()
	opts := env.AdapterOptions[name]
	if len(opts) == 0 {
		return nil
	}
	// Both lists are copied before sorting: KnownOptions may hand back a
	// slice the adapter keeps, and sorting the keys makes a message
	// carrying two bad ones name the same one on every run.
	known := append([]string(nil), aware.KnownOptions()...)
	sort.Strings(known)
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !contains(known, key) {
			return &UnknownOptionError{Adapter: name, Key: key, Event: env.Event, Known: known}
		}
	}
	return nil
}

// contains reports whether list holds v.
func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
