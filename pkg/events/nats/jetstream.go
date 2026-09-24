package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

var (
	_ events.Publisher      = (*JetStream)(nil)
	_ events.Subscriber     = (*JetStream)(nil)
	_ events.BatchPublisher = (*JetStream)(nil)
	_ events.OptionAware    = (*JetStream)(nil)
	_ events.Dispositioner  = (*JetStream)(nil)
)

// JetStream publishes to and consumes from JetStream streams, which it never
// creates. Each group is one durable filtering all of the group's subjects on
// one stream; an existing durable may be widened, not narrowed ([AllowNarrow]).
type JetStream struct {
	js      jetstream.JetStream
	subject func(contract string) string
	onError func(sub events.Subscription, msg *events.Message, err error)
	backoff func(deliveries int) time.Duration
	log     *slog.Logger

	probeTimeout  time.Duration
	ackWait       time.Duration
	ackTimeout    time.Duration
	drainTimeout  time.Duration
	maxInFlight   int
	maxDeliveries int
	perGroup      map[events.Group]*groupConfig

	closeCtx  context.Context
	closeStop context.CancelFunc
	closing   chan struct{}
	closeOnce sync.Once

	mu        sync.Mutex
	consuming []jetstream.ConsumeContext
	groups    map[events.Group]bool
	accountOK bool
	streamFor map[string]string
}

// JetStreamOption configures a [JetStream].
type JetStreamOption func(*JetStream)

// WithJetStreamSubject replaces the contract-to-subject mapping, as
// [WithSubject] does for [Transport]. It must be one-to-one and yield
// concrete subjects: a delivery finds its consumer by subject.
func WithJetStreamSubject(fn func(contract string) string) JetStreamOption {
	return func(j *JetStream) { j.subject = fn }
}

// WithJetStreamErrorHandler installs a callback for handler errors and the
// client's own failures. A failure of a whole group, such as a deleted
// durable, arrives with only sub.Group set.
func WithJetStreamErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) JetStreamOption {
	return func(j *JetStream) { j.onError = fn }
}

// WithProbeTimeout bounds each query craftgo makes of the server: the
// start-up checks and the durable check after a missed heartbeat. Default
// 5s. A clustered server with JetStream disabled answers only by timing out.
func WithProbeTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.probeTimeout = d }
}

// WithPublishAckTimeout bounds a publish's wait for the stream's verdict;
// keep it under the duplicate window. Default 30s; zero waits until
// [JetStream.Close]; negative fails [NewJetStream].
func WithPublishAckTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.ackTimeout = d }
}

// WithAckWait sets how long the server waits for an answer before it
// redelivers, for durables this transport creates. Default 30s; [AckWait]
// overrides it per group.
func WithAckWait(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.ackWait = d }
}

// WithMaxInFlight sets how many messages a durable's pull buffers. Default
// 1; [MaxInFlight] overrides it per group. Messages are handled one at a
// time, so keep n × the slowest handler under AckWait.
func WithMaxInFlight(n int) JetStreamOption {
	return func(j *JetStream) { j.maxInFlight = n }
}

// WithMaxDeliveries caps the deliveries of one message: at the cap, a
// Redeliver the chain asked for becomes a reported termination. Default 5;
// zero is unbounded.
func WithMaxDeliveries(n int) JetStreamOption {
	return func(j *JetStream) { j.maxDeliveries = n }
}

// WithRedeliverBackoff delays a redelivery the chain asked for by
// fn(deliveries), where deliveries is the attempt just made, 1 on the
// first. Without it a redelivery is immediate.
func WithRedeliverBackoff(fn func(deliveries int) time.Duration) JetStreamOption {
	return func(j *JetStream) { j.backoff = fn }
}

// WithDrainTimeout bounds how long [JetStream.Close] waits for a message
// already inside a handler to finish. Default 30s; zero stops at once.
func WithDrainTimeout(d time.Duration) JetStreamOption {
	return func(j *JetStream) { j.drainTimeout = d }
}

// WithJetStreamLogger sets the logger for the durables the transport creates
// or re-points. Default [slog.Default]; nil is ignored.
func WithJetStreamLogger(l *slog.Logger) JetStreamOption {
	return func(j *JetStream) {
		if l != nil {
			j.log = l
		}
	}
}

// groupConfig is one group's settings; a zero field keeps the default.
type groupConfig struct {
	maxInFlight int
	ackWait     time.Duration
	deliver     jetstream.DeliverPolicy
	deliverSet  bool
	configure   func(cfg *jetstream.ConsumerConfig)
	allowNarrow bool
}

// GroupOption is one setting of one consumer group's durable.
type GroupOption func(*groupConfig)

// WithGroupConfig sets how one group's durable is created and consumed.
// Repeated calls for a group accumulate.
func WithGroupConfig(group events.Group, opts ...GroupOption) JetStreamOption {
	return func(j *JetStream) {
		cfg := j.perGroup[group]
		if cfg == nil {
			cfg = &groupConfig{}
			j.perGroup[group] = cfg
		}
		for _, o := range opts {
			if o != nil {
				o(cfg)
			}
		}
	}
}

// MaxInFlight is this group's prefetch, overriding [WithMaxInFlight].
// Zero keeps the transport-wide value; a negative one fails [NewJetStream].
func MaxInFlight(n int) GroupOption {
	return func(c *groupConfig) { c.maxInFlight = n }
}

// AckWait is this group's redelivery timer, overriding [WithAckWait] for a
// durable this transport creates. Zero keeps the transport-wide value.
func AckWait(d time.Duration) GroupOption {
	return func(c *groupConfig) { c.ackWait = d }
}

// DeliverPolicy is where this group's durable starts reading, applied only
// when the durable is created.
func DeliverPolicy(p jetstream.DeliverPolicy) GroupOption {
	return func(c *groupConfig) { c.deliver, c.deliverSet = p, true }
}

// ConsumerConfig lets fn adjust the config this group's durable is created
// with. The name, the filter subjects and the explicit ack policy are
// re-asserted after fn returns.
func ConsumerConfig(fn func(cfg *jetstream.ConsumerConfig)) GroupOption {
	return func(c *groupConfig) { c.configure = fn }
}

// AllowNarrow lets this group's durable be re-pointed at fewer subjects than
// it filters. Without it [JetStream.Subscribe] refuses such a durable, since
// the subjects it would drop would reach no consumer.
func AllowNarrow() GroupOption {
	return func(c *groupConfig) { c.allowNarrow = true }
}

// NewJetStream binds a transport to an existing connection, which the caller
// owns and [JetStream.Close] leaves open. It fails on an invalid option value.
func NewJetStream(conn *nats.Conn, opts ...JetStreamOption) (*JetStream, error) {
	j := &JetStream{
		subject:       func(c string) string { return c },
		log:           slog.Default(),
		probeTimeout:  5 * time.Second,
		ackWait:       30 * time.Second,
		ackTimeout:    30 * time.Second,
		drainTimeout:  30 * time.Second,
		maxInFlight:   1,
		maxDeliveries: 5,
		perGroup:      map[events.Group]*groupConfig{},
		groups:        map[events.Group]bool{},
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
	for _, group := range slices.Sorted(maps.Keys(j.perGroup)) {
		if n := j.perGroup[group].maxInFlight; n < 0 {
			return nil, fmt.Errorf("nats: WithGroupConfig(%q, MaxInFlight(%d)) must be at least 1", group, n)
		}
	}

	js, err := jetstream.New(conn, jetstream.WithPublishAsyncTimeout(j.ackTimeout))
	if err != nil {
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	j.js = js
	j.closeCtx, j.closeStop = context.WithCancel(context.Background())
	return j, nil
}

// ErrClosed is what a publish or subscribe reports after [JetStream.Close], and a subscribe
// after [Transport.Close].
var ErrClosed = errors.New("transport closed")

// AdapterName implements [events.OptionAware]; both transports are named [Adapter].
func (j *JetStream) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: it reads no per-message options.
func (j *JetStream) KnownOptions() []string { return nil }

// CanDisposition implements [events.Dispositioner]: it settles, redelivers
// and rejects. A durable without explicit acks is refused at subscribe.
func (j *JetStream) CanDisposition(d events.Disposition) bool {
	switch d {
	case events.DispositionSettle, events.DispositionRedeliver, events.DispositionReject:
		return true
	}
	return false
}

// Publish sends one message and waits for the stream to store it. A ctx
// already done sends nothing; ending it later does not end the wait, which
// [WithPublishAckTimeout] bounds and [JetStream.Close] ends with [ErrClosed].
func (j *JetStream) Publish(ctx context.Context, msg *events.Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nats: publish %s: %w", msg.Event, err)
	}
	if j.closeCtx.Err() != nil {
		return fmt.Errorf("nats: publish %s: %w", msg.Event, ErrClosed)
	}
	waitCtx, cancel := j.ackContext(ctx)
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

// ackContext detaches ctx from its cancellation and bounds it by [WithPublishAckTimeout];
// zero is the longest timeout there is, as the client bounds a context with no deadline.
func (j *JetStream) ackContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := j.ackTimeout
	if timeout == 0 {
		timeout = math.MaxInt64
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// PublishBatch publishes the whole batch, then waits for every
// acknowledgement; the error names each message the stream did not confirm.
// ctx and Close act as on [JetStream.Publish].
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

// Subscribe binds each group to one durable filtering all of the group's
// subjects, so all of a group's contracts must arrive in one call. Groups
// started before a refusal stay live. A group subscribes again once its
// context has ended and its running handler has returned. Once
// [JetStream.Close] has begun it returns [ErrClosed].
func (j *JetStream) Subscribe(ctx context.Context, subs []events.Subscription) error {
	if j.isClosing() {
		return fmt.Errorf("nats: subscribe: %w", ErrClosed)
	}
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

// groupPlan is one group's durable and the consumer behind each subject.
type groupPlan struct {
	name     events.Group
	stream   string
	config   groupConfig
	subjects []string
	handlers map[string]events.Subscription
}

func (j *JetStream) plan(ctx context.Context, subs []events.Subscription) ([]*groupPlan, error) {
	byName := map[events.Group]*groupPlan{}
	var order []*groupPlan
	for _, sub := range subs {
		group := sub.Group
		if err := checkGroup(group); err != nil {
			return nil, err
		}
		if j.subscribed(group) {
			return nil, fmt.Errorf("nats: consumer group %q is already subscribed on this transport - hand every consumer of a group to one Subscribe", group)
		}
		subject := j.subject(sub.Event)
		stream, err := j.streamCovering(ctx, subject)
		if err != nil {
			return nil, err
		}
		g := byName[group]
		if g == nil {
			g = &groupPlan{name: group, stream: stream, config: j.configFor(group), handlers: map[string]events.Subscription{}}
			byName[group] = g
			order = append(order, g)
		}
		if g.stream != stream {
			return nil, fmt.Errorf("nats: consumer group %q reads %s from stream %q and %s from stream %q - a JetStream durable reads one stream, so give each stream's consumers a group of their own",
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

// configFor returns group's settings filled in from the transport defaults.
func (j *JetStream) configFor(group events.Group) groupConfig {
	cfg := groupConfig{maxInFlight: j.maxInFlight, ackWait: j.ackWait}
	g := j.perGroup[group]
	if g == nil {
		return cfg
	}
	if g.maxInFlight > 0 {
		cfg.maxInFlight = g.maxInFlight
	}
	if g.ackWait > 0 {
		cfg.ackWait = g.ackWait
	}
	cfg.deliver, cfg.deliverSet = g.deliver, g.deliverSet
	cfg.configure, cfg.allowNarrow = g.configure, g.allowNarrow
	return cfg
}

func (j *JetStream) subscribed(group events.Group) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.groups[group]
}

// ErrConsumerStopped reports, through [WithJetStreamErrorHandler], a group
// whose durable or its stream was deleted after boot. Nothing recreates the
// durable; an application that wants the group back matches this error and
// subscribes the group again.
var ErrConsumerStopped = errors.New("nats: consumer stopped consuming")

func (j *JetStream) consumeGroup(ctx context.Context, g *groupPlan) error {
	consumer, ackWait, err := j.durable(ctx, g)
	if err != nil {
		return err
	}
	whole := events.Subscription{Group: g.name}
	cc, err := consumer.Consume(
		func(m jetstream.Msg) { j.dispatch(ctx, g, ackWait, m) },
		jetstream.PullMaxMessages(g.config.maxInFlight),
		jetstream.ConsumeErrHandler(func(cc jetstream.ConsumeContext, err error) {
			if ctx.Err() != nil {
				return
			}
			j.report(whole, nil, fmt.Errorf("nats: consume group %q: %w", g.name, err))
			if !errors.Is(err, jetstream.ErrNoHeartbeat) {
				return
			}
			// The server sends no status for a durable deleted with no pull
			// waiting, so a missed heartbeat checks whether it still exists.
			probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
			defer cancel()
			if _, err := consumer.Info(probeCtx); errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound) {
				cc.Stop()
			}
		}),
	)
	if err != nil {
		return fmt.Errorf("nats: consume %q on stream %q: %w", g.name, g.stream, err)
	}

	j.mu.Lock()
	if j.isClosing() {
		j.mu.Unlock()
		cc.Stop()
		return fmt.Errorf("nats: consume %q on stream %q: %w", g.name, g.stream, ErrClosed)
	}
	j.consuming = append(j.consuming, cc)
	j.groups[g.name] = true
	j.mu.Unlock()

	go func() {
		select {
		case <-cc.Closed():
			j.release(g.name, cc)
			if ctx.Err() == nil && !j.isClosing() {
				j.report(whole, nil, fmt.Errorf("%w: %q on stream %q - it was deleted, or the stream was", ErrConsumerStopped, g.name, g.stream))
			}
		case <-ctx.Done():
			cc.Stop()
			<-cc.Closed()
			j.release(g.name, cc)
		case <-j.closing:
		}
	}()
	return nil
}

// release forgets group and its consume context cc once cc has closed, so the group can
// subscribe again.
func (j *JetStream) release(group events.Group, cc jetstream.ConsumeContext) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.groups, group)
	j.consuming = slices.DeleteFunc(j.consuming, func(c jetstream.ConsumeContext) bool { return c == cc })
}

func (j *JetStream) isClosing() bool {
	select {
	case <-j.closing:
		return true
	default:
		return false
	}
}

// durable adopts or creates the group's durable and returns its AckWait.
// Of an existing durable, only the filter subjects are ever written.
func (j *JetStream) durable(ctx context.Context, g *groupPlan) (jetstream.Consumer, time.Duration, error) {
	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()

	name := string(g.name)
	existing, err := j.js.Consumer(probeCtx, g.stream, name)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		return j.createDurable(probeCtx, g)
	case err != nil:
		return nil, 0, fmt.Errorf("nats: look up consumer %q on stream %q: %w", g.name, g.stream, err)
	}

	cfg := existing.CachedInfo().Config
	if cfg.AckPolicy != jetstream.AckExplicitPolicy {
		return nil, 0, fmt.Errorf("nats: consumer %q on stream %q does not acknowledge explicitly, so Redeliver and Reject would do nothing - recreate it with AckExplicit", g.name, g.stream)
	}
	carried := filterOf(cfg)
	switch adopting(carried, g.subjects, g.config.allowNarrow) {
	case adoptAsIs:
		return existing, effectiveAckWait(existing, g.config.ackWait), nil
	case repoint:
		setFilter(&cfg, g.subjects)
		updated, err := j.js.UpdateConsumer(probeCtx, g.stream, cfg)
		if err != nil {
			return nil, 0, fmt.Errorf("nats: point consumer %q on stream %q at %v: %w", g.name, g.stream, g.subjects, err)
		}
		j.log.InfoContext(probeCtx, "nats: repointed jetstream consumer",
			slog.String("group", name),
			slog.String("stream", g.stream),
			slog.Any("was", carried),
			slog.Any("subjects", g.subjects))
		return updated, effectiveAckWait(updated, g.config.ackWait), nil
	}
	return nil, 0, narrowed(g, carried)
}

// createDurable creates the group's durable and logs its configuration.
func (j *JetStream) createDurable(ctx context.Context, g *groupPlan) (jetstream.Consumer, time.Duration, error) {
	name := string(g.name)
	cfg := jetstream.ConsumerConfig{Durable: name, Name: name, AckWait: g.config.ackWait}
	if g.config.deliverSet {
		cfg.DeliverPolicy = g.config.deliver
	}
	if g.config.configure != nil {
		g.config.configure(&cfg)
	}
	cfg.Durable, cfg.Name = name, name
	cfg.AckPolicy = jetstream.AckExplicitPolicy
	setFilter(&cfg, g.subjects)

	created, err := j.js.CreateConsumer(ctx, g.stream, cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("nats: create consumer %q on stream %q: %w", g.name, g.stream, err)
	}
	j.log.InfoContext(ctx, "nats: created jetstream consumer",
		slog.String("group", name),
		slog.String("stream", g.stream),
		slog.Any("subjects", g.subjects),
		slog.String("deliver_policy", cfg.DeliverPolicy.String()),
		slog.Duration("ack_wait", cfg.AckWait),
		slog.Int("max_in_flight", g.config.maxInFlight))
	return created, effectiveAckWait(created, g.config.ackWait), nil
}

// adoption is what to do with a durable that already exists.
type adoption int

const (
	// adoptAsIs is a durable already filtering exactly the plan.
	adoptAsIs adoption = iota
	// repoint is a durable to be pointed at the plan.
	repoint
	// refuse is a durable this process would have to narrow.
	refuse
)

// adopting compares a durable's carried filter set with the planned one.
// Only a widening is repointed without allowNarrow; no filter is every subject.
func adopting(carried, planned []string, allowNarrow bool) adoption {
	switch {
	case slices.Equal(carried, planned):
		return adoptAsIs
	case allowNarrow:
		return repoint
	case len(carried) > 0 && subset(carried, planned):
		return repoint
	}
	return refuse
}

// subset reports whether every subject of a is in b.
func subset(a, b []string) bool {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	for _, s := range a {
		if !in[s] {
			return false
		}
	}
	return true
}

// narrowed is the refusal to point a durable at less than it has.
func narrowed(g *groupPlan, carried []string) error {
	return fmt.Errorf("nats: consumer group %q on stream %q filters %s, and this process plans %v - pointing it at the plan would stop delivering the subjects it loses, which no replica would pick up; add craftnats.WithGroupConfig(%q, craftnats.AllowNarrow()) if that is the intention, or give this deployable a group of its own",
		g.name, g.stream, describeFilter(carried), g.subjects, g.name)
}

func describeFilter(subjects []string) string {
	if len(subjects) == 0 {
		return "every subject on its stream (it carries no filter)"
	}
	return fmt.Sprintf("%v", subjects)
}

func effectiveAckWait(c jetstream.Consumer, fallback time.Duration) time.Duration {
	if info := c.CachedInfo(); info != nil && info.Config.AckWait > 0 {
		return info.Config.AckWait
	}
	return fallback
}

// setFilter writes one subject to FilterSubject, which every server takes.
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

func (j *JetStream) report(sub events.Subscription, msg *events.Message, err error) {
	if j.onError != nil {
		j.onError(sub, msg, err)
	}
}

// checkAccount refuses a server without JetStream; success is cached.
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

// streamCovering returns the stream carrying subject. A consumer on a subject
// no stream carries would be created and silently receive nothing.
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

// dispatch routes a delivery to its subject's consumer. A subject nothing
// here handles is handed back for a replica, on another version, that does.
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

// holdOpen resets m's redelivery timer until the returned func is called and
// returns, so a slow handler keeps its message and no reset follows the answer.
func holdOpen(m jetstream.Msg, ackWait time.Duration) func() {
	every := ackWait / 2
	if every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
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
	return func() { close(done); <-stopped }
}

// answer acks, naks or terms m as the chain decided; unset settles.
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

// deliveryCount is m's attempt number, 1 on the first; 0 if unknown.
func deliveryCount(m jetstream.Msg) int {
	meta, err := m.Metadata()
	if err != nil || meta == nil {
		return 0
	}
	return int(meta.NumDelivered)
}

// Close stops consuming, waits up to [WithDrainTimeout] for running
// handlers, then ends waiting publishes with [ErrClosed]. It returns an
// error if a handler outlives the wait. The connection stays open.
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

// checkGroup refuses a group the server would reject as a durable name.
func checkGroup(group events.Group) error {
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

func firstRejected(s events.Group) string {
	for _, r := range s {
		if strings.ContainsRune(groupRejected, r) {
			return string(r)
		}
	}
	return ""
}
