package telemetry

import (
	"errors"
	"net"
	"net/http"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// scrapeHandler serves g in Prometheus text exposition, or OpenMetrics
// when the scraper asks for `application/openmetrics-text`.
func scrapeHandler(g prom.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// startAdmin serves h on path from a dedicated listener on addr, so the
// scrape stays off the public API port and can be firewalled separately.
// Returns the listening server (its Addr resolved, so a `:0` bind is
// loggable), or nil when the bind failed, and a channel carrying the bind
// or serve failure; the channel is buffered, so an unread failure never
// blocks the listener goroutine.
func startAdmin(addr, path string, h http.Handler) (*http.Server, <-chan error) {
	mux := http.NewServeMux()
	mux.Handle(path, h)
	s := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		errCh <- err
		close(errCh)
		return nil, errCh
	}
	s.Addr = ln.Addr().String()
	go func() {
		if err := s.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	return s, errCh
}
