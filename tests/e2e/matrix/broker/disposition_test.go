package broker

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	craftkafka "github.com/craftgodotdev/craftgo/pkg/events/kafka"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// attempts records what each delivery looked like to a middleware
// wrapping a registered handler.
type attempts struct {
	mu   sync.Mutex
	rows []attempt
}

type attempt struct {
	consumer   string
	deliveries int
}

func (a *attempts) add(r attempt) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, r)
}

func (a *attempts) of(consumer string) []attempt {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []attempt
	for _, r := range a.rows {
		if r.consumer == consumer {
			out = append(out, r)
		}
	}
	return out
}

// A middleware asking for a message back gets it back, and one giving a
// message up is not asked again - through the GENERATED descriptor, which
// is the part a transport test cannot reach.
//
// The decision is written on the delivery, and between the writing and
// the broker reading it sit the descriptor's decode-validate-dispatch
// wrapper and both of the bus's recover frames. A wrapper that handed the
// handler a different message, or a recover that cleared the decision,
// would lose the ask with nothing to see it: every message the chain
// meant to retry would be taken as done.
//
// The sequence below is one goroutine's, which soleListenerOf checks
// against what this deployable registers rather than this comment
// asserting it.
func TestARedeliveredMessageComesBackAndARejectedOneDoesNot(t *testing.T) {
	addrs := cluster(t, itemStocked, warehouseClosed, forged, tierPromoted)
	shareFromEarliest(t, addrs, tierGroup(t))

	seen := &attempts{}
	// A bus middleware wraps EVERY subscription, and the decision below
	// is the TierPromoted listener's alone: asking for a message back is
	// unbounded here, so left unscoped this would arm a redelivery loop on
	// every other share consumer the moment one of them is sent anything.
	//
	// The scope is the contract, which soleListenerOf has just checked
	// exactly one subscription of this deployable carries.
	decide := func(sub craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		if sub.Event != tierPromoted {
			return next
		}
		return func(ctx context.Context, msg *craftevents.Message) error {
			err := next(ctx, msg)
			seen.add(attempt{consumer: trackTier, deliveries: msg.Deliveries()})
			// Ask for it back once, then give it up.
			if msg.Deliveries() <= 1 {
				msg.Redeliver()
			} else {
				msg.Reject()
			}
			return err
		}
	}

	// Published through a transport of its own: the consuming one is in
	// share mode, and a producer needs nothing from it.
	_, publisher := boot(t, addrs, false, nil, nil)
	if err := events.TierPromoted.Publish(context.Background(), publisher,
		&eventtypes.TierPromoted{Tier: 2, MemberID: "mem-1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	svc, _ := boot(t, addrs, true,
		[]craftkafka.Option{craftkafka.WithShareGroup(), craftkafka.WithMaxDeliveries(0)},
		[]craftevents.Option{craftevents.WithMiddleware(decide)})

	delivered := counts(svc, trackTier)
	waitFor(t, 60*time.Second, "the redelivered message", "TrackTier=2", delivered)
	stillTrue(t, 4*time.Second, "the message the chain gave up was delivered again",
		"TrackTier=2", delivered)

	rows := seen.of(trackTier)
	if len(rows) != 2 {
		t.Fatalf("the middleware saw %d deliveries, want 2", len(rows))
	}
	// The broker's own count, this attempt included: the same message
	// came back rather than a second one arriving.
	if rows[0].deliveries != 1 || rows[1].deliveries != 2 {
		t.Errorf("delivery counts = %d then %d, want 1 then 2", rows[0].deliveries, rows[1].deliveries)
	}
	got := svc.DeliveredTo(trackTier)
	if len(got) != 2 {
		t.Fatalf("the handler ran %d times", len(got))
	}
	first, _ := got[0].(*eventtypes.TierPromoted)
	second, _ := got[1].(*eventtypes.TierPromoted)
	if first == nil || second == nil || !reflect.DeepEqual(*first, *second) {
		t.Errorf("the consumer saw %#v then %#v, want the same payload twice", got[0], got[1])
	}
}
