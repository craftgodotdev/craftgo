package nats

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

var (
	_ events.Publisher       = (*JetStream)(nil)
	_ events.Subscriber      = (*JetStream)(nil)
	_ events.BatchSubscriber = (*JetStream)(nil)
	_ events.BatchPublisher  = (*JetStream)(nil)
	_ events.OptionAware     = (*JetStream)(nil)
	_ events.Dispositioner   = (*JetStream)(nil)
)

// JetStream publishes into, and consumes from, NATS JetStream streams.
//
// A subscription's group is the durable consumer's name, and one durable
// filters every subject the group consumes: replicas sharing a group
// share one durable and divide its work, and a group that has run keeps
// its position under that name. The stream a durable reads is the one
// carrying the group's subjects - craftgo never creates one, and a group
// whose subjects sit on two streams is refused.
//
// Within one process a durable's messages are handled one at a time, in
// stream order, on one goroutine ([WithMaxInFlight] raises the prefetch).
// That is an observation, not a promise: a second replica pulls from the
// same durable, and a delayed redelivery re-enters the stream behind
// newer messages.
//
// It is a second type beside [Transport] because a JetStream delivery is
// not a [nats.Msg]. The wire format is shared, so a message published
// through either arrives identically.
type JetStream struct {
	js        jetstream.JetStream
	subject   func(contract string) string
	onError   func(sub events.Subscription, msg *events.Message, err error)
	configure func(group string, cfg *jetstream.ConsumerConfig)
	backoff   func(deliveries int) time.Duration

	probeTimeout  time.Duration
	ackWait       time.Duration
	ackTimeout    time.Duration
	drainTimeout  time.Duration
	maxInFlight   int
	maxDeliveries int

	closeCtx  context.Context
	closeStop context.CancelFunc
	closing   chan struct{}
	closeOnce sync.Once

	mu        sync.Mutex
	consuming []jetstream.ConsumeContext
	groups    map[string]bool
	accountOK bool
	streamFor map[string]string
}

// JetStreamOption configures a [JetStream].
type JetStreamOption func(*JetStream)

// WithJetStreamSubject replaces the contract-to-subject mapping, the way
// [WithSubject] does for the core transport. The mapping must be
// one-to-one and yield concrete subjects, because a delivery is matched
// to its consumer by the subject it arrived on.
func WithJetStreamSubject(fn func(contract string) string) JetStreamOption {
	return func(j *JetStream) { j.subject = fn }
}

// WithJetStreamErrorHandler installs a callback for a handler that
// returns an error, and for the failures the client reports on its own.
// A failure that belongs to a whole group rather than one consumer - a
// durable deleted underneath, a subject nothing in this process handles
// - arrives with only sub.Group set.
func WithJetStreamErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) JetStreamOption {
	return func(j *JetStream) { j.onError = fn }
}

// WithProbeTimeout bounds each start-up check against the server.
// Default 5s. A clustered server with JetStream disabled never answers
// the account query, so a timeout is the only way to learn.
func WithProbeTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.probeTimeout = d }
}

// WithPublishAckTimeout bounds how long a publish waits for the stream's
// verdict. Default 30s; a negative value is refused by [NewJetStream].
//
// Too short and it fires on a message the stream stored, which the
// caller then publishes again - survivable when the message carries
// [events.Message.DedupID], which the stream deduplicates within its
// window (2 minutes by default), so keep this shorter than that window.
// Zero waits for ever, and [JetStream.Close] is then the only way out.
func WithPublishAckTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.ackTimeout = d }
}

// WithAckWait is how long the server waits for an answer before it
// redelivers, set when a durable is created. Default 30s. A durable that
// already exists keeps its own.
func WithAckWait(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.ackWait = d }
}

// WithMaxInFlight is how many messages one durable's pull keeps buffered
// in this process. Default 1.
//
// Messages are handled one at a time, so a buffered message waits for
// every handler ahead of it with the server's AckWait clock already
// running, and only the message inside the handler is held open. Raise
// this only where n × the slowest handler stays under AckWait, or a
// message is redelivered while it still sits in the buffer.
func WithMaxInFlight(n int) JetStreamOption {
	return func(j *JetStream) { j.maxInFlight = n }
}

// WithMaxDeliveries caps how many times the server may hand one message
// over before a redelivery the chain asked for is answered with a
// termination instead. Default 5; zero is unbounded. A delivery that
// succeeds on the last attempt is still taken as done.
//
// The cap is applied here rather than through the consumer's own
// MaxDeliver, which counts every delivery whatever its outcome.
func WithMaxDeliveries(n int) JetStreamOption {
	return func(j *JetStream) { j.maxDeliveries = n }
}

// WithRedeliverBackoff delays a redelivery the chain asked for: the
// server hands the message back no sooner than fn(deliveries), where
// deliveries is the attempt just made, 1 on the first. Without it a
// redelivery is immediate, which burns [WithMaxDeliveries] in
// milliseconds on a failure that would have cleared a second later.
func WithRedeliverBackoff(fn func(deliveries int) time.Duration) JetStreamOption {
	return func(j *JetStream) { j.backoff = fn }
}

// WithDrainTimeout bounds how long [JetStream.Close] waits for a message
// already inside a handler to finish. Default 30s; zero stops at once.
func WithDrainTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.drainTimeout = d }
}

// WithConsumerConfig adjusts the configuration a durable is CREATED with
// - a deliver policy, a start sequence, replicas, an inactive threshold.
// It runs once, when the durable does not exist yet; an existing durable
// keeps the configuration it has, and only its filter subjects follow the
// design. The name, the filter subjects and the explicit ack policy are
// craftgo's and are re-asserted after fn returns.
func WithConsumerConfig(fn func(group string, cfg *jetstream.ConsumerConfig)) JetStreamOption {
	return func(j *JetStream) { j.configure = fn }
}

// NewJetStream binds a transport to an existing connection. The caller
// owns the connection's lifetime; [JetStream.Close] stops only this
// transport's subscriptions and waits.
func NewJetStream(conn *nats.Conn, opts ...JetStreamOption) (*JetStream, error) {
	j := &JetStream{
		subject:       func(c string) string { return c },
		probeTimeout:  5 * time.Second,
		ackWait:       30 * time.Second,
		ackTimeout:    30 * time.Second,
		drainTimeout:  30 * time.Second,
		maxInFlight:   1,
		maxDeliveries: 5,
		groups:        map[string]bool{},
		streamFor:     map[string]string{},
		closing:       make(chan struct{}),
	}
	for _, o := range opts {
		o(j)
	}
	if j.ackTimeout < 0 {
		return nil, fmt.Errorf("nats: WithPublishAckTimeout(%s) is negative; use a positive duration, or zero to wait for ever", j.ackTimeout)
	}
	if j.maxInFlight < 1 {
		return nil, fmt.Errorf("nats: WithMaxInFlight(%d) must be at least 1", j.maxInFlight)
	}

	clientOpts := []jetstream.JetStreamOpt{jetstream.WithPublishAsyncTimeout(j.ackTimeout)}
	if j.ackTimeout > 0 {
		clientOpts = append(clientOpts, jetstream.WithDefaultTimeout(j.ackTimeout))
	}
	js, err := jetstream.New(conn, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	j.js = js
	j.closeCtx, j.closeStop = context.WithCancel(context.Background())
	return j, nil
}

// ErrClosed is what a publish reports after [JetStream.Close].
var ErrClosed = errors.New("transport closed")

// AdapterName implements [events.OptionAware] with the core transport's
// name: they are one adapter's two halves.
func (j *JetStream) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]; this adapter reads no
// per-message options.
func (j *JetStream) KnownOptions() []string { return nil }

// CanDisposition implements [events.Dispositioner]. It is a constant:
// the bus asks before the durable exists. A durable that could make the
// answer a lie - one not acknowledging explicitly - is refused at
// subscribe instead.
func (j *JetStream) CanDisposition(d events.Disposition) bool {
	switch d {
	case events.DispositionSettle, events.DispositionRedeliver, events.DispositionReject:
		return true
	}
	return false
}

// Publish sends one message and waits for the stream to store it.
//
// ctx decides whether the message is published, not what becomes of it:
// one already cancelled sends nothing, and one cancelled while the
// acknowledgement is in flight does not end the wait - the message is on
// the wire, so giving up would report a stored message as failed and
// have the caller publish it twice. The wait is bounded by
// [WithPublishAckTimeout] and ended by [JetStream.Close].
func (j *JetStream) Publish(ctx context.Context, msg *events.Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nats: publish %s: %w", msg.Event, err)
	}
	if j.closeCtx.Err() != nil {
		return fmt.Errorf("nats: publish %s: %w", msg.Event, ErrClosed)
	}
	waitCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stopOnClose := context.AfterFunc(j.closeCtx, cancel)
	defer stopOnClose()

	if _, err := j.js.PublishMsg(waitCtx, encodeTo(j.subject(msg.Event), msg)); err != nil {
		if j.closeCtx.Err() != nil {
			return fmt.Errorf("nats: publish %s: %w", msg.Event, ErrClosed)
		}
		return fmt.Errorf("nats: publish %s: %w", msg.Event, err)
	}
	return nil
}

// PublishBatch publishes the whole batch, then waits for every
// acknowledgement and names exactly the messages the stream did not
// store. ctx follows the [JetStream.Publish] rule: it decides whether the
// batch goes out, and a cancellation after that does not cut the wait
// short.
func (j *JetStream) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nats: publish batch of %d: %w", len(msgs), err)
	}
	if j.closeCtx.Err() != nil {
		return fmt.Errorf("nats: publish batch of %d: %w", len(msgs), ErrClosed)
	}

	futures := make([]jetstream.PubAckFuture, 0, len(msgs))
	var unsent []int
	var firstErr error
	for i, msg := range msgs {
		f, err := j.js.PublishMsgAsync(encodeTo(j.subject(msg.Event), msg))
		if err != nil {
			firstErr = err
			for k := i; k < len(msgs); k++ {
				unsent = append(unsent, k)
			}
			break
		}
		futures = append(futures, f)
	}

	for i, f := range futures {
		select {
		case <-f.Ok():
		case err := <-f.Err():
			if firstErr == nil {
				firstErr = err
			}
			unsent = append(unsent, i)
		case <-j.closeCtx.Done():
			if firstErr == nil {
				firstErr = ErrClosed
			}
			unsent = append(unsent, i)
		}
	}
	if firstErr == nil {
		return nil
	}
	return events.UnsentAt(unsent, msgs, firstErr)
}

// Subscribe registers one subscription as a group of its own. A group
// with several contracts has to arrive together, through
// [JetStream.SubscribeBatch] - which is what [events.Bus.SubscribeAll]
// does - because its durable filters every subject at once.
func (j *JetStream) Subscribe(ctx context.Context, sub events.Subscription) error {
	return j.SubscribeBatch(ctx, []events.Subscription{sub})
}

// SubscribeBatch binds every subscription to the durable of its group,
// one durable per group, and refuses rather than proceeding when
// anything about that cannot be established: JetStream off, a subject no
// stream carries, a group spanning two streams, a group already
// subscribed on this transport, or a durable that does not acknowledge
// explicitly. Groups already started when a later one fails stay live.
func (j *JetStream) SubscribeBatch(ctx context.Context, subs []events.Subscription) error {
	if err := j.checkAccount(ctx); err != nil {
		return err
	}
	groups, err := j.plan(ctx, subs)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := j.consumeGroup(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

// groupPlan is one durable: its stream, its filter subjects and the
// consumer behind each subject.
type groupPlan struct {
	name     string
	stream   string
	subjects []string
	handlers map[string]events.Subscription
}

func (j *JetStream) plan(ctx context.Context, subs []events.Subscription) ([]*groupPlan, error) {
	byName := map[string]*groupPlan{}
	var order []*groupPlan
	for _, sub := range subs {
		group := sub.GroupName()
		if err := checkGroup(group); err != nil {
			return nil, err
		}
		if j.subscribed(group) {
			return nil, fmt.Errorf("nats: consumer group %q is already subscribed on this transport - hand every consumer of a group to one SubscribeAll", group)
		}
		subject := j.subject(sub.Event)
		stream, err := j.streamCovering(ctx, subject)
		if err != nil {
			return nil, err
		}
		g := byName[group]
		if g == nil {
			g = &groupPlan{name: group, stream: stream, handlers: map[string]events.Subscription{}}
			byName[group] = g
			order = append(order, g)
		}
		if g.stream != stream {
			return nil, fmt.Errorf("nats: consumer group %q reads %s from stream %q and %s from stream %q - a JetStream durable reads one stream, so give each stream's consumers their own @consumerGroup",
				group, g.subjects[0], g.stream, subject, stream)
		}
		if _, dup := g.handlers[subject]; dup {
			return nil, fmt.Errorf("nats: consumer group %q subscribes %s twice", group, subject)
		}
		g.handlers[subject] = sub
		g.subjects = append(g.subjects, subject)
	}
	for _, g := range order {
		sort.Strings(g.subjects)
	}
	return order, nil
}

func (j *JetStream) subscribed(group string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.groups[group]
}

func (j *JetStream) consumeGroup(ctx context.Context, g *groupPlan) error {
	consumer, ackWait, err := j.durable(ctx, g)
	if err != nil {
		return err
	}
	whole := events.Subscription{Group: g.name}
	cc, err := consumer.Consume(
		func(m jetstream.Msg) { j.dispatch(ctx, g, ackWait, m) },
		jetstream.PullMaxMessages(j.maxInFlight),
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			if ctx.Err() == nil {
				j.report(whole, nil, fmt.Errorf("nats: consume group %q: %w", g.name, err))
			}
		}),
	)
	if err != nil {
		return fmt.Errorf("nats: consume %q on stream %q: %w", g.name, g.stream, err)
	}

	j.mu.Lock()
	j.consuming = append(j.consuming, cc)
	j.groups[g.name] = true
	j.mu.Unlock()

	// A durable or stream deleted after boot stops delivery with nothing
	// else to notice: the process keeps passing readiness with the group
	// gone.
	go func() {
		select {
		case <-cc.Closed():
			if ctx.Err() == nil && !j.isClosing() {
				j.report(whole, nil, fmt.Errorf("nats: consumer %q on stream %q stopped consuming - it was deleted, or the stream was", g.name, g.stream))
			}
		case <-ctx.Done():
			cc.Stop()
		case <-j.closing:
		}
	}()
	return nil
}

func (j *JetStream) isClosing() bool {
	select {
	case <-j.closing:
		return true
	default:
		return false
	}
}

// durable adopts the group's durable when it exists and creates it
// otherwise, and reports the AckWait it runs with.
//
// An existing durable is patched only where the design is the authority
// - its filter subjects - so an operator's DeliverPolicy, replicas or
// start sequence survive a redeploy. The server refuses to change several
// of those anyway, and a full config sent at every boot was rejected for
// any durable created with DeliverNew.
func (j *JetStream) durable(ctx context.Context, g *groupPlan) (jetstream.Consumer, time.Duration, error) {
	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()

	existing, err := j.js.Consumer(probeCtx, g.stream, g.name)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		cfg := jetstream.ConsumerConfig{Durable: g.name, Name: g.name, AckWait: j.ackWait}
		if j.configure != nil {
			j.configure(g.name, &cfg)
		}
		cfg.Durable, cfg.Name = g.name, g.name
		cfg.AckPolicy = jetstream.AckExplicitPolicy
		setFilter(&cfg, g.subjects)
		created, err := j.js.CreateConsumer(probeCtx, g.stream, cfg)
		if err != nil {
			return nil, 0, fmt.Errorf("nats: create consumer %q on stream %q: %w", g.name, g.stream, err)
		}
		return created, effectiveAckWait(created, j.ackWait), nil
	case err != nil:
		return nil, 0, fmt.Errorf("nats: look up consumer %q on stream %q: %w", g.name, g.stream, err)
	}

	cfg := existing.CachedInfo().Config
	if cfg.AckPolicy != jetstream.AckExplicitPolicy {
		return nil, 0, fmt.Errorf("nats: consumer %q on stream %q does not acknowledge explicitly, so Redeliver and Reject would do nothing - recreate it with AckExplicit", g.name, g.stream)
	}
	if equalSets(filterOf(cfg), g.subjects) {
		return existing, effectiveAckWait(existing, j.ackWait), nil
	}
	setFilter(&cfg, g.subjects)
	updated, err := j.js.UpdateConsumer(probeCtx, g.stream, cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("nats: point consumer %q on stream %q at %v: %w", g.name, g.stream, g.subjects, err)
	}
	return updated, effectiveAckWait(updated, j.ackWait), nil
}

func effectiveAckWait(c jetstream.Consumer, fallback time.Duration) time.Duration {
	if info := c.CachedInfo(); info != nil && info.Config.AckWait > 0 {
		return info.Config.AckWait
	}
	return fallback
}

// setFilter writes subjects in the form the server accepts: the single
// field for one subject, which every server version takes, the list
// otherwise.
func setFilter(cfg *jetstream.ConsumerConfig, subjects []string) {
	cfg.FilterSubject, cfg.FilterSubjects = "", nil
	if len(subjects) == 1 {
		cfg.FilterSubject = subjects[0]
		return
	}
	cfg.FilterSubjects = subjects
}

func filterOf(cfg jetstream.ConsumerConfig) []string {
	if len(cfg.FilterSubjects) > 0 {
		out := append([]string(nil), cfg.FilterSubjects...)
		sort.Strings(out)
		return out
	}
	if cfg.FilterSubject != "" {
		return []string{cfg.FilterSubject}
	}
	return nil
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (j *JetStream) report(sub events.Subscription, msg *events.Message, err error) {
	if j.onError != nil {
		j.onError(sub, msg, err)
	}
}

// checkAccount rules out a server with JetStream switched off. Every
// later call would answer "no responders available", which names nothing
// an operator can act on.
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
// none does: a consumer on a subject no stream carries is created
// successfully and receives nothing, for ever, with no error anywhere.
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

// dispatch routes one delivery to the consumer of its subject. A subject
// nothing in this process handles is handed back rather than taken:
// during a rolling deploy a replica on another version of the design
// shares the durable and has the consumer.
func (j *JetStream) dispatch(ctx context.Context, g *groupPlan, ackWait time.Duration, m jetstream.Msg) {
	sub, ok := g.handlers[m.Subject()]
	if !ok {
		j.report(events.Subscription{Group: g.name}, nil, fmt.Errorf("nats: durable %q delivered subject %s, which no consumer in this process handles - handed back for a replica that does", g.name, m.Subject()))
		_ = m.NakWithDelay(ackWait)
		return
	}
	j.deliver(ctx, sub, ackWait, m)
}

func (j *JetStream) deliver(ctx context.Context, sub events.Subscription, ackWait time.Duration, m jetstream.Msg) {
	msg := decodeFrom(sub.Event, m.Headers(), m.Data())
	msg.SetDeliveries(deliveryCount(m))

	stop := holdOpen(m, ackWait)
	err := sub.Handle(withJetStreamMsg(ctx, m), msg)
	stop()

	if err != nil {
		j.report(sub, msg, err)
	}
	if j.capped(msg) {
		j.report(sub, msg, fmt.Errorf("nats: giving up on %s after %d deliveries - the chain asked for another and WithMaxDeliveries is %d", sub.Event, msg.Deliveries(), j.maxDeliveries))
	}
	if ackErr := j.answer(m, msg); ackErr != nil {
		j.report(sub, msg, fmt.Errorf("nats: answering for %s: %w", sub.Event, ackErr))
	}
}

// holdOpen resets the server's redelivery timer while the handler runs.
// Without it a handler slower than AckWait is redelivered behind itself,
// nondeterministically. A hung handler therefore stalls its durable, a
// visible failure in place of an invisible one; measured, the server
// does not redeliver an outstanding delivery, so bounding this would
// change nothing.
func holdOpen(m jetstream.Msg, ackWait time.Duration) func() {
	every := ackWait / 2
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
// unset disposition settles.
func (j *JetStream) answer(m jetstream.Msg, msg *events.Message) error {
	switch msg.Disposition() {
	case events.DispositionRedeliver:
		if j.capped(msg) {
			return m.Term()
		}
		if j.backoff != nil {
			if d := j.backoff(msg.Deliveries()); d > 0 {
				return m.NakWithDelay(d)
			}
		}
		return m.Nak()
	case events.DispositionReject:
		return m.Term()
	}
	return m.Ack()
}

func (j *JetStream) capped(msg *events.Message) bool {
	return msg.Disposition() == events.DispositionRedeliver &&
		j.maxDeliveries > 0 && msg.Deliveries() >= j.maxDeliveries
}

// deliveryCount is the server's attempt number, 1 on the first delivery
// - the same base as a Kafka share group.
func deliveryCount(m jetstream.Msg) int {
	meta, err := m.Metadata()
	if err != nil || meta == nil {
		return 0
	}
	return int(meta.NumDelivered)
}

// Close stops pulling on every durable this transport consumes, waits up
// to [WithDrainTimeout] for a message already inside a handler to be
// answered for, then ends every publish still waiting on a verdict, which
// reports its messages unsent wrapping [ErrClosed]. The connection is
// left open. A handler still running at the deadline is abandoned and
// its message redelivered once AckWait lapses.
func (j *JetStream) Close() error {
	j.closeOnce.Do(func() { close(j.closing) })
	defer j.closeStop()

	j.mu.Lock()
	consuming := j.consuming
	j.consuming = nil
	j.mu.Unlock()

	for _, cc := range consuming {
		cc.Drain()
	}
	deadline := time.After(j.drainTimeout)
	for _, cc := range consuming {
		select {
		case <-cc.Closed():
		case <-deadline:
			for _, late := range consuming {
				late.Stop()
			}
			return fmt.Errorf("nats: a handler was still running %s after Close; its message will be redelivered", j.drainTimeout)
		}
	}
	return nil
}

// groupRejected is the character set a durable name may not contain.
const groupRejected = ". > * / \\ \t\r\n"

// checkGroup refuses a group the server would reject as a durable name,
// quoting what the user wrote: the group is theirs to rename, and a
// silent rename would move where its consumers resume.
func checkGroup(group string) error {
	if group == "" {
		return errors.New("nats: a subscription needs a consumer group - it is the durable name")
	}
	if bad := firstRejected(group); bad != "" {
		return fmt.Errorf("nats: consumer group %q cannot contain %q - a JetStream durable name may not, and the group is yours to rename", group, bad)
	}
	if len(group) > 255 {
		return fmt.Errorf("nats: consumer group %q is %d characters, over JetStream's limit of 255 for a durable name", group, len(group))
	}
	return nil
}

func firstRejected(s string) string {
	for _, r := range s {
		if strings.ContainsRune(groupRejected, r) || r == ' ' {
			return string(r)
		}
	}
	return ""
}

var errNoJetStreamMsg = errors.New("nats: no JetStream message on this context - this middleware is installed on a transport that is not JetStream")
