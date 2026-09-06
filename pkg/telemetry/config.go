package telemetry

// Config is the telemetry block of a project's config.yaml. ServiceName
// sits above both signals and fills in for an empty per-signal name.
type Config struct {
	ServiceName string        `yaml:"serviceName"`
	OTel        OTelConfig    `yaml:"otel"`
	Metrics     MetricsConfig `yaml:"metrics"`
}

// Exporter selector values for [OTelConfig.Exporter] and
// [MetricsConfig.Exporter]. Stdout is traces-only, Prometheus metrics-only.
const (
	ExporterNone       = "none"
	ExporterStdout     = "stdout"
	ExporterPrometheus = "prometheus"
	ExporterOTLPgRPC   = "otlp_grpc"
	ExporterOTLPHTTP   = "otlp_http"
)

// DefaultAdminAddr is the conventional Prometheus scrape port;
// DefaultMetricsPath is the route the listener serves when
// [MetricsConfig.Path] is empty.
const (
	DefaultAdminAddr   = ":9090"
	DefaultMetricsPath = "/metrics"
)

// OTelConfig is the `otel:` block: whether traces are on and where spans go.
type OTelConfig struct {
	// Enabled installs the tracer and the trace side of the HTTP wrapper.
	// False is a complete no-op.
	Enabled bool `yaml:"enabled"`
	// ServiceName is the `service.name` stamped on every span. Empty
	// inherits the top-level serviceName; set it only to report spans
	// under a different identity from metrics. With both empty the SDK
	// default `unknown_service:<binary>` applies.
	ServiceName string `yaml:"serviceName"`
	// Exporter selects the destination for spans:
	//   - "none" / "" - in-process spans only (ids in logs, no export)
	//   - "stdout"    - JSON spans on stdout (debugging)
	//   - "otlp_grpc" - push to an OTLP collector via gRPC
	//   - "otlp_http" - push to an OTLP collector via HTTP/protobuf
	Exporter string `yaml:"exporter"`
	// Endpoint is the collector address for the OTLP exporters, ignored
	// for "none" / "stdout":
	//   - otlp_http: a full URL WITH scheme - the scheme picks transport
	//     security: `http://collector:4318` (plaintext) or
	//     `https://collector.example.com` (TLS).
	//   - otlp_grpc: a bare `host:port` (e.g. `collector:4317`, plaintext)
	//     OR a full URL whose scheme picks security
	//     (`https://collector:4317` for TLS).
	Endpoint string `yaml:"endpoint"`
}

// MetricsConfig is the `metrics:` block: whether metrics are on, where
// they go, and the scrape listener for the Prometheus path.
type MetricsConfig struct {
	// Enabled installs the meter, the metrics side of the HTTP wrapper
	// and, for the prometheus exporter, the scrape listener. False is a
	// complete no-op.
	Enabled bool `yaml:"enabled"`
	// Exporter selects the data path:
	//   - "prometheus" / "" - pull on AdminAddr (the default; any unknown
	//     value scrapes too, so a typo never silently turns metrics off)
	//   - "otlp_grpc"  - push via OTLP gRPC
	//   - "otlp_http"  - push via OTLP HTTP/protobuf
	//   - "none"       - meter installed without exporter (testing)
	Exporter string `yaml:"exporter"`
	// Endpoint is the collector address for the OTLP exporters, in the
	// same forms as [OTelConfig.Endpoint]. Ignored for "prometheus" / "none".
	Endpoint string `yaml:"endpoint"`
	// ServiceName is the `service.name` stamped on every metric. Empty
	// inherits the top-level serviceName; set it only to report metrics
	// under a different identity from traces.
	ServiceName string `yaml:"serviceName"`
	// AdminAddr is the bind address of the Prometheus scrape listener
	// (`:9090`, `127.0.0.1:9090`, ...). Empty starts no listener - serve
	// [Telemetry.ScrapeHandler] on a route of the public server instead.
	// Ignored unless the exporter scrapes.
	AdminAddr string `yaml:"adminAddr"`
	// Path is the scrape route, [DefaultMetricsPath] when empty. Override
	// when a reverse proxy already claims that path.
	Path string `yaml:"path"`
}
