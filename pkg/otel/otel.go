// Package otel is superseded by [github.com/craftgodotdev/craftgo/pkg/telemetry],
// which wires traces and metrics as one stack.
//
// Deprecated: import pkg/telemetry. This package keeps only the config
// type and exporter names as aliases for one release.
package otel

import "github.com/craftgodotdev/craftgo/pkg/telemetry"

// Config is [telemetry.OTelConfig].
type Config = telemetry.OTelConfig

// Exporter selector values for [Config.Exporter].
const (
	ExporterNone     = telemetry.ExporterNone
	ExporterStdout   = telemetry.ExporterStdout
	ExporterOTLPgRPC = telemetry.ExporterOTLPgRPC
	ExporterOTLPHTTP = telemetry.ExporterOTLPHTTP
)
