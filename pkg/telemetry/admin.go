package telemetry

import (
	"errors"
	"net"
	"net/http"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// scrapeHandler serves g as Prometheus text, or as OpenMetrics when asked.
func scrapeHandler(g prom.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// startAdmin serves h on path at addr and returns the server with Addr resolved
// (nil when the bind fails) and a channel carrying the bind or serve error.
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
