module github.com/craftgodotdev/craftgo/tests/e2e/matrix

go 1.26.6

require (
	github.com/craftgodotdev/craftgo v0.0.0
	github.com/craftgodotdev/craftgo/pkg/events/kafka v0.0.0
	github.com/twmb/franz-go v1.21.6
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20260914031441-1623827d1042
	github.com/twmb/franz-go/pkg/kmsg v1.13.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/craftgodotdev/craftgo/pkg/events v0.0.0
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
)

replace github.com/craftgodotdev/craftgo => ../../..

replace github.com/craftgodotdev/craftgo/pkg/events => ../../../pkg/events

replace github.com/craftgodotdev/craftgo/pkg/events/kafka => ../../../pkg/events/kafka
