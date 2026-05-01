package metrics

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// SnapshotHandler returns the Prometheus text-format exposition
// handler bound to the package's registry. Wire it manually on a
// route when [StartAdmin]'s dedicated listener is overkill (single
// port deployments, sidecar scrape configs):
//
//	srv.Handle("GET /metrics", metrics.SnapshotHandler())
//
// The handler honours the standard scrape negotiation: clients
// asking for `application/openmetrics-text` get OpenMetrics; the
// default Prometheus exposition (text/plain; version=0.0.4) is
// emitted otherwise. When [Init] / [InitDefault] has not been
// called the registry is empty - the response stays valid (an empty
// scrape) so health probes and monitor smoke checks still see 200.
func SnapshotHandler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// DefaultAdminAddr is the conventional Prometheus admin port. Used
// as a hint in docs; pass any addr string to [StartAdmin] to override.
const DefaultAdminAddr = ":9090"

// DefaultMetricsPath is the canonical Prometheus exposition path.
// `StartAdmin` uses this when no [WithPath] option is supplied.
const DefaultMetricsPath = "/metrics"

// AdminOption mutates the admin server's configuration. Callers
// compose options at the call site:
//
//	metrics.StartAdmin(":9090", metrics.WithPath("/internal/metrics"))
//
// New knobs land here as additional `WithX` constructors so the
// StartAdmin signature stays stable.
type AdminOption func(*adminConfig)

type adminConfig struct {
	path string
}

// WithPath overrides the route the metrics snapshot is served on.
// Defaults to [DefaultMetricsPath] (`/metrics`). Useful when an
// existing reverse proxy already claims that path or the company
// convention is something else (`/internal/metrics`,
// `/_/observability`, ...).
func WithPath(p string) AdminOption {
	return func(c *adminConfig) {
		if p != "" {
			c.path = p
		}
	}
}

// StartAdmin spins up a dedicated HTTP listener exposing the metrics
// snapshot on a separate admin port (Prometheus convention, default
// `:9090`). Keeping telemetry off the public traffic listener is the
// idiomatic split - public clients get the typed API on the main
// port, ops scrape the admin port without being firewalled in.
//
// addr controls the listen address (`:9090`, `127.0.0.1:9090`, ...);
// pass an empty string to opt out of the listener entirely (the
// function returns nil + nil so callers can leave the call site
// unconditional). Path defaults to [DefaultMetricsPath]; override
// with [WithPath] when a different convention applies.
//
// The returned `*http.Server` is the live listener; callers Shutdown
// it during their main lifecycle (typical pattern: pair with the
// public server's Shutdown so `Ctrl+C` drains both).
//
// On listener errors the returned `<-chan error` channel surfaces
// the failure asynchronously so the caller can decide whether to
// log + continue (admin failures usually shouldn't kill the public
// server) or hard-exit. The channel is buffered so a never-read
// receiver does not block the goroutine that closes it.
func StartAdmin(addr string, opts ...AdminOption) (*http.Server, <-chan error) {
	if addr == "" {
		return nil, nil
	}
	cfg := adminConfig{path: DefaultMetricsPath}
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.Handle(cfg.path, SnapshotHandler())
	s := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		errCh <- err
		close(errCh)
		return s, errCh
	}
	// Overwrite the configured addr with the listener's resolved
	// address. When the caller passes `:0` to grab a free port the
	// resolved addr (`127.0.0.1:54321`) is the only way to discover
	// what the OS picked - without this, callers would have to dive
	// into the listener via reflection.
	s.Addr = ln.Addr().String()
	go func() {
		serveErr := s.Serve(ln)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
		close(errCh)
	}()
	return s, errCh
}

// ShutdownAdmin gracefully closes a running admin server with a
// bounded deadline. Tolerates a nil server so callers don't have to
// guard the StartAdmin("") sentinel - a no-op when nothing was started.
func ShutdownAdmin(ctx context.Context, s *http.Server) error {
	if s == nil {
		return nil
	}
	return s.Shutdown(ctx)
}
