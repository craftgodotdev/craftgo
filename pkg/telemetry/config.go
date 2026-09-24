package telemetry

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
	// Exporter is "stdout", "otlp_grpc" or "otlp_http"; any other value keeps
	// spans in process, with valid ids for logs but no export.
	Exporter string `yaml:"exporter"`
	// Endpoint is the OTLP collector: an http:// or https:// URL, whose scheme
	// picks TLS, or for otlp_grpc also a bare host:port, dialled without TLS.
	// [Init] fails on one that names no host, and on any other otlp_http one.
	Endpoint string `yaml:"endpoint"`
}

// MetricsConfig configures metrics and the Prometheus scrape listener.
type MetricsConfig struct {
	// Enabled turns metrics on; false installs nothing.
	Enabled bool `yaml:"enabled"`
	// Exporter is "otlp_grpc", "otlp_http" or "none" (a meter with no export);
	// any other value, "" included, serves the Prometheus scrape.
	Exporter string `yaml:"exporter"`
	// Endpoint is the OTLP collector, in the forms [OTelConfig.Endpoint] takes.
	Endpoint string `yaml:"endpoint"`
	// ServiceName overrides [Config.ServiceName] for metrics.
	ServiceName string `yaml:"serviceName"`
	// AdminAddr is the scrape listener's bind address; empty starts no
	// listener, leaving the scrape to [Telemetry.ScrapeHandler].
	AdminAddr string `yaml:"adminAddr"`
	// Path is the scrape route, [DefaultMetricsPath] when empty.
	Path string `yaml:"path"`
}
