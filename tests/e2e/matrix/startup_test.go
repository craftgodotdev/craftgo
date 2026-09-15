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

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/middleware"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/wiring"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// wiredContext is a container with every HTTP middleware the design
// applies assigned. Register refuses one that is missing any of them, so
// a test reaching past that check builds its container through here.
func wiredContext() *svccontext.ServiceContext {
	svc := svccontext.NewServiceContext()
	svc.Audit = middleware.NewAuditMiddleware()
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware()
	svc.BasicAuth = middleware.NewBasicAuthMiddleware()
	svc.ProfileAuth = middleware.NewProfileAuthMiddleware("")
	svc.RateLimit = middleware.NewRateLimitMiddleware()
	svc.RequestStamp = middleware.NewRequestStampMiddleware()
	svc.Timing = middleware.NewTimingMiddleware()
	return svc
}

// capableTransport is an in-process transport that answers yes to every
// disposition. It is not a broker that can honour one - it is what a
// transport that CAN looks like to the startup check, which reads the
// capability once and never asks again.
type capableTransport struct{ *memory.Transport }

func (capableTransport) CanDisposition(craftevents.Disposition) bool { return true }

// A delivery guarantee the deployment states is worth nothing if the
// transport cannot keep it, so registration is refused rather than
// running a chain whose Redeliver is silently settled. The refusal comes
// out of the deployable's own Register: that is the only thing standing
// between it and a binary that boots, serves HTTP, passes readiness and
// quietly loses every message it meant to retry.
func TestARequiredDispositionTheTransportLacksFailsRegistration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		build     func() craftevents.Option
		registers bool
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
			registers: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := craftevents.New(tc.build(),
				craftevents.WithCodec(codecjson.Codec{}),
				craftevents.WithDispositionRequired(craftevents.DispositionRedeliver))

			err := consumers.Register(bus, svccontext.NewServiceContext())

			if tc.registers {
				if err != nil {
					t.Fatalf("registration failed on a transport that can redeliver: %v", err)
				}
				if err := bus.Start(context.Background()); err != nil {
					t.Fatalf("start: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("registered on a transport that cannot honour the required disposition")
			}
			if !errors.Is(err, craftevents.ErrDispositionUnsupported) {
				t.Errorf("registration failed with %v, want ErrDispositionUnsupported", err)
			}
			if !strings.Contains(err.Error(), "redeliver") {
				t.Errorf("the failure does not name the disposition asked for: %v", err)
			}
		})
	}
}

// A design with events generates a wiring umbrella that knows nothing
// about them: the bus, the groups and the subscriptions are the
// application's, so Register attaches HTTP and hands back a shutdown
// without a container carrying anything event-shaped.
func TestWiringRegisterIsHTTPOnly(t *testing.T) {
	svc := wiredContext()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdown, err := wiring.Register(ctx, server.New(svc), svc)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Register returned no shutdown")
	}
	if err := shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// The complement: an HTTP middleware the design applies but nothing wired
// is skipped by the chain rather than called, so the guarantee would be
// missing with nothing to notice. Register names the line to add instead.
func TestWiringRegisterRefusesAnUnwiredHTTPMiddleware(t *testing.T) {
	svc := wiredContext()
	svc.Audit = nil

	_, err := wiring.Register(context.Background(), server.New(svc), svc)
	if err == nil {
		t.Fatal("registered with a declared middleware left nil")
	}
	for _, want := range []string{"middleware Audit", "svcCtx.Audit is nil", "NewAuditMiddleware"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
}
