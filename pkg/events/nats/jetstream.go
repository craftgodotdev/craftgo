package nats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// A JetStream is a full transport that can also redeliver and reject,
// because the server tracks every message until a consumer answers for
// it. Asserted here so a change to the runtime interfaces fails this
// package rather than a user's wiring.
var (
	_ events.Publisher      = (*JetStream)(nil)
	_ events.Subscriber     = (*JetStream)(nil)
	_ events.BatchPublisher = (*JetStream)(nil)
	_ events.OptionAware    = (*JetStream)(nil)
	_ events.Dispositioner  = (*JetStream)(nil)
)

// JetStream publishes into, and consumes from, NATS JetStream streams.
//
// It is a second type beside [Transport] rather than a mode of it,
// because a JetStream delivery is not a [nats.Msg]: the modern client
// hands a consumer an interface whose underlying message is unexported,
// so one [MsgFrom] could not be honest across both. The wire format is
// shared, so a message published through either arrives identically.
//
// # What an operator must provision
//
// A stream covering the subjects craftgo publishes. **craftgo never
// creates one**, and could not correctly: one stream covering `orders.>`
// spans contracts any single subscription knows nothing about, and
// per-contract streams would overlap on those subjects, which the server
// refuses. Provisioning is a deployment decision about retention, replicas
// and limits, and it belongs where those are made.
//
// [JetStream.Subscribe] refuses rather than proceeds when the stream is
// missing or does not cover the subject, so a missing one is a start-up
// failure and not a consumer that receives nothing.
type JetStream struct {
	js      jetstream.JetStream
	subject func(contract string) string
	onError func(sub events.Subscription, msg *events.Message, err error)

	probeTimeout  time.Duration
	ackWait       time.Duration
	maxInFlight   int
	maxDeliveries int
	heartbeat     time.Duration

	mu        sync.Mutex
	consuming []jetstream.ConsumeContext
	// accountOK and streamFor cache SUCCESS only. A probe that failed on
	// a blip must be retried, and one that failed because JetStream is
	// off will fail again just as fast.
	accountOK bool
	streamFor map[string]string
}

// JetStreamOption configures a [JetStream].
type JetStreamOption func(*JetStream)

// WithJetStreamSubject replaces the contract-to-subject mapping, the way
// [WithSubject] does for the core transport. The mapping must be
// one-to-one, and the subjects it produces must be covered by a stream.
func WithJetStreamSubject(fn func(contract string) string) JetStreamOption {
	return func(j *JetStream) { j.subject = fn }
}

// WithJetStreamErrorHandler installs a callback for a handler that
// returns an error, and for the failures the client reports on its own -
// a consumer deleted underneath, a pull that could not be refilled.
func WithJetStreamErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) JetStreamOption {
	return func(j *JetStream) { j.onError = fn }
}

// WithProbeTimeout bounds each start-up check against the server.
// Default 5s.
//
// It has to be a timeout rather than an error check because a CLUSTERED
// server with JetStream disabled does not answer the account query at
// all - it simply never replies, so the only way to learn is to stop
// waiting.
func WithProbeTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.probeTimeout = d }
}

// WithAckWait is how long the server waits for an answer before it
// redelivers. Default 30s.
//
// A handler slower than this does not cause a spurious redelivery -
// [JetStream.Subscribe] holds the message open while the handler runs -
// but it is still the bound on how long a crashed consumer's messages sit
// before another member gets them.
func WithAckWait(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.ackWait = d }
}

// WithMaxInFlight caps how many messages one subscription holds
// unanswered. Default 64.
//
// It bounds work, and it bounds the cost of holding messages open: each
// one in flight is a ticker resetting the server's redelivery timer.
func WithMaxInFlight(n int) JetStreamOption {
	return func(j *JetStream) { j.maxInFlight = n }
}

// WithMaxDeliveries caps how many times the server may hand one message
// over before this adapter gives it up rather than asking for it again.
// It bounds a redelivery loop: a middleware that keeps calling
// [events.Message.Redeliver] on a message nothing can handle stops being
// obeyed once the count is reached, and the message is terminated. A
// delivery that SUCCEEDS on the last attempt is still taken as done.
//
// Default 5. Zero is unbounded and has to be chosen.
//
// The cap is applied here rather than through the consumer's own
// MaxDeliver, which counts every delivery whatever its outcome and would
// give up on a message three crashed consumers merely handed on. It also
// lives on the consumer, so the last subscriber to start would set it for
// every other member of the group.
func WithMaxDeliveries(n int) JetStreamOption {
	return func(j *JetStream) { j.maxDeliveries = n }
}

// NewJetStream binds a transport to an existing connection. The caller
// owns the connection's lifetime; [JetStream.Close] stops only this
// transport's subscriptions.
func NewJetStream(conn *nats.Conn, opts ...JetStreamOption) (*JetStream, error) {
	js, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	j := &JetStream{
		js:            js,
		subject:       func(c string) string { return c },
		probeTimeout:  5 * time.Second,
		ackWait:       30 * time.Second,
		maxInFlight:   64,
		maxDeliveries: 5,
		heartbeat:     0, // derived from ackWait at subscribe time
		streamFor:     map[string]string{},
	}
	for _, o := range opts {
		o(j)
	}
	return j, nil
}

// AdapterName implements [events.OptionAware]. It is the same name the
// core transport answers to: they are one adapter's two halves, and an
// option written for one is meant for the other.
func (j *JetStream) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: this adapter reads no
// per-message options, so one addressed to `nats` is a mistake.
func (j *JetStream) KnownOptions() []string { return nil }

// CanDisposition implements [events.Dispositioner]. It is a constant and
// never asks the server, because it cannot: the bus checks it inside
// Subscribe BEFORE the transport subscribes, so the consumer this would
// ask about does not exist yet.
//
// The configuration that would make the answer a lie - a consumer with an
// ack policy that discards acknowledgements, where Nak returns nil and
// does nothing - is refused by [JetStream.Subscribe] instead, at the
// point where it can be seen.
func (j *JetStream) CanDisposition(d events.Disposition) bool {
	switch d {
	case events.DispositionSettle, events.DispositionRedeliver, events.DispositionReject:
		return true
	}
	return false
}

// Publish sends one message and waits for the stream to acknowledge it.
// A nil error means the stream stored it.
//
// For fire-and-forget, publish through the core [Transport] instead: the
// two are separate types precisely so a project can use one for each side.
func (j *JetStream) Publish(ctx context.Context, msg *events.Message) error {
	if _, err := j.js.PublishMsg(ctx, encodeTo(j.subject(msg.Event), msg)); err != nil {
		return fmt.Errorf("nats: publish %s: %w", msg.Event, err)
	}
	return nil
}

// PublishBatch publishes the whole batch without waiting on each, then
// waits for every acknowledgement. A failure names exactly which messages
// the stream did not store.
func (j *JetStream) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	futures := make([]jetstream.PubAckFuture, 0, len(msgs))
	for _, msg := range msgs {
		f, err := j.js.PublishMsgAsync(encodeTo(j.subject(msg.Event), msg))
		if err != nil {
			// Nothing was handed over for this one and the ones before it
			// are still in flight, so the honest report is that this and
			// everything after it are unsent.
			return events.UnsentFrom(len(futures), msgs, err)
		}
		futures = append(futures, f)
	}

	var unsent []int
	var firstErr error
	for i, f := range futures {
		select {
		case <-f.Ok():
		case err := <-f.Err():
			if firstErr == nil {
				firstErr = err
			}
			unsent = append(unsent, i)
		case <-ctx.Done():
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			unsent = append(unsent, i)
		}
	}
	if firstErr == nil {
		return nil
	}
	return events.UnsentAt(unsent, msgs, firstErr)
}

// Subscribe binds sub to a durable consumer on the stream carrying its
// contract, and refuses rather than proceeding when anything about that
// cannot be established.
//
// Every check here exists because its absence is silent. The one that
// matters most is the stream lookup: a consumer whose filter subject the
// stream does not carry is created successfully, validates, consumes
// successfully - and receives nothing, for ever, with no error on any
// path at any time.
func (j *JetStream) Subscribe(ctx context.Context, sub events.Subscription) error {
	subject := j.subject(sub.Event)

	if err := j.checkAccount(ctx); err != nil {
		return err
	}
	stream, err := j.streamCovering(ctx, subject)
	if err != nil {
		return err
	}
	durable, err := durableName(sub.GroupName(), sub.Event)
	if err != nil {
		return err
	}

	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()
	consumer, err := j.js.CreateOrUpdateConsumer(probeCtx, stream, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: subject,
		// Explicit, and checked: the other policies acknowledge on
		// delivery, which would make Redeliver and Reject silently do
		// nothing while CanDisposition says they work.
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       j.ackWait,
		MaxAckPending: j.maxInFlight,
	})
	if err != nil {
		return fmt.Errorf("nats: consumer %q on stream %q for %s: %w", durable, stream, sub.Event, err)
	}

	cc, err := consumer.Consume(
		func(m jetstream.Msg) { j.deliver(ctx, sub, m) },
		jetstream.PullMaxMessages(j.maxInFlight),
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			if j.onError != nil && ctx.Err() == nil {
				j.onError(sub, nil, fmt.Errorf("nats: consume %s: %w", sub.Event, err))
			}
		}),
	)
	if err != nil {
		return fmt.Errorf("nats: consume %q on stream %q: %w", durable, stream, err)
	}

	j.mu.Lock()
	j.consuming = append(j.consuming, cc)
	j.mu.Unlock()

	// A consumer or stream deleted AFTER boot stops delivery with nothing
	// else to notice it: the process keeps serving HTTP and passing
	// readiness with this consumer gone. Kafka has no equivalent because
	// its group cannot be deleted underneath a live member.
	go func() {
		select {
		case <-cc.Closed():
			if j.onError != nil && ctx.Err() == nil {
				j.onError(sub, nil, fmt.Errorf("nats: consumer %q on stream %q stopped consuming %s - it was deleted, or the stream was",
					durable, stream, sub.Event))
			}
		case <-ctx.Done():
			cc.Stop()
		}
	}()
	return nil
}

// checkAccount rules out a server with JetStream switched off.
//
// It is the first check because it is the only one whose failure is
// legible: building the client does no I/O and always succeeds, and every
// later call answers "no responders available", which names nothing an
// operator can act on.
func (j *JetStream) checkAccount(ctx context.Context) error {
	j.mu.Lock()
	ok := j.accountOK
	j.mu.Unlock()
	if ok {
		return nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()
	if _, err := j.js.AccountInfo(probeCtx); err != nil {
		return fmt.Errorf("nats: JetStream is not available on this server after %s: %w - a server started without it answers this query with an error, and a CLUSTERED one with JetStream disabled does not answer at all, so a timeout here means the same thing",
			j.probeTimeout, err)
	}

	j.mu.Lock()
	j.accountOK = true
	j.mu.Unlock()
	return nil
}

// streamCovering returns the stream carrying subject, and refuses when
// none does.
//
// This is the check that earns the whole probe. Without it a consumer is
// created on a stream that does not carry the subject, every call
// succeeds, and the subscription receives nothing for ever. The server is
// asked rather than the subject matched here: `orders.>` against
// `orders.Placed` is NATS's rule, and re-implementing it would make
// craftgo own it. The server also refuses overlapping streams, so one
// answer is the answer.
func (j *JetStream) streamCovering(ctx context.Context, subject string) (string, error) {
	j.mu.Lock()
	name, ok := j.streamFor[subject]
	j.mu.Unlock()
	if ok {
		return name, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()
	name, err := j.js.StreamNameBySubject(probeCtx, subject)
	if err != nil {
		return "", fmt.Errorf("nats: no JetStream stream carries subject %q: %w - craftgo does not create streams, because one stream's subjects span contracts a single subscription knows nothing about; provision one covering this subject", subject, err)
	}

	j.mu.Lock()
	j.streamFor[subject] = name
	j.mu.Unlock()
	return name, nil
}

// deliver hands one message to the subscription and answers for it,
// holding it open while the handler runs.
func (j *JetStream) deliver(ctx context.Context, sub events.Subscription, m jetstream.Msg) {
	msg := decodeFrom(sub.Event, m.Headers(), m.Data())
	msg.SetDeliveries(deliveryCount(m))

	// A stream may carry several contracts. A filter subject should make
	// this unreachable, but a durable retargeted by a name collision would
	// deliver another contract's messages here, and handing those to a
	// handler that decodes them as its own type is the failure the check
	// exists for.
	if msg.Event != sub.Event {
		if j.onError != nil {
			j.onError(sub, msg, fmt.Errorf("nats: subject %s carried %s, which %s does not consume - skipped", m.Subject(), msg.Event, sub.Consumer))
		}
		_ = m.Ack()
		return
	}

	stop := j.holdOpen(m)
	err := sub.Handle(withJetStreamMsg(ctx, m), msg)
	stop()

	if err != nil && j.onError != nil {
		j.onError(sub, msg, err)
	}
	// A capped Redeliver is answered with a term, which the chain that
	// asked for it never sees.
	if j.capped(msg) && j.onError != nil {
		j.onError(sub, msg, fmt.Errorf("nats: giving up on %s after %d deliveries - the chain asked for another and WithMaxDeliveries is %d", sub.Event, msg.Deliveries(), j.maxDeliveries))
	}
	if ackErr := j.answer(m, msg); ackErr != nil && j.onError != nil {
		j.onError(sub, msg, fmt.Errorf("nats: answering for %s: %w", sub.Event, ackErr))
	}
}

// holdOpen resets the server's redelivery timer while the handler runs,
// and returns the function that stops doing so.
//
// Without it a handler slower than AckWait is redelivered while it is
// still working, and the duplicate is not deterministic: the client
// refills its pull after the handler returns while the acknowledgement is
// an asynchronous publish, so which lands first decides whether the
// message comes back. Nothing in the client does this automatically.
//
// The trade is deliberate. A hung handler now stalls its subscription
// instead of being redelivered behind itself: a visible, deterministic
// liveness failure - a consumer that stops and a pending count that
// climbs, both of which an operator already watches - in place of an
// invisible, nondeterministic correctness failure.
//
// There is no option to bound it, because a bounded one would do nothing.
// Measured against a real server, with in-flight bounded: stop the
// heartbeat after 3s on a handler that never returns, with AckWait at 1s,
// and after 30s the message has still been delivered exactly once. The
// server does not redeliver while the delivery is outstanding, so an
// escape hatch would only stop resetting a timer that never fires. A hung
// handler needs process-level detection - a liveness probe, a watchdog -
// and this package cannot provide one.
func (j *JetStream) holdOpen(m jetstream.Msg) func() {
	every := j.ackWait / 2
	if every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = m.InProgress()
			}
		}
	}()
	return func() { close(done) }
}

// answer turns what the chain asked for into the server's answer. An
// unset disposition settles: a middleware that decided nothing is not
// asking for the message back.
func (j *JetStream) answer(m jetstream.Msg, msg *events.Message) error {
	switch msg.Disposition() {
	case events.DispositionRedeliver:
		if j.capped(msg) {
			return m.Term()
		}
		return m.Nak()
	case events.DispositionReject:
		return m.Term()
	}
	return m.Ack()
}

// capped reports whether [WithMaxDeliveries] overrides a redelivery the
// chain asked for. It is the one place the cap is read, so the answer the
// server gets and the report the subscriber gets cannot disagree.
func (j *JetStream) capped(msg *events.Message) bool {
	return msg.Disposition() == events.DispositionRedeliver &&
		j.maxDeliveries > 0 && msg.Deliveries() >= j.maxDeliveries
}

// deliveryCount reads the server's attempt number, 1 on the first
// delivery - the same base as a Kafka share group, so a middleware
// comparing against a limit means the same thing on both.
func deliveryCount(m jetstream.Msg) int {
	meta, err := m.Metadata()
	if err != nil || meta == nil {
		return 0
	}
	return int(meta.NumDelivered)
}

// Close stops every subscription this transport started. The connection
// is left open.
func (j *JetStream) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cc := range j.consuming {
		cc.Stop()
	}
	j.consuming = nil
	return nil
}

// durableRejected is the character set a durable name may not contain.
// The server rejects these, and a composed name is not the user's to
// rename silently.
const durableRejected = ". > * / \\ \t\r\n"

// durableName is the consumer name for one subscription: its group and
// its contract.
//
// **The contract has to be in the name.** A durable carries exactly one
// filter subject, so two subscriptions sharing a name with different
// contracts do not divide work between them - the second RETARGETS the
// first, and the first then receives the other contract's messages and
// decodes them as its own type. craftgo actively encourages one group
// across several contracts, so a name built from the group alone breaks
// the documented idiom on the first design that uses it.
//
// [events.Subscription.Group]'s promise survives: replicas share a group
// AND a contract, so they share one durable and divide the work, while
// two groups on one contract get two durables and each receives
// everything.
func durableName(group, contract string) (string, error) {
	if bad := firstRejected(group); bad != "" {
		return "", fmt.Errorf("nats: consumer group %q cannot contain %q - a JetStream durable name may not, and the group is yours to rename", group, bad)
	}
	name := group + "-" + sanitiseDurable(contract)
	if len(name) > 255 {
		return "", fmt.Errorf("nats: the durable name for group %q and contract %q is %d characters, over JetStream's limit of 255 - shorten the group or the contract",
			group, contract, len(name))
	}
	return name, nil
}

// firstRejected returns the first character of s a durable name may not
// carry, or "" when there is none.
func firstRejected(s string) string {
	for _, r := range s {
		if strings.ContainsRune(durableRejected, r) || r == ' ' {
			return string(r)
		}
	}
	return ""
}

// sanitiseDurable replaces the characters a durable name may not carry.
// A CONTRACT is sanitised rather than refused: its name is the design's,
// and `orders.Placed` is the ordinary shape of one.
func sanitiseDurable(contract string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(durableRejected, r) || r == ' ' {
			return '_'
		}
		return r
	}, contract)
}

// errNoJetStreamMsg is what MustJetStreamMsg panics with.
var errNoJetStreamMsg = errors.New("nats: no JetStream message on this context - this middleware is installed on a transport that is not JetStream")
