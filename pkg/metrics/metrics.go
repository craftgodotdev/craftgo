// Package metrics is superseded by [github.com/craftgodotdev/craftgo/pkg/telemetry],
// which wires traces and metrics as one stack.
//
// Deprecated: import pkg/telemetry. This package keeps only the config
// type, exporter names and defaults as aliases for one release.
package metrics

import "github.com/craftgodotdev/craftgo/pkg/telemetry"

// Config is [telemetry.MetricsConfig].
type Config = telemetry.MetricsConfig

// Exporter selector values for [Config.Exporter].
const (
	ExporterPrometheus = telemetry.ExporterPrometheus
	ExporterOTLPgRPC   = telemetry.ExporterOTLPgRPC
	ExporterOTLPHTTP   = telemetry.ExporterOTLPHTTP
	ExporterNone       = telemetry.ExporterNone
)

// DefaultAdminAddr and DefaultMetricsPath are [telemetry.DefaultAdminAddr]
// and [telemetry.DefaultMetricsPath].
const (
	DefaultAdminAddr   = telemetry.DefaultAdminAddr
	DefaultMetricsPath = telemetry.DefaultMetricsPath
)
