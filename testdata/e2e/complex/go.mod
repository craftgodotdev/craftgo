module github.com/dropship-dev/craftgo/testdata/e2e/complex

go 1.26

require github.com/dropship-dev/craftgo v0.0.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
)

replace github.com/dropship-dev/craftgo => ../../..
