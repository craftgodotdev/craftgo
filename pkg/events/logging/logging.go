// Package logging is craftgo's consumer middleware for logging what a
// bus delivered. It is a sub-package so that [log/slog] stays out of the
// exported surface of `pkg/events`, which every generated contract
// package imports.
package logging

import (
	"context"
	"log/slog"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/events"
)

// AccessLog logs one line per delivery: the contract, the consumer, its
// group, the key, how long the handler took, and the error when there
// was one.
//
//	craftevents.New(
//	    craftevents.WithTransport(tr),
//	    craftevents.WithCodec(codecjson.Codec{}),
//	    craftevents.WithMiddleware(logging.AccessLog(log.Slog())),
//	)
//
// # One line, one level
//
// A failure is logged at the SAME level as a success, with an `error`
// attribute, not at Error. The transport's own error handler already logs
// failures at Error, so a second Error line here is the same failure
// reported twice - two stack traces, and roughly four times the cost on
// the path that is already the slowest.
//
// Do not delete that error handler to make room for this one. It sees
// failures a middleware cannot: a fetch that did not return, a commit
// that did not land, a record for a contract this consumer does not
// handle. Those never enter the chain, and they are exactly what matters
// once a project points its scaffold at a real broker.
//
// # Cost
//
// One line per delivery is one line per delivery. A consumer taking
// thousands a second pays for all of them, and the level gate does not
// help because the line is built to be gated. Use [AccessLogLevel] to put
// them below the running level, or [AccessLogSkipContracts] to drop the
// chatty ones, on a consumer where that matters.
func AccessLog(l *slog.Logger, opts ...AccessLogOption) events.Middleware {
	cfg := accessLogConfig{level: slog.LevelInfo}
	for _, o := range opts {
		o(&cfg)
	}
	return func(sub events.Subscription, next events.Handler) events.Handler {
		if l == nil || cfg.skip[sub.Event] {
			return next
		}
		event, consumer, group := sub.Event, sub.Consumer, string(sub.Group)
		return func(ctx context.Context, msg *events.Message) error {
			start := time.Now()
			err := next(ctx, msg)

			// The context has to reach the handler, so this is LogAttrs
			// and not Info: slog's level shortcuts pass a background
			// context of their own, and a trace id would vanish with the
			// line still looking right.
			if !l.Enabled(ctx, cfg.level) {
				return err
			}
			attrs := make([]slog.Attr, 0, 6+len(cfg.extra))
			attrs = append(attrs,
				slog.String("event", event),
				slog.String("consumer", consumer),
				slog.String("group", group),
				slog.String("key", msg.Key),
				slog.Duration("took", time.Since(start)),
			)
			if err != nil {
				attrs = append(attrs, slog.Any("error", err))
			}
			for _, fn := range cfg.extra {
				attrs = append(attrs, fn(ctx, msg)...)
			}
			l.LogAttrs(ctx, cfg.level, "consumed", attrs...)
			return err
		}
	}
}

// AccessLogOption configures [AccessLog].
type AccessLogOption func(*accessLogConfig)

type accessLogConfig struct {
	level slog.Level
	skip  map[string]bool
	extra []func(context.Context, *events.Message) []slog.Attr
}

// AccessLogLevel sets the level every line is written at, successes and
// failures alike. The default is Info.
func AccessLogLevel(l slog.Level) AccessLogOption {
	return func(c *accessLogConfig) { c.level = l }
}

// AccessLogSkipContracts leaves the named contracts unlogged, for the
// high-volume ones whose lines are noise. A skipped contract is not
// wrapped at all, so it costs nothing rather than costing a check.
func AccessLogSkipContracts(contracts ...string) AccessLogOption {
	return func(c *accessLogConfig) {
		if c.skip == nil {
			c.skip = make(map[string]bool, len(contracts))
		}
		for _, name := range contracts {
			c.skip[name] = true
		}
	}
}

// AccessLogFields adds attributes derived from each delivery - a tenant
// off a header, a field off the message. It runs only on a line that
// passes the level gate.
func AccessLogFields(fn func(ctx context.Context, msg *events.Message) []slog.Attr) AccessLogOption {
	return func(c *accessLogConfig) {
		if fn != nil {
			c.extra = append(c.extra, fn)
		}
	}
}
