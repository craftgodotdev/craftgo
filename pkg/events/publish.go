package events

import (
	"fmt"
	"sort"
	"strings"
)

// Envelope is one message to publish, before encoding; [Bus.PublishAll] encodes each with
// its own contract's codec.
type Envelope struct {
	// Event is the contract name.
	Event string
	// Key identifies the entity the message is about; empty is keyless. See [WithKey].
	Key string
	// DedupID lets a transport that deduplicates recognise a repeat; see [WithDedupID].
	DedupID string
	// Payload is the value to encode.
	Payload any
	// Metadata is merged into [Message.Metadata]; a key [IsReservedMeta] accepts is
	// dropped. Nil and empty behave alike.
	Metadata map[string]string
	// AdapterOptions holds [WithAdapterOption] values, keyed adapter name → option key.
	AdapterOptions map[string]map[string]any
}

// PublishOption configures one message before it is published. Options apply in order,
// so the last one setting a value wins.
type PublishOption func(*Envelope)

// WithKey sets the key of the entity the message is about. A transport that orders per
// entity keeps one key's messages in order within a contract; a message without a key
// goes wherever the transport places it.
func WithKey(key string) PublishOption {
	return func(env *Envelope) { env.Key = key }
}

// WithDedupID sets the ID a transport that deduplicates recognises a repeat by: publishes
// sharing it within the broker's window are one message.
func WithDedupID(id string) PublishOption {
	return func(env *Envelope) { env.DedupID = id }
}

// WithHeader sets one side-band value carried beside the payload; a later call with the
// same key replaces it. A key [IsReservedMeta] accepts is dropped.
func WithHeader(key, value string) PublishOption {
	return func(env *Envelope) {
		if env.Metadata == nil {
			env.Metadata = map[string]string{}
		}
		env.Metadata[key] = value
	}
}

// WithAdapterOption sets a value the named adapter reads. On an [OptionAware] transport,
// an option for another adapter is ignored and one it does not read under its own name
// fails the publish with an [*UnknownOptionError].
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

// JoinOptions returns defaults followed by opts, so the per-call options win. Neither
// input is written to.
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

// OptionAware is the optional upgrade for a transport with a name in [WithAdapterOption]'s
// namespace. The bus then fails, before anything is sent, a publish carrying an option
// under that name the adapter does not read. Without it no adapter option is checked.
type OptionAware interface {
	// AdapterName is the name [WithAdapterOption] addresses this adapter by.
	AdapterName() string
	// KnownOptions lists every option key this adapter reads; nil means none.
	KnownOptions() []string
}

// UnknownOptionError reports an option addressed to the configured adapter under a key
// the adapter does not read.
type UnknownOptionError struct {
	// Adapter is the configured adapter the option named.
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

// AdapterOption returns the value published for adapter under key.
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
	// KnownOptions may return the adapter's own slice, so sort a copy; sorted keys make
	// the reported key the same on every run.
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
