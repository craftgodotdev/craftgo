module github.com/craftgodotdev/craftgo/example/brokers

go 1.26.6

replace github.com/craftgodotdev/craftgo/pkg/events => ../../pkg/events

replace github.com/craftgodotdev/craftgo/pkg/events/nats => ../../pkg/events/nats

replace github.com/craftgodotdev/craftgo/pkg/events/kafka => ../../pkg/events/kafka

replace github.com/craftgodotdev/craftgo => ../..

require (
	github.com/craftgodotdev/craftgo v0.0.0-00010101000000-000000000000
	github.com/craftgodotdev/craftgo/pkg/events v1.10.0
	github.com/craftgodotdev/craftgo/pkg/events/kafka v0.0.0-00010101000000-000000000000
	github.com/craftgodotdev/craftgo/pkg/events/nats v0.0.0-00010101000000-000000000000
	github.com/nats-io/nats.go v1.54.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/twmb/franz-go v1.22.0 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.14.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)
