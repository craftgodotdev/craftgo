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
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/craftgodotdev/craftgo/pkg/events v1.8.0
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
)

replace github.com/craftgodotdev/craftgo => ../../..

replace github.com/craftgodotdev/craftgo/pkg/events => ../../../pkg/events

replace github.com/craftgodotdev/craftgo/pkg/events/kafka => ../../../pkg/events/kafka
