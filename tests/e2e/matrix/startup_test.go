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

// wiredContext returns a service context with every HTTP middleware the
// design applies assigned, as wiring.Register requires.
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

// capableTransport is a memory transport that claims every disposition.
type capableTransport struct{ *memory.Transport }

func (capableTransport) CanDisposition(craftevents.Disposition) bool { return true }

// consumers.Register fails with ErrDispositionUnsupported when the bus
// requires a disposition the transport cannot honour.
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

// wiring.Register attaches HTTP with no bus and returns a working shutdown.
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

// wiring.Register refuses a nil declared middleware and names its constructor.
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
