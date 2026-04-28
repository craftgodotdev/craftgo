// Command example is the runnable Bookstore showcase. It wires the
// generated routes onto the runtime server, attaches global runtime
// middleware via srv.Use, and assigns the design-declared middleware
// fields on ServiceContext to concrete implementations.
//
// Run:
//
//	go run ./example
//
// Then in another shell:
//
//	curl http://localhost:8080/api/v1/books
//	curl -X POST http://localhost:8080/api/v1/books \
//	    -H 'Content-Type: application/json' \
//	    -H 'Authorization: Bearer secret-token' \
//	    -d '{"title":"Go Programming","author":"Alan A. A. Donovan","isbn":"9780134190440","priceCents":4995,"stock":10}'
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	craftlog "github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/pkg/metrics"
	"github.com/dropship-dev/craftgo/pkg/otel"
	"github.com/dropship-dev/craftgo/pkg/server"

	"github.com/dropship-dev/craftgo/example/internal/middleware"
	"github.com/dropship-dev/craftgo/example/internal/routes"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

func main() {
	svc := svccontext.NewServiceContext()

	// Wire each design-declared middleware field on ServiceContext to a
	// concrete implementation from the user-owned middleware package.
	// craftgo never overwrites these impl files after the first gen.
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware("Bearer secret-token")
	svc.RateLimit = middleware.NewRateLimitMiddleware(10, time.Minute)

	// Toggle the optional observability subsystems at runtime. Both
	// gates start CLOSED, so default `go run` produces no telemetry —
	// users opt in by uncommenting (or driven by env / config).
	//
	// log.Discard() silences the access log entirely; main.go can flip
	// it on demand without touching the rest of the boot sequence.
	//
	// otel.Init() and metrics.Init() turn on per-request OpenTelemetry
	// spans and bucketed counters respectively; both are pass-throughs
	// when off so leaving them in main.go costs only an atomic load.
	// otel.InitDefault wires a default in-process TracerProvider so
	// trace_id / span_id are populated even without an external
	// exporter. For prod, use otel.Init() and configure your own SDK.
	otel.InitDefault()
	metrics.Init()
	// Switch to human-readable logging during local development. Use
	// craftlog.New() (the JSON default) for production, craftlog.Discard()
	// to silence completely.
	_ = craftlog.Discard

	// Build the Server, attach RUNTIME globals via Use. Order matters
	// — RequestID must run before AccessLog so the log can capture
	// the request ID; metrics + otel run innermost so they observe
	// the actual handler latency, not the log overhead.
	srv := server.New(svc)
	srv.SetLogger(craftlog.NewConsole())
	srv.Use(server.RequestID())
	srv.Use(server.AccessLog(srv.Logger()))
	srv.Use(otel.HTTPMiddleware("bookstore"))
	srv.Use(metrics.HTTPMiddleware())

	// One call wires every service. The umbrella RegisterAll is
	// generated from the DSL service set on every `craftgo gen`. All
	// HTTP modes (multipart, SSE, NDJSON, raw, raw+stream) are now
	// expressed in the DSL — see example/design/admin/admin-service.craftgo.
	routes.RegisterAll(srv, svc)

	go func() {
		log.Println("listening on :8080")
		if err := srv.Start(":8080"); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Stop(ctx)
}
