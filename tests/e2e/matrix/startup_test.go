package matrix

import (
	"context"
	"errors"
	"strings"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
	"github.com/craftgodotdev/craftgo/pkg/server"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/wiring"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// capableTransport is an in-process transport that answers yes to every
// disposition. It is not a broker that can honour one - it is what a
// transport that CAN looks like to the startup check, which reads the
// capability once and never asks again.
type capableTransport struct{ *memory.Transport }

func (capableTransport) CanDisposition(craftevents.Disposition) bool { return true }

// A delivery guarantee the design states is worth nothing if the
// transport cannot keep it, so Register refuses to start rather than
// running a chain whose Redeliver is silently settled. The refusal has to
// come out of the generated umbrella: that is the only thing standing
// between the refusal and a binary that boots, serves HTTP, passes
// readiness and quietly loses every message it meant to retry.
func TestARequiredDispositionTheTransportLacksFailsStartup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() craftevents.Option
		boots bool
	}{
		{
			name:  "a transport that settles and nothing else",
			build: func() craftevents.Option { return craftevents.WithTransport(memory.New()) },
		},
		{
			name: "a transport that can honour it",
			build: func() craftevents.Option {
				return craftevents.WithTransport(capableTransport{memory.New()})
			},
			boots: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := craftevents.New(tc.build(),
				craftevents.WithCodec(codecjson.Codec{}),
				craftevents.WithDispositionRequired(craftevents.DispositionRedeliver))
			svc := svccontext.NewServiceContext()
			svc.Events = svccontext.NewEvents(bus)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			shutdown, err := wiring.Register(ctx, server.New(svc), svc)

			if tc.boots {
				if err != nil {
					t.Fatalf("startup failed on a transport that can redeliver: %v", err)
				}
				if shutdown != nil {
					_ = shutdown(ctx)
				}
				return
			}
			if err == nil {
				t.Fatal("started on a transport that cannot honour the required disposition")
			}
			if !errors.Is(err, craftevents.ErrDispositionUnsupported) {
				t.Errorf("startup failed with %v, want ErrDispositionUnsupported", err)
			}
			if !strings.Contains(err.Error(), "redeliver") {
				t.Errorf("the failure does not name the disposition asked for: %v", err)
			}
		})
	}
}

// A design that declares consumers and is handed no bus consumes nothing.
// Saying so at startup is the difference between a deployment that fails
// and one that runs with half its work silently undone, so the umbrella
// names both halves of what the design declared.
//
// The counts are the halves, and they are counted rather than written, so
// they are what a reader has to be able to trust: 10 events is the 9
// contracts a generated publisher exposes plus payments.settled.v1, which
// this design consumes and never publishes.
func TestStartupRefusesAContainerWithNoBus(t *testing.T) {
	svc := svccontext.NewServiceContext()
	_, err := wiring.Register(context.Background(), server.New(svc), svc)
	if err == nil {
		t.Fatal("started with no bus on a design that declares consumers")
	}
	if want := "10 event(s) and 10 consumer(s)"; !strings.Contains(err.Error(), want) {
		t.Errorf("the failure does not say what the design declares (%q): %v", want, err)
	}
	if !strings.Contains(err.Error(), "NewEvents") {
		t.Errorf("the failure does not say how to supply a bus: %v", err)
	}
}
