// Package logging provides an [events.Middleware] that logs each delivery with log/slog,
// and an [events.BatchMiddleware] that logs each batch.
package logging

import (
	"context"
	"log/slog"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/events"
)

// AccessLog logs one line per delivery: the contract, consumer, group, key, duration and,
// on failure, an `error` attribute. Successes and failures share one level, Info by
// default; a nil logger leaves the chain unchanged.
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
			// LogAttrs hands the slog handler the delivery's ctx.
			l.LogAttrs(ctx, cfg.level, "consumed", attrs...)
			return err
		}
	}
}

// BatchAccessLog logs one line per batch: the contract, consumer, group, the batch's size,
// duration, how many messages the chain asked to redeliver and to reject, and, on failure,
// an `error` attribute. It takes [AccessLog]'s options; [AccessLogFields], which derives
// from one delivery, adds nothing to a batch line. A nil logger leaves the chain unchanged.
func BatchAccessLog(l *slog.Logger, opts ...AccessLogOption) events.BatchMiddleware {
	cfg := accessLogConfig{level: slog.LevelInfo}
	for _, o := range opts {
		o(&cfg)
	}
	return func(sub events.Subscription, next events.BatchHandler) events.BatchHandler {
		if l == nil || cfg.skip[sub.Event] {
			return next
		}
		event, consumer, group := sub.Event, sub.Consumer, string(sub.Group)
		return func(ctx context.Context, batch []*events.Message) error {
			start := time.Now()
			err := next(ctx, batch)

			if !l.Enabled(ctx, cfg.level) {
				return err
			}
			var redeliver, reject int
			for _, msg := range batch {
				switch msg.Disposition() {
				case events.DispositionRedeliver:
					redeliver++
				case events.DispositionReject:
					reject++
				}
			}
			attrs := []slog.Attr{
				slog.String("event", event),
				slog.String("consumer", consumer),
				slog.String("group", group),
				slog.Int("size", len(batch)),
				slog.Duration("took", time.Since(start)),
				slog.Int("redeliver", redeliver),
				slog.Int("reject", reject),
			}
			if err != nil {
				attrs = append(attrs, slog.Any("error", err))
			}
			l.LogAttrs(ctx, cfg.level, "consumed batch", attrs...)
			return err
		}
	}
}

// AccessLogOption configures [AccessLog] and [BatchAccessLog].
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

// AccessLogSkipContracts leaves the named contracts unlogged; they are not wrapped at all.
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

// AccessLogFields adds attributes derived from each delivery; fn runs only for a line
// that passes the level gate.
func AccessLogFields(fn func(ctx context.Context, msg *events.Message) []slog.Attr) AccessLogOption {
	return func(c *accessLogConfig) {
		if fn != nil {
			c.extra = append(c.extra, fn)
		}
	}
}
