package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BatchHandler processes one batch: messages of one contract, in the order they arrived.
// Once it returns, the transport answers each message per its [Message.Disposition],
// settling an unset one.
type BatchHandler func(ctx context.Context, batch []*Message) error

// BatchMiddleware wraps next, the batch handler for sub. It continues by calling next,
// never sub.Batch.Handle, which is undecorated.
type BatchMiddleware func(sub Subscription, next BatchHandler) BatchHandler

// BatchChain is a list of batch middlewares, outermost first: NewBatchChain(A, B) wraps a
// handler as A(B(h)). Append returns a new chain, and nil entries are skipped.
type BatchChain []BatchMiddleware

// NewBatchChain returns a chain of mws, outermost first, copied from mws.
func NewBatchChain(mws ...BatchMiddleware) BatchChain {
	if len(mws) == 0 {
		return nil
	}
	out := make(BatchChain, len(mws))
	copy(out, mws)
	return out
}

// Append returns a new chain with mws added at the innermost end; the receiver is
// unchanged.
func (c BatchChain) Append(mws ...BatchMiddleware) BatchChain {
	if len(mws) == 0 {
		return c
	}
	out := make(BatchChain, len(c)+len(mws))
	copy(out, c)
	copy(out[len(c):], mws)
	return out
}

// wrap folds the chain over h, from the innermost entry outwards.
func (c BatchChain) wrap(sub Subscription, h BatchHandler) BatchHandler {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		h = c[i](sub, h)
	}
	return h
}

// BatchSize bounds a batch: at most Max messages, none of which waits longer than Wait
// for the batch to fill. A transport may hand a batch over sooner.
type BatchSize struct {
	Max  int
	Wait time.Duration
}

// batchSizeJSON is the wire shape of a [BatchSize].
type batchSizeJSON struct {
	Max  int    `json:"max"`
	Wait string `json:"wait"`
}

// MarshalJSON renders the bounds as {"max":100,"wait":"1s"}.
func (s BatchSize) MarshalJSON() ([]byte, error) {
	return json.Marshal(batchSizeJSON{s.Max, s.Wait.String()})
}

// UnmarshalJSON reads the bounds MarshalJSON renders.
func (s *BatchSize) UnmarshalJSON(data []byte) error {
	var raw batchSizeJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var wait time.Duration
	if raw.Wait != "" {
		var err error
		if wait, err = time.ParseDuration(raw.Wait); err != nil {
			return fmt.Errorf("events: batch wait: %w", err)
		}
	}
	s.Max, s.Wait = raw.Max, wait
	return nil
}

// Batch makes a [Subscription] consume in batches, which only a [BatchSubscriber]
// transport does; see [Bus.Register].
type Batch struct {
	BatchSize
	// Chain is this subscription's own batch middleware, run inside the bus batch chain;
	// see [Bus.Start].
	Chain BatchChain
	// Handle processes one batch; [Bus.Start] wraps it in a recover.
	Handle BatchHandler
}

// BatchSubscriber is the optional upgrade for a transport that consumes a subscription
// with [Subscription.Batch] set: it gathers the subscription's messages into batches of
// its [BatchSize], hands each to Batch.Handle, and answers every message of it once
// Handle returns, as [Subscriber] describes for one message. It is asked per instance, and
// its answer must not change after construction.
type BatchSubscriber interface {
	SubscribesBatches() bool
}

// ErrBatchUnsupported is returned by [Bus.Register] for a batch subscription on a
// transport that does not consume in batches; a bus with no subscribe half leaves the
// refusal to [Bus.Start], as for any subscription.
var ErrBatchUnsupported = errors.New("events: transport cannot consume in batches")

// ErrBatchGroupShared is returned by [Bus.Register] for a batch subscription in a group a
// subscription of another contract holds, and for a subscription of another contract in a
// batch subscription's group: a batch subscription holds its group alone. The same
// contract twice in a group is [ErrDuplicateSubscription].
var ErrBatchGroupShared = errors.New("events: a batch subscription holds its group alone")

// ErrInvalidBatch is returned by [Bus.Register] for a batch subscription whose Max is
// below 1, whose Wait is not positive, or that sets Handle or Chain beside Batch.
var ErrInvalidBatch = errors.New("events: invalid batch subscription")

// batchProblem returns why sub, a batch subscription, cannot be registered, or nil.
func batchProblem(sub Subscription) error {
	b := sub.Batch
	switch {
	case b.Handle == nil:
		return ErrNoHandler
	case sub.Handle != nil || len(sub.Chain) > 0:
		return fmt.Errorf("%w: it takes its handler and chain in Batch, not in Handle and Chain", ErrInvalidBatch)
	case b.Max < 1:
		return fmt.Errorf("%w: Max %d is below 1", ErrInvalidBatch, b.Max)
	case b.Wait <= 0:
		return fmt.Errorf("%w: Wait %s is not positive", ErrInvalidBatch, b.Wait)
	}
	return nil
}

// canBatch reports whether sub consumes in batches.
func canBatch(sub Subscriber) bool {
	bs, ok := sub.(BatchSubscriber)
	return ok && bs.SubscribesBatches()
}

// ItemErrors is a batch handler's error naming the messages that failed, by index into
// the batch; a message it does not name succeeded.
type ItemErrors map[int]error

func (e ItemErrors) Error() string {
	const shown = 3
	idx := e.indices()
	parts := make([]string, 0, shown+1)
	for _, i := range idx[:min(len(idx), shown)] {
		parts = append(parts, fmt.Sprintf("[%d] %v", i, e[i]))
	}
	if len(idx) > shown {
		parts = append(parts, fmt.Sprintf("and %d more", len(idx)-shown))
	}
	return fmt.Sprintf("events: %d message(s) of the batch failed: %s", len(idx), strings.Join(parts, "; "))
}

// Unwrap returns the failures in index order, for [errors.Is] and [errors.As].
func (e ItemErrors) Unwrap() []error {
	out := make([]error, 0, len(e))
	for _, i := range e.indices() {
		out = append(out, e[i])
	}
	return out
}

// indices returns the failed indices, ascending.
func (e ItemErrors) indices() []int {
	out := make([]int, 0, len(e))
	for i := range e {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// itemErrors returns the non-nil entries of errs by index, or nil when there are none.
func itemErrors(errs []error) error {
	var out ItemErrors
	for i, err := range errs {
		if err == nil {
			continue
		}
		if out == nil {
			out = ItemErrors{}
		}
		out[i] = err
	}
	if out == nil {
		return nil
	}
	return out
}

// decoratedBatch wraps sub's batch handler in the order [Bus.Start] describes; busChain is
// the batch chain Start read under the lock, escaped what the outer recover asks for.
func decoratedBatch(busChain BatchChain, sub Subscription, escaped Disposition) BatchHandler {
	h := recoverBatch(sub, sub.Batch.Handle, DispositionUnset)
	chain := busChain.Append(sub.Batch.Chain...)
	if len(chain) == 0 {
		return h
	}
	return recoverBatch(sub, chain.wrap(sub, h), escaped)
}

// recoverBatch turns a panic in h into a [*PanicError] naming sub and sets the disposition
// of every message of the batch to escaped, voiding whatever the panicking frames asked for.
func recoverBatch(sub Subscription, h BatchHandler, escaped Disposition) BatchHandler {
	return func(ctx context.Context, batch []*Message) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			err = newPanicError(sub, r)
			for _, msg := range batch {
				if msg != nil {
					msg.disposition = escaped
				}
			}
		}()
		return h(ctx, batch)
	}
}
