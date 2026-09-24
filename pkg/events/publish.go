package events

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
