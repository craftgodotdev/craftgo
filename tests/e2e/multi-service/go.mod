module github.com/craftgodotdev/craftgo/tests/e2e/multi-service

go 1.24.2

require github.com/craftgodotdev/craftgo v0.0.0

require (
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
)

replace github.com/craftgodotdev/craftgo => ../../..
