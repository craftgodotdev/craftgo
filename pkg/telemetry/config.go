package telemetry

import (
	"fmt"
	"net/http"
	"strings"
)

// Config describes the stack [Init] builds. ServiceName is the `service.name`
// of each signal that sets none; with neither set the SDK's
// `unknown_service:<binary>` applies.
type Config struct {
	ServiceName string        `yaml:"serviceName"`
	OTel        OTelConfig    `yaml:"otel"`
	Metrics     MetricsConfig `yaml:"metrics"`
}

// Exporter values for [OTelConfig.Exporter] and [MetricsConfig.Exporter];
// stdout is traces-only, prometheus metrics-only.
const (
	ExporterNone       = "none"
	ExporterStdout     = "stdout"
	ExporterPrometheus = "prometheus"
	ExporterOTLPgRPC   = "otlp_grpc"
	ExporterOTLPHTTP   = "otlp_http"
)

// DefaultAdminAddr is the conventional scrape listener address; an empty
// [MetricsConfig.AdminAddr] still starts no listener. DefaultMetricsPath is
// the scrape route when [MetricsConfig.Path] is empty.
const (
	DefaultAdminAddr   = ":9090"
	DefaultMetricsPath = "/metrics"
)

// OTelConfig configures traces.
type OTelConfig struct {
	// Enabled turns traces on; false installs nothing.
	Enabled bool `yaml:"enabled"`
	// ServiceName overrides [Config.ServiceName] for spans.
	ServiceName string `yaml:"serviceName"`
	// Exporter is "stdout", "otlp_grpc" or "otlp_http"; "none" or "" keeps
	// spans in process, with valid ids for logs but no export. [Init] fails on
	// any other value.
	Exporter string `yaml:"exporter"`
	// Endpoint is the OTLP collector: an http:// or https:// URL, whose scheme
	// picks TLS, or for otlp_grpc also a bare host:port, dialled without TLS.
	// otlp_http sends each signal to its own path under a URL with no path or
	// "/". Empty leaves it to the exporter: the OTEL_EXPORTER_OTLP_*ENDPOINT
	// variables, else localhost:4317 or localhost:4318. [Init] fails on any
	// other value.
	Endpoint string `yaml:"endpoint"`
}

// MetricsConfig configures metrics and the Prometheus scrape listener.
type MetricsConfig struct {
	// Enabled turns metrics on; false installs nothing.
	Enabled bool `yaml:"enabled"`
	// Exporter is "otlp_grpc", "otlp_http" or "none" (a meter with no export);
	// "prometheus" or "" serves the Prometheus scrape. [Init] fails on any other
	// value.
	Exporter string `yaml:"exporter"`
	// Endpoint is the OTLP collector, in the forms [OTelConfig.Endpoint] takes.
	Endpoint string `yaml:"endpoint"`
	// ServiceName overrides [Config.ServiceName] for metrics.
	ServiceName string `yaml:"serviceName"`
	// AdminAddr is the scrape listener's bind address; empty starts no
	// listener, leaving the scrape to [Telemetry.ScrapeHandler].
	AdminAddr string `yaml:"adminAddr"`
	// Path is the scrape route, [DefaultMetricsPath] when empty. [Init] fails on
	// one that does not start with "/" or that a ServeMux refuses.
	Path string `yaml:"path"`
}

// validate reports, by its config.yaml key, an enabled signal's exporter that
// [Init] does not know and a scrape path no listener can serve.
func (c Config) validate() error {
	if c.OTel.Enabled {
		switch c.OTel.Exporter {
		case "", ExporterNone, ExporterStdout, ExporterOTLPgRPC, ExporterOTLPHTTP:
		default:
			return fmt.Errorf("otel.exporter %q is not %q, %q, %q or %q", c.OTel.Exporter,
				ExporterNone, ExporterStdout, ExporterOTLPgRPC, ExporterOTLPHTTP)
		}
	}
	if !c.Metrics.Enabled {
		return nil
	}
	switch c.Metrics.Exporter {
	case "", ExporterPrometheus, ExporterNone, ExporterOTLPgRPC, ExporterOTLPHTTP:
	default:
		return fmt.Errorf("metrics.exporter %q is not %q, %q, %q or %q", c.Metrics.Exporter,
			ExporterPrometheus, ExporterNone, ExporterOTLPgRPC, ExporterOTLPHTTP)
	}
	if p := c.Metrics.Path; p != "" {
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("metrics.path %q does not start with \"/\"", p)
		}
		if err := servable(p); err != nil {
			return fmt.Errorf("metrics.path %q: %w", p, err)
		}
	}
	return nil
}

// servable returns the error a ServeMux panics with on registering pattern.
func servable(pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	http.NewServeMux().Handle(pattern, http.NotFoundHandler())
	return nil
}
